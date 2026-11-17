package serverdriven

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The driver resolves an event's node, and the capability gate walks a driven
// op's introduced nodes, through every position a node holds another in (the
// keyed reach the apply engine resolves through). Both used to stop short of
// slot arguments and the envelope fallback.

const flexJSON = `"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"`

// An event on a Button held in a node's envelope fallback names a node the
// tree holds; it was refused UnknownNode.
func TestAnEventOnANodeHeldInAFallbackReachesTheHandler(t *testing.T) {
	tree := `{"id":"root","kind":{"$type":"Box","children":[` +
		`{"fallback":{"id":"inc","kind":{"$type":"Button","label":"+","onClick":{"$type":"Navigate","route":"/noop"},"variant":"Primary"}},` +
		`"id":"md","kind":{"$type":"Markdown","text":"m"}}` +
		`],` + flexJSON + `}}`
	handled := false
	session := NewSession(mustDecodeNode(t, tree), func(wire.Node, Event) ([]wire.Obj, error) {
		handled = true
		return nil, nil
	})
	if _, reject := session.Step(Event{NodeID: "inc", Event: "click"}); reject != nil {
		t.Fatalf("event refused: %+v", reject)
	}
	if !handled {
		t.Error("the handler did not run")
	}
}

// A Mount carried in another Mount's slot input is introduced into the tree as
// surely as a child is, so the gate vets it; it used to pass unvetted.
func TestGateVetsAMountCarriedInASlotInput(t *testing.T) {
	child := `{"id":"outer","kind":{"$type":"Mount","capabilities":["notify"],"channel":{"direction":"OutOnly"},` +
		`"inputs":{"seed":{"$type":"SlotArg","tree":{"id":"guest","kind":{"$type":"Mount","capabilities":["net.fetch"],` +
		`"channel":{"direction":"OutOnly"},"scopeId":"inner"}}}},"scopeId":"outer"}}`
	op, err := wire.DecodeOp(`{"$type":"InsertChild","child":` + child + `,"parentId":"root"}`)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	session := gateSession(t, op).WithCapabilityGate(NewCapabilityGate("notify"))
	_, reject := session.Step(Event{NodeID: "inc", Event: "click"})
	if reject == nil {
		t.Fatal("a mount carried in a slot input passed the gate unvetted")
	}
	if reject.Reason != ReasonCapabilityDenied || len(reject.MissingCapabilities) != 1 || reject.MissingCapabilities[0] != "net.fetch" {
		t.Errorf("reject = %+v, want CapabilityDenied naming exactly net.fetch", reject)
	}
}
