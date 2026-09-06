package serverdriven

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// A tree whose root Box holds a Button. The handler inserts a Mount declaring
// capabilities; the gate decides whether the mount reaches the tree.
const gateTreeJSON = `{"id":"root","kind":{"$type":"Box","children":[` +
	`{"id":"inc","kind":{"$type":"Button","label":"+","onClick":{"$type":"Navigate","route":"/noop"},"variant":"Primary"}}` +
	`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`

// mountOp is an InsertChild introducing a Mount that declares the given
// capabilities — the shape the gate exists to vet.
func mountOp(t *testing.T, capabilities ...string) wire.Obj {
	t.Helper()
	quoted := make([]string, len(capabilities))
	for i, c := range capabilities {
		quoted[i] = `"` + c + `"`
	}
	childJSON := `{"id":"guest","kind":{"$type":"Mount","capabilities":[` +
		strings.Join(quoted, ",") +
		`],"channel":{"direction":"OutOnly"},"onBubble":"noop","scopeId":"s1"}}`
	opJSON := `{"$type":"InsertChild","child":` + childJSON + `,"parentId":"root"}`
	op, err := wire.DecodeOp(opJSON)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	return op
}

func gateSession(t *testing.T, op wire.Obj) *Session {
	t.Helper()
	return NewSession(mustDecodeNode(t, gateTreeJSON), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		return []wire.Obj{op}, nil
	})
}

func TestGateDeniesAnUngrantedMount(t *testing.T) {
	// The Go driver applied whatever the handler returned. A handler that
	// computes an op from client-supplied values could therefore mount a guest
	// declaring capabilities the host never granted, and the sibling Rust host
	// refused exactly this.
	session := gateSession(t, mountOp(t, "net.fetch", "storage.write")).
		WithCapabilityGate(NewCapabilityGate("storage.write"))

	ops, reject := session.Step(Event{NodeID: "inc", Event: "click"})

	if reject == nil {
		t.Fatalf("ungranted mount was applied: %d ops", len(ops))
	}
	if reject.Reason != ReasonCapabilityDenied {
		t.Errorf("reason = %q, want %q", reject.Reason, ReasonCapabilityDenied)
	}
	if len(reject.MissingCapabilities) != 1 || reject.MissingCapabilities[0] != "net.fetch" {
		t.Errorf("missing = %v, want exactly [net.fetch] — the granted one must not be named",
			reject.MissingCapabilities)
	}
	// The tree is untouched: a refused step mutates nothing.
	if _, found := findNode(session.Tree(), "guest"); found {
		t.Error("the refused mount reached the tree")
	}
}

func TestGateAllowsAFullyGrantedMount(t *testing.T) {
	// A gate that denied everything would be a liveness bug wearing a security
	// fix's clothes.
	session := gateSession(t, mountOp(t, "net.fetch")).
		WithCapabilityGate(NewCapabilityGate("net.fetch"))

	ops, reject := session.Step(Event{NodeID: "inc", Event: "click"})
	if reject != nil {
		t.Fatalf("granted mount refused: %+v", reject)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %d, want 1", len(ops))
	}
	if _, found := findNode(session.Tree(), "guest"); !found {
		t.Error("the granted mount did not reach the tree")
	}
}

func TestGateVetsInsideABatch(t *testing.T) {
	// Batch is where a gate that only looked at the top-level op would be
	// bypassed by one extra level of nesting.
	inner := mountOp(t, "net.fetch")
	batch := wire.Obj{Tag: "Batch", Fields: map[string]wire.Value{
		"ops": wire.Arr{inner},
	}}
	session := gateSession(t, batch).WithCapabilityGate(NewCapabilityGate())

	_, reject := session.Step(Event{NodeID: "inc", Event: "click"})
	if reject == nil || reject.Reason != ReasonCapabilityDenied {
		t.Fatalf("a Batch-wrapped ungranted mount was not refused: %+v", reject)
	}
}

func TestNoGateLeavesBehaviourUnchanged(t *testing.T) {
	// The gate is opt-in on this host: a session built without one behaves as
	// it always has, so turning it on stays a deliberate act rather than a
	// silent runtime break for every shipped host.
	session := gateSession(t, mountOp(t, "net.fetch"))

	_, reject := session.Step(Event{NodeID: "inc", Event: "click"})
	if reject != nil {
		t.Fatalf("a gate-less session refused: %+v", reject)
	}
}

func TestAuditMountsNamesEachMountsVerdict(t *testing.T) {
	tree := mustDecodeNode(t, `{"id":"root","kind":{"$type":"Box","children":[`+
		`{"id":"a","kind":{"$type":"Mount","capabilities":["net.fetch"],"channel":{"direction":"OutOnly"},"onBubble":"n","scopeId":"s1"}},`+
		`{"id":"b","kind":{"$type":"Mount","capabilities":["storage.write"],"channel":{"direction":"OutOnly"},"onBubble":"n","scopeId":"s2"}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`)

	decisions := NewCapabilityGate("net.fetch").AuditMounts(tree)
	if len(decisions) != 2 {
		t.Fatalf("decisions = %d, want 2", len(decisions))
	}
	if decisions[0].NodeID != "a" || !decisions[0].Allowed() {
		t.Errorf("mount a = %+v, want allowed", decisions[0])
	}
	if decisions[1].NodeID != "b" || decisions[1].Allowed() {
		t.Errorf("mount b = %+v, want denied", decisions[1])
	}
}

// ── The recover boundary ───────────────────────────────────────────────────

func TestPanickingHandlerIsRefusedNotPropagated(t *testing.T) {
	// Step's doc said "Never panics" while a panicking handler propagated —
	// on the WebSocket transport, straight out of a goroutine net/http does
	// not recover, ending the process.
	session := NewSession(mustDecodeNode(t, gateTreeJSON), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		panic("the host handler dereferenced something it should not have")
	})

	var recoveredNode string
	var recoveredValue any
	session.WithOnHandlerPanic(func(nodeID string, r any) {
		recoveredNode, recoveredValue = nodeID, r
	})

	ops, reject := session.Step(Event{NodeID: "inc", Event: "click"})

	if reject == nil {
		t.Fatalf("a panicking handler produced %d ops and no reject", len(ops))
	}
	if reject.Reason != ReasonHandlerPanicked {
		t.Errorf("reason = %q, want %q", reject.Reason, ReasonHandlerPanicked)
	}
	// The panic value goes to the HOST, not into a message bound for a client:
	// a panic string can carry whatever the handler was holding.
	if strings.Contains(reject.Message, "dereferenced") {
		t.Error("the reject message leaked the panic value to the client")
	}
	if recoveredNode != "inc" || recoveredValue == nil {
		t.Errorf("panic sink got (%q, %v), want the node id and the recovered value",
			recoveredNode, recoveredValue)
	}
	// The connection survives and the tree is untouched.
	if _, found := findNode(session.Tree(), "guest"); found {
		t.Error("the tree advanced through a panicking handler")
	}
}

func TestPanickingHandlerWithNoSinkStillRefuses(t *testing.T) {
	// The recover boundary must not depend on the host having registered a
	// sink — the default path is the one that carries the process.
	session := NewSession(mustDecodeNode(t, gateTreeJSON), func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		panic("boom")
	})
	_, reject := session.Step(Event{NodeID: "inc", Event: "click"})
	if reject == nil || reject.Reason != ReasonHandlerPanicked {
		t.Fatalf("reject = %+v, want a HandlerPanicked refusal", reject)
	}
}
