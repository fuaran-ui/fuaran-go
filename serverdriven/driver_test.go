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
	// The server tree advanced: the metric source is now Static 1.
	metric, ok := findNode(s.Tree(), "count")
	if !ok {
		t.Fatal("count node missing after step")
	}
	src, ok := metric.Kind.Fields["source"].(wire.Obj)
	if !ok || src.Tag != "Static" {
		t.Fatalf("source not a Static binding: %+v", metric.Kind.Fields["source"])
	}
	if v, ok := src.Fields["value"].(wire.Int); !ok || v != 1 {
		t.Errorf("metric source = %v, want Static 1", src.Fields["value"])
	}

	// A second click advances to 2 — state persists across steps.
	if _, reject := s.Step(clickInc("c1", 1)); reject != nil {
		t.Fatalf("second click rejected: %+v", reject)
	}
	metric, _ = findNode(s.Tree(), "count")
	src, _ = metric.Kind.Fields["source"].(wire.Obj)
	if v, _ := src.Fields["value"].(wire.Int); v != 2 {
		t.Errorf("after two clicks source = %v, want 2", src.Fields["value"])
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
