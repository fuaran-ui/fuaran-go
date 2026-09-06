package opstream

import (
	"errors"
	"sync"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// ── two concurrent writers, one shared sink ─────────────────────────────────

// Two goroutines concurrently persist against ONE shared in-memory sink and
// ONE shared stream via ApplyAndPersist. Both ops must land — sequences
// contiguous (1 and 2) — and neither writer's OnSinkError hook may fire,
// because that would mean ApplyAndPersist reported success (it never returns
// a persist error) while the op was actually lost.
//
// Run this under `go test -race`. Before the CasSink/AppendNext fix, the
// in-memory sink held its records map with NO synchronisation at all, so two
// goroutines racing the map itself hit a fatal Go runtime error ("concurrent
// map writes"), not merely a lost op — see the go-red confirmation recorded
// beside this test's commit.
func TestApplyAndPersistTwoWritersBothPersist(t *testing.T) {
	sink := NewInMemorySink()
	base := mustDecodeNode(t, stackTwo)

	editA := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path": wire.Str("Text"), "target": wire.Str("a"), "value": wire.Str("from-writer-1"),
	}}
	editB := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path": wire.Str("Text"), "target": wire.Str("b"), "value": wire.Str("from-writer-2"),
	}}

	lost := make(chan error, 2)
	applyFailed := make(chan error, 2)
	writer := func(op wire.Obj, userID string) {
		ctx := PersistContext{
			StreamID:  "shared",
			UserID:    userID,
			Timestamp: 1700000000,
			OnSinkError: func(err error) {
				lost <- err
			},
		}
		if _, err := ApplyAndPersist(sink, ctx, op, base); err != nil {
			applyFailed <- err
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); writer(editA, "writer-1") }()
	go func() { defer wg.Done(); writer(editB, "writer-2") }()
	wg.Wait()
	close(lost)
	close(applyFailed)

	for err := range applyFailed {
		t.Fatalf("ApplyAndPersist: the apply itself failed: %v", err)
	}
	for err := range lost {
		t.Fatalf("a writer's op was lost (OnSinkError fired) although ApplyAndPersist reported success: %v", err)
	}

	if got := sink.LatestSequence("shared"); got != 2 {
		t.Fatalf("latest sequence = %d, want 2 (both writers' ops must persist)", got)
	}
	records := sink.Replay("shared", 1, 2)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Sequence != 1 || records[1].Sequence != 2 {
		t.Fatalf("sequences not contiguous: got %d, %d", records[0].Sequence, records[1].Sequence)
	}
	if err := VerifyChain(records); err != nil {
		t.Fatalf("chain does not verify: %v", err)
	}

	// Both writers' distinct ops actually landed — not two copies of one.
	values := map[string]bool{}
	for _, r := range records {
		if v, ok := r.Op.Fields["value"].(wire.Str); ok {
			values[string(v)] = true
		}
	}
	if !values["from-writer-1"] || !values["from-writer-2"] {
		t.Fatalf("expected both writers' values in the persisted records, got %v", values)
	}
}

// ── stale-head compare-and-append ───────────────────────────────────────────

// AppendIf at a stale (wrong) expected head persists nothing and reports the
// actual head to rebuild against.
func TestAppendIfRejectsStaleHead(t *testing.T) {
	sink := NewInMemorySink()
	op := wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("a")}}

	first, err := AppendNext(sink, PersistContext{StreamID: "s", UserID: "u", Timestamp: 1}, op)
	if err != nil {
		t.Fatalf("AppendNext (seed): %v", err)
	}
	if got := sink.Head("s"); got != first.Hash {
		t.Fatalf("Head() = %q, want the seeded record's hash %q", got, first.Hash)
	}

	staleRecord := OpRecord{
		StreamID:     "s",
		Sequence:     2,
		PreviousHash: "not-the-real-head",
		Hash:         "irrelevant-would-be-hash",
		Op:           op,
		Actor:        HumanActor{ID: "u"},
	}
	_, err = sink.AppendIf(staleRecord, "not-the-real-head")
	if err == nil {
		t.Fatal("expected a stale-head error")
	}
	var stale *StaleHeadError
	if !errors.As(err, &stale) {
		t.Fatalf("want *StaleHeadError, got %T: %v", err, err)
	}
	if stale.Expected != "not-the-real-head" {
		t.Errorf("stale.Expected = %q, want %q", stale.Expected, "not-the-real-head")
	}
	if stale.Actual != first.Hash {
		t.Errorf("stale.Actual = %q, want the real head %q", stale.Actual, first.Hash)
	}

	// The rejected attempt persisted nothing.
	if got := sink.LatestSequence("s"); got != 1 {
		t.Fatalf("latest sequence = %d, want 1 (a rejected AppendIf must persist nothing)", got)
	}
	if got := sink.Head("s"); got != first.Hash {
		t.Fatalf("Head() = %q, want unchanged at %q", got, first.Hash)
	}
}

// AppendNext itself recovers from a stale head by rebuilding against the
// actual one and retrying — exercised end to end rather than by calling
// AppendIf directly.
func TestAppendNextRetriesPastAStaleHead(t *testing.T) {
	sink := NewInMemorySink()
	op := wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("a")}}

	// Land a record out from under a caller that already captured the OLD head,
	// by seeding the stream first.
	seed, err := AppendNext(sink, PersistContext{StreamID: "s", UserID: "seed", Timestamp: 1}, op)
	if err != nil {
		t.Fatalf("seed AppendNext: %v", err)
	}

	// A fresh AppendNext call reads the CURRENT head itself (it does not carry
	// a stale one in), so it must land at sequence 2 chained to the seed.
	next, err := AppendNext(sink, PersistContext{StreamID: "s", UserID: "u", Timestamp: 2}, op)
	if err != nil {
		t.Fatalf("AppendNext: %v", err)
	}
	if next.Sequence != 2 || next.PreviousHash != seed.Hash {
		t.Fatalf("got sequence %d, previousHash %q; want sequence 2 chained to %q", next.Sequence, next.PreviousHash, seed.Hash)
	}
	if err := VerifyChain(sink.Replay("s", 1, 2)); err != nil {
		t.Fatalf("chain does not verify: %v", err)
	}
}

// ── gap refusal ──────────────────────────────────────────────────────────────

// previousHashFor refuses when the prior record is missing (a sink invariant
// violation), rather than silently substituting genesis and baking a
// permanent poison pill into the chain.
func TestPreviousHashForRefusesAGap(t *testing.T) {
	sink := NewInMemorySink() // empty: nothing at any sequence

	if _, err := previousHashFor(sink, "s", 1); err != nil {
		t.Fatalf("sequence 1 always links to genesis, got an error: %v", err)
	}
	if _, err := previousHashFor(sink, "s", 5); err == nil {
		t.Fatal("expected a gap error: no record exists at sequence 4")
	}
}

// gapSink is a minimal, deliberately-inconsistent Sink fake: it claims a
// LatestSequence no Replay could substantiate — the sink invariant violation
// previousHashFor must refuse rather than paper over. It is not a CasSink, so
// this exercises AppendOp (and AppendNext's fallback to it) directly.
type gapSink struct{ latest int }

func (g *gapSink) Append(OpRecord) error              { return nil }
func (g *gapSink) Replay(string, int, int) []OpRecord { return nil }
func (g *gapSink) LatestSequence(string) int          { return g.latest }
func (g *gapSink) Streams() []string                  { return nil }

func TestAppendOpRefusesAndReportsAGap(t *testing.T) {
	sink := &gapSink{latest: 4} // claims 4 prior records; Replay proves none exist
	var reported error
	ctx := PersistContext{
		StreamID:    "s",
		UserID:      "u",
		Timestamp:   1,
		OnSinkError: func(err error) { reported = err },
	}
	op := wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("a")}}

	_, err := AppendOp(sink, ctx, op)
	if err == nil {
		t.Fatal("expected AppendOp to refuse the gap")
	}
	if reported == nil {
		t.Fatal("OnSinkError must be invoked for a gap refusal — it was not consulted before this error return")
	}
	if reported.Error() != err.Error() {
		t.Fatalf("returned error and the one reported to OnSinkError differ:\n returned: %v\n reported: %v", err, reported)
	}
}
