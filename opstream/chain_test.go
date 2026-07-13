package opstream

import (
	"errors"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func removeOp(target string) wire.Obj {
	return wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str(target)}}
}

func TestComputeHashIsDeterministicAndActorSensitive(t *testing.T) {
	op := removeOp("m1")
	h1, err := ComputeHash(GenesisPreviousHash, op, 1, 1700000000, HumanActor{ID: "u"}, nil, Success{})
	if err != nil {
		t.Fatalf("ComputeHash: %v", err)
	}
	h2, _ := ComputeHash(GenesisPreviousHash, op, 1, 1700000000, HumanActor{ID: "u"}, nil, Success{})
	if h1 != h2 {
		t.Error("ComputeHash is not deterministic")
	}
	// A different actor breaks the hash (attribution is folded in).
	h3, _ := ComputeHash(GenesisPreviousHash, op, 1, 1700000000, HumanActor{ID: "v"}, nil, Success{})
	if h1 == h3 {
		t.Error("changing the actor did not change the hash")
	}
	// A Failure outcome breaks the hash (outcome is folded in).
	h4, _ := ComputeHash(GenesisPreviousHash, op, 1, 1700000000, HumanActor{ID: "u"}, nil, Failure{Code: "X", Message: "y"})
	if h1 == h4 {
		t.Error("changing the outcome did not change the hash")
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(h1))
	}
}

func TestFormatVersion(t *testing.T) {
	entry, err := EncodeStreamEntry(removeOp("m1"), 1700000000, nil, Success{})
	if err != nil {
		t.Fatalf("EncodeStreamEntry: %v", err)
	}
	if v, ok := FormatVersion(entry); !ok || v != ChainFormatVersion {
		t.Errorf("FormatVersion = (%d,%v), want (%d,true)", v, ok, ChainFormatVersion)
	}
	if _, ok := FormatVersion(`{"op":...}`); ok {
		t.Error("a tagless envelope must report no version")
	}
}

func TestVerifyChainCleanAndTampered(t *testing.T) {
	sink := NewInMemorySink()
	pid := "p-1"
	for i := 0; i < 3; i++ {
		ctx := PersistContext{StreamID: "s", UserID: "u", Timestamp: int64(1700000000 + i)}
		if i == 1 {
			ctx.PromptID = &pid
		}
		if _, err := AppendOp(sink, ctx, removeOp("m")); err != nil {
			t.Fatalf("AppendOp: %v", err)
		}
	}
	records := sink.Replay("s", 1, 3)
	if err := VerifyChain(records); err != nil {
		t.Fatalf("clean chain failed verification: %v", err)
	}

	// Tamper with a recorded op — verification catches it as a HashMismatch.
	tampered := append([]OpRecord(nil), records...)
	tampered[1].Op = removeOp("DIFFERENT")
	err := VerifyChain(tampered)
	var hm *HashMismatch
	if !errors.As(err, &hm) || hm.Sequence != 2 {
		t.Errorf("tampered chain: got %v, want HashMismatch at sequence 2", err)
	}

	// Break the previous-hash link.
	broken := append([]OpRecord(nil), records...)
	broken[2].PreviousHash = GenesisPreviousHash
	var ph *PreviousHashMismatch
	if !errors.As(VerifyChain(broken), &ph) || ph.Sequence != 3 {
		t.Errorf("broken link not caught as PreviousHashMismatch at sequence 3")
	}

	// Out-of-order sequence.
	reordered := []OpRecord{records[0], records[2]}
	var oo *OutOfOrder
	if !errors.As(VerifyChain(reordered), &oo) {
		t.Errorf("out-of-order chain not caught")
	}
}

func TestSnapshotHashBindsToPosition(t *testing.T) {
	tree := `{"id":"a","kind":{"$type":"Skeleton","rows":1}}`
	h1 := SnapshotHash(GenesisPreviousHash, 5, tree)
	h2 := SnapshotHash(GenesisPreviousHash, 6, tree)
	if h1 == h2 {
		t.Error("snapshot hash must bind to the sequence position")
	}
}
