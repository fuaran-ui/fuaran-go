package aitools

import "testing"

// Introspection reads two reaches, as the reference's does: a node's children
// are what its own KIND holds (ChildNodes, WalkNodes and the searches over
// them), while FindNode resolves through every position, like the apply
// engine. ChildNodes used to walk the kind reflectively, reaching a Mount's
// slot inputs, and FindNode ran over that walk, missing the state
// alternatives and the envelope fallback.

const reachJSON = `{"id":"r","kind":{"$type":"Box","children":[` +
	`{"id":"m","kind":{"$type":"Mount","capabilities":[],"channel":{"direction":"OutOnly"},` +
	`"inputs":{"seed":{"$type":"SlotArg","tree":{"id":"seed","kind":{"$type":"Markdown","text":"s"}}}},"scopeId":"g"}},` +
	`{"fallback":{"id":"fb","kind":{"$type":"Markdown","text":"f"}},"id":"a","kind":{"$type":"Markdown","text":"a"},` +
	`"state":{"onEmpty":{"id":"empty","kind":{"$type":"Markdown","text":"e"}}}}` +
	`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`

func TestFindNodeReachesTheStateAlternativesAndTheFallback(t *testing.T) {
	tree := decode(t, reachJSON)
	for _, id := range []string{"empty", "fb", "seed"} {
		if _, ok := FindNode(tree, id); !ok {
			t.Errorf("FindNode(%q) = not found", id)
		}
	}
}

func TestChildNodesAreWhatTheKindHolds(t *testing.T) {
	tree := decode(t, reachJSON)
	m, _ := FindNode(tree, "m")
	if got := ChildIDs(m); len(got) != 0 {
		t.Errorf("ChildIDs(Mount) = %v: a slot input belongs to the scope it is passed into, not to the Mount", got)
	}
	if got := ChildIDs(tree); len(got) != 2 || got[0] != "m" || got[1] != "a" {
		t.Errorf("ChildIDs(root) = %v, want [m a]", got)
	}
}
