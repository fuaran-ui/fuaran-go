// The footprint of a tree op, and Fuaran Core's conflict report over two
// deltas (WIRE_FORMAT.md §34.2, Phase 2643).
//
// A footprint says what an op reads and writes, as Fuaran Core's `Footprint`
// record: the ids it depends on (Reads), the named parents whose child list it
// changes (StructureWrites), the nodes it authors, destroys, relocates or
// rewrites (ContentWrites), the nodes it removes or rewrites under a parent it
// cannot name (UnknownParentWrites), and the (node, slot) pairs it reads or
// writes without touching the whole node. Two scripts whose footprints fail no
// clause of Core's `Ops.independent` commute.
//
// §34.2 makes the projection itself part of the contract: the pairs a merge by
// fold judges are exactly the pairs Core's `Dag.conflictsIn` reports over the
// reference's base-aware footprint (`TreeOpFootprint.ofOpIn`). A coarser
// projection refuses pairs the reference admits, a finer one admits pairs it
// refuses, and the `foldFixtures` family catches both. So this file ports the
// reference's rules arm for arm, and nothing else:
//
//   - The PURE form (FootprintOf) is computed from the op alone. The structural
//     ops are Core's skeleton clauses; an in-place edit is Core's `UpdateNode`
//     footprint — the node read and content-written with every id of a subtree
//     the op puts in, and an unknown-parent write of the node.
//   - The BASE-AWARE form (FootprintIn) is computed at the tree the op applies
//     to (Core's `Ops.footprintIn`, fuaran-core Phase 500). A removal whose
//     target the STRUCTURAL walk locates under a structural parent names that
//     parent (a read and a structure write) and content-writes the whole subtree
//     it destroys; a move located the same way, whose destination is located
//     too, also reads the destination's proper ancestors (the cycle check reads
//     them). A located in-place edit that applies is declared at the SLOT it
//     changes — `kind`, `state`, `style`, `style.<field>` beside a read of
//     `style`, `accessibility`, `tooltip` — with every id it puts in or takes
//     out. Anything the walk does not locate takes the pure arm. A Batch is
//     threaded member by member, and from an op that does not apply onwards
//     every op takes the pure arm.
//   - THE CHILD LIST (fuaran#2691, Core 0.42.0 D246). Core compares a
//     structure write of p against a slot access as a read and a write of
//     (p, "#children"), so every arm that reads or rewrites a node's structural
//     children by slot declares that slot: a resolved reorder and a resolved
//     move's source parent (beside the `kind` slot the tier holds the list
//     in), an EditNode, and any in-place edit whose held set moved.
//   - A resolved MOVE (fuaran#2651, #2670, #2691) touches no parent whole: the
//     target is read and written at its `parent` slot, the source parent at
//     its `kind` and child-list slots, the destination and every ancestor above
//     it are read at their `parent` slots (the cycle check reads the chain),
//     and the destination keeps Core's named structure write and read. A
//     resolved REORDER (fuaran#2670) reads and writes its parent at the `kind`
//     and child-list slots and reads each listed child at its `parent` slot.
//
// Ids are counted over every position a node holds another in (keyedIDs, the
// reference's keyed walk); the base-aware arms LOCATE through the ordered child
// lists alone (structuralChildren), as Core's resolution does, so a node held
// only in a keyed position takes the pure arm.
//
// Sets enumerate in ordinal (UTF-16) order wherever order is observable (the
// conflict report). Pure, total, no IO.
package merge

import (
	"sort"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// ordinalCmp is the ordinal comparison over UTF-16 code units — F#'s
// String.CompareOrdinal, which orders the reference's sets.
func ordinalCmp(x, y string) int {
	switch {
	case x == y:
		return 0
	case canonical.LessUTF16(x, y):
		return -1
	default:
		return 1
	}
}

// Slot is a (node id, slot) pair.
type Slot struct {
	Node string
	Slot string
}

// IDSet is a set of node ids.
type IDSet map[string]bool

// SlotSet is a set of (node, slot) pairs.
type SlotSet map[Slot]bool

// Footprint is the read/write footprint of a tree op — Fuaran Core's
// `Footprint` record, every address an id. See the file header for each set's
// role. A Footprint is never mutated once built.
type Footprint struct {
	Reads               IDSet
	StructureWrites     IDSet
	ContentWrites       IDSet
	UnknownParentWrites IDSet
	SlotReads           SlotSet
	SlotWrites          SlotSet
}

func idSet(ids ...string) IDSet {
	s := make(IDSet, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

func unionIDs(a, b IDSet) IDSet {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make(IDSet, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func unionSlots(a, b SlotSet) SlotSet {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := make(SlotSet, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func unionFootprint(a, b Footprint) Footprint {
	return Footprint{
		Reads:               unionIDs(a.Reads, b.Reads),
		StructureWrites:     unionIDs(a.StructureWrites, b.StructureWrites),
		ContentWrites:       unionIDs(a.ContentWrites, b.ContentWrites),
		UnknownParentWrites: unionIDs(a.UnknownParentWrites, b.UnknownParentWrites),
		SlotReads:           unionSlots(a.SlotReads, b.SlotReads),
		SlotWrites:          unionSlots(a.SlotWrites, b.SlotWrites),
	}
}

// ── walks ───────────────────────────────────────────────────────────────────

// structuralChildren is a node's ordered child list — the surface the
// structural ops edit (idl.OrderedChildren, the apply engine's layout set).
func structuralChildren(n wire.Node) []wire.Node {
	slot, ok := idl.OrderedChildren(n.Kind.Tag)
	if !ok {
		return nil
	}
	arr, _ := n.Kind.Fields[slot].(wire.Arr)
	out := make([]wire.Node, 0, len(arr))
	for _, item := range arr {
		if c, ok := item.(wire.Node); ok {
			out = append(out, c)
		}
	}
	return out
}

// heldNodes is every node n holds another in: its ordered child list, the
// kind's single-node arms and Switch case children, the envelope's state
// alternatives and its `fallback`, and the subtree of every slot argument (a
// FragmentRef's `args`, a Mount's `inputs`, typed since fuaran#2701). The
// reference's `Reach.keyed`.
func heldNodes(n wire.Node) []wire.Node {
	out := structuralChildren(n)
	fields := n.Kind.Fields
	for _, arm := range idl.NodeArms(n.Kind.Tag) {
		if c, ok := fields[arm].(wire.Node); ok {
			out = append(out, c)
		}
	}
	if cases, ok := fields["cases"].(wire.Arr); ok {
		for _, item := range cases {
			if rec, ok := item.(wire.Obj); ok {
				if c, ok := rec.Fields["child"].(wire.Node); ok {
					out = append(out, c)
				}
			}
		}
	}
	for _, bag := range []string{"args", "inputs"} {
		if args, ok := fields[bag].(wire.Obj); ok {
			for _, a := range args.Fields {
				if arg, ok := a.(wire.Obj); ok && arg.Tag == "SlotArg" {
					if c, ok := arg.Fields["tree"].(wire.Node); ok {
						out = append(out, c)
					}
				}
			}
		}
	}
	if state, ok := n.Extras["state"].(wire.Obj); ok {
		for _, key := range []string{"onLoading", "onEmpty"} {
			if c, ok := state.Fields[key].(wire.Node); ok {
				out = append(out, c)
			}
		}
	}
	if c, ok := n.Extras["fallback"].(wire.Node); ok {
		out = append(out, c)
	}
	return out
}

// keyedIDs adds every id in n's subtree, n included, through every position.
func keyedIDs(n wire.Node, into IDSet) IDSet {
	into[n.ID] = true
	for _, c := range heldNodes(n) {
		keyedIDs(c, into)
	}
	return into
}

func idsOf(nodes []wire.Node) IDSet {
	acc := IDSet{}
	for _, n := range nodes {
		keyedIDs(n, acc)
	}
	return acc
}

// heldIDs is every id n holds below itself, through every position.
func heldIDs(n wire.Node) IDSet {
	ids := keyedIDs(n, IDSet{})
	delete(ids, n.ID)
	return ids
}

// structuralPath is the path from root to id over the ordered child lists,
// both ends included, or nil when the walk does not reach id.
func structuralPath(root wire.Node, id string) []wire.Node {
	if root.ID == id {
		return []wire.Node{root}
	}
	for _, c := range structuralChildren(root) {
		if p := structuralPath(c, id); p != nil {
			return append([]wire.Node{root}, p...)
		}
	}
	return nil
}

func findStructural(root wire.Node, id string) (wire.Node, bool) {
	p := structuralPath(root, id)
	if p == nil {
		return wire.Node{}, false
	}
	return p[len(p)-1], true
}

// structuralParentOf is the node whose ordered child list holds id, preorder
// first.
func structuralParentOf(root wire.Node, id string) (wire.Node, bool) {
	kids := structuralChildren(root)
	for _, c := range kids {
		if c.ID == id {
			return root, true
		}
	}
	for _, c := range kids {
		if p, ok := structuralParentOf(c, id); ok {
			return p, true
		}
	}
	return wire.Node{}, false
}

func sameIDs(a, b IDSet) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func opStr(op wire.Obj, field string) string {
	s, _ := op.Fields[field].(wire.Str)
	return string(s)
}

func batchMembers(op wire.Obj) []wire.Obj {
	arr, _ := op.Fields["ops"].(wire.Arr)
	out := make([]wire.Obj, 0, len(arr))
	for _, item := range arr {
		if o, ok := item.(wire.Obj); ok {
			out = append(out, o)
		}
	}
	return out
}

// ── the pure form ───────────────────────────────────────────────────────────

// vertical is Core's `UpdateNode` footprint: an in-place rewrite of id putting
// incoming in.
func vertical(id string, incoming []wire.Node) Footprint {
	touched := idsOf(incoming)
	touched[id] = true
	return Footprint{Reads: touched, ContentWrites: touched, UnknownParentWrites: idSet(id)}
}

// FootprintOf is the footprint of one op computed from the op alone. Total
// over the TreeOp union; an unknown op tag has the empty footprint.
func FootprintOf(op wire.Obj) Footprint {
	switch op.Tag {
	case "InsertChild":
		parent := opStr(op, "parentId")
		inserted := IDSet{}
		if child, ok := op.Fields["child"].(wire.Node); ok {
			keyedIDs(child, inserted)
		}
		reads := unionIDs(idSet(parent), inserted)
		return Footprint{Reads: reads, StructureWrites: idSet(parent), ContentWrites: inserted}
	case "RemoveNode":
		t := opStr(op, "target")
		return Footprint{Reads: idSet(t), ContentWrites: idSet(t), UnknownParentWrites: idSet(t)}
	case "MoveNode":
		t := opStr(op, "target")
		np := opStr(op, "newParentId")
		return Footprint{
			Reads:               idSet(t, np),
			StructureWrites:     idSet(np),
			ContentWrites:       idSet(t),
			UnknownParentWrites: idSet(t),
		}
	case "ReorderChildren":
		parent := opStr(op, "parentId")
		reads := idSet(parent)
		if order, ok := op.Fields["newOrder"].(wire.Arr); ok {
			for _, v := range order {
				if id, ok := v.(wire.Str); ok {
					reads[string(id)] = true
				}
			}
		}
		return Footprint{Reads: reads, StructureWrites: idSet(parent)}
	case "EditNode", "UpdateState":
		return vertical(opStr(op, "target"), ops.InsertedNodes(op))
	case "UpdateProp", "ReplaceBinding", "UpdateStyle", "PatchStyle", "UpdateAccessibility", "UpdateTooltip":
		return vertical(opStr(op, "target"), nil)
	case "ReplaceRoot":
		node, _ := op.Fields["node"].(wire.Node)
		ids := keyedIDs(node, IDSet{})
		return Footprint{
			Reads:               ids,
			ContentWrites:       ids,
			StructureWrites:     idSet(node.ID),
			UnknownParentWrites: idSet(node.ID),
		}
	case "Batch":
		acc := Footprint{}
		for _, m := range batchMembers(op) {
			acc = unionFootprint(acc, FootprintOf(m))
		}
		return acc
	}
	return Footprint{}
}

// ── the base-aware form ─────────────────────────────────────────────────────

// slotted is a slot-precise in-place rewrite of target, with every id it moves
// in or out.
func slotted(writes, readSlots []string, target string, touched IDSet) Footprint {
	sr := SlotSet{}
	sw := SlotSet{}
	for _, s := range writes {
		sr[Slot{target, s}] = true
		sw[Slot{target, s}] = true
	}
	for _, s := range readSlots {
		sr[Slot{target, s}] = true
	}
	return Footprint{Reads: touched, ContentWrites: touched, SlotReads: sr, SlotWrites: sw}
}

// childListSlot is Core's child-list slot (`Footprint.childListSlot`,
// fuaran-core Phase 625).
const childListSlot = "#children"

// slotEdit is Core's `slotEdit`: a read and a write of one slot of a node.
func slotEdit(node, slot string) Footprint {
	return Footprint{SlotReads: SlotSet{{node, slot}: true}, SlotWrites: SlotSet{{node, slot}: true}}
}

// readingSlot is Core's `readingSlot`: a read of one slot of a node.
func readingSlot(node, slot string) Footprint {
	return Footprint{SlotReads: SlotSet{{node, slot}: true}}
}

// childList is f with a read and a write of node's child-list slot beside it.
func childList(node string, f Footprint) Footprint {
	return unionFootprint(f, slotEdit(node, childListSlot))
}

// guarded is slotted for an op whose contract moves no node: where its held set
// moved anyway (touched non-empty), the child list is written too. Empty for
// every such op the engine has, so the slot is a guard, not an arm.
func guarded(writes, readSlots []string, target string, touched IDSet) Footprint {
	f := slotted(writes, readSlots, target, touched)
	if len(touched) == 0 {
		return f
	}
	return childList(target, f)
}

// styleFields is the wire member names a PatchStyle delta sets.
func styleFields(op wire.Obj) []string {
	delta, _ := op.Fields["style"].(wire.Obj)
	out := make([]string, 0, len(delta.Fields))
	for k := range delta.Fields {
		out = append(out, "style."+k)
	}
	sort.Slice(out, func(i, j int) bool { return canonical.LessUTF16(out[i], out[j]) })
	return out
}

func stateAlternatives(n wire.Node) []wire.Node {
	var out []wire.Node
	if state, ok := n.Extras["state"].(wire.Obj); ok {
		for _, key := range []string{"onLoading", "onEmpty"} {
			if c, ok := state.Fields[key].(wire.Node); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

// resolvedAt is the base-aware arm of one non-batch op at state, given the tree
// it reaches (nil when it does not apply).
func resolvedAt(state wire.Node, after *wire.Node, op wire.Obj) Footprint {
	// The ids an op that puts no node in moved anyway: a guard, empty for every
	// such op the engine has.
	heldChange := func(id string, node wire.Node) IDSet {
		before := heldIDs(node)
		if after == nil {
			return before
		}
		later, ok := findStructural(*after, id)
		if !ok {
			return before
		}
		a := heldIDs(later)
		if sameIDs(a, before) {
			return IDSet{}
		}
		return unionIDs(before, a)
	}

	// A vertical op is slot-precise only where the structural walk locates its
	// node and the op applies; otherwise it is the pure arm.
	verticalAt := func(arm func(node wire.Node) Footprint) Footprint {
		node, ok := findStructural(state, opStr(op, "target"))
		if !ok || after == nil {
			return FootprintOf(op)
		}
		return arm(node)
	}
	id := opStr(op, "target")

	switch op.Tag {
	case "RemoveNode":
		sub, located := findStructural(state, id)
		parent, hasParent := structuralParentOf(state, id)
		if !located || !hasParent {
			return FootprintOf(op)
		}
		return Footprint{
			Reads:           idSet(id, parent.ID),
			StructureWrites: idSet(parent.ID),
			ContentWrites:   keyedIDs(sub, IDSet{}),
		}
	case "MoveNode":
		np := opStr(op, "newParentId")
		_, located := findStructural(state, id)
		parent, hasParent := structuralParentOf(state, id)
		destination := structuralPath(state, np)
		if !located || !hasParent || destination == nil {
			return FootprintOf(op)
		}
		// A resolved move touches no parent WHOLE: the source parent's child
		// list, held in its `kind`, is read and written at its `kind` and
		// child-list slots; the destination and every ancestor above it are
		// read at their `parent` slot (the cycle check reads the chain), and
		// the destination keeps Core's named read and structure write — the
		// address the fold interleaves two appends at. The target changes its
		// position and nothing of itself: its `parent` slot. A move within one
		// parent keeps the parent whole.
		source := ""
		if parent.ID != np {
			source = parent.ID
		}
		f := Footprint{Reads: idSet(np), StructureWrites: idSet(np)}
		f = unionFootprint(f, slotEdit(id, "parent"))
		f = unionFootprint(f, readingSlot(np, "parent"))
		for _, n := range destination[:len(destination)-1] {
			if n.ID != id && n.ID != source {
				f = unionFootprint(f, readingSlot(n.ID, "parent"))
			}
		}
		if source != "" {
			f = unionFootprint(f, childList(source, slotEdit(source, "kind")))
		}
		return f
	case "ReorderChildren":
		// A resolved reorder permutes the parent's child list, held in its
		// `kind`, and touches no child: the parent at its `kind` and child-list
		// slots, each listed child read at its `parent` slot.
		parent := opStr(op, "parentId")
		if _, ok := findStructural(state, parent); !ok || after == nil {
			return FootprintOf(op)
		}
		f := childList(parent, slotEdit(parent, "kind"))
		if order, ok := op.Fields["newOrder"].(wire.Arr); ok {
			for _, v := range order {
				if c, ok := v.(wire.Str); ok {
					f = unionFootprint(f, readingSlot(string(c), "parent"))
				}
			}
		}
		return f
	case "EditNode":
		return verticalAt(func(node wire.Node) Footprint {
			old := ops.InsertedNodes(wire.Obj{Tag: "EditNode", Fields: map[string]wire.Value{
				"newKind": node.Kind, "target": wire.Str(id),
			}})
			return slotted([]string{"kind", childListSlot}, nil, id, unionIDs(idsOf(old), idsOf(ops.InsertedNodes(op))))
		})
	case "UpdateProp", "ReplaceBinding":
		return verticalAt(func(node wire.Node) Footprint {
			return guarded([]string{"kind"}, nil, id, heldChange(id, node))
		})
	case "UpdateState":
		return verticalAt(func(node wire.Node) Footprint {
			return slotted([]string{"state"}, nil, id, unionIDs(idsOf(stateAlternatives(node)), idsOf(ops.InsertedNodes(op))))
		})
	case "UpdateStyle":
		return verticalAt(func(node wire.Node) Footprint {
			return guarded([]string{"style"}, nil, id, heldChange(id, node))
		})
	case "UpdateAccessibility":
		return verticalAt(func(node wire.Node) Footprint {
			return guarded([]string{"accessibility"}, nil, id, heldChange(id, node))
		})
	case "UpdateTooltip":
		return verticalAt(func(node wire.Node) Footprint {
			return guarded([]string{"tooltip"}, nil, id, heldChange(id, node))
		})
	case "PatchStyle":
		return verticalAt(func(node wire.Node) Footprint {
			return guarded(styleFields(op), []string{"style"}, id, heldChange(id, node))
		})
	case "ReplaceRoot":
		plain := FootprintOf(op)
		replaced := keyedIDs(state, IDSet{})
		plain.Reads = unionIDs(plain.Reads, replaced)
		plain.ContentWrites = unionIDs(plain.ContentWrites, replaced)
		plain.StructureWrites = unionIDs(plain.StructureWrites, idSet(state.ID))
		return plain
	}
	// InsertChild: the pure arm names everything it touches.
	return FootprintOf(op)
}

// applied is the tree op reaches from tree, or nil when it does not apply.
func applied(tree wire.Node, op wire.Obj) *wire.Node {
	next, err := ops.Apply(op, tree)
	if err != nil {
		return nil
	}
	return &next
}

// threaded is one op's footprint at the state the ops before it reached (nil:
// an earlier op did not apply, so the pure arm), and the state it reaches.
func threaded(state *wire.Node, op wire.Obj) (Footprint, *wire.Node) {
	if state == nil {
		return FootprintOf(op), nil
	}
	if op.Tag == "Batch" {
		acc := Footprint{}
		st := state
		for _, m := range batchMembers(op) {
			var f Footprint
			f, st = threaded(st, m)
			acc = unionFootprint(acc, f)
		}
		return acc, st
	}
	after := applied(*state, op)
	return resolvedAt(*state, after, op), after
}

// FootprintIn is the footprint of one op at the tree it applies to:
// FootprintOf's address kinds with every parent the pure form cannot name
// resolved against state, and a located in-place edit declared at its slot. A
// Batch is threaded member by member. Total; sound where FootprintOf is and
// never less precise.
func FootprintIn(state wire.Node, op wire.Obj) Footprint {
	f, _ := threaded(&state, op)
	return f
}

// FootprintEachIn is each op of a script at the tree the ops before it reach,
// in order.
func FootprintEachIn(base wire.Node, script []wire.Obj) []Footprint {
	out := make([]Footprint, 0, len(script))
	st := &base
	for _, op := range script {
		var f Footprint
		f, st = threaded(st, op)
		out = append(out, f)
	}
	return out
}

// ── Core's conflict report (`Dag.conflicts` / `Dag.conflictsIn`) ────────────

// The four conflict shapes of Core's `MergeConflictShape`.
const (
	ShapeConcurrentUpdate    = "ConcurrentUpdate"
	ShapeInsertPositionClash = "InsertPositionClash"
	ShapeMoveVsRemove        = "MoveVsRemove"
	ShapeSlotClash           = "SlotClash"
)

// OpConflict is one entry of Core's conflict report: the op from the first
// delta (Left), the op from the second (Right), the address they collide on (a
// node id, or a parent id for InsertPositionClash), and the shape. Slot names
// the slot exactly when Shape is ShapeSlotClash, and is empty otherwise.
type OpConflict struct {
	Left    wire.Obj
	Right   wire.Obj
	Address string
	Shape   string
	Slot    string
}

func sortedIDs(s IDSet) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return canonical.LessUTF16(out[i], out[j]) })
	return out
}

// intersectInto adds every id held by both a and b to into.
func intersectInto(into, a, b IDSet) {
	for k := range a {
		if b[k] {
			into[k] = true
		}
	}
}

// childListSlots is the child-list slots a footprint's structure writes denote
// — Core's `Footprint.childListSlots` (fuaran-core Phase 625).
func childListSlots(f Footprint) SlotSet {
	out := SlotSet{}
	for p := range f.StructureWrites {
		out[Slot{p, childListSlot}] = true
	}
	return out
}

// slotClash is the slots one side writes and the other reads or writes, and
// the child-list slot of each parent one side structure-writes that the other
// accesses as a slot — Core's `Footprint.slotClash` (0.42.0), ordered by node
// then slot.
func slotClash(a, b Footprint) []Slot {
	hits := SlotSet{}
	meet := func(x, y SlotSet) {
		for s := range x {
			if y[s] {
				hits[s] = true
			}
		}
	}
	meet(a.SlotWrites, b.SlotWrites)
	meet(a.SlotWrites, b.SlotReads)
	meet(a.SlotReads, b.SlotWrites)
	meet(childListSlots(a), unionSlots(b.SlotWrites, b.SlotReads))
	meet(unionSlots(a.SlotWrites, a.SlotReads), childListSlots(b))
	out := make([]Slot, 0, len(hits))
	for s := range hits {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if c := ordinalCmp(out[i].Node, out[j].Node); c != 0 {
			return c < 0
		}
		return ordinalCmp(out[i].Slot, out[j].Slot) < 0
	})
	return out
}

func slotNodes(s SlotSet) IDSet {
	out := IDSet{}
	for sl := range s {
		out[sl.Node] = true
	}
	return out
}

// slotsAgainstNode adds the nodes a accesses through a slot that b WRITES
// WHOLE — Core's `slotsAgainstNode` (0.42.0): a read of the node is of its
// existence and a structure write is compared at the child-list slot, so only
// a content write collides here.
func slotsAgainstNode(into IDSet, a, b Footprint) {
	intersectInto(into, slotNodes(unionSlots(a.SlotWrites, a.SlotReads)), b.ContentWrites)
}

// writesStructure is Core's `Footprint.structural` non-empty (0.42.0): a named
// parent's child list, an unknown source parent, or a child-list slot read or
// written — the set the unknown-parent clause meets.
func writesStructure(f Footprint) bool {
	if len(f.StructureWrites) > 0 || len(f.UnknownParentWrites) > 0 {
		return true
	}
	for s := range unionSlots(f.SlotWrites, f.SlotReads) {
		if s.Slot == childListSlot {
			return true
		}
	}
	return false
}

type opAt struct {
	op wire.Obj
	f  Footprint
}

// conflictsOver is Core's `Dag.conflicts`: every op pair (a ∈ left, b ∈ right)
// whose footprints are not independent, one entry per address by shape
// priority (content, then position, then move/remove), plus one per clashing
// slot. Left order × right order × ordinal addresses. Detection only.
func conflictsOver(left, right []opAt) []OpConflict {
	var out []OpConflict
	for _, a := range left {
		for _, b := range right {
			fa, fb := a.f, b.f
			c1 := IDSet{}
			intersectInto(c1, fa.ContentWrites, fb.ContentWrites)
			intersectInto(c1, fa.ContentWrites, fb.Reads)
			intersectInto(c1, fb.ContentWrites, fa.Reads)
			slotsAgainstNode(c1, fa, fb)
			slotsAgainstNode(c1, fb, fa)
			c2 := IDSet{}
			for k := range fa.StructureWrites {
				if fb.StructureWrites[k] && !c1[k] {
					c2[k] = true
				}
			}
			moveRemove := IDSet{}
			if len(fa.UnknownParentWrites) > 0 && writesStructure(fb) {
				moveRemove = unionIDs(moveRemove, fa.UnknownParentWrites)
			}
			if len(fb.UnknownParentWrites) > 0 && writesStructure(fa) {
				moveRemove = unionIDs(moveRemove, fb.UnknownParentWrites)
			}
			c3 := IDSet{}
			for k := range moveRemove {
				if !c1[k] && !c2[k] {
					c3[k] = true
				}
			}
			for _, addr := range sortedIDs(c1) {
				out = append(out, OpConflict{Left: a.op, Right: b.op, Address: addr, Shape: ShapeConcurrentUpdate})
			}
			for _, addr := range sortedIDs(c2) {
				out = append(out, OpConflict{Left: a.op, Right: b.op, Address: addr, Shape: ShapeInsertPositionClash})
			}
			for _, addr := range sortedIDs(c3) {
				out = append(out, OpConflict{Left: a.op, Right: b.op, Address: addr, Shape: ShapeMoveVsRemove})
			}
			for _, s := range slotClash(fa, fb) {
				out = append(out, OpConflict{Left: a.op, Right: b.op, Address: s.Node, Shape: ShapeSlotClash, Slot: s.Slot})
			}
		}
	}
	return out
}

// threadDelta is each op of a delta with its footprint at the state the ops
// before it reach; an op that does not apply leaves the state where it was, as
// Core's does.
func threadDelta(base wire.Node, delta []wire.Obj) []opAt {
	out := make([]opAt, 0, len(delta))
	st := base
	for _, op := range delta {
		out = append(out, opAt{op, FootprintIn(st, op)})
		if next := applied(st, op); next != nil {
			st = *next
		}
	}
	return out
}

// ConflictsIn is Core's `Dag.conflictsIn` over the base-aware footprint: each
// delta threaded through ops.Apply from base, and the report over the
// footprints so computed. An empty report promises the two deltas commute from
// base.
func ConflictsIn(base wire.Node, deltaA, deltaB []wire.Obj) []OpConflict {
	return conflictsOver(threadDelta(base, deltaA), threadDelta(base, deltaB))
}
