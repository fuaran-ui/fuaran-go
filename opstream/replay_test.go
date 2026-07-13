package opstream

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func mustDecodeNode(t *testing.T, canonicalJSON string) wire.Node {
	t.Helper()
	node, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	return node
}

func mustEncodeNode(t *testing.T, n wire.Node) string {
	t.Helper()
	s, err := wire.EncodeNode(n)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	return s
}

const stackTwo = `{"id":"s","kind":{"$type":"Box","children":[` +
	`{"id":"a","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"A"}}},` +
	`{"id":"b","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"B"}}}` +
	`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`

// A tree driven by apply-and-persist reconstructs identically from its op-stream
// — the Time Machine "scrub history" substrate.
func TestApplyAndPersistThenReplayReconstructs(t *testing.T) {
	sink := NewInMemorySink()
	tree := mustDecodeNode(t, stackTwo)
	ctx := func(seq int) PersistContext {
		return PersistContext{StreamID: "app", UserID: "u", Timestamp: int64(1700000000 + seq)}
	}

	// Three edits: rename a's text, remove b, reorder (trivially).
	edits := []wire.Obj{
		{Tag: "UpdateProp", Fields: map[string]wire.Value{"path": wire.Str("Text"), "target": wire.Str("a"), "value": wire.Str("A-edited")}},
		{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("b")}},
		{Tag: "UpdateProp", Fields: map[string]wire.Value{"path": wire.Str("Text"), "target": wire.Str("a"), "value": wire.Str("A-final")}},
	}
	for i, op := range edits {
		next, err := ApplyAndPersist(sink, ctx(i+1), op, tree)
		if err != nil {
			t.Fatalf("ApplyAndPersist edit %d: %v", i, err)
		}
		tree = next
	}

	if got := sink.LatestSequence("app"); got != 3 {
		t.Fatalf("latest sequence = %d, want 3", got)
	}
	// The whole chain verifies.
	if err := VerifyChain(sink.Replay("app", 1, 3)); err != nil {
		t.Fatalf("chain does not verify: %v", err)
	}
	// Replay from genesis reconstructs the exact live tree.
	reconstructed, err := ReplayStream(sink, "app", mustDecodeNode(t, stackTwo), 1, 0)
	if err != nil {
		t.Fatalf("ReplayStream: %v", err)
	}
	if mustEncodeNode(t, reconstructed) != mustEncodeNode(t, tree) {
		t.Errorf("replay did not reconstruct the live tree:\n got %s\nwant %s",
			mustEncodeNode(t, reconstructed), mustEncodeNode(t, tree))
	}
}

// Partial replay (a checkpoint at seq 1, resume from seq 2) reaches the same
// state as full replay.
func TestPartialReplayFromCheckpoint(t *testing.T) {
	sink := NewInMemorySink()
	tree := mustDecodeNode(t, stackTwo)
	edits := []wire.Obj{
		{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("a")}},
		{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("b")}},
	}
	for i, op := range edits {
		next, _ := ApplyAndPersist(sink, PersistContext{StreamID: "app", UserID: "u", Timestamp: int64(i)}, op, tree)
		tree = next
	}
	// Checkpoint the tree after seq 1 (a still-has-b tree), then resume seq 2.
	afterSeq1, _ := ReplayStream(sink, "app", mustDecodeNode(t, stackTwo), 1, 1)
	final, err := ReplayStream(sink, "app", afterSeq1, 2, 0)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if mustEncodeNode(t, final) != mustEncodeNode(t, tree) {
		t.Error("checkpoint-resume replay diverged from full replay")
	}
}
