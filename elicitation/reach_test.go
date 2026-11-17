package elicitation

import "testing"

// The CONTRACT_UNKNOWN_NODE membership check collects the tree's ids through
// every position a node holds another in (the keyed reach node lookup runs
// on). It used to stop short of slot arguments and the envelope fallback, so a
// contract naming a node held in a Mount's slot input or a fallback was refused
// as naming no node at all.
func TestContractMayNameANodeHeldInASlotInputOrAFallback(t *testing.T) {
	tree := `{"id":"r","kind":{"$type":"Box","children":[` +
		`{"id":"m","kind":{"$type":"Mount","capabilities":[],"channel":{"direction":"OutOnly"},` +
		`"inputs":{"seed":{"$type":"SlotArg","tree":{"id":"seed","kind":{"$type":"Markdown","text":"s"}}}},"scopeId":"g"}},` +
		`{"fallback":{"id":"fb","kind":{"$type":"Markdown","text":"f"}},"id":"a","kind":{"$type":"Markdown","text":"a"}}` +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
	for _, nodeID := range []string{"seed", "fb"} {
		doc := `{"$elicitation":"1","contract":{"fields":[{"name":"choice","nodeId":"` + nodeID +
			`","required":true,"space":{"$type":"anyString"},"stateKey":"choice"}]},"id":"elc","tree":` + tree + `}`
		if _, err := DecodeElicitation(doc); err != nil {
			t.Errorf("a contract naming %q: %v", nodeID, err)
		}
	}
}
