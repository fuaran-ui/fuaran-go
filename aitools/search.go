package aitools

import "github.com/fuaran-ui/fuaran-go/wire"

// Structural search + assertions over a decoded tree — the substrate for Grep
// Your Apps (query matched nodes), the Pattern Bank (structural search), and
// Unit-Test Your UI (assert the living structure). Search matches structure,
// not pixels, so a restyle leaves the results unchanged.

// Search returns every node satisfying predicate, depth-first.
func Search(tree wire.Node, predicate func(wire.Node) bool) []wire.Node {
	var out []wire.Node
	for _, node := range WalkNodes(tree) {
		if predicate(node) {
			out = append(out, node)
		}
	}
	return out
}

// FindByKind returns every node whose kind discriminator equals kind.
func FindByKind(tree wire.Node, kind string) []wire.Node {
	return Search(tree, func(n wire.Node) bool { return n.Kind.Tag == kind })
}

// CountByKind returns a kind → count histogram over the whole tree (the
// grep-style aggregation the Grep demo runs).
func CountByKind(tree wire.Node) map[string]int {
	counts := make(map[string]int)
	for _, node := range WalkNodes(tree) {
		counts[KindName(node)]++
	}
	return counts
}

// Assertion is a single structural check's outcome (Unit-Test): the result +
// a human/AI-readable reason.
type Assertion struct {
	OK     bool
	Reason string
}

// AssertExists asserts a node with id is present.
func AssertExists(tree wire.Node, id string) Assertion {
	if _, ok := FindNode(tree, id); ok {
		return Assertion{OK: true, Reason: "node '" + id + "' exists"}
	}
	return Assertion{OK: false, Reason: "node '" + id + "' not found"}
}

// AssertKind asserts a node with id exists and carries the given kind.
func AssertKind(tree wire.Node, id, kind string) Assertion {
	node, ok := FindNode(tree, id)
	if !ok {
		return Assertion{OK: false, Reason: "node '" + id + "' not found"}
	}
	if node.Kind.Tag != kind {
		return Assertion{OK: false, Reason: "node '" + id + "' is a " + KindName(node) + ", not a " + kind}
	}
	return Assertion{OK: true, Reason: "node '" + id + "' is a " + kind}
}

// AssertBound asserts a node with id carries a binding whose source matches
// (e.g. "State", "Query") — the "this control is wired to $state.x" check.
func AssertBound(tree wire.Node, id, source string) Assertion {
	node, ok := FindNode(tree, id)
	if !ok {
		return Assertion{OK: false, Reason: "node '" + id + "' not found"}
	}
	for _, slot := range BindingSlots(node) {
		if slot.Source == source {
			return Assertion{OK: true, Reason: "node '" + id + "' has a " + source + " binding at " + slot.Slot}
		}
	}
	return Assertion{OK: false, Reason: "node '" + id + "' has no " + source + " binding"}
}
