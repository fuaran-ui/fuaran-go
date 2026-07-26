// Package diff is the structural tree diff → op-script (the What-If mechanic):
// given a *before* and an *after* Node tree, it derives the canonical TreeOp
// script that transforms one into the other, so a "what if I changed this?"
// preview is a real, replayable edit rather than a re-render. The script is
// correct by construction — the round-trip law
//
//	Apply(Diff(a, b), a) == b
//
// holds for every pair under canonical-JSON equality (certified in
// diff_test.go and the conformance package). Diff is the producer half of the
// pair whose consumer is the ops.Apply engine; a Go service produces a script,
// any conformant host replays it.
//
// This is a sibling implementation of the reference engines, built to match
// their op choices where they are deterministic — it mirrors the Rust
// producer (fuaran-rs src/diff): recurse through structurally-matched children
// and, at the shallowest node that actually differs, emit a whole-node
// replacement — ReplaceRoot at the root, else a RemoveNode + InsertChild at the
// node's own seat (which captures every field: kind, style, state,
// accessibility). It favours a localised edit over replacing the whole tree,
// but never at the cost of correctness.
//
// Granularity divergence from the Rust producer (documented, correctness-
// neutral): the recursion descends only through the kinds the Go apply engine
// addresses structurally — the six layout kinds in layoutKinds below, matching
// ops.layoutKinds. The Rust producer additionally recurses through Modal and
// ScrollArea; the Go apply engine has no structural-child surface for those, so
// a difference inside one escalates here to a whole-node replace at the nearest
// layout ancestor (or ReplaceRoot). The emitted script stays applyable by every
// conformant host and still satisfies the round-trip law; only the op count can
// be coarser. A granular-field refinement pass (matching the Python producer's
// UpdateProp/UpdateStyle/UpdateState op choices) is a possible follow-up.
package diff

import (
	"github.com/fuaran-ui/fuaran-go/wire"
)

// layoutKinds are the kinds carrying an ordered "children" array — the only
// kinds the structural child ops (InsertChild / RemoveNode) address, and the
// only kinds this diff recurses through. It MUST stay in step with
// ops/traverse.go's layoutKinds (the apply engine's structural-child set); a
// script emitted against a parent outside this set would not apply.
var layoutKinds = map[string]bool{
	"Box":         true,
	"SplitPanel":  true,
	"Tabs":        true,
	"Stepper":     true,
	"SummaryList": true,
	"Disclosure":  true,
}

// structuralChildren returns a layout node's ordered child list, or
// (nil, false) for a kind with no addressable structural children.
func structuralChildren(n wire.Node) ([]wire.Node, bool) {
	if !layoutKinds[n.Kind.Tag] {
		return nil, false
	}
	arr, ok := n.Kind.Fields["children"].(wire.Arr)
	if !ok {
		return nil, false
	}
	children := make([]wire.Node, 0, len(arr))
	for _, item := range arr {
		if child, ok := item.(wire.Node); ok {
			children = append(children, child)
		}
	}
	return children, true
}

// stripStructuralChildren returns a clone of n with its structural child list
// emptied — for comparing a node's own content (kind-minus-children, plus the
// style/state/accessibility extras) independently of what its children became.
func stripStructuralChildren(n wire.Node) wire.Node {
	if _, ok := structuralChildren(n); !ok {
		return n
	}
	fields := make(map[string]wire.Value, len(n.Kind.Fields))
	for k, v := range n.Kind.Fields {
		fields[k] = v
	}
	fields["children"] = wire.Arr{}
	return wire.Node{ID: n.ID, Kind: wire.Obj{Tag: n.Kind.Tag, Fields: fields}, Extras: n.Extras}
}

func encode(n wire.Node) string {
	// EncodeNode never errors for a decoded/constructed Node (the canonical
	// encoder is total over the structural model); the string is the equality
	// oracle the whole wire format commits to.
	s, _ := wire.EncodeNode(n)
	return s
}

func sameChildIDs(a, b []wire.Node) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func ownContentSame(a, b wire.Node) bool {
	return encode(stripStructuralChildren(a)) == encode(stripStructuralChildren(b))
}

// ── op constructors (the decoded wire.Obj form ops.Apply consumes) ───────────

func replaceRoot(node wire.Node) wire.Obj {
	return wire.Obj{Tag: "ReplaceRoot", Fields: map[string]wire.Value{"node": node}}
}

func removeNode(target string) wire.Obj {
	return wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str(target)}}
}

func insertChild(parentID string, child wire.Node) wire.Obj {
	return wire.Obj{Tag: "InsertChild", Fields: map[string]wire.Value{
		"parentId": wire.Str(parentID),
		"child":    child,
	}}
}

func reorderChildren(parentID string, ids []string) wire.Obj {
	arr := make(wire.Arr, 0, len(ids))
	for _, id := range ids {
		arr = append(arr, wire.Str(id))
	}
	return wire.Obj{Tag: "ReorderChildren", Fields: map[string]wire.Value{
		"parentId": wire.Str(parentID),
		"newOrder": arr,
	}}
}

// seat is a node's place in its parent's structural child list — the target of
// a RemoveNode + InsertChild whole-node replace. siblingIDs is the parent's
// full ordered child id list, needed because 0.4.0's InsertChild APPENDS: the
// re-inserted node lands last, so the original order has to be restated.
type seat struct {
	parentID   string
	position   int
	siblingIDs []string
}

// diffNode recurses a pair of nodes sharing an id. parent, when non-nil, is the
// node's seat in its parent (the RemoveNode+InsertChild target); a nil parent
// marks the root (a ReplaceRoot).
func diffNode(a, b wire.Node, parent *seat, script *[]wire.Obj) {
	if encode(a) == encode(b) {
		return // identical subtree — nothing to do
	}
	ak, aOK := structuralChildren(a)
	bk, bOK := structuralChildren(b)
	// Recurse only when both sides are layout kinds, their own content is
	// unchanged, and their children line up id-for-id in order — every other
	// difference (kind change, style/state drift, insert / remove / reorder)
	// escalates to a whole-node replace at this seat.
	if aOK && bOK && ownContentSame(a, b) && sameChildIDs(ak, bk) {
		for i := range ak {
			child := seat{parentID: a.ID, position: i, siblingIDs: childIDs(bk)}
			diffNode(ak[i], bk[i], &child, script)
		}
		return
	}
	if parent == nil {
		*script = append(*script, replaceRoot(b))
		return
	}
	// Remove the old node and re-insert it (same id, so no duplicate-id
	// collision). 0.4.0's InsertChild appends, so unless the seat was already
	// last, restate the parent's order — an exact permutation, since the
	// remove+insert pair leaves the id SET unchanged.
	*script = append(*script, removeNode(a.ID))
	*script = append(*script, insertChild(parent.parentID, b))
	if parent.position != len(parent.siblingIDs)-1 {
		*script = append(*script, reorderChildren(parent.parentID, parent.siblingIDs))
	}
}

// Diff derives the TreeOp script (as decoded wire.Obj ops) transforming before
// into after. The result is empty when the trees are canonically identical.
// When the roots carry different ids the whole tree is replaced (ReplaceRoot —
// the only op that may change the root id); otherwise the diff localises to the
// changed nodes. Guarantee: Apply(Diff(before, after), before) == after under
// canonical-JSON equality.
func Diff(before, after wire.Node) []wire.Obj {
	if before.ID != after.ID {
		return []wire.Obj{replaceRoot(after)}
	}
	var script []wire.Obj
	diffNode(before, after, nil, &script)
	return script
}

// DiffBatched is Diff wrapped in a single atomic Batch op when it produced two
// or more ops (an all-or-nothing apply); a zero- or one-op script is returned
// unwrapped. Mirrors the sibling producers' batched entry point.
func DiffBatched(before, after wire.Node) []wire.Obj {
	script := Diff(before, after)
	if len(script) >= 2 {
		inner := make(wire.Arr, len(script))
		for i, op := range script {
			inner[i] = op
		}
		return []wire.Obj{{Tag: "Batch", Fields: map[string]wire.Value{"ops": inner}}}
	}
	return script
}

// childIDs is the ordered id list of a structural child slice — the exact
// permutation a ReorderChildren must name.
func childIDs(children []wire.Node) []string {
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, c.ID)
	}
	return ids
}
