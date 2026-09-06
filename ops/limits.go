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
// ONLY THE THREE GROWING OPS ARE CHECKED — InsertChild, ReplaceRoot, and Batch
// (which can contain either). The other seven cannot increase depth or count:
// UpdateProp, ReplaceBinding, UpdateStyle, UpdateState and EditNode rewrite a
// node in place, and RemoveNode and MoveNode shrink or reshape. Checking them
// would cost a full tree walk per op on the hot path to establish something
// their own semantics already guarantee. MoveNode is the one worth naming
// explicitly: it relocates a subtree, so it CAN deepen the tree — but only
// within a total node count that cannot change, and the depth it can reach is
// bounded by the tree that already passed. A tree that is already over the
// limit got there through an insert.

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

// opCanGrow reports whether an op can increase the tree's depth or node count,
// and therefore whether the result needs checking.
func opCanGrow(op wire.Obj) bool {
	switch op.Tag {
	case "InsertChild", "ReplaceRoot":
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
		return false
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
