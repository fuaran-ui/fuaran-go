package ops

import (
	"sort"
	"strconv"

	"github.com/fuaran-ui/fuaran-go/wire"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// What a node's children are — the ONE enumeration of the parent/child
// relation in this host, with the reach a caller wants stated rather than
// re-derived.
//
// The relation used to be written by hand in every walker that needed it, and
// the copies disagreed: the apply engine's walk never reached the envelope
// `fallback`, so an op addressed to a node held there was refused NodeNotFound
// and the §21 bounds did not count it; the elicitation and server-driven
// lookups stopped short of slot arguments and the envelope `fallback`; the
// renderer's fragment registry read an ErrorBoundary's child but not its
// fallback, nor the state alternatives, nor a fragment body; and the
// introspection walk reached slot arguments, which the reference's does not.
//
// So this file names the questions. Every position a node holds another node
// in has a CLASS (Position), and a caller asks for the classes it means
// (Reach). Which kind fields hold nodes, and how, is read from the generated
// IDL tables (idl.NodeSlots); the envelope's two positions are the only ones
// stated here, because they sit on the node rather than in a kind's table.

// Position is the kind of place one node holds another in.
type Position uint8

const (
	// Ordered is a kind's ordered child list — the surface the structural ops
	// (InsertChild / RemoveNode / MoveNode / ReorderChildren) edit.
	Ordered Position = iota + 1
	// Arm is a node the KIND holds in a named arm: ErrorBoundary's child and
	// fallback, every Switch case child and its default, a FragmentDecl's body.
	// (The reference host classes the body as Ordered; this host's structural
	// ops address list slots only, so here it is an arm.)
	Arm
	// StateArm is the envelope's state.onLoading / state.onEmpty, which render
	// INSTEAD of the node.
	StateArm
	// Fallback is the envelope's author-declared `fallback`, which a reader
	// that cannot render the node renders instead.
	Fallback
	// Argument is a subtree passed as an argument: a FragmentRef's slot
	// arguments and a Mount's slot inputs.
	Argument
)

// Reach is the set of position classes a walk descends through.
type Reach uint8

// ReachOf is the reach over exactly the given positions.
func ReachOf(positions ...Position) Reach {
	var r Reach
	for _, p := range positions {
		r |= 1 << p
	}
	return r
}

// Has reports whether the reach descends through p.
func (r Reach) Has(p Position) bool { return r&(1<<p) != 0 }

// The named reaches — the reference host's. A walk passes one explicitly; there
// is no default.
var (
	// ReachStructural is the ordered child lists alone — what the structural
	// ops edit, and what the diff recurses through.
	ReachStructural = ReachOf(Ordered)
	// ReachKindHeld is every node the node's own KIND holds: the ordered lists
	// plus the arms. It leaves out the envelope alternatives (which render
	// instead of the node) and slot arguments (which belong to the scope they
	// are passed into). The island walk and the introspection walk.
	ReachKindHeld = ReachOf(Ordered, Arm)
	// ReachFragmentScope is the kind-held positions plus the two state
	// alternatives: what the fragment registry walk descends through.
	ReachFragmentScope = ReachOf(Ordered, Arm, StateArm)
	// ReachKeyed is every position a node holds another node in: the surface
	// id uniqueness, node lookup, the apply engine's id checks, the merge
	// footprint and the §21 bounds quantify over.
	ReachKeyed = ReachOf(Ordered, Arm, StateArm, Fallback, Argument)
)

// envelopeSlots is the node envelope's node-holding positions, in the
// canonical order (the loading alternative, the empty one, then the declared
// fallback). They are the IDL's NodeFields `fallback` and the node members of
// its `state` record; children_test.go holds this list to that table.
var envelopeSlots = []struct {
	member, inner string
	position      Position
}{
	{"state", "onLoading", StateArm},
	{"state", "onEmpty", StateArm},
	{"fallback", "", Fallback},
}

// slotLoc says where one held node sits under its parent, so a rebuild can put
// a replacement back in exactly that place.
type slotLoc struct {
	envelope bool
	field    string        // the kind field, or the envelope member
	shape    idl.SlotShape // kind slots only
	index    int           // SlotList / SlotRecords
	key      string        // SlotArgs
	inner    string        // SlotRecords / SlotArgs field, or the state member
}

// Slot is one position a node holds another node in.
type Slot struct {
	Position Position
	Node     wire.Node
	loc      slotLoc
}

// Path is the slot's location under its node as wire member names and
// indices: [kind children 0], [state onLoading], [fallback],
// [kind cases 0 child], [kind args header tree].
func (s Slot) Path() []string {
	l := s.loc
	switch {
	case l.envelope && l.inner != "":
		return []string{l.field, l.inner}
	case l.envelope:
		return []string{l.field}
	}
	switch l.shape {
	case idl.SlotList:
		return []string{"kind", l.field, strconv.Itoa(l.index)}
	case idl.SlotRecords:
		return []string{"kind", l.field, strconv.Itoa(l.index), l.inner}
	case idl.SlotArgs:
		return []string{"kind", l.field, l.key, l.inner}
	}
	return []string{"kind", l.field}
}

// JSONPath is the slot's location under its node in the WIRE_FORMAT section
// 29.3 path grammar, as a suffix to the holding node's path: a member is
// `.name` and an element is `[i]` — `.kind.children[0]`, `.state.onLoading`,
// `.fallback`, `.kind.cases[0].child`, `.kind.args.header.tree`.
func (s Slot) JSONPath() string {
	l := s.loc
	switch {
	case l.envelope && l.inner != "":
		return "." + l.field + "." + l.inner
	case l.envelope:
		return "." + l.field
	}
	switch l.shape {
	case idl.SlotList:
		return ".kind." + l.field + "[" + strconv.Itoa(l.index) + "]"
	case idl.SlotRecords:
		return ".kind." + l.field + "[" + strconv.Itoa(l.index) + "]." + l.inner
	case idl.SlotArgs:
		return ".kind." + l.field + "." + l.key + "." + l.inner
	}
	return ".kind." + l.field
}

// each visits every position n holds a node in within reach, in the canonical
// order: the ordered list, then the envelope (state.onLoading, state.onEmpty,
// fallback), then the kind's other slots in table order (Switch cases in case
// order before its default; slot arguments in key order). A kind holds an
// ordered list or arms and arguments, never both, so a container reads list
// then envelope and an arm kind reads envelope then arms — the reference
// host's order.
func each(n wire.Node, reach Reach, visit func(Position, slotLoc, wire.Node)) {
	kindSlots := idl.NodeSlots(n.Kind.Tag)
	fields := n.Kind.Fields
	if reach.Has(Ordered) {
		for _, s := range kindSlots {
			if s.Shape != idl.SlotList {
				continue
			}
			arr, _ := fields[s.Field].(wire.Arr)
			for i, item := range arr {
				if c, ok := item.(wire.Node); ok {
					visit(Ordered, slotLoc{field: s.Field, shape: s.Shape, index: i}, c)
				}
			}
		}
	}
	for _, e := range envelopeSlots {
		if !reach.Has(e.position) {
			continue
		}
		v := n.Extras[e.member]
		if e.inner != "" {
			rec, ok := v.(wire.Obj)
			if !ok {
				continue
			}
			v = rec.Fields[e.inner]
		}
		if c, ok := v.(wire.Node); ok {
			visit(e.position, slotLoc{envelope: true, field: e.member, inner: e.inner}, c)
		}
	}
	for _, s := range kindSlots {
		switch s.Shape {
		case idl.SlotNode:
			if !reach.Has(Arm) {
				continue
			}
			if c, ok := fields[s.Field].(wire.Node); ok {
				visit(Arm, slotLoc{field: s.Field, shape: s.Shape}, c)
			}
		case idl.SlotRecords:
			if !reach.Has(Arm) {
				continue
			}
			arr, _ := fields[s.Field].(wire.Arr)
			for i, item := range arr {
				if rec, ok := item.(wire.Obj); ok {
					if c, ok := rec.Fields[s.Inner].(wire.Node); ok {
						visit(Arm, slotLoc{field: s.Field, shape: s.Shape, index: i, inner: s.Inner}, c)
					}
				}
			}
		case idl.SlotArgs:
			if !reach.Has(Argument) {
				continue
			}
			bag, ok := fields[s.Field].(wire.Obj)
			if !ok {
				continue
			}
			keys := make([]string, 0, len(bag.Fields))
			for k := range bag.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				arg, ok := bag.Fields[k].(wire.Obj)
				if !ok || arg.Tag != s.Case {
					continue
				}
				if c, ok := arg.Fields[s.Inner].(wire.Node); ok {
					visit(Argument, slotLoc{field: s.Field, shape: s.Shape, key: k, inner: s.Inner}, c)
				}
			}
		}
	}
}

// Children returns the immediate children of n within reach, in the canonical
// order.
func Children(n wire.Node, reach Reach) []wire.Node {
	var out []wire.Node
	each(n, reach, func(_ Position, _ slotLoc, c wire.Node) { out = append(out, c) })
	return out
}

// Slots returns the positions n holds a node in within reach, labelled, in the
// canonical order — for a walk that reports where it found something.
func Slots(n wire.Node, reach Reach) []Slot {
	var out []Slot
	each(n, reach, func(p Position, l slotLoc, c wire.Node) { out = append(out, Slot{p, c, l}) })
	return out
}

// MapChildren rebuilds n with every immediate child within reach replaced by
// f of it. f reports whether it changed the child; when no call does, n itself
// is returned with false and nothing is copied. The positions are enumerated
// once per call.
func MapChildren(n wire.Node, reach Reach, f func(wire.Node) (wire.Node, bool)) (wire.Node, bool) {
	type change struct {
		loc  slotLoc
		node wire.Node
	}
	var changes []change
	each(n, reach, func(_ Position, l slotLoc, c wire.Node) {
		if m, changed := f(c); changed {
			changes = append(changes, change{l, m})
		}
	})
	if len(changes) == 0 {
		return n, false
	}

	var kindFields, extras, stateFields map[string]wire.Value
	lists := map[string]wire.Arr{}
	bags := map[string]map[string]wire.Value{}
	for _, ch := range changes {
		l := ch.loc
		if l.envelope {
			if extras == nil {
				extras = copyFields(n.Extras)
			}
			if l.inner == "" {
				extras[l.field] = ch.node
				continue
			}
			if stateFields == nil {
				stateFields = copyFields(n.Extras[l.field].(wire.Obj).Fields)
			}
			stateFields[l.inner] = ch.node
			continue
		}
		if kindFields == nil {
			kindFields = copyFields(n.Kind.Fields)
		}
		switch l.shape {
		case idl.SlotNode:
			kindFields[l.field] = ch.node
		case idl.SlotList, idl.SlotRecords:
			arr, ok := lists[l.field]
			if !ok {
				arr = append(wire.Arr(nil), n.Kind.Fields[l.field].(wire.Arr)...)
				lists[l.field] = arr
			}
			if l.shape == idl.SlotList {
				arr[l.index] = ch.node
				continue
			}
			rec := arr[l.index].(wire.Obj)
			recFields := copyFields(rec.Fields)
			recFields[l.inner] = ch.node
			arr[l.index] = wire.Obj{Tag: rec.Tag, Fields: recFields}
		case idl.SlotArgs:
			bag, ok := bags[l.field]
			if !ok {
				bag = copyFields(n.Kind.Fields[l.field].(wire.Obj).Fields)
				bags[l.field] = bag
			}
			arg := bag[l.key].(wire.Obj)
			argFields := copyFields(arg.Fields)
			argFields[l.inner] = ch.node
			bag[l.key] = wire.Obj{Tag: arg.Tag, Fields: argFields}
		}
	}
	for field, arr := range lists {
		kindFields[field] = arr
	}
	for field, bag := range bags {
		kindFields[field] = wire.Obj{Tag: n.Kind.Fields[field].(wire.Obj).Tag, Fields: bag}
	}
	if stateFields != nil {
		extras["state"] = wire.Obj{Tag: n.Extras["state"].(wire.Obj).Tag, Fields: stateFields}
	}
	out := wire.Node{ID: n.ID, Kind: n.Kind, Extras: n.Extras}
	if kindFields != nil {
		out.Kind = wire.Obj{Tag: n.Kind.Tag, Fields: kindFields}
	}
	if extras != nil {
		out.Extras = extras
	}
	return out, true
}

func copyFields(src map[string]wire.Value) map[string]wire.Value {
	out := make(map[string]wire.Value, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	return out
}

// OrderedChildren returns n's ordered child list, or (nil, false) for a kind
// with none — the distinction the structural ops' ChildlessKind refusal rests
// on, which Children(n, ReachStructural) cannot draw for an empty list.
func OrderedChildren(n wire.Node) ([]wire.Node, bool) {
	if _, ok := idl.OrderedChildren(n.Kind.Tag); !ok {
		return nil, false
	}
	children := Children(n, ReachStructural)
	if children == nil {
		children = []wire.Node{}
	}
	return children, true
}

// FindNode returns the first node with id in tree (depth-first, root first),
// searching every position (ReachKeyed, the reference's keyed walk) — the
// surface the apply engine's id checks run on.
func FindNode(tree wire.Node, id string) (wire.Node, bool) {
	if tree.ID == id {
		return tree, true
	}
	for _, c := range Children(tree, ReachKeyed) {
		if found, ok := FindNode(c, id); ok {
			return found, true
		}
	}
	return wire.Node{}, false
}

func setKindField(n wire.Node, key string, value wire.Value) wire.Node {
	fields := copyFields(n.Kind.Fields)
	fields[key] = value
	return wire.Node{ID: n.ID, Kind: wire.Obj{Tag: n.Kind.Tag, Fields: fields}, Extras: n.Extras}
}

func setExtra(n wire.Node, key string, value wire.Value) wire.Node {
	extras := copyFields(n.Extras)
	extras[key] = value
	return wire.Node{ID: n.ID, Kind: n.Kind, Extras: extras}
}

// dropExtra returns n without the envelope slot key.
func dropExtra(n wire.Node, key string) wire.Node {
	extras := make(map[string]wire.Value, len(n.Extras))
	for k, v := range n.Extras {
		if k != key {
			extras[k] = v
		}
	}
	return wire.Node{ID: n.ID, Kind: n.Kind, Extras: extras}
}

func withLayoutChildren(n wire.Node, children []wire.Node) wire.Node {
	arr := make(wire.Arr, len(children))
	for i, c := range children {
		arr[i] = c
	}
	slot, _ := idl.OrderedChildren(n.Kind.Tag)
	return setKindField(n, slot, arr)
}

// mapNode rewrites the target node via f, rebuilding the spine above it.
// Returns (tree, false) when the target is absent.
func mapNode(target string, f func(wire.Node) wire.Node, n wire.Node) (wire.Node, bool) {
	if n.ID == target {
		return f(n), true
	}
	found := false
	out, _ := MapChildren(n, ReachKeyed, func(c wire.Node) (wire.Node, bool) {
		if found {
			return c, false
		}
		m, ok := mapNode(target, f, c)
		found = ok
		return m, ok
	})
	if !found {
		return wire.Node{}, false
	}
	return out, true
}

func allIDs(n wire.Node) []string {
	ids := []string{n.ID}
	for _, c := range Children(n, ReachKeyed) {
		ids = append(ids, allIDs(c)...)
	}
	return ids
}

// findLayoutParent finds the node whose ordered child list holds target,
// searching every position for it.
func findLayoutParent(target string, n wire.Node) (wire.Node, bool) {
	for _, c := range Children(n, ReachStructural) {
		if c.ID == target {
			return n, true
		}
	}
	for _, c := range Children(n, ReachKeyed) {
		if found, ok := findLayoutParent(target, c); ok {
			return found, true
		}
	}
	return wire.Node{}, false
}

func isAncestor(ancestorID, descendantID string, root wire.Node) bool {
	ancestor, ok := FindNode(root, ancestorID)
	if !ok {
		return false
	}
	for _, c := range Children(ancestor, ReachKeyed) {
		if _, found := FindNode(c, descendantID); found {
			return true
		}
	}
	return false
}
