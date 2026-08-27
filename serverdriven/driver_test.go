package serverdriven

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func TestStepAppliesOpsAndAdvancesTree(t *testing.T) {
	s := newCounterSession(t)
	opsList, reject := s.Step(clickInc("c1", 0))
	if reject != nil {
		t.Fatalf("unexpected reject: %+v", reject)
	}
	if len(opsList) != 1 || opsList[0].Tag != "UpdateProp" {
		t.Fatalf("expected one UpdateProp op, got %+v", opsList)
	}
	// The server tree advanced: the metric value is now Static 1.
	metric, ok := findNode(s.Tree(), "count")
	if !ok {
		t.Fatal("count node missing after step")
	}
	src, ok := metric.Kind.Fields["value"].(wire.Obj)
	if !ok || src.Tag != "Static" {
		t.Fatalf("value not a Static binding: %+v", metric.Kind.Fields["value"])
	}
	if v, ok := src.Fields["value"].(wire.Int); !ok || v != 1 {
		t.Errorf("metric value = %v, want Static 1", src.Fields["value"])
	}

	// A second click advances to 2 — state persists across steps.
	if _, reject := s.Step(clickInc("c1", 1)); reject != nil {
		t.Fatalf("second click rejected: %+v", reject)
	}
	metric, _ = findNode(s.Tree(), "count")
	src, _ = metric.Kind.Fields["value"].(wire.Obj)
	if v, _ := src.Fields["value"].(wire.Int); v != 2 {
		t.Errorf("after two clicks value = %v, want 2", src.Fields["value"])
	}
}

func TestStepRejects(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want RejectReason
	}{
		{"unknown node", Event{NodeID: "ghost", Event: "click"}, ReasonUnknownNode},
		{"illegitimate event for kind", Event{NodeID: "inc", Event: "change"}, ReasonIllegitimateEvent},
		{"non-interactive kind takes nothing", Event{NodeID: "count", Event: "click"}, ReasonIllegitimateEvent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newCounterSession(t)
			opsList, reject := s.Step(c.ev)
			if reject == nil {
				t.Fatalf("expected a reject, got ops %+v", opsList)
			}
			if reject.Reason != c.want {
				t.Errorf("reason = %s, want %s", reject.Reason, c.want)
			}
			// A rejected step leaves the tree untouched.
			metric, _ := findNode(s.Tree(), "count")
			src, _ := metric.Kind.Fields["source"].(wire.Obj)
			if v, _ := src.Fields["value"].(wire.Int); v != 0 {
				t.Errorf("rejected step mutated the tree (source=%v)", src.Fields["value"])
			}
		})
	}
}

func TestStepRejectsWhenHandlerErrors(t *testing.T) {
	// A handler that always errors on a legitimate Button click → DispatchDenied.
	s := NewSession(mustDecodeNode(t, counterTreeJSON), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		return nil, errDenied
	})
	_, reject := s.Step(clickInc("c1", 0))
	if reject == nil || reject.Reason != ReasonDispatchDenied {
		t.Fatalf("expected DispatchDenied, got %+v", reject)
	}
}

func TestStepRejectsInapplicableOp(t *testing.T) {
	// A handler producing an op targeting a node that does not exist → the
	// apply engine refuses → the driver rejects (no partial mutation, no panic).
	s := NewSession(mustDecodeNode(t, counterTreeJSON), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		return []wire.Obj{{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("ghost")}}}, nil
	})
	_, reject := s.Step(clickInc("c1", 0))
	if reject == nil || reject.Reason != ReasonDispatchDenied {
		t.Fatalf("expected DispatchDenied for an inapplicable op, got %+v", reject)
	}
}

func TestStepEmptyOpsIsNoOp(t *testing.T) {
	s := NewSession(mustDecodeNode(t, counterTreeJSON), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		return nil, nil // legitimate no-op
	})
	opsList, reject := s.Step(clickInc("c1", 0))
	if reject != nil || len(opsList) != 0 {
		t.Fatalf("expected an empty no-op step, got ops=%v reject=%+v", opsList, reject)
	}
}

var errDenied = &denyError{}

type denyError struct{}

func (*denyError) Error() string { return "denied by policy" }

// ── The interaction hand-off, pinned (Phase 869) ─────────────────────────────

// The README's "Server-driven hand-off" section makes two claims that are only
// worth making if something checks them. A doc asserting a design commitment
// that the code stopped honouring is worse than no doc, because it is the thing
// a reader consults instead of reading the code.
//
// Claim one: the declarative grid vocabulary's interactive verbs — sort, page,
// commit an edit — do not reach this driver at all. `DataGrid` is absent from
// the event-legitimacy table (it is absent from the reference driver's table
// too), so those events are refused default-deny BEFORE any handler runs. A
// phase that quietly routed a page cursor through here would have to add the
// entry, and this test is what makes that addition a conversation.
func TestGridInteractionNeverReachesTheDriver(t *testing.T) {
	const grid = `{"id":"g","kind":{"$type":"DataGrid","columns":[{"field":"name","format":{"$type":"None"},` +
		`"kind":{"$type":"Text"},"label":"Name","sortable":true,"width":{"$type":"Auto"}}],` +
		`"editable":false,"source":{"$type":"State","key":"rows"},"sortStateKey":"grid-sort"}}`

	handlerRan := false
	s := NewSession(mustDecodeNode(t, grid), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		handlerRan = true
		return nil, nil
	})
	before, err := wire.EncodeNode(s.Tree())
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}

	for _, event := range []string{"click", "change", "input", "sort", "page", "commit"} {
		opsList, reject := s.Step(Event{NodeID: "g", Event: event})
		if reject == nil {
			t.Fatalf("event %q on a DataGrid was accepted; the driver holds no grid interaction", event)
		}
		if reject.Reason != ReasonIllegitimateEvent {
			t.Errorf("event %q: reason = %s, want %s", event, reject.Reason, ReasonIllegitimateEvent)
		}
		if len(opsList) != 0 {
			t.Errorf("event %q produced %d ops on a refusal", event, len(opsList))
		}
	}
	if handlerRan {
		t.Error("the host handler ran for a grid event — legitimacy is checked BEFORE dispatch")
	}
	after, err := wire.EncodeNode(s.Tree())
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	if after != before {
		t.Errorf("a refused event advanced the tree:\n got %s\nwant %s", after, before)
	}
}

// Claim two: a Form's events DO reach the driver, and the driver decides nothing
// about them. A declared field `rule` is carried, and enforcement is the host
// handler's — this driver runs no field validation, which is a named divergence
// from the reference driver rather than an oversight. Pinned in the direction
// that would break silently: a submit violating the declared rule is accepted
// here, because nothing in this driver is looking.
func TestFormFieldRuleIsNotEnforcedByTheDriver(t *testing.T) {
	const form = `{"id":"f","kind":{"$type":"Form","fields":[{"id":"code","kind":{"$type":"Text",` +
		`"value":{"$type":"State","key":"code"}},"label":"Code","required":true,` +
		`"rule":{"minLength":8}}],"onSubmit":{"$type":"Chain","ops":[]},"submitLabel":"Send"}}`

	saw := ""
	s := NewSession(mustDecodeNode(t, form), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		saw = ev.Event
		return nil, nil
	})
	// "x" is one character against a declared minLength of 8.
	if _, reject := s.Step(Event{NodeID: "f", Event: "submit", Payload: `{"code":"x"}`}); reject != nil {
		t.Fatalf("form submit refused: %+v — the driver enforces no rule, so this must reach the handler", reject)
	}
	if saw != "submit" {
		t.Errorf("handler saw %q, want \"submit\" — the rule decision is the host's", saw)
	}
}
