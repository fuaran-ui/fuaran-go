package ops

// Placement algebra — placed insert / move / nudge, and the clone verbs
// (duplicate / paste) built on top of them.
//
// The op vocabulary is deliberately positionless: InsertChild and MoveNode
// APPEND, and an explicit order is stated only by ReorderChildren naming every
// sibling id (an id is checkable; an ordinal is not — see README, "Retired wire
// vocabulary"). Placing a node anywhere but last is therefore
// Batch [InsertChild|MoveNode, ReorderChildren] — correct, but it leaves every
// caller deriving the full sibling permutation itself. A Go service composing
// server-driven updates is exactly that caller.
//
// This file ships the derivation once, purely additively: every helper emits
// ops built from the EXISTING vocabulary (InsertChild / MoveNode /
// ReorderChildren / Batch), so the wire format, the conformance corpus and the
// apply engine are untouched — and the reorder leg is dropped whenever
// appending already yields the wanted order, keeping the common case one bare
// op.
//
// Pre-checks mirror the apply engine's own rejections (absent parent, childless
// kind, absent or non-structural node, move-into-self, move-into-descendant,
// duplicate id) so a caller can refuse an illegal placement without a dry-run
// apply — with one deliberate tightening: an anchor that is not among the
// destination's post-op children is REFUSED (UnknownAnchor) rather than
// silently appended. The only op that could honour such an anchor would be a
// ReorderChildren naming it, which the apply engine refuses as
// OrderingMismatch; saying so before emission is friendlier than a rejection
// after it.
//
// Everything here is a pure function of its arguments: no package-level state,
// nothing retained between calls. The library-not-a-runtime line is unmoved.

import (
	"fmt"
	"strconv"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// ── Placement vocabulary ────────────────────────────────────────────────────

// PlacementKind names where a node should sit among its destination siblings,
// stated the only way the op vocabulary allows: by naming an existing sibling,
// or an end. Go has no closed sum type, so the zero value is reachable and is
// deliberately NOT a synonym for Last — an unset or misspelled kind is refused
// as UnknownPlacement rather than silently appending (default-deny by shape).
type PlacementKind string

const (
	// PlacementLast appends — what InsertChild / MoveNode do on their own.
	PlacementLast PlacementKind = "Last"
	// PlacementFirst prepends — before every current sibling.
	PlacementFirst PlacementKind = "First"
	// PlacementBefore sits immediately before the named sibling.
	PlacementBefore PlacementKind = "Before"
	// PlacementAfter sits immediately after the named sibling.
	PlacementAfter PlacementKind = "After"
)

// Placement is a position among a parent's children. Anchor is meaningful for
// PlacementBefore / PlacementAfter only, and is ignored by the others.
type Placement struct {
	Kind   PlacementKind
	Anchor string
}

// Last appends the node after every current sibling.
func Last() Placement { return Placement{Kind: PlacementLast} }

// First prepends the node before every current sibling.
func First() Placement { return Placement{Kind: PlacementFirst} }

// Before places the node immediately before the named sibling.
func Before(anchor string) Placement { return Placement{Kind: PlacementBefore, Anchor: anchor} }

// After places the node immediately after the named sibling.
func After(anchor string) Placement { return Placement{Kind: PlacementAfter, Anchor: anchor} }

// Target is a structural destination: which parent, and where among its
// children.
type Target struct {
	ParentID  string
	Placement Placement
}

// ── Typed refusals ──────────────────────────────────────────────────────────

// PlaceErrorCode is why a placement could not become an op. The first seven are
// pre-statements of the apply-time refusal the emitted op would have met, so a
// helper rejection and an apply rejection agree — no false permit, no false
// refuse; the correspondence is pinned by the rejection-parity tests. The last
// three claim no apply-side counterpart and have none: a nudge that cannot be
// expressed yields no op at all, and an unrecognised placement kind is a
// Go-only shape defect (see PlacementKind).
type PlaceErrorCode string

const (
	// CodePlaceParentNotFound — the destination parent is not in the tree
	// (apply: ParentNotFound).
	CodePlaceParentNotFound PlaceErrorCode = "ParentNotFound"
	// CodePlaceChildlessKind — the destination parent's kind carries no
	// children list (apply: ChildlessKind).
	CodePlaceChildlessKind PlaceErrorCode = "ChildlessKind"
	// CodePlaceNodeNotFound — the node to move / nudge / duplicate is not
	// structurally addressable: absent, or held in a non-structural slot (a
	// Switch case, an ErrorBoundary fallback, a state placeholder) that the
	// structural ops cannot reach (apply: NodeNotFound).
	CodePlaceNodeNotFound PlaceErrorCode = "NodeNotFound"
	// CodePlaceUnknownAnchor — the placement anchor is not among the
	// destination's post-op children. The only op that could honour it, a
	// ReorderChildren naming it, is refused as OrderingMismatch.
	CodePlaceUnknownAnchor PlaceErrorCode = "UnknownAnchor"
	// CodePlaceDuplicateID — the subtree being inserted carries an id already
	// present in the tree (apply: DuplicateNodeId).
	CodePlaceDuplicateID PlaceErrorCode = "DuplicateNodeId"
	// CodePlaceMoveIntoSelf — the node would become its own parent (apply:
	// KindMismatch).
	CodePlaceMoveIntoSelf PlaceErrorCode = "MoveIntoSelf"
	// CodePlaceMoveIntoDescendant — the destination sits inside the node's own
	// subtree, which would be a cycle (apply: KindMismatch).
	CodePlaceMoveIntoDescendant PlaceErrorCode = "MoveIntoDescendant"
	// CodePlaceCannotNudgeRoot — the root has no siblings to nudge among.
	CodePlaceCannotNudgeRoot PlaceErrorCode = "CannotNudgeRoot"
	// CodePlaceNudgeOutOfRange — the nudge would leave the sibling range
	// (already first / already last).
	CodePlaceNudgeOutOfRange PlaceErrorCode = "NudgeOutOfRange"
	// CodePlaceUnknownPlacement — the Placement carries no recognised kind.
	CodePlaceUnknownPlacement PlaceErrorCode = "UnknownPlacement"
)

// PlaceError is a structured, recoverable placement refusal (never panicked),
// shaped like *ApplyError so the two read alike at a call site.
//
// Which id fields carry meaning depends on Code: ParentNotFound and
// ChildlessKind name ParentID; NodeNotFound, MoveIntoSelf, CannotNudgeRoot and
// NudgeOutOfRange name NodeID; DuplicateNodeId, MoveIntoDescendant and
// UnknownAnchor name both (UnknownAnchor carries the anchor in NodeID);
// NudgeOutOfRange additionally carries Delta. UnknownPlacement names neither.
type PlaceError struct {
	Code     PlaceErrorCode
	Message  string
	NodeID   string
	ParentID string
	Delta    int
}

func (e *PlaceError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ── Fresh-id strategy (the clone verbs' id-minting seam) ────────────────────

// FreshIDs mints a replacement id for a cloned node: given the id being
// replaced and a predicate reporting whether a candidate is already claimed —
// by the target tree, by the incoming subtree, or by an id minted earlier in
// the same remap — it returns an id the predicate refuses.
//
// Injectable so a host with its own id discipline can supply one; DerivedIDs is
// the default and SequentialIDs is the deterministic-replay option.
type FreshIDs func(oldID string, taken func(string) bool) string

// DerivedIDs is the default strategy: "<oldID>-copy", then "<oldID>-copy-2",
// "-copy-3", … — the first candidate not already taken. Deterministic (derived
// from the id it replaces, with no ambient state) and collision-free by
// probing.
func DerivedIDs(oldID string, taken func(string) bool) string {
	for n := 1; ; n++ {
		candidate := oldID + "-copy"
		if n > 1 {
			candidate = oldID + "-copy-" + strconv.Itoa(n)
		}
		if !taken(candidate) {
			return candidate
		}
	}
}

// SequentialIDs mints sequential ids under a fixed prefix ("<prefix>-1", "-2",
// …) — the deterministic-replay option: the minted sequence depends only on the
// prefix and the order of requests, never on the ids being replaced. Each call
// to SequentialIDs starts its own counter, so the returned FreshIDs carries
// mutable state: it is single-use and not safe for concurrent use.
func SequentialIDs(prefix string) FreshIDs {
	counter := 0
	return func(_ string, taken func(string) bool) string {
		for {
			counter++
			candidate := prefix + "-" + strconv.Itoa(counter)
			if !taken(candidate) {
				return candidate
			}
		}
	}
}

// ── Shared derivation ───────────────────────────────────────────────────────

// containerChildren returns the destination's current child ids, or the
// mirrored apply-side refusal (absent parent / childless kind).
func containerChildren(root wire.Node, parentID string) ([]string, *PlaceError) {
	parent, found := findNode(parentID, root)
	if !found {
		return nil, &PlaceError{
			Code:     CodePlaceParentNotFound,
			Message:  fmt.Sprintf("Parent node '%s' not found in tree.", parentID),
			ParentID: parentID,
		}
	}
	children, isLayout := layoutChildren(parent)
	if !isLayout {
		return nil, &PlaceError{
			Code: CodePlaceChildlessKind,
			Message: fmt.Sprintf(
				"Node '%s' (kind=%s) has no children field — only layout kinds accept structural child ops.",
				parentID, parent.Kind.Tag),
			ParentID: parentID,
		}
	}
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, c.ID)
	}
	return ids, nil
}

// reposition places moved within order (which already contains it) per
// placement. An anchor that is not in the list is refused — the honest
// alternative, silently appending, would emit an op that does not honour the
// caller's stated intent.
func reposition(order []string, moved, parentID string, placement Placement) ([]string, *PlaceError) {
	rest := make([]string, 0, len(order))
	for _, id := range order {
		if id != moved {
			rest = append(rest, id)
		}
	}

	anchored := func(anchor string, offset int) ([]string, *PlaceError) {
		at := -1
		for i, id := range rest {
			if id == anchor {
				at = i + offset
				break
			}
		}
		if at < 0 {
			return nil, &PlaceError{
				Code: CodePlaceUnknownAnchor,
				Message: fmt.Sprintf(
					"Anchor %q is not among the post-op children of %q; a ReorderChildren naming it would be refused as OrderingMismatch.",
					anchor, parentID),
				NodeID:   anchor,
				ParentID: parentID,
			}
		}
		out := make([]string, 0, len(rest)+1)
		out = append(out, rest[:at]...)
		out = append(out, moved)
		out = append(out, rest[at:]...)
		return out, nil
	}

	switch placement.Kind {
	case PlacementLast:
		return append(rest, moved), nil
	case PlacementFirst:
		return append([]string{moved}, rest...), nil
	case PlacementBefore:
		return anchored(placement.Anchor, 0)
	case PlacementAfter:
		return anchored(placement.Anchor, 1)
	}
	return nil, &PlaceError{
		Code: CodePlaceUnknownPlacement,
		Message: fmt.Sprintf(
			"Placement kind %q is not one of Last / First / Before / After.", placement.Kind),
	}
}

// structurallyPresent reports whether nodeID is addressable by the structural
// ops: the root, or a node reachable through a layout children list. A node
// held in a non-structural slot is visible to traversal but not movable, and
// the apply engine refuses ops against it as NodeNotFound.
func structurallyPresent(nodeID string, root wire.Node) bool {
	if root.ID == nodeID {
		return true
	}
	_, found := findLayoutParent(nodeID, root)
	return found
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func insertChildOp(parentID string, child wire.Node) wire.Obj {
	return wire.Obj{Tag: "InsertChild", Fields: map[string]wire.Value{
		"child":    child,
		"parentId": wire.Str(parentID),
	}}
}

func moveNodeOp(target, newParentID string) wire.Obj {
	return wire.Obj{Tag: "MoveNode", Fields: map[string]wire.Value{
		"newParentId": wire.Str(newParentID),
		"target":      wire.Str(target),
	}}
}

func reorderChildrenOp(parentID string, order []string) wire.Obj {
	arr := make(wire.Arr, 0, len(order))
	for _, id := range order {
		arr = append(arr, wire.Str(id))
	}
	return wire.Obj{Tag: "ReorderChildren", Fields: map[string]wire.Value{
		"newOrder": arr,
		"parentId": wire.Str(parentID),
	}}
}

func batchOp(inner ...wire.Obj) wire.Obj {
	arr := make(wire.Arr, 0, len(inner))
	for _, op := range inner {
		arr = append(arr, op)
	}
	return wire.Obj{Tag: "Batch", Fields: map[string]wire.Value{"ops": arr}}
}

// ── The verbs ───────────────────────────────────────────────────────────────

// CanPlace reports whether moved may legally take up residence at target — the
// pre-check a caller uses to refuse an illegal placement without a dry-run
// apply. It returns nil, or a *PlaceError mirroring the apply engine's own
// rejection: absent or non-structural node, move into itself, move into its own
// descendant (a cycle), absent or childless destination, unknown anchor.
func CanPlace(root wire.Node, moved string, target Target) error {
	if err := canPlace(root, moved, target); err != nil {
		return err
	}
	return nil
}

func canPlace(root wire.Node, moved string, target Target) *PlaceError {
	if !structurallyPresent(moved, root) {
		return &PlaceError{
			Code:    CodePlaceNodeNotFound,
			Message: fmt.Sprintf("Node %q is not structurally addressable in the tree.", moved),
			NodeID:  moved,
		}
	}
	if target.ParentID == moved {
		return &PlaceError{
			Code:     CodePlaceMoveIntoSelf,
			Message:  fmt.Sprintf("Cannot move node %q into itself.", moved),
			NodeID:   moved,
			ParentID: target.ParentID,
		}
	}
	if isAncestor(moved, target.ParentID, root) {
		return &PlaceError{
			Code: CodePlaceMoveIntoDescendant,
			Message: fmt.Sprintf(
				"Cannot move node %q into its own descendant %q (would create a cycle).",
				moved, target.ParentID),
			NodeID:   moved,
			ParentID: target.ParentID,
		}
	}
	siblings, err := containerChildren(root, target.ParentID)
	if err != nil {
		return err
	}
	_, err = reposition(postMoveMembership(siblings, moved), moved, target.ParentID, target.Placement)
	return err
}

// postMoveMembership is the destination's child ids after the op lands: the
// siblings WITHOUT the moved node, plus it. MoveNode appends, and the node may
// already be one of that parent's children (a re-placement within one parent),
// so removing it first is what makes the two cases one computation.
func postMoveMembership(siblings []string, moved string) []string {
	out := make([]string, 0, len(siblings)+1)
	for _, id := range siblings {
		if id != moved {
			out = append(out, id)
		}
	}
	return append(out, moved)
}

// PlaceOp is the op an insertion becomes. InsertChild appends, so the wanted
// order is computed over the post-insert membership and stated by a
// ReorderChildren naming every sibling id; the reorder leg is dropped when
// appending already produces that order, leaving one bare InsertChild.
//
// The returned error, when non-nil, is a *PlaceError.
func PlaceOp(root, child wire.Node, target Target) (wire.Obj, error) {
	op, err := placeOp(root, child, target)
	if err != nil {
		return wire.Obj{}, err
	}
	return op, nil
}

func placeOp(root, child wire.Node, target Target) (wire.Obj, *PlaceError) {
	siblings, err := containerChildren(root, target.ParentID)
	if err != nil {
		return wire.Obj{}, err
	}
	if dup, clash := firstSharedID(root, child); clash {
		return wire.Obj{}, &PlaceError{
			Code: CodePlaceDuplicateID,
			Message: fmt.Sprintf(
				"NodeId %q is already present in the tree; ids must be unique.", dup),
			NodeID:   dup,
			ParentID: target.ParentID,
		}
	}

	appended := make([]string, 0, len(siblings)+1)
	appended = append(appended, siblings...)
	appended = append(appended, child.ID)

	wanted, err := reposition(appended, child.ID, target.ParentID, target.Placement)
	if err != nil {
		return wire.Obj{}, err
	}
	insert := insertChildOp(target.ParentID, child)
	if equalIDs(wanted, appended) {
		return insert, nil
	}
	return batchOp(insert, reorderChildrenOp(target.ParentID, wanted)), nil
}

// MoveOp is the op a move becomes: MoveNode alone when appending already yields
// the wanted order, else Batch [MoveNode, ReorderChildren].
//
// The returned error, when non-nil, is a *PlaceError.
func MoveOp(root wire.Node, moved string, target Target) (wire.Obj, error) {
	op, err := moveOp(root, moved, target)
	if err != nil {
		return wire.Obj{}, err
	}
	return op, nil
}

func moveOp(root wire.Node, moved string, target Target) (wire.Obj, *PlaceError) {
	if err := canPlace(root, moved, target); err != nil {
		return wire.Obj{}, err
	}
	siblings, err := containerChildren(root, target.ParentID)
	if err != nil {
		return wire.Obj{}, err
	}
	appended := postMoveMembership(siblings, moved)

	wanted, err := reposition(appended, moved, target.ParentID, target.Placement)
	if err != nil {
		return wire.Obj{}, err
	}
	move := moveNodeOp(moved, target.ParentID)
	if equalIDs(wanted, appended) {
		return move, nil
	}
	return batchOp(move, reorderChildrenOp(target.ParentID, wanted)), nil
}

// NudgeOp is the op a keyboard move-up (-1) / move-down (+1) becomes: the node
// swapped with the sibling delta positions away, stated as the FULL sibling id
// order — which is what ReorderChildren requires, a partial list being refused
// by the apply engine, and rightly, since a partial order is not one.
//
// A delta of 0 is in range and emits a ReorderChildren restating the current
// order. That mirrors the reference host and the parity is deliberate; a caller
// that wants no op for a no-op nudge tests delta itself, or reaches for
// ReorderOp, which drops the identity restatement by contract.
//
// The returned error, when non-nil, is a *PlaceError.
func NudgeOp(root wire.Node, nodeID string, delta int) (wire.Obj, error) {
	op, err := nudgeOp(root, nodeID, delta)
	if err != nil {
		return wire.Obj{}, err
	}
	return op, nil
}

func nudgeOp(root wire.Node, nodeID string, delta int) (wire.Obj, *PlaceError) {
	if root.ID == nodeID {
		return wire.Obj{}, &PlaceError{
			Code:    CodePlaceCannotNudgeRoot,
			Message: fmt.Sprintf("Node %q is the root; it has no siblings to nudge among.", nodeID),
			NodeID:  nodeID,
		}
	}
	parent, found := findLayoutParent(nodeID, root)
	if !found {
		return wire.Obj{}, &PlaceError{
			Code:    CodePlaceNodeNotFound,
			Message: fmt.Sprintf("Node %q is not structurally addressable in the tree.", nodeID),
			NodeID:  nodeID,
		}
	}
	children, _ := layoutChildren(parent)
	ids := make([]string, 0, len(children))
	index := -1
	for i, c := range children {
		if c.ID == nodeID {
			index = i
		}
		ids = append(ids, c.ID)
	}

	swapWith := index + delta
	if swapWith < 0 || swapWith >= len(ids) {
		return wire.Obj{}, &PlaceError{
			Code: CodePlaceNudgeOutOfRange,
			Message: fmt.Sprintf(
				"Nudging %q by %d would leave the sibling range of %q (%d sibling(s)).",
				nodeID, delta, parent.ID, len(ids)),
			NodeID:   nodeID,
			ParentID: parent.ID,
			Delta:    delta,
		}
	}

	reordered := make([]string, len(ids))
	copy(reordered, ids)
	reordered[index], reordered[swapWith] = ids[swapWith], ids[index]
	return reorderChildrenOp(parent.ID, reordered), nil
}

// ReorderOp is the op an ORDER-LEVEL reorder becomes: an arbitrary whole
// permutation of a parent's children, stated as one bare ReorderChildren — or
// (zero, false) when desired is already the order the children are in.
//
// It sits beside the anchor-relative verbs above rather than inside them
// because it serves a different caller: one that has already computed the whole
// order it wants and holds no tree to hand. Dropping the identity restatement
// is the point — an op that changes nothing still enters the op-stream, replays
// as an edit, and shows up in a diff a human is asked to review.
//
// The permutation obligation is the CALLER's, deliberately: ReorderChildren
// requires the full sibling id set, and this helper takes current rather than a
// tree precisely so it needs no traversal — which means it has no way to verify
// membership beyond what it was handed. The apply engine remains the enforcer.
func ReorderOp(parentID string, current, desired []string) (wire.Obj, bool) {
	if equalIDs(current, desired) {
		return wire.Obj{}, false
	}
	return reorderChildrenOp(parentID, desired), true
}

// ── Clone verbs ─────────────────────────────────────────────────────────────

// firstSharedID reports the first id of incoming (in traversal order) that is
// already present in root.
func firstSharedID(root, incoming wire.Node) (string, bool) {
	existing := make(map[string]bool)
	for _, id := range allIDs(root) {
		existing[id] = true
	}
	for _, id := range allIDs(incoming) {
		if existing[id] {
			return id, true
		}
	}
	return "", false
}

// remapNodeIDs rewrites every id named by rename, rebuilding the spine through
// the same childSlots seam the apply engine traverses — so the rewrite reaches
// every node the tree-wide duplicate-id contract covers, not just the
// structural child lists. A clone that kept an old id inside a Switch case or an
// ErrorBoundary slot would smuggle a duplicate past that contract.
//
// The slot list is recomputed each iteration on purpose: a childSlot's rebuild
// closure is relative to the node its slots were taken from, so accumulating
// several slot rewrites means re-deriving the slots from the node as rebuilt so
// far. Slot count and order are stable under child replacement, so the loop
// terminates.
func remapNodeIDs(n wire.Node, rename map[string]string) wire.Node {
	cur := n
	if fresh, ok := rename[cur.ID]; ok {
		cur = wire.Node{ID: fresh, Kind: cur.Kind, Extras: cur.Extras}
	}
	for i := 0; ; i++ {
		slots := childSlots(cur)
		if i >= len(slots) {
			return cur
		}
		cur = slots[i].rebuild(remapNodeIDs(slots[i].child, rename))
	}
}

// remapForInsert rewrites every id in incoming that collides with an id in
// targetRoot to a fresh, collision-free one. Ids with no collision are
// preserved — a pasted subtree keeps its identity where it can; a subtree
// duplicated within its own tree remaps every id, since every one collides.
func remapForInsert(fresh FreshIDs, targetRoot, incoming wire.Node) wire.Node {
	existing := make(map[string]bool)
	for _, id := range allIDs(targetRoot) {
		existing[id] = true
	}
	// Minted ids must also dodge the incoming subtree's own ids — one colliding
	// with a not-yet-visited incoming node would re-introduce the duplicate the
	// remap exists to remove — and each other.
	taken := make(map[string]bool, len(existing))
	for id := range existing {
		taken[id] = true
	}
	for _, id := range allIDs(incoming) {
		taken[id] = true
	}

	rename := make(map[string]string)
	for _, oldID := range allIDs(incoming) {
		if !existing[oldID] {
			continue
		}
		if _, done := rename[oldID]; done {
			continue
		}
		minted := fresh(oldID, func(candidate string) bool { return taken[candidate] })
		taken[minted] = true
		rename[oldID] = minted
	}
	if len(rename) == 0 {
		return incoming
	}
	return remapNodeIDs(incoming, rename)
}

// DuplicateOp duplicates the subtree rooted at source and places the clone at
// target, minting replacement ids with the default DerivedIDs strategy.
//
// The returned error, when non-nil, is a *PlaceError.
func DuplicateOp(root wire.Node, source string, target Target) (wire.Obj, error) {
	return DuplicateOpWith(DerivedIDs, root, source, target)
}

// DuplicateOpWith is DuplicateOp under a caller-supplied id strategy. The
// emitted op is an ordinary placed insert — the clone is a fresh subtree, so the
// standard apply gate, tree-wide duplicate-id check included, accepts it
// unchanged.
//
// The returned error, when non-nil, is a *PlaceError.
func DuplicateOpWith(fresh FreshIDs, root wire.Node, source string, target Target) (wire.Obj, error) {
	sub, found := findNode(source, root)
	if !found {
		return wire.Obj{}, &PlaceError{
			Code:    CodePlaceNodeNotFound,
			Message: fmt.Sprintf("Node %q not found in tree.", source),
			NodeID:  source,
		}
	}
	op, err := placeOp(root, remapForInsert(fresh, root, sub), target)
	if err != nil {
		return wire.Obj{}, err
	}
	return op, nil
}

// PasteOp places a subtree lifted from a DIFFERENT tree into targetRoot,
// remapping any id that collides with one already present (ids with no
// collision are preserved) under the default DerivedIDs strategy. The incoming
// subtree's ids must be unique within itself — a subtree extracted from any
// well-formed tree is.
//
// The returned error, when non-nil, is a *PlaceError.
func PasteOp(targetRoot, incoming wire.Node, target Target) (wire.Obj, error) {
	return PasteOpWith(DerivedIDs, targetRoot, incoming, target)
}

// PasteOpWith is PasteOp under a caller-supplied id strategy.
//
// The returned error, when non-nil, is a *PlaceError.
func PasteOpWith(fresh FreshIDs, targetRoot, incoming wire.Node, target Target) (wire.Obj, error) {
	op, err := placeOp(targetRoot, remapForInsert(fresh, targetRoot, incoming), target)
	if err != nil {
		return wire.Obj{}, err
	}
	return op, nil
}
