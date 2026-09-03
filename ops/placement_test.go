package ops

import (
	"errors"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Placement / clone-verb tests, mirroring the reference host's property cases:
//
//  1. the emitted op applies, and applies to the DECLARED order;
//  2. helper rejections correspond 1:1 to apply-engine rejections — no false
//     permit, no false refuse;
//  3. a duplicate never collides with any id in the target tree, and is
//     structurally equal to its source modulo ids;
//  4. a paste of a subtree carrying ids already present in the target still
//     succeeds, via remap.
//
// The order property is checked against an INDEPENDENT oracle (position of the
// moved id relative to the anchor, plus "every other sibling keeps its relative
// order"), never by re-running the derivation the helper used — a check that
// re-used reposition would agree with the implementation by construction and
// prove nothing.

// ── Fixtures ────────────────────────────────────────────────────────────────

func switchOf(id, caseChildJSON, defaultJSON string) string {
	return `{"id":"` + id + `","kind":{"$type":"Switch","cases":[{"child":` + caseChildJSON +
		`,"match":"one"}],"default":` + defaultJSON + `,"stateKey":"view"}}`
}

// placementFixtureJSON is the sweep tree. It deliberately carries all four
// shapes the rejection contract distinguishes: nested layout parents (root,
// mid), childless kinds (every Markdown), and nodes held in NON-structural
// slots (swd / swc under the Switch) that traversal can see but the
// structural ops cannot address.
//
//	root ── a, mid ── (m1, m2), sw ── {default: swd, case child: swc}, z
func placementFixtureJSON() string {
	return stackOf("root",
		markdownOf("a", "A"),
		stackOf("mid", markdownOf("m1", "M1"), markdownOf("m2", "M2")),
		switchOf("sw", markdownOf("swc", "SWC"), markdownOf("swd", "SWD")),
		markdownOf("z", "Z"),
	)
}

// placementCandidates covers every id in the fixture plus one that is not in
// the tree at all, so the sweep reaches the absent-node and absent-parent arms.
var placementCandidates = []string{"root", "a", "mid", "m1", "m2", "sw", "swd", "swc", "z", "ghost"}

func sweepPlacements() []Placement {
	out := []Placement{Last(), First()}
	for _, anchor := range placementCandidates {
		out = append(out, Before(anchor), After(anchor))
	}
	return out
}

func childIDs(t *testing.T, tree wire.Node, parentID string) ([]string, bool) {
	t.Helper()
	parent, found := findNode(parentID, tree)
	if !found {
		return nil, false
	}
	children, isLayout := layoutChildren(parent)
	if !isLayout {
		return nil, false
	}
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, c.ID)
	}
	return ids, true
}

func placeErrOf(t *testing.T, err error) *PlaceError {
	t.Helper()
	var perr *PlaceError
	if !errors.As(err, &perr) {
		t.Fatalf("expected a *PlaceError, got %v", err)
	}
	return perr
}

func indexOf(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

func without(ids []string, id string) []string {
	out := make([]string, 0, len(ids))
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// assertDeclaredOrder is the independent oracle: the moved id sits exactly
// where the placement said, and every other sibling keeps its relative order.
func assertDeclaredOrder(t *testing.T, label string, before, after []string, moved string, p Placement) {
	t.Helper()

	if got := indexOf(after, moved); got < 0 {
		t.Fatalf("%s: %q is absent from the post-op children %v", label, moved, after)
	}
	if !equalIDs(without(after, moved), without(before, moved)) {
		t.Fatalf("%s: the other siblings' relative order changed: before %v, after %v",
			label, without(before, moved), without(after, moved))
	}

	at := indexOf(after, moved)
	switch p.Kind {
	case PlacementLast:
		if at != len(after)-1 {
			t.Fatalf("%s: Last placed %q at %d of %v", label, moved, at, after)
		}
	case PlacementFirst:
		if at != 0 {
			t.Fatalf("%s: First placed %q at %d of %v", label, moved, at, after)
		}
	case PlacementBefore:
		if anchorAt := indexOf(after, p.Anchor); anchorAt != at+1 {
			t.Fatalf("%s: Before(%q) placed %q at %d, anchor at %d, in %v",
				label, p.Anchor, moved, at, anchorAt, after)
		}
	case PlacementAfter:
		if anchorAt := indexOf(after, p.Anchor); anchorAt != at-1 {
			t.Fatalf("%s: After(%q) placed %q at %d, anchor at %d, in %v",
				label, p.Anchor, moved, at, anchorAt, after)
		}
	default:
		t.Fatalf("%s: a helper accepted the unrecognised placement kind %q", label, p.Kind)
	}
}

// ── 1. The emitted op applies, to the declared order ────────────────────────

func TestMoveOpEmitsTheDeclaredOrder(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	accepted := 0
	for _, moved := range placementCandidates {
		for _, parentID := range placementCandidates {
			for _, p := range sweepPlacements() {
				target := Target{ParentID: parentID, Placement: p}
				op, err := MoveOp(root, moved, target)
				if err != nil {
					placeErrOf(t, err) // every refusal is typed, never a bare error
					continue
				}
				accepted++
				label := "MoveOp(" + moved + " -> " + parentID + "/" + string(p.Kind) + " " + p.Anchor + ")"

				before, ok := childIDs(t, root, parentID)
				if !ok {
					t.Fatalf("%s: accepted a destination that is not a layout", label)
				}
				after, applyErr := Apply(op, root)
				if applyErr != nil {
					t.Fatalf("%s: emitted op was refused by the apply engine: %v", label, applyErr)
				}
				afterIDs, ok := childIDs(t, after, parentID)
				if !ok {
					t.Fatalf("%s: destination vanished after apply", label)
				}
				assertDeclaredOrder(t, label, postMoveMembership(before, moved), afterIDs, moved, p)
			}
		}
	}
	if accepted == 0 {
		t.Fatal("the sweep accepted nothing — the fixture or the candidate set is wrong")
	}
	t.Logf("MoveOp sweep: %d accepted placements applied to their declared order", accepted)
}

func TestPlaceOpEmitsTheDeclaredOrder(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())
	child := mustDecode(t, markdownOf("fresh", "F"))

	accepted := 0
	for _, parentID := range placementCandidates {
		for _, p := range sweepPlacements() {
			target := Target{ParentID: parentID, Placement: p}
			op, err := PlaceOp(root, child, target)
			if err != nil {
				placeErrOf(t, err)
				continue
			}
			accepted++
			label := "PlaceOp(fresh -> " + parentID + "/" + string(p.Kind) + " " + p.Anchor + ")"

			before, ok := childIDs(t, root, parentID)
			if !ok {
				t.Fatalf("%s: accepted a destination that is not a layout", label)
			}
			after, applyErr := Apply(op, root)
			if applyErr != nil {
				t.Fatalf("%s: emitted op was refused by the apply engine: %v", label, applyErr)
			}
			afterIDs, ok := childIDs(t, after, parentID)
			if !ok {
				t.Fatalf("%s: destination vanished after apply", label)
			}
			assertDeclaredOrder(t, label, append(append([]string{}, before...), "fresh"), afterIDs, "fresh", p)
		}
	}
	if accepted == 0 {
		t.Fatal("the sweep accepted nothing — the fixture or the candidate set is wrong")
	}
	t.Logf("PlaceOp sweep: %d accepted placements applied to their declared order", accepted)
}

// TestAppendingDropsTheReorderLeg pins the ergonomic half: when appending
// already yields the wanted order, the emitted op is ONE bare op, not a Batch
// carrying a reorder that restates the order the tree is already in.
func TestAppendingDropsTheReorderLeg(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())
	child := mustDecode(t, markdownOf("fresh", "F"))

	cases := []struct {
		name    string
		op      func() (wire.Obj, error)
		wantTag string
	}{
		{"place-last", func() (wire.Obj, error) {
			return PlaceOp(root, child, Target{ParentID: "mid", Placement: Last()})
		}, "InsertChild"},
		{"place-after-final-sibling", func() (wire.Obj, error) {
			return PlaceOp(root, child, Target{ParentID: "mid", Placement: After("m2")})
		}, "InsertChild"},
		{"place-first", func() (wire.Obj, error) {
			return PlaceOp(root, child, Target{ParentID: "mid", Placement: First()})
		}, "Batch"},
		{"move-last", func() (wire.Obj, error) {
			return MoveOp(root, "a", Target{ParentID: "mid", Placement: Last()})
		}, "MoveNode"},
		{"move-within-parent-to-end", func() (wire.Obj, error) {
			return MoveOp(root, "m1", Target{ParentID: "mid", Placement: Last()})
		}, "MoveNode"},
		{"move-before", func() (wire.Obj, error) {
			return MoveOp(root, "a", Target{ParentID: "mid", Placement: Before("m2")})
		}, "Batch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op, err := tc.op()
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if op.Tag != tc.wantTag {
				t.Errorf("emitted %q, want %q", op.Tag, tc.wantTag)
			}
			if !CanApply(op, root) {
				t.Errorf("emitted op does not apply")
			}
		})
	}
}

// ── 2. Rejection parity with the apply engine ───────────────────────────────

// TestMoveRejectionParityIsBiconditional is the 1:1 claim, established by
// exhaustion rather than asserted: over every (moved, parent) pair in the
// fixture, CanPlace accepts exactly when the apply engine accepts the bare
// MoveNode a caller would otherwise have hand-written.
//
// It is run at Last() deliberately. Last introduces no anchor, so the only
// refusals reachable are the seven that mirror the apply engine — which is what
// makes the biconditional exact rather than approximate. The anchor arm is a
// deliberate TIGHTENING and is covered separately below.
func TestMoveRejectionParityIsBiconditional(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	permits, refusals := 0, 0
	for _, moved := range placementCandidates {
		for _, parentID := range placementCandidates {
			helperOK := canPlace(root, moved, Target{ParentID: parentID, Placement: Last()}) == nil
			engineOK := CanApply(moveNodeOp(moved, parentID), root)
			if helperOK != engineOK {
				verdict := "REFUSED"
				if helperOK {
					verdict = "PERMITTED"
				}
				t.Errorf("move %q -> %q: helper %s, apply engine disagreed (engineOK=%v)",
					moved, parentID, verdict, engineOK)
			}
			if helperOK {
				permits++
			} else {
				refusals++
			}
		}
	}
	if permits == 0 || refusals == 0 {
		t.Fatalf("a vacuous sweep proves nothing: %d permits, %d refusals", permits, refusals)
	}
	t.Logf("move rejection parity: %d permits, %d refusals, %d pairs — biconditional holds",
		permits, refusals, permits+refusals)
}

// TestPlaceRejectionParityIsBiconditional is the same exhaustion for insertion:
// PlaceOp accepts exactly when the apply engine accepts the bare InsertChild.
// Run twice — once with a fresh child, once with a child whose id is already in
// the tree — so the duplicate-id arm is exercised on both sides.
func TestPlaceRejectionParityIsBiconditional(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	children := map[string]wire.Node{
		"fresh":     mustDecode(t, markdownOf("fresh", "F")),
		"colliding": mustDecode(t, markdownOf("m1", "clash")),
		"nested-collision": mustDecode(t, stackOf("outer-new",
			markdownOf("swd", "clashes with a non-structural slot"))),
	}

	for name, child := range children {
		t.Run(name, func(t *testing.T) {
			permits, refusals := 0, 0
			for _, parentID := range placementCandidates {
				_, helperErr := placeOp(root, child, Target{ParentID: parentID, Placement: Last()})
				helperOK := helperErr == nil
				engineOK := CanApply(insertChildOp(parentID, child), root)
				if helperOK != engineOK {
					t.Errorf("insert %q -> %q: helperOK=%v, engineOK=%v (helper said %v)",
						child.ID, parentID, helperOK, engineOK, helperErr)
				}
				if helperOK {
					permits++
				} else {
					refusals++
				}
			}
			if refusals == 0 {
				t.Fatalf("vacuous: no refusals over %d parents", permits)
			}
			t.Logf("%s: %d permits, %d refusals", name, permits, refusals)
		})
	}
}

// TestPlaceErrorCodesMirrorApplyCodes pins the CODE correspondence, which the
// biconditional above deliberately does not: the two check different orderings
// of the same predicates, so a curated table is the honest place to state which
// apply code each refusal pre-states.
func TestPlaceErrorCodesMirrorApplyCodes(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	cases := []struct {
		name      string
		place     func() error
		wantPlace PlaceErrorCode
		engineOp  wire.Obj
		wantApply ApplyErrorCode
	}{
		{
			name:      "absent destination",
			place:     func() error { return CanPlace(root, "a", Target{"ghost", Last()}) },
			wantPlace: CodePlaceParentNotFound,
			engineOp:  moveNodeOp("a", "ghost"),
			wantApply: CodeParentNotFound,
		},
		{
			name:      "childless destination",
			place:     func() error { return CanPlace(root, "a", Target{"z", Last()}) },
			wantPlace: CodePlaceChildlessKind,
			engineOp:  moveNodeOp("a", "z"),
			wantApply: CodeChildlessKind,
		},
		{
			name:      "absent node",
			place:     func() error { return CanPlace(root, "ghost", Target{"mid", Last()}) },
			wantPlace: CodePlaceNodeNotFound,
			engineOp:  moveNodeOp("ghost", "mid"),
			wantApply: CodeNodeNotFound,
		},
		{
			name:      "node in a non-structural slot",
			place:     func() error { return CanPlace(root, "swc", Target{"mid", Last()}) },
			wantPlace: CodePlaceNodeNotFound,
			engineOp:  moveNodeOp("swc", "mid"),
			wantApply: CodeNodeNotFound,
		},
		{
			name:      "move into self",
			place:     func() error { return CanPlace(root, "mid", Target{"mid", Last()}) },
			wantPlace: CodePlaceMoveIntoSelf,
			engineOp:  moveNodeOp("mid", "mid"),
			wantApply: CodeKindMismatch,
		},
		{
			name:      "move into own descendant",
			place:     func() error { return CanPlace(root, "root", Target{"mid", Last()}) },
			wantPlace: CodePlaceMoveIntoDescendant,
			engineOp:  moveNodeOp("root", "mid"),
			wantApply: CodeKindMismatch,
		},
		{
			name: "duplicate id",
			place: func() error {
				_, err := PlaceOp(root, mustDecode(t, markdownOf("m1", "clash")), Target{"mid", Last()})
				return err
			},
			wantPlace: CodePlaceDuplicateID,
			engineOp:  insertChildOp("mid", mustDecode(t, markdownOf("m1", "clash"))),
			wantApply: CodeDuplicateNodeID,
		},
		{
			name:      "unknown anchor",
			place:     func() error { return CanPlace(root, "a", Target{"mid", Before("z")}) },
			wantPlace: CodePlaceUnknownAnchor,
			// The tightening: the only op that could honour the anchor is a
			// ReorderChildren naming it, and the engine refuses that.
			engineOp:  reorderChildrenOp("mid", []string{"m1", "m2", "a", "z"}),
			wantApply: CodeOrderingMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perr := placeErrOf(t, tc.place())
			if perr.Code != tc.wantPlace {
				t.Errorf("PlaceError code = %q, want %q (%s)", perr.Code, tc.wantPlace, perr.Message)
			}
			if got := applyErrCode(t, tc.engineOp, root); got != tc.wantApply {
				t.Errorf("apply code = %q, want %q", got, tc.wantApply)
			}
		})
	}
}

// TestUnknownAnchorRefusalsAreExactlyTheNonSiblings sweeps the anchor arm: an
// anchor that IS among the post-op children is never refused (no false refuse),
// and one that is not is always refused as UnknownAnchor (the tightening).
func TestUnknownAnchorRefusalsAreExactlyTheNonSiblings(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	checked := 0
	for _, moved := range placementCandidates {
		for _, parentID := range placementCandidates {
			if canPlace(root, moved, Target{parentID, Last()}) != nil {
				continue // the destination itself is illegal; the anchor arm is unreachable
			}
			siblings, cErr := containerChildren(root, parentID)
			if cErr != nil {
				t.Fatalf("Last() accepted %q but containerChildren refused it: %v", parentID, cErr)
			}
			membership := postMoveMembership(siblings, moved)

			for _, anchor := range placementCandidates {
				isSibling := indexOf(membership, anchor) >= 0 && anchor != moved
				for _, p := range []Placement{Before(anchor), After(anchor)} {
					checked++
					err := canPlace(root, moved, Target{parentID, p})
					switch {
					case isSibling && err != nil:
						t.Errorf("move %q -> %q %s(%q): refused a real sibling anchor: %v",
							moved, parentID, p.Kind, anchor, err)
					case !isSibling && err == nil:
						t.Errorf("move %q -> %q %s(%q): permitted a non-sibling anchor",
							moved, parentID, p.Kind, anchor)
					case !isSibling && err.Code != CodePlaceUnknownAnchor:
						t.Errorf("move %q -> %q %s(%q): code %q, want UnknownAnchor",
							moved, parentID, p.Kind, anchor, err.Code)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("vacuous sweep")
	}
	t.Logf("anchor arm: %d (moved, parent, anchor, kind) combinations checked", checked)
}

// TestUnrecognisedPlacementKindIsRefused covers the one refusal Go forces and
// the reference host's closed DU makes unreachable: the zero value.
func TestUnrecognisedPlacementKindIsRefused(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	for _, p := range []Placement{{}, {Kind: "Middle"}} {
		if perr := placeErrOf(t, CanPlace(root, "a", Target{"mid", p})); perr.Code != CodePlaceUnknownPlacement {
			t.Errorf("Placement{Kind:%q}: code %q, want UnknownPlacement", p.Kind, perr.Code)
		}
		_, err := PlaceOp(root, mustDecode(t, markdownOf("fresh", "F")), Target{"mid", p})
		if perr := placeErrOf(t, err); perr.Code != CodePlaceUnknownPlacement {
			t.Errorf("PlaceOp Placement{Kind:%q}: code %q, want UnknownPlacement", p.Kind, perr.Code)
		}
	}
}

// ── Nudge ───────────────────────────────────────────────────────────────────

func TestNudgeOp(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	ok := []struct {
		name  string
		node  string
		delta int
		want  []string
	}{
		{"down", "a", 1, []string{"mid", "a", "sw", "z"}},
		{"up", "mid", -1, []string{"mid", "a", "sw", "z"}},
		{"down-two", "a", 2, []string{"sw", "mid", "a", "z"}},
		{"zero-restates-the-current-order", "a", 0, []string{"a", "mid", "sw", "z"}},
		{"nested-parent", "m2", -1, []string{"m2", "m1"}},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			op, err := NudgeOp(root, tc.node, tc.delta)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if op.Tag != "ReorderChildren" {
				t.Fatalf("emitted %q, want a bare ReorderChildren", op.Tag)
			}
			after, applyErr := Apply(op, root)
			if applyErr != nil {
				t.Fatalf("emitted op refused by the apply engine: %v", applyErr)
			}
			parent, _ := findLayoutParent(tc.node, root)
			got, _ := childIDs(t, after, parent.ID)
			if !equalIDs(got, tc.want) {
				t.Errorf("order = %v, want %v", got, tc.want)
			}
		})
	}

	bad := []struct {
		name  string
		node  string
		delta int
		want  PlaceErrorCode
	}{
		{"root", "root", 1, CodePlaceCannotNudgeRoot},
		{"already-first", "a", -1, CodePlaceNudgeOutOfRange},
		{"already-last", "z", 1, CodePlaceNudgeOutOfRange},
		{"past-the-end", "a", 4, CodePlaceNudgeOutOfRange},
		{"absent", "ghost", 1, CodePlaceNodeNotFound},
		{"non-structural-slot", "swc", 1, CodePlaceNodeNotFound},
	}
	for _, tc := range bad {
		t.Run("reject-"+tc.name, func(t *testing.T) {
			_, err := NudgeOp(root, tc.node, tc.delta)
			if perr := placeErrOf(t, err); perr.Code != tc.want {
				t.Errorf("code = %q, want %q (%s)", perr.Code, tc.want, perr.Message)
			}
		})
	}
}

func TestReorderOpDropsTheIdentityRestatement(t *testing.T) {
	current := []string{"a", "mid", "sw", "z"}

	if _, emitted := ReorderOp("root", current, []string{"a", "mid", "sw", "z"}); emitted {
		t.Error("an identity reorder was emitted; it must be dropped")
	}
	op, emitted := ReorderOp("root", current, []string{"z", "a", "mid", "sw"})
	if !emitted {
		t.Fatal("a real permutation was dropped")
	}
	root := mustDecode(t, placementFixtureJSON())
	after, err := Apply(op, root)
	if err != nil {
		t.Fatalf("emitted op refused: %v", err)
	}
	got, _ := childIDs(t, after, "root")
	if !equalIDs(got, []string{"z", "a", "mid", "sw"}) {
		t.Errorf("order = %v", got)
	}
}

// ── 3 + 4. Clone verbs ──────────────────────────────────────────────────────

// canonicalShape renames every id to its pre-order position and re-encodes, so
// two subtrees compare equal exactly when they are structurally identical
// MODULO ids — which is the property a clone owes its source.
func canonicalShape(t *testing.T, n wire.Node) string {
	t.Helper()
	rename := make(map[string]string)
	for i, id := range allIDs(n) {
		rename[id] = "#" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	return mustEncode(t, remapNodeIDs(n, rename))
}

func assertNoDuplicateIDs(t *testing.T, tree wire.Node) {
	t.Helper()
	seen := make(map[string]bool)
	for _, id := range allIDs(tree) {
		if seen[id] {
			t.Fatalf("duplicate node id %q in the post-apply tree", id)
		}
		seen[id] = true
	}
}

func TestDuplicateOpNeverCollides(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	for _, source := range []string{"a", "mid", "m1", "sw", "z"} {
		for _, p := range []Placement{Last(), First(), Before("m1"), After("m2")} {
			target := Target{ParentID: "mid", Placement: p}
			label := "duplicate " + source + " -> mid/" + string(p.Kind)

			op, err := DuplicateOp(root, source, target)
			if err != nil {
				t.Fatalf("%s: unexpected refusal: %v", label, err)
			}
			after, applyErr := Apply(op, root)
			if applyErr != nil {
				t.Fatalf("%s: emitted op refused by the apply engine: %v", label, applyErr)
			}
			assertNoDuplicateIDs(t, after)

			sourceNode, _ := findNode(source, root)
			if want, got := len(allIDs(root))+len(allIDs(sourceNode)), len(allIDs(after)); want != got {
				t.Errorf("%s: tree holds %d ids, want %d", label, got, want)
			}

			// Structurally equal to the source modulo ids.
			cloneID := source + "-copy"
			clone, found := findNode(cloneID, after)
			if !found {
				t.Fatalf("%s: expected the clone at %q; ids are %v", label, cloneID, allIDs(after))
			}
			if canonicalShape(t, clone) != canonicalShape(t, sourceNode) {
				t.Errorf("%s: the clone is not structurally equal to its source modulo ids", label)
			}

			// And it landed where the placement said.
			before, _ := childIDs(t, root, "mid")
			afterIDs, _ := childIDs(t, after, "mid")
			assertDeclaredOrder(t, label, append(append([]string{}, before...), cloneID), afterIDs, cloneID, p)
		}
	}
}

func TestDuplicateOpRemapsNonStructuralSlots(t *testing.T) {
	// sw's default + case children are non-structural slots. A remap that walked
	// only layout children would clone them with their original ids and smuggle
	// a duplicate past the tree-wide id contract.
	root := mustDecode(t, placementFixtureJSON())

	op, err := DuplicateOp(root, "sw", Target{ParentID: "mid", Placement: Last()})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	after, applyErr := Apply(op, root)
	if applyErr != nil {
		t.Fatalf("emitted op refused by the apply engine: %v", applyErr)
	}
	assertNoDuplicateIDs(t, after)
	for _, want := range []string{"sw-copy", "swd-copy", "swc-copy"} {
		if _, found := findNode(want, after); !found {
			t.Errorf("expected a remapped %q; ids are %v", want, allIDs(after))
		}
	}
}

func TestDuplicateOpRefusals(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	cases := []struct {
		name   string
		source string
		target Target
		want   PlaceErrorCode
	}{
		{"absent source", "ghost", Target{"mid", Last()}, CodePlaceNodeNotFound},
		{"childless destination", "a", Target{"z", Last()}, CodePlaceChildlessKind},
		{"absent destination", "a", Target{"ghost", Last()}, CodePlaceParentNotFound},
		{"unknown anchor", "a", Target{"mid", Before("z")}, CodePlaceUnknownAnchor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DuplicateOp(root, tc.source, tc.target)
			if perr := placeErrOf(t, err); perr.Code != tc.want {
				t.Errorf("code = %q, want %q (%s)", perr.Code, tc.want, perr.Message)
			}
		})
	}
}

func TestPasteOpRemapsCollidingIDsAndPreservesTheRest(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())

	// Lifted from a different tree: "m1" collides with the target, "outsider"
	// and "lifted" do not.
	incoming := mustDecode(t, stackOf("lifted",
		markdownOf("m1", "collides"),
		markdownOf("outsider", "does not"),
	))

	op, err := PasteOp(root, incoming, Target{ParentID: "mid", Placement: First()})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	after, applyErr := Apply(op, root)
	if applyErr != nil {
		t.Fatalf("emitted op refused by the apply engine: %v", applyErr)
	}
	assertNoDuplicateIDs(t, after)

	for _, want := range []string{"lifted", "outsider", "m1-copy"} {
		if _, found := findNode(want, after); !found {
			t.Errorf("expected %q after paste; ids are %v", want, allIDs(after))
		}
	}
	if got, _ := childIDs(t, after, "mid"); !equalIDs(got, []string{"lifted", "m1", "m2"}) {
		t.Errorf("mid children = %v, want [lifted m1 m2]", got)
	}
	pasted, _ := findNode("lifted", after)
	if canonicalShape(t, pasted) != canonicalShape(t, incoming) {
		t.Error("the pasted subtree is not structurally equal to the lifted one modulo ids")
	}
}

func TestPasteOpWholeSubtreeColliding(t *testing.T) {
	// Every incoming id already present — the intra-tree duplicate case, reached
	// through the cross-tree verb.
	root := mustDecode(t, placementFixtureJSON())
	incoming, _ := findNode("mid", root)

	op, err := PasteOp(root, incoming, Target{ParentID: "root", Placement: After("a")})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	after, applyErr := Apply(op, root)
	if applyErr != nil {
		t.Fatalf("emitted op refused by the apply engine: %v", applyErr)
	}
	assertNoDuplicateIDs(t, after)
	if got, _ := childIDs(t, after, "root"); !equalIDs(got, []string{"a", "mid-copy", "mid", "sw", "z"}) {
		t.Errorf("root children = %v", got)
	}
}

// ── Fresh-id strategies ─────────────────────────────────────────────────────

func TestDerivedIDsProbesPastTakenCandidates(t *testing.T) {
	taken := map[string]bool{"n-copy": true, "n-copy-2": true}
	if got := DerivedIDs("n", func(c string) bool { return taken[c] }); got != "n-copy-3" {
		t.Errorf("DerivedIDs = %q, want n-copy-3", got)
	}
	if got := DerivedIDs("n", func(string) bool { return false }); got != "n-copy" {
		t.Errorf("DerivedIDs = %q, want n-copy", got)
	}
}

func TestSequentialIDsIsDeterministicForReplay(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())
	target := Target{ParentID: "root", Placement: Last()}

	run := func() []string {
		op, err := DuplicateOpWith(SequentialIDs("clone"), root, "mid", target)
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		after, applyErr := Apply(op, root)
		if applyErr != nil {
			t.Fatalf("emitted op refused: %v", applyErr)
		}
		return allIDs(after)
	}

	first, second := run(), run()
	if !equalIDs(first, second) {
		t.Errorf("SequentialIDs is not replay-deterministic:\n %v\n %v", first, second)
	}
	for _, want := range []string{"clone-1", "clone-2", "clone-3"} {
		if indexOf(first, want) < 0 {
			t.Errorf("expected %q among %v", want, first)
		}
	}
}

func TestFreshIDsDodgesTheIncomingSubtreesOwnIDs(t *testing.T) {
	// A strategy that would mint an id belonging to a not-yet-visited incoming
	// node must be steered off it by the `taken` predicate — otherwise the remap
	// re-introduces the very duplicate it exists to remove.
	root := mustDecode(t, placementFixtureJSON())
	incoming, _ := findNode("mid", root)

	op, err := PasteOpWith(SequentialIDs("m"), root, incoming, Target{ParentID: "root", Placement: Last()})
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	after, applyErr := Apply(op, root)
	if applyErr != nil {
		t.Fatalf("emitted op refused by the apply engine: %v", applyErr)
	}
	assertNoDuplicateIDs(t, after)
	// "m-1" / "m-2" would collide with the incoming subtree's own m1 / m2 only
	// if the prefix produced them; the sequential prefix "m" mints m-1, m-2, m-3,
	// none of which is m1/m2 — the assertion that matters is the absence of any
	// duplicate above, plus that the pre-existing ids survived untouched.
	for _, want := range []string{"m1", "m2", "mid"} {
		if _, found := findNode(want, after); !found {
			t.Errorf("paste disturbed the target tree: %q is gone", want)
		}
	}
}

// TestPlacementHelpersNeverPanic re-states the totality contract the apply
// engine holds, for the helpers that sit in front of it: every combination of
// garbage in the sweep yields a value or a typed error.
func TestPlacementHelpersNeverPanic(t *testing.T) {
	root := mustDecode(t, placementFixtureJSON())
	child := mustDecode(t, markdownOf("fresh", "F"))
	empty := mustDecode(t, markdownOf("solo", "S"))

	for _, tree := range []wire.Node{root, empty} {
		for _, id := range append(append([]string{}, placementCandidates...), "", "solo") {
			for _, p := range append(sweepPlacements(), Placement{}, Placement{Kind: "Nonsense"}) {
				target := Target{ParentID: id, Placement: p}
				_ = CanPlace(tree, id, target)
				if _, err := PlaceOp(tree, child, target); err != nil {
					placeErrOf(t, err)
				}
				if _, err := MoveOp(tree, id, target); err != nil {
					placeErrOf(t, err)
				}
				if _, err := DuplicateOp(tree, id, target); err != nil {
					placeErrOf(t, err)
				}
				if _, err := PasteOp(tree, child, target); err != nil {
					placeErrOf(t, err)
				}
				for _, delta := range []int{-99, -1, 0, 1, 99} {
					if _, err := NudgeOp(tree, id, delta); err != nil {
						placeErrOf(t, err)
					}
				}
			}
		}
	}
}
