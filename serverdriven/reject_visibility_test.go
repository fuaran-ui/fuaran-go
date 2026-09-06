package serverdriven

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func TestRejectReachesTheClientThroughTheProjection(t *testing.T) {
	// A rejected step pushed nothing at all, so from the browser a refused
	// click and a click that never arrived were the same event: nothing
	// happened. The operator was told through the audit hook; the person who
	// clicked was told nothing.
	ch := &InMemoryChannel{}
	session := newCounterSession(t)

	var audited []Reject
	conn := NewConnection("c1", session, ch,
		WithOnReject(func(r Reject) { audited = append(audited, r) }),
		WithRejectProjection(func(r Reject) []wire.Obj {
			return []wire.Obj{{Tag: "UpdateProp", Fields: map[string]wire.Value{
				"path":   wire.Str("Label"),
				"target": wire.Str("count"),
				"value":  wire.Str("refused: " + string(r.Reason)),
			}}}
		}))

	// A forged node id — the first structural check.
	ch.Send(Event{ConnID: "c1", NodeID: "not-a-node", Event: "click"})

	if len(audited) != 1 || audited[0].Reason != ReasonUnknownNode {
		t.Fatalf("audit hook saw %v, want one UnknownNode reject", audited)
	}
	pushed := ch.Pushed()
	if len(pushed) != 1 {
		t.Fatalf("frames pushed = %d, want 1 — the refusal must reach the client", len(pushed))
	}
	if pushed[0].Seq != conn.Sequence() {
		t.Errorf("reject frame seq = %d, connection seq = %d — it must replay like any other frame",
			pushed[0].Seq, conn.Sequence())
	}
}

func TestRejectProjectionReturningNothingPushesNothing(t *testing.T) {
	// The right answer for a reject the user should not see.
	ch := &InMemoryChannel{}
	NewConnection("c1", newCounterSession(t), ch,
		WithRejectProjection(func(r Reject) []wire.Obj { return nil }))

	ch.Send(Event{ConnID: "c1", NodeID: "not-a-node", Event: "click"})

	if got := len(ch.Pushed()); got != 0 {
		t.Errorf("frames pushed = %d, want 0", got)
	}
}

func TestNoRejectProjectionKeepsTheSilentBehaviour(t *testing.T) {
	// Opt-in: a connection without a projection behaves as it always has.
	ch := &InMemoryChannel{}
	NewConnection("c1", newCounterSession(t), ch)

	ch.Send(Event{ConnID: "c1", NodeID: "not-a-node", Event: "click"})

	if got := len(ch.Pushed()); got != 0 {
		t.Errorf("frames pushed = %d, want 0", got)
	}
}
