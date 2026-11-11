// Merge by fold: the reconciliation policy over Core's conflict report
// (WIRE_FORMAT.md §34, Phase 2643).
//
// A merge of two branches is a DERIVATION: both branches' op deltas are folded
// from their common base, and the merge records only what the fold cannot
// decide on its own — a script. Which op pairs interfere is Core's report over
// the base-aware footprint (ConflictsIn, footprint.go). Each reported pair is
// exactly one of:
//
//  1. An AGREEMENT — the two ops' canonical encodings are byte-identical, so the
//     fold already holds the effect when it reaches the second. Admitted.
//  2. An INTERLEAVE — an InsertPositionClash at parent p where each op appends
//     a node to p (an InsertChild under it, or a MoveNode to it) and the two
//     appended ids differ. Admitted, ordered by the child-order rule below.
//  3. A REBASE (fuaran#2671) — one op is a ReorderChildren(p, order) and the
//     other changes p's child list: an InsertChild under p, a MoveNode to p, or
//     a RemoveNode or MoveNode of a child order lists. Neither may be a Batch.
//     Admitted: the fold applies the reorder REBASED to the children p holds
//     when it is reached (the listed children still there, in the declared
//     order, then the rest in held order), and the child-order rule places the
//     appends.
//  4. A REFUSAL — anything else. DeleteModify when exactly one of the two ops
//     removes (a RemoveNode, or a Batch holding one), Conflict otherwise. Two
//     reorders of one parent that differ stay refused.
//
// THE CHILD-ORDER RULE (§34.4). Under an interleaved or rebased parent the
// children neither branch appended keep the fold's order (the rebased
// reorder's, where a branch reordered the parent) and the appended ones
// follow: each branch's run of appends in its own order, the two runs merged
// by comparing heads ordinally by NodeId, the smaller first, and a node both
// runs hold (two agreeing moves of it) listed once, where the merge first
// reaches it. An append a later reorder of the same parent on the same branch
// lists is settled by that reorder and leaves the run. The merge records
// ReorderChildren(p, order) for EVERY interleaved or rebased parent
// (fuaran#2645): a merge node is replayed by Core's closure fold in its own
// drain order, so the script — not the order a reconcile checked — fixes it.
//
// CHECKED, not assumed: the admitted classes lie outside Core's independence
// theorem, so the fold runs in both orders, each followed by the script, and
// two trees that differ refuse the merge as OrderSensitive.
//
// Since fuaran#2645 this is the DAG's ONLY merge (the three-way tree merge was
// retired with its merge-conformance keys, fuaran#2646); the `foldFixtures`
// family certifies it.
package merge

import (
	"sort"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// FoldSide is which branch of a two-branch reconcile an op or a fault belongs
// to.
type FoldSide string

const (
	FoldSideA FoldSide = "A"
	FoldSideB FoldSide = "B"
)

// FoldRefusalClass is how the policy classes a pair it refuses.
type FoldRefusalClass string

const (
	FoldDeleteModify FoldRefusalClass = "DeleteModify"
	FoldConflict     FoldRefusalClass = "Conflict"
)

// FoldRefusal is one refused pair: Core's report entry with the policy's class.
type FoldRefusal struct {
	Conflict OpConflict
	Class    FoldRefusalClass
}

// FoldFaultKind is why a reconcile refused.
type FoldFaultKind string

const (
	// FaultDeltaDoesNotApply — a branch's own delta does not apply from the
	// base: Side, Index and Err name the first failing op.
	FaultDeltaDoesNotApply FoldFaultKind = "DeltaDoesNotApply"
	// FaultRefused — Core reported pairs the policy does not admit; Refusals
	// holds them in canonical order.
	FaultRefused FoldFaultKind = "Refused"
	// FaultOrderSensitive — every pair was admitted and the two fold orders
	// reached different trees (AThenB, BThenA: their canonical encodings).
	FaultOrderSensitive FoldFaultKind = "OrderSensitive"
	// FaultFoldFaulted — every pair was admitted and the fold faulted applying
	// an op (Err).
	FaultFoldFaulted FoldFaultKind = "FoldFaulted"
	// FaultIntroducedDefects — the derived tree carries defects neither branch
	// head had (ReconcileFoldGated returns them).
	FaultIntroducedDefects FoldFaultKind = "IntroducedDefects"
	// FaultUnencodable — an op or a derived tree has no canonical encoding, so
	// agreement and order cannot be judged (Err). Only a hand-built op reaches
	// it: every decoded op and tree encodes. Never a silent equality.
	FaultUnencodable FoldFaultKind = "Unencodable"
)

// FoldFault is a typed refusal of a reconcile; the members its Kind names are
// set, the others are zero.
type FoldFault struct {
	Kind     FoldFaultKind
	Side     FoldSide
	Index    int
	Err      error
	Refusals []FoldRefusal
	AThenB   string
	BThenA   string
}

// FoldReconciled is a derived merge: the tree, the script the merge records
// (one ReorderChildren per interleaved or rebased parent, parents in ordinal
// order), how many distinct agreeing ops the fold applied as no change, and
// how many parents it interleaved or rebased.
type FoldReconciled struct {
	Tree        wire.Node
	Script      []wire.Obj
	Agreements  int
	Interleaves int
}

// opTexts encodes ops canonically, keeping the first encoding failure.
type opTexts struct {
	err error
}

func (t *opTexts) of(op wire.Obj) string {
	s, err := wire.EncodeOp(op)
	if err != nil && t.err == nil {
		t.err = err
	}
	return s
}

// held reports whether the effect of op is already held by tree — the two cases
// where re-applying an identical op FAILS; every other op is idempotent under
// ops.Apply.
func held(op wire.Obj, tree wire.Node) bool {
	switch op.Tag {
	case "RemoveNode":
		_, found := findStructural(tree, opStr(op, "target"))
		return !found
	case "InsertChild":
		p, found := findStructural(tree, opStr(op, "parentId"))
		child, ok := op.Fields["child"].(wire.Node)
		if !found || !ok {
			return false
		}
		want, err := wire.EncodeNode(child)
		if err != nil {
			return false
		}
		for _, c := range structuralChildren(p) {
			if c.ID == child.ID {
				if got, err := wire.EncodeNode(c); err == nil && got == want {
					return true
				}
			}
		}
	}
	return false
}

// ApplyHeld is ops.Apply with an op whose effect tree already holds applied as
// no change — the fold's reading of an agreement. A Batch is applied member by
// member under the same rule; the first member that fails and is not held
// fails it, and tree is returned untouched.
func ApplyHeld(op wire.Obj, tree wire.Node) (wire.Node, error) {
	if op.Tag == "Batch" {
		return foldHeld(batchMembers(op), tree)
	}
	next, err := ops.Apply(op, tree)
	if err != nil {
		if held(op, tree) {
			return tree, nil
		}
		return tree, err
	}
	return next, nil
}

func foldHeld(script []wire.Obj, start wire.Node) (wire.Node, error) {
	return foldRebased(nil, script, start)
}

// reorderOp is the canonical ReorderChildren op.
func reorderOp(parent string, order []string) wire.Obj {
	arr := make(wire.Arr, 0, len(order))
	for _, id := range order {
		arr = append(arr, wire.Str(id))
	}
	return wire.Obj{Tag: "ReorderChildren", Fields: map[string]wire.Value{
		"newOrder": arr,
		"parentId": wire.Str(parent),
	}}
}

// orderOf is a ReorderChildren op's listed ids.
func orderOf(op wire.Obj) []string {
	arr, _ := op.Fields["newOrder"].(wire.Arr)
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if id, ok := v.(wire.Str); ok {
			out = append(out, string(id))
		}
	}
	return out
}

func listed(order []string, id string) bool {
	for _, x := range order {
		if x == id {
			return true
		}
	}
	return false
}

// rebased is a reorder of parent rebased to the children it holds in tree: the
// listed children still there, in the declared order, then the others in the
// order they hold. op itself where the parent is not in the tree.
func rebased(tree wire.Node, parent string, op wire.Obj) wire.Obj {
	p, found := findStructural(tree, parent)
	if !found {
		return op
	}
	present := IDSet{}
	var kids []string
	for _, c := range structuralChildren(p) {
		kids = append(kids, c.ID)
		present[c.ID] = true
	}
	var order []string
	for _, c := range orderOf(op) {
		if present[c] && !listed(order, c) {
			order = append(order, c)
		}
	}
	for _, k := range kids {
		if !listed(order, k) {
			order = append(order, k)
		}
	}
	return reorderOp(parent, order)
}

// foldRebased is the fold: ApplyHeld op by op, with a reorder of a parent in
// rebase applied rebased to the children the parent holds when the fold
// reaches it.
func foldRebased(rebase map[string]bool, script []wire.Obj, start wire.Node) (wire.Node, error) {
	t := start
	for _, op := range script {
		if op.Tag == "ReorderChildren" && rebase[opStr(op, "parentId")] {
			op = rebased(t, opStr(op, "parentId"), op)
		}
		next, err := ApplyHeld(op, t)
		if err != nil {
			return start, err
		}
		t = next
	}
	return t, nil
}

// firstFailure is the index and error of the first op of delta that does not
// apply from base, or -1.
func firstFailure(delta []wire.Obj, base wire.Node) (int, error) {
	t := base
	for i, op := range delta {
		next, err := ops.Apply(op, t)
		if err != nil {
			return i, err
		}
		t = next
	}
	return -1, nil
}

func removes(op wire.Obj) bool {
	if op.Tag == "RemoveNode" {
		return true
	}
	if op.Tag == "Batch" {
		for _, m := range batchMembers(op) {
			if removes(m) {
				return true
			}
		}
	}
	return false
}

// appendInto is the node op appends to parent, when it is an append there.
func appendInto(parent string, op wire.Obj) (string, bool) {
	switch op.Tag {
	case "InsertChild":
		if opStr(op, "parentId") == parent {
			if child, ok := op.Fields["child"].(wire.Node); ok {
				return child.ID, true
			}
		}
	case "MoveNode":
		if opStr(op, "newParentId") == parent {
			return opStr(op, "target"), true
		}
	}
	return "", false
}

// rebaseOf is the parent whose child list reorder and other both change, when
// reorder is a ReorderChildren and other an append to its parent or a removal
// or move of a child it lists.
func rebaseOf(reorder, other wire.Obj) (string, bool) {
	if reorder.Tag != "ReorderChildren" {
		return "", false
	}
	p := opStr(reorder, "parentId")
	order := orderOf(reorder)
	switch other.Tag {
	case "InsertChild":
		if opStr(other, "parentId") == p {
			return p, true
		}
	case "MoveNode":
		if opStr(other, "newParentId") == p || listed(order, opStr(other, "target")) {
			return p, true
		}
	case "RemoveNode":
		if listed(order, opStr(other, "target")) {
			return p, true
		}
	}
	return "", false
}

type verdict int

const (
	verdictAgreement verdict = iota
	verdictInterleave
	verdictRebase
	verdictRefuse
)

// judge classes one reported pair; for a rebase the second result names the
// rebased parent.
func judge(c OpConflict, texts *opTexts) (verdict, FoldRefusalClass, string) {
	if texts.of(c.Left) == texts.of(c.Right) {
		return verdictAgreement, "", ""
	}
	l, lok := appendInto(c.Address, c.Left)
	r, rok := appendInto(c.Address, c.Right)
	if c.Shape == ShapeInsertPositionClash && lok && rok && l != r {
		return verdictInterleave, "", ""
	}
	if p, ok := rebaseOf(c.Left, c.Right); ok {
		return verdictRebase, "", p
	}
	if p, ok := rebaseOf(c.Right, c.Left); ok {
		return verdictRebase, "", p
	}
	if removes(c.Left) != removes(c.Right) {
		return verdictRefuse, FoldDeleteModify, ""
	}
	return verdictRefuse, FoldConflict, ""
}

func orderKey(r FoldRefusal, texts *opTexts) [5]string {
	c := r.Conflict
	return [5]string{c.Address, c.Shape, c.Slot, texts.of(c.Left), texts.of(c.Right)}
}

// SortFoldRefusals orders refusals canonically — address, shape, slot (empty
// when absent), left op bytes, right op bytes, each ordinal — with exact
// duplicates removed (§34.5). It errs only on an op with no canonical encoding.
func SortFoldRefusals(refusals []FoldRefusal) ([]FoldRefusal, error) {
	texts := &opTexts{}
	type keyed struct {
		key [5]string
		r   FoldRefusal
	}
	seen := make(map[[5]string]bool, len(refusals))
	out := make([]keyed, 0, len(refusals))
	for _, r := range refusals {
		k := orderKey(r, texts)
		if !seen[k] {
			seen[k] = true
			out = append(out, keyed{k, r})
		}
	}
	if texts.err != nil {
		return nil, texts.err
	}
	sort.SliceStable(out, func(i, j int) bool {
		for n := range out[i].key {
			if c := ordinalCmp(out[i].key[n], out[j].key[n]); c != 0 {
				return c < 0
			}
		}
		return false
	})
	sorted := make([]FoldRefusal, len(out))
	for i, k := range out {
		sorted[i] = k.r
	}
	return sorted, nil
}

// interleave is the child-order rule's run-merge: each run in its own order,
// heads compared ordinally. An id both runs hold appears twice; reorderFor
// keeps its first occurrence.
func interleave(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if ordinalCmp(a[i], b[j]) <= 0 {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

// reorderFor is the ReorderChildren the rule asks of parent, or none when the
// parent is not in the tree. Recorded even where the fold already gives the
// rule's order (fuaran#2645): a merge node's replay applies the branches in
// Core's drain order, so the script has to fix the order.
func reorderFor(tree wire.Node, parent string, runA, runB []string) []wire.Obj {
	p, found := findStructural(tree, parent)
	if !found {
		return nil
	}
	kids := make([]string, 0)
	present := IDSet{}
	for _, c := range structuralChildren(p) {
		kids = append(kids, c.ID)
		present[c.ID] = true
	}
	appended := idSet(append(append([]string(nil), runA...), runB...)...)
	keep := func(run []string) []string {
		seen := IDSet{}
		var out []string
		for _, x := range run {
			if present[x] && !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
		return out
	}
	var order []string
	for _, k := range kids {
		if !appended[k] {
			order = append(order, k)
		}
	}
	// A node both runs hold (two agreeing moves of it) is listed once, where
	// the merge first reaches it — the same whichever run is named first.
	for _, id := range interleave(keep(runA), keep(runB)) {
		if !listed(order, id) {
			order = append(order, id)
		}
	}
	return []wire.Obj{reorderOp(parent, order)}
}

// appendsOf is a branch's run of appends into parent, in branch order. An
// append a later reorder of the same parent on the branch lists is settled by
// that reorder: the branch placed it, so it leaves the run.
func appendsOf(parent string, delta []wire.Obj) []string {
	var run []string
	for _, op := range delta {
		if op.Tag == "ReorderChildren" && opStr(op, "parentId") == parent {
			order := orderOf(op)
			kept := run[:0:0]
			for _, id := range run {
				if !listed(order, id) {
					kept = append(kept, id)
				}
			}
			run = kept
			continue
		}
		if id, ok := appendInto(parent, op); ok && !listed(run, id) {
			run = append(run, id)
		}
	}
	return run
}

// ReconcileFold reconciles two branch deltas from their common base under the
// §34 policy: the derived tree and the script a merge records, or a typed
// refusal. Both deltas apply from base, op by op, as recorded on their
// branches.
func ReconcileFold(base wire.Node, deltaA, deltaB []wire.Obj) (FoldReconciled, *FoldFault) {
	if i, err := firstFailure(deltaA, base); i >= 0 {
		return FoldReconciled{}, &FoldFault{Kind: FaultDeltaDoesNotApply, Side: FoldSideA, Index: i, Err: err}
	}
	if i, err := firstFailure(deltaB, base); i >= 0 {
		return FoldReconciled{}, &FoldFault{Kind: FaultDeltaDoesNotApply, Side: FoldSideB, Index: i, Err: err}
	}

	texts := &opTexts{}
	var refusals []FoldRefusal
	interleaved := map[string]bool{}
	rebase := map[string]bool{}
	agreed := map[string]bool{}
	for _, c := range ConflictsIn(base, deltaA, deltaB) {
		switch v, class, p := judge(c, texts); v {
		case verdictAgreement:
			// A pair is reported once per address it collides on: count
			// distinct agreeing ops.
			agreed[texts.of(c.Left)] = true
		case verdictInterleave:
			interleaved[c.Address] = true
		case verdictRebase:
			interleaved[p] = true
			rebase[p] = true
		default:
			refusals = append(refusals, FoldRefusal{Conflict: c, Class: class})
		}
	}
	if texts.err != nil {
		return FoldReconciled{}, &FoldFault{Kind: FaultUnencodable, Err: texts.err}
	}
	if len(refusals) > 0 {
		sorted, err := SortFoldRefusals(refusals)
		if err != nil {
			return FoldReconciled{}, &FoldFault{Kind: FaultUnencodable, Err: err}
		}
		return FoldReconciled{}, &FoldFault{Kind: FaultRefused, Refusals: sorted}
	}

	parents := sortedIDs(interleaved)
	derive := func(first, second []wire.Obj) (wire.Node, []wire.Obj, error) {
		script := append(append([]wire.Obj(nil), first...), second...)
		folded, err := foldRebased(rebase, script, base)
		if err != nil {
			return wire.Node{}, nil, err
		}
		var reorders []wire.Obj
		for _, p := range parents {
			reorders = append(reorders, reorderFor(folded, p, appendsOf(p, deltaA), appendsOf(p, deltaB))...)
		}
		tree, err := foldHeld(reorders, folded)
		return tree, reorders, err
	}

	ab, script, err := derive(deltaA, deltaB)
	if err != nil {
		return FoldReconciled{}, &FoldFault{Kind: FaultFoldFaulted, Err: err}
	}
	ba, _, err := derive(deltaB, deltaA)
	if err != nil {
		return FoldReconciled{}, &FoldFault{Kind: FaultFoldFaulted, Err: err}
	}
	abText, errAB := wire.EncodeNode(ab)
	baText, errBA := wire.EncodeNode(ba)
	if errAB != nil || errBA != nil {
		if errAB == nil {
			errAB = errBA
		}
		return FoldReconciled{}, &FoldFault{Kind: FaultUnencodable, Err: errAB}
	}
	if abText != baText {
		return FoldReconciled{}, &FoldFault{Kind: FaultOrderSensitive, AThenB: abText, BThenA: baText}
	}
	return FoldReconciled{Tree: ab, Script: script, Agreements: len(agreed), Interleaves: len(parents)}, nil
}

// ReconcileFoldGated is ReconcileFold, then the validator gate: introduced is
// handed the two branch heads and the derived tree and answers the defects the
// derived tree carries that neither head does (§27.8.2's introduced-defect diff
// under the domain's validator). Any refuses the merge as
// FaultIntroducedDefects, with the defects returned beside the fault.
func ReconcileFoldGated[D any](
	introduced func(headA, headB, derived wire.Node) []D,
	base wire.Node,
	deltaA, deltaB []wire.Obj,
) (FoldReconciled, []D, *FoldFault) {
	r, fault := ReconcileFold(base, deltaA, deltaB)
	if fault != nil {
		return r, nil, fault
	}
	// Both deltas applied from base inside ReconcileFold, so the heads exist.
	headA, _ := foldHeld(deltaA, base)
	headB, _ := foldHeld(deltaB, base)
	if defects := introduced(headA, headB, r.Tree); len(defects) > 0 {
		return FoldReconciled{}, defects, &FoldFault{Kind: FaultIntroducedDefects}
	}
	return r, nil, nil
}

// EncodeFoldEnvelope is the refusal envelope (§34.5): a JSON array of the
// refused pairs in canonical order, each
// {"address","class","left","right","shape"[,"slot"]} with the ops as their
// canonical encodings and slot present only for a SlotClash. sha256 over it is
// the cross-host refusal hash.
func EncodeFoldEnvelope(refusals []FoldRefusal) (string, error) {
	sorted, err := SortFoldRefusals(refusals)
	if err != nil {
		return "", err
	}
	texts := &opTexts{}
	var b strings.Builder
	b.WriteByte('[')
	for i, r := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		c := r.Conflict
		b.WriteString(`{"address":` + canonical.EscapeString(c.Address) +
			`,"class":` + canonical.EscapeString(string(r.Class)) +
			`,"left":` + texts.of(c.Left) +
			`,"right":` + texts.of(c.Right) +
			`,"shape":` + canonical.EscapeString(c.Shape))
		if c.Shape == ShapeSlotClash {
			b.WriteString(`,"slot":` + canonical.EscapeString(c.Slot))
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String(), texts.err
}
