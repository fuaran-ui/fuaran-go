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
	// The seven ops that cannot grow the tree must not pay for a walk to
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
