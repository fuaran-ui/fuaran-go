package ops

import (
	"errors"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The apply engine enforced no §21 limit at all. A tree assembled op by op —
// a progressive stream, a replay, a driven session — could grow past
// MaxNodeDepth without any single op looking unusual, producing a tree this
// host held happily and no host could decode, itself included on the next round
// trip. The renderer found it later and blamed whichever node it reached.

func box(id string, children ...wire.Node) wire.Node {
	items := make(wire.Arr, len(children))
	for i, c := range children {
		items[i] = c
	}
	return wire.Node{ID: id, Kind: wire.Obj{Tag: "Box", Fields: map[string]wire.Value{
		"children": items,
		"layout":   wire.Obj{Tag: "Flex", Fields: map[string]wire.Value{"direction": wire.Str("Vertical"), "wrap": wire.Bool(false)}},
		"role":     wire.Str("Group"),
	}}}
}

// chain builds a Box nested `depth` levels (the root is depth 1).
func chain(depth int) wire.Node {
	n := box("n1")
	for i := 2; i <= depth; i++ {
		n = box("n"+itoa(i), n)
	}
	return n
}

func insertChild(parentID string, child wire.Node) wire.Obj {
	return wire.Obj{Tag: "InsertChild", Fields: map[string]wire.Value{
		"child":    child,
		"parentId": wire.Str(parentID),
	}}
}

func TestInsertChildRefusesPastMaxNodeDepth(t *testing.T) {
	// A tree exactly at the ceiling, and the one insert that crosses it.
	tree := chain(wire.MaxNodeDepth)
	deepest := "n1"

	_, err := Apply(insertChild(deepest, box("over")), tree)

	var ae *ApplyError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T), want a *ApplyError", err, err)
	}
	if ae.Code != CodeLimitExceeded {
		t.Errorf("code = %q, want %q", ae.Code, CodeLimitExceeded)
	}
}

func TestInsertChildAtTheDepthLimitIsAccepted(t *testing.T) {
	// A guard that refused the boundary case would be a liveness bug wearing a
	// safety fix's clothes.
	tree := chain(wire.MaxNodeDepth - 1)

	after, err := Apply(insertChild("n1", box("at-the-limit")), tree)
	if err != nil {
		t.Fatalf("an insert reaching exactly MaxNodeDepth was refused: %v", err)
	}
	if depth, _ := treeMetrics(after); depth != wire.MaxNodeDepth {
		t.Errorf("resulting depth = %d, want %d", depth, wire.MaxNodeDepth)
	}
}

func TestReplaceRootRefusesAnOverDeepTree(t *testing.T) {
	// ReplaceRoot swaps the whole tree, so it is the one op that can breach the
	// limit in a single step from any starting point.
	op := wire.Obj{Tag: "ReplaceRoot", Fields: map[string]wire.Value{
		"node": chain(wire.MaxNodeDepth + 1),
	}}

	_, err := Apply(op, box("root"))

	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Code != CodeLimitExceeded {
		t.Fatalf("err = %v, want a LimitExceeded *ApplyError", err)
	}
}

func TestBatchRefusesWhenItsResultBreaches(t *testing.T) {
	// The check is on the RESULT, so a Batch whose individual ops each look
	// fine but whose composition crosses the line is refused — and the original
	// tree is returned, since a Batch is all-or-nothing.
	tree := chain(wire.MaxNodeDepth - 1)
	batch := wire.Obj{Tag: "Batch", Fields: map[string]wire.Value{
		"ops": wire.Arr{
			insertChild("n1", box("a")),
			insertChild("a", box("b")),
		},
	}}

	after, err := Apply(batch, tree)

	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Code != CodeLimitExceeded {
		t.Fatalf("err = %v, want a LimitExceeded *ApplyError", err)
	}
	if d, _ := treeMetrics(after); d != wire.MaxNodeDepth-1 {
		t.Errorf("the refused batch left a depth-%d tree; the original was %d", d, wire.MaxNodeDepth-1)
	}
}

func TestNonGrowingOpsSkipTheGuard(t *testing.T) {
	// The ops that cannot grow the tree must not pay for a walk to
	// establish what their own semantics already guarantee. Observable only as
	// behaviour: a rewrite on an ALREADY-over-limit tree still applies, because
	// it is not the op that put it there and refusing would strand the tree.
	over := chain(wire.MaxNodeDepth + 5)
	op := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path":   wire.Str("Role"),
		"target": wire.Str("n1"),
		"value":  wire.Str("Region"),
	}}

	if _, err := Apply(op, over); err != nil {
		var ae *ApplyError
		if errors.As(err, &ae) && ae.Code == CodeLimitExceeded {
			t.Error("a non-growing op was refused by the limit guard, stranding an over-limit tree")
		}
	}
}

func TestTreeMetricsCountsBothAxesInOneWalk(t *testing.T) {
	tree := box("root", box("a", box("a1")), box("b"))
	depth, count := treeMetrics(tree)
	if depth != 3 {
		t.Errorf("depth = %d, want 3", depth)
	}
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}
}

// ── Phase 2141: every op that can grow the tree is checked ──────────────────
//
// EditNode can give a node a kind that holds children and UpdateState can
// attach onLoading / onEmpty subtrees, so both put nodes into the tree; MoveNode
// adds none, but moving one legal branch under the leaf of another stacks two
// depths that each passed. All three were accepted past the limit.

// namedChain is a Box chain `depth` levels deep: top <prefix><depth>, deepest
// <prefix>1.
func namedChain(prefix string, depth int) wire.Node {
	n := box(prefix + "1")
	for i := 2; i <= depth; i++ {
		n = box(prefix+itoa(i), n)
	}
	return n
}

// repeatedIDChain is a Box chain `depth` levels deep in which every node has
// the id "x".
func repeatedIDChain(depth int) wire.Node {
	n := box("x")
	for i := 2; i <= depth; i++ {
		n = box("x", n)
	}
	return n
}

func editNode(target string, kindOf wire.Node) wire.Obj {
	return wire.Obj{Tag: "EditNode", Fields: map[string]wire.Value{
		"newKind": kindOf.Kind,
		"target":  wire.Str(target),
	}}
}

func updateLoading(target string, loading wire.Node) wire.Obj {
	return wire.Obj{Tag: "UpdateState", Fields: map[string]wire.Value{
		"state":  wire.Obj{Fields: map[string]wire.Value{"onLoading": loading}},
		"target": wire.Str(target),
	}}
}

func moveNode(target, newParentID string) wire.Obj {
	return wire.Obj{Tag: "MoveNode", Fields: map[string]wire.Value{
		"newParentId": wire.Str(newParentID),
		"target":      wire.Str(target),
	}}
}

func wide(id string, n int) wire.Node {
	children := make([]wire.Node, n)
	for i := range children {
		children[i] = box("m" + itoa(i))
	}
	return box(id, children...)
}

func requireLimitExceeded(t *testing.T, err error, what string) {
	t.Helper()
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Code != CodeLimitExceeded {
		t.Fatalf("%s: err = %v, want a LimitExceeded *ApplyError", what, err)
	}
}

func TestGrowingOpsRefusePastTheLimits(t *testing.T) {
	half := wire.MaxNodeDepth / 2
	cases := []struct {
		name string
		tree wire.Node
		op   wire.Obj
	}{
		{"EditNode past MaxNodeDepth", chain(wire.MaxNodeDepth), editNode("n1", box("carrier", box("over")))},
		{"EditNode past MaxNodes", chain(2), editNode("n1", wide("carrier", wire.MaxNodes))},
		{"UpdateState past MaxNodeDepth", chain(wire.MaxNodeDepth), updateLoading("n1", box("loading"))},
		{"UpdateState past MaxNodes", chain(2), updateLoading("n1", wide("loading", wire.MaxNodes))},
		{"MoveNode stacking two legal branches", box("root", namedChain("a", half), namedChain("b", half)), moveNode("b"+itoa(half), "a1")},
		{"ReplaceRoot with a repeated-id chain", box("root"), wire.Obj{Tag: "ReplaceRoot", Fields: map[string]wire.Value{"node": repeatedIDChain(wire.MaxNodeDepth + 6)}}},
		{"EditNode installing a repeated-id chain", chain(2), editNode("n1", box("carrier", repeatedIDChain(wire.MaxNodeDepth+6)))},
		{"Batch carrying a growing EditNode", chain(wire.MaxNodeDepth), wire.Obj{Tag: "Batch", Fields: map[string]wire.Value{"ops": wire.Arr{editNode("n1", box("carrier", box("over")))}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			after, err := Apply(c.op, c.tree)
			requireLimitExceeded(t, err, c.name)
			wantDepth, wantCount := treeMetrics(c.tree)
			if d, n := treeMetrics(after); d != wantDepth || n != wantCount {
				t.Errorf("a refused op must return the original tree")
			}
		})
	}
}

func TestGrowingOpsLandingExactlyAtTheLimitApply(t *testing.T) {
	half := wire.MaxNodeDepth / 2
	cases := []struct {
		name string
		tree wire.Node
		op   wire.Obj
	}{
		{"EditNode", chain(wire.MaxNodeDepth - 1), editNode("n1", box("carrier", box("at-the-limit")))},
		{"UpdateState", chain(wire.MaxNodeDepth - 1), updateLoading("n1", box("loading"))},
		{"MoveNode", box("root", namedChain("a", half), namedChain("b", half-1)), moveNode("b"+itoa(half-1), "a1")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			after, err := Apply(c.op, c.tree)
			if err != nil {
				t.Fatalf("an op reaching exactly MaxNodeDepth was refused: %v", err)
			}
			if d, _ := treeMetrics(after); d != wire.MaxNodeDepth {
				t.Errorf("resulting depth = %d, want %d", d, wire.MaxNodeDepth)
			}
		})
	}
}

func TestEditNodeToAChildlessKindOnAnOverLimitTreeApplies(t *testing.T) {
	// An edit that puts no subtree in cannot grow the tree, so it is not
	// charged the walk, and refusing it would strand a tree it did not create.
	over := chain(wire.MaxNodeDepth + 5)
	if _, err := Apply(editNode("n1", box("carrier")), over); err != nil {
		t.Fatalf("a non-growing EditNode was refused: %v", err)
	}
}
