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
// A style change is the one granular facet (Phase 2314, the reference engine's
// rule since Phase 2291): when a matched node differs from its counterpart in
// its style and nothing else of its own, the diff emits the style edit —
// PatchStyle naming exactly the members that moved, the whole-record
// UpdateStyle only when all six did — instead of the replace, and a layout
// node's children are then recursed as usual. Every host's diff records a
// restyle as the same op, so two restyles of different members of one node
// carry disjoint member sets wherever they were recorded.
//
// The recursion descends through the kinds the apply engine addresses
// structurally — the kinds whose generated idl.json table carries an ordered
// list-of-nodes slot (idl.OrderedChildren, the one set ops reads too, so a
// script emitted against a parent always applies) — less the two named in
// recursionBounded below. A granular kind-field refinement pass (matching the
// Python producer's UpdateProp/UpdateState op choices) is a possible
// follow-up.
package diff

import (
	"github.com/fuaran-ui/fuaran-go/wire"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// recursionBounded are the kinds with an ordered child list that this diff
// does NOT recurse through: a difference inside one escalates to a whole-node
// replace at the kind's own seat. It is a granularity choice, not a
// correctness one — the apply engine addresses both kinds' children, so a
// recursing script would apply too — and it is kept only because the corpus
// pins this host's output for it: diff/goldens.json's divergent entries
// modal-interior-change and scrollarea-interior-change record the seat-level
// replace under hosts["go"]. Lifting it is a regeneration of those two entries
// and the deletion of this set.
var recursionBounded = map[string]bool{"Modal": true, "ScrollArea": true}

// structuralChildren returns a layout node's ordered child list, or
// (nil, false) for a kind this diff does not recurse through.
func structuralChildren(n wire.Node) ([]wire.Node, bool) {
	slot, ok := idl.OrderedChildren(n.Kind.Tag)
	if !ok || recursionBounded[n.Kind.Tag] {
		return nil, false
	}
	arr, ok := n.Kind.Fields[slot].(wire.Arr)
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
	slot, _ := idl.OrderedChildren(n.Kind.Tag)
	fields[slot] = wire.Arr{}
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

// withStyleOf returns n carrying src's style slot (absent when src's is), so
// comparing it against src isolates every difference that is NOT the style.
func withStyleOf(n, src wire.Node) wire.Node {
	extras := make(map[string]wire.Value, len(n.Extras)+1)
	for k, v := range n.Extras {
		extras[k] = v
	}
	if style, ok := src.Extras["style"]; ok {
		extras["style"] = style
	} else {
		delete(extras, "style")
	}
	return wire.Node{ID: n.ID, Kind: n.Kind, Extras: extras}
}

// styleRecord is a node's whole style record with every member materialised:
// an absent slot, or a member omitted at its identity, reads as that member's
// default token (WIRE_FORMAT §3.1/§3.6).
func styleRecord(n wire.Node) map[string]wire.Value {
	identity := wire.StyleIdentity()
	present, _ := n.Extras["style"].(wire.Obj)
	record := make(map[string]wire.Value, len(identity))
	for member, def := range identity {
		if v, ok := present.Fields[member]; ok {
			record[member] = v
		} else {
			record[member] = wire.Str(def)
		}
	}
	return record
}

func sameValue(a, b wire.Value) bool {
	ea, errA := wire.EncodeValue(a)
	eb, errB := wire.EncodeValue(b)
	return errA == nil && errB == nil && ea == eb
}

// styleEdit is the op taking a's style to b's: none when the records are
// equal; PatchStyle naming exactly the members that moved (a member returning
// to its default is spelled with the default token — in a delta that is an
// edit, not an omission); and the whole-record UpdateStyle only when every
// member moved.
func styleEdit(a, b wire.Node) []wire.Obj {
	before, after := styleRecord(a), styleRecord(b)
	moved := make(map[string]wire.Value, len(after))
	for member, v := range after {
		if !sameValue(before[member], v) {
			moved[member] = v
		}
	}
	switch len(moved) {
	case 0:
		return nil
	case len(after):
		identity := wire.StyleIdentity()
		record := make(map[string]wire.Value, len(after))
		for member, v := range after {
			if s, ok := v.(wire.Str); !ok || string(s) != identity[member] {
				record[member] = v
			}
		}
		return []wire.Obj{styleOp("UpdateStyle", b.ID, record)}
	default:
		return []wire.Obj{styleOp("PatchStyle", b.ID, moved)}
	}
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

func styleOp(tag, target string, style map[string]wire.Value) wire.Obj {
	return wire.Obj{Tag: tag, Fields: map[string]wire.Value{
		"target": wire.Str(target),
		"style":  wire.Obj{Fields: style},
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
	// a restyled to b's style: what still differs from b is everything else.
	restyled := withStyleOf(a, b)
	if encode(restyled) == encode(b) {
		*script = append(*script, styleEdit(a, b)...) // the style is the only difference
		return
	}
	ak, aOK := structuralChildren(a)
	bk, bOK := structuralChildren(b)
	// Recurse only when both sides are layout kinds, their own content is
	// unchanged but for the style, and their children line up id-for-id in
	// order — every other difference (kind change, state drift, insert / remove
	// / reorder) escalates to a whole-node replace at this seat.
	if aOK && bOK && ownContentSame(restyled, b) && sameChildIDs(ak, bk) {
		*script = append(*script, styleEdit(a, b)...)
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
