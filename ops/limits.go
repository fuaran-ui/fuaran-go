package ops

import (
	"strconv"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Apply-time §21 limits.
//
// The decoder bounds what ARRIVES; nothing bounded what an apply produces. A
// tree assembled op by op — a progressive stream of small frames, a replay, a
// driven session — can therefore grow past MaxNodeDepth or MaxNodes without any
// single op looking unusual, and the result is a tree this host holds happily
// and NO host can decode, including this one on the next round trip.
//
// The failure was found late and attributed to nothing. A renderer walking the
// tree hits the depth ceiling and refuses at whichever node it happened to
// reach, which names a node that is not at fault and an operation that is long
// finished. Checking here names the op that crossed the line, at the moment it
// crossed it.
//
// WHICH OPS ARE CHECKED IS DERIVED FROM WHAT THEY CARRY, not listed. An op that
// puts nodes into the tree (insertedNodes non-empty) is checked: InsertChild,
// ReplaceRoot, an EditNode whose new kind holds children, an UpdateState that
// attaches onLoading / onEmpty. The derivation reads NODES, never ids, because
// a payload whose ids repeat has fewer ids than nodes. MoveNode is checked as
// well: it adds no node, but moving one legal branch under the leaf of another
// stacks two depths that each passed. A Batch is checked when any member is.
//
// The rest cannot grow the tree and are not charged a walk: UpdateProp,
// ReplaceBinding and UpdateStyle carry no node, RemoveNode shrinks, and
// ReorderChildren permutes. They still apply to a tree that is already over a
// limit, which they did not put there.

// CodeLimitExceeded — the applied tree breaches a §21 wire limit. Named the
// same on every host, because a client that recovers from it must not have to
// know which engine refused.
const CodeLimitExceeded ApplyErrorCode = "LimitExceeded"

// treeMetrics is one walk yielding both axes; two walks would cost twice for
// the same traversal.
func treeMetrics(n wire.Node) (depth, count int) {
	count = 1
	depth = 1
	for _, slot := range childSlots(n) {
		d, c := treeMetrics(slot.child)
		count += c
		if d+1 > depth {
			depth = d + 1
		}
	}
	return depth, count
}

// checkTreeLimits reports a *ApplyError when the resulting tree breaches the
// §21 node-depth or node-count limit.
//
// It runs on the RESULT rather than on the op, because the op alone does not
// determine either figure: the same InsertChild is fine under a shallow parent
// and over the line under a deep one. The cost is one walk of the produced
// tree, paid only by the ops that can grow it.
func checkTreeLimits(tree wire.Node) *ApplyError {
	depth, count := treeMetrics(tree)
	if depth > wire.MaxNodeDepth {
		return apErr(CodeLimitExceeded,
			"Applying this op would nest nodes "+itoa(depth)+" levels deep, past the wire limit MaxNodeDepth = "+
				itoa(wire.MaxNodeDepth)+". The resulting tree would not decode on any host.")
	}
	if count > wire.MaxNodes {
		return apErr(CodeLimitExceeded,
			"Applying this op would produce a tree of "+itoa(count)+" nodes, past the wire limit MaxNodes = "+
				itoa(wire.MaxNodes)+". The resulting tree would not decode on any host.")
	}
	return nil
}

// insertedNodes returns the subtree roots op puts INTO the tree, in the order
// it names them: an inserted child, a replacement root, the nodes a new kind
// holds (EditNode), a new state block's onLoading / onEmpty (UpdateState), and
// a Batch's members' insertions. Every other op carries no node.
func insertedNodes(op wire.Obj) []wire.Node {
	switch op.Tag {
	case "InsertChild":
		if child, ok := op.Fields["child"].(wire.Node); ok {
			return []wire.Node{child}
		}
	case "ReplaceRoot":
		if node, ok := op.Fields["node"].(wire.Node); ok {
			return []wire.Node{node}
		}
	case "EditNode":
		if kind, ok := op.Fields["newKind"].(wire.Obj); ok {
			// An envelope-free carrier, so only the kind's own positions are
			// enumerated: the edited node keeps its existing state block.
			var held []wire.Node
			for _, slot := range childSlots(wire.Node{Kind: kind}) {
				held = append(held, slot.child)
			}
			return held
		}
	case "UpdateState":
		if state, ok := op.Fields["state"].(wire.Obj); ok {
			var held []wire.Node
			for _, key := range []string{"onLoading", "onEmpty"} {
				if n, ok := state.Fields[key].(wire.Node); ok {
					held = append(held, n)
				}
			}
			return held
		}
	case "Batch":
		if inner, ok := op.Fields["ops"].(wire.Arr); ok {
			var held []wire.Node
			for _, item := range inner {
				if innerOp, ok := item.(wire.Obj); ok {
					held = append(held, insertedNodes(innerOp)...)
				}
			}
			return held
		}
	}
	return nil
}

// opCanGrow reports whether an op can increase the tree's depth or node count,
// and therefore whether the result needs checking: MoveNode, any op that puts
// nodes in, or a Batch containing either.
func opCanGrow(op wire.Obj) bool {
	switch op.Tag {
	case "MoveNode":
		return true
	case "Batch":
		inner, ok := op.Fields["ops"].(wire.Arr)
		if !ok {
			return false
		}
		for _, item := range inner {
			if innerOp, ok := item.(wire.Obj); ok && opCanGrow(innerOp) {
				return true
			}
		}
		return false
	default:
		return len(insertedNodes(op)) > 0
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
