// Package ops is the tree-op apply engine — the reducer half of "the op-stream
// is a journal". Apply folds one of the 11 TreeOp cases over a decoded Node
// tree, returning either the new tree or a typed, recoverable *ApplyError — it
// never panics on any path, and on any error the input tree is returned
// untouched (revert is implicit; wrap an op list in Batch for all-or-nothing
// atomicity).
//
// This is a sibling implementation of the reference engines, built to match
// their semantics: structural child ops (InsertChild / RemoveNode / MoveNode /
// ReorderChildren) address layout kinds only (every layout spec carries an
// ordered children list); UpdateProp paths follow the WIRE_FORMAT.md §3.4
// grammar, traversed through the same per-kind nested surface; ReplaceRoot is
// the only op that may change the root id; Batch is recursive and
// all-or-nothing.
package ops

import (
	"fmt"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// ApplyErrorCode is one of the canonical apply-error codes (parity with the
// sibling engines).
type ApplyErrorCode string

const (
	CodeNodeNotFound        ApplyErrorCode = "NodeNotFound"
	CodeParentNotFound      ApplyErrorCode = "ParentNotFound"
	CodeChildlessKind       ApplyErrorCode = "ChildlessKind"
	CodePositionOutOfRange  ApplyErrorCode = "PositionOutOfRange"
	CodeDuplicateNodeID     ApplyErrorCode = "DuplicateNodeId"
	CodeFieldNotFound       ApplyErrorCode = "FieldNotFound"
	CodeSlotNotFound        ApplyErrorCode = "SlotNotFound"
	CodeKindMismatch        ApplyErrorCode = "KindMismatch"
	CodePathInvalid         ApplyErrorCode = "PathInvalid"
	CodePathNotSupportedYet ApplyErrorCode = "PathNotSupportedYet"
	CodeOrderingMismatch    ApplyErrorCode = "OrderingMismatch"
	CodeBatchAborted        ApplyErrorCode = "BatchAborted"
)

// ApplyError is a structured, recoverable apply failure (never panicked).
// BatchIndex is the failing inner-op index when Code is BatchAborted, else -1.
type ApplyError struct {
	Code       ApplyErrorCode
	Message    string
	BatchIndex int
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func apErr(code ApplyErrorCode, message string) *ApplyError {
	return &ApplyError{Code: code, Message: message, BatchIndex: -1}
}

// Apply applies a single decoded TreeOp to tree, returning the new tree, or
// the ORIGINAL tree plus a typed *ApplyError. Never panics on a decoded op;
// fold across an op list to apply many, or wrap them in a Batch op for
// atomicity.
func Apply(op wire.Obj, tree wire.Node) (wire.Node, error) {
	result, applyErr := applyOne(op, tree)
	if applyErr != nil {
		return tree, applyErr
	}
	return result, nil
}

// CanApply is the dry-run: it reports whether op would apply cleanly to tree,
// without mutating anything (Apply is pure, so the law canApply ≡ apply
// success holds by construction; the conformance tests pin it).
func CanApply(op wire.Obj, tree wire.Node) bool {
	_, applyErr := applyOne(op, tree)
	return applyErr == nil
}

// ── Narrowing helpers (the decoded op fields are typed Value) ───────────────
//
// A decoded op guarantees these shapes; a hand-constructed op that violates
// one surfaces a KindMismatch rather than a panic (the totality contract).

var errBadShape = apErr(CodeKindMismatch, "op field has an unexpected shape (not a decoded TreeOp)")

func asStr(v wire.Value) (string, bool) {
	s, ok := v.(wire.Str)
	return string(s), ok
}

func asInt(v wire.Value) (int, bool) {
	i, ok := v.(wire.Int)
	return int(i), ok
}

// ── Single-op apply ─────────────────────────────────────────────────────────

func applyOne(op wire.Obj, root wire.Node) (wire.Node, *ApplyError) {
	fields := op.Fields

	switch op.Tag {
	case "EditNode":
		newKind, kindOK := fields["newKind"].(wire.Obj)
		target, targetOK := asStr(fields["target"])
		if !kindOK || !targetOK {
			return root, errBadShape
		}
		tree, found := mapNode(target, func(n wire.Node) wire.Node {
			return wire.Node{ID: n.ID, Kind: newKind, Extras: n.Extras}
		}, root)
		if !found {
			return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
		}
		return tree, nil

	case "UpdateProp":
		return applyUpdateProp(fields, root)

	case "ReplaceBinding":
		target, targetOK := asStr(fields["target"])
		slot, slotOK := asStr(fields["slot"])
		if !targetOK || !slotOK || fields["binding"] == nil {
			return root, errBadShape
		}
		node, found := findNode(target, root)
		if !found {
			return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
		}
		newKind, slotFound := replaceBindingSlot(slot, fields["binding"], node.Kind)
		if !slotFound {
			return root, apErr(CodeSlotNotFound, fmt.Sprintf("Binding slot '%s' not found on node '%s'.", slot, target))
		}
		tree, _ := mapNode(target, func(n wire.Node) wire.Node {
			return wire.Node{ID: n.ID, Kind: newKind, Extras: n.Extras}
		}, root)
		return tree, nil

	case "UpdateStyle":
		target, ok := asStr(fields["target"])
		if !ok || fields["style"] == nil {
			return root, errBadShape
		}
		tree, found := mapNode(target, func(n wire.Node) wire.Node {
			return setExtra(n, "style", fields["style"])
		}, root)
		if !found {
			return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
		}
		return tree, nil

	case "UpdateState":
		target, ok := asStr(fields["target"])
		if !ok || fields["state"] == nil {
			return root, errBadShape
		}
		tree, found := mapNode(target, func(n wire.Node) wire.Node {
			return setExtra(n, "state", fields["state"])
		}, root)
		if !found {
			return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
		}
		return tree, nil

	case "InsertChild":
		parentID, parentOK := asStr(fields["parentId"])
		position, posOK := asInt(fields["position"])
		child, childOK := fields["child"].(wire.Node)
		if !parentOK || !posOK || !childOK {
			return root, errBadShape
		}
		parent, found := findNode(parentID, root)
		if !found {
			return root, apErr(CodeParentNotFound, fmt.Sprintf("Parent node '%s' not found in tree.", parentID))
		}
		children, isLayout := layoutChildren(parent)
		if !isLayout {
			return root, apErr(CodeChildlessKind, fmt.Sprintf(
				"Node '%s' (kind=%s) has no children field — only layout kinds accept structural child ops.",
				parentID, parent.Kind.Tag))
		}
		if position < 0 || position > len(children) {
			return root, apErr(CodePositionOutOfRange, fmt.Sprintf(
				"Position %d is out of range for parent '%s' (valid: 0..%d).", position, parentID, len(children)))
		}
		existing := make(map[string]bool)
		for _, id := range allIDs(root) {
			existing[id] = true
		}
		for _, id := range allIDs(child) {
			if existing[id] {
				return root, apErr(CodeDuplicateNodeID, fmt.Sprintf(
					"NodeId '%s' is already present in the tree; ids must be unique.", id))
			}
		}
		newChildren := make([]wire.Node, 0, len(children)+1)
		newChildren = append(newChildren, children[:position]...)
		newChildren = append(newChildren, child)
		newChildren = append(newChildren, children[position:]...)
		tree, _ := mapNode(parentID, func(n wire.Node) wire.Node {
			return withLayoutChildren(n, newChildren)
		}, root)
		return tree, nil

	case "RemoveNode":
		target, ok := asStr(fields["target"])
		if !ok {
			return root, errBadShape
		}
		if root.ID == target {
			return root, apErr(CodeKindMismatch, "Cannot RemoveNode the root.")
		}
		parent, found := findLayoutParent(target, root)
		if !found {
			return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
		}
		children, _ := layoutChildren(parent)
		kept := make([]wire.Node, 0, len(children))
		for _, c := range children {
			if c.ID != target {
				kept = append(kept, c)
			}
		}
		tree, _ := mapNode(parent.ID, func(n wire.Node) wire.Node {
			return withLayoutChildren(n, kept)
		}, root)
		return tree, nil

	case "MoveNode":
		target, targetOK := asStr(fields["target"])
		newParentID, parentOK := asStr(fields["newParentId"])
		newPosition, posOK := asInt(fields["newPosition"])
		if !targetOK || !parentOK || !posOK {
			return root, errBadShape
		}
		if target == newParentID {
			return root, apErr(CodeKindMismatch, "Cannot move a node into itself.")
		}
		if isAncestor(target, newParentID, root) {
			return root, apErr(CodeKindMismatch, "Cannot move a node into its own descendant (would create a cycle).")
		}
		moving, found := findNode(target, root)
		if !found {
			return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
		}
		newParent, found := findNode(newParentID, root)
		if !found {
			return root, apErr(CodeParentNotFound, fmt.Sprintf("Parent node '%s' not found in tree.", newParentID))
		}
		if _, isLayout := layoutChildren(newParent); !isLayout {
			return root, apErr(CodeChildlessKind, fmt.Sprintf(
				"Node '%s' (kind=%s) has no children field.", newParentID, newParent.Kind.Tag))
		}
		afterRemove, removeErr := applyOne(wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{
			"target": wire.Str(target),
		}}, root)
		if removeErr != nil {
			return root, removeErr
		}
		return applyOne(wire.Obj{Tag: "InsertChild", Fields: map[string]wire.Value{
			"child":    moving,
			"parentId": wire.Str(newParentID),
			"position": wire.Int(newPosition),
		}}, afterRemove)

	case "ReorderChildren":
		parentID, parentOK := asStr(fields["parentId"])
		orderArr, orderOK := fields["newOrder"].(wire.Arr)
		if !parentOK || !orderOK {
			return root, errBadShape
		}
		newOrder := make([]string, len(orderArr))
		for i, v := range orderArr {
			id, ok := asStr(v)
			if !ok {
				return root, errBadShape
			}
			newOrder[i] = id
		}
		parent, found := findNode(parentID, root)
		if !found {
			return root, apErr(CodeParentNotFound, fmt.Sprintf("Parent node '%s' not found in tree.", parentID))
		}
		children, isLayout := layoutChildren(parent)
		if !isLayout {
			return root, apErr(CodeChildlessKind, fmt.Sprintf(
				"Node '%s' (kind=%s) has no children field.", parentID, parent.Kind.Tag))
		}
		if !sameIDMultiset(children, newOrder) {
			return root, apErr(CodeOrderingMismatch, fmt.Sprintf(
				"ReorderChildren for '%s' did not list exactly the current child ids.", parentID))
		}
		byID := make(map[string]wire.Node, len(children))
		for _, c := range children {
			byID[c.ID] = c
		}
		reordered := make([]wire.Node, len(newOrder))
		for i, id := range newOrder {
			reordered[i] = byID[id]
		}
		tree, _ := mapNode(parentID, func(n wire.Node) wire.Node {
			return withLayoutChildren(n, reordered)
		}, root)
		return tree, nil

	case "ReplaceRoot":
		// The whole-tree swap: the only op that legally changes the root id.
		node, ok := fields["node"].(wire.Node)
		if !ok {
			return root, errBadShape
		}
		return node, nil

	case "Batch":
		opsArr, ok := fields["ops"].(wire.Arr)
		if !ok {
			return root, errBadShape
		}
		state := root
		for i, item := range opsArr {
			inner, ok := item.(wire.Obj)
			if !ok {
				return root, errBadShape
			}
			next, innerErr := applyOne(inner, state)
			if innerErr != nil {
				// All-or-nothing: discard partial state, surface the inner failure.
				return root, &ApplyError{
					Code:       CodeBatchAborted,
					Message:    fmt.Sprintf("Batch aborted at inner op #%d: %s", i, innerErr.Message),
					BatchIndex: i,
				}
			}
			state = next
		}
		return state, nil
	}

	return root, apErr(CodeKindMismatch, fmt.Sprintf("unrecognised op kind '%s'", op.Tag))
}

func sameIDMultiset(children []wire.Node, order []string) bool {
	if len(children) != len(order) {
		return false
	}
	counts := make(map[string]int, len(children))
	for _, c := range children {
		counts[c.ID]++
	}
	for _, id := range order {
		counts[id]--
		if counts[id] < 0 {
			return false
		}
	}
	return true
}

func applyUpdateProp(fields map[string]wire.Value, root wire.Node) (wire.Node, *ApplyError) {
	path, pathOK := asStr(fields["path"])
	target, targetOK := asStr(fields["target"])
	value := fields["value"]
	if !pathOK || !targetOK || value == nil {
		return root, errBadShape
	}
	segs, reason := parsePath(path)
	if reason != "" {
		return root, apErr(CodePathInvalid, fmt.Sprintf("Path '%s' is structurally invalid: %s.", path, reason))
	}
	node, found := findNode(target, root)
	if !found {
		return root, apErr(CodeNodeNotFound, fmt.Sprintf("Node '%s' not found in tree.", target))
	}

	finish := func(newKind wire.Obj) (wire.Node, *ApplyError) {
		tree, _ := mapNode(target, func(n wire.Node) wire.Node {
			return wire.Node{ID: n.ID, Kind: newKind, Extras: n.Extras}
		}, root)
		return tree, nil
	}

	if len(segs) == 1 && !segs[0].hasIndex {
		// Top-level path — the per-kind flat field dispatch.
		outcome := updateField(path, value, node.Kind)
		switch outcome.tag {
		case outcomeUpdated:
			return finish(outcome.kind)
		case outcomeUnknownField:
			return root, apErr(CodeFieldNotFound, fmt.Sprintf("Field '%s' not found on node '%s'.", path, target))
		case outcomeNotSupported:
			return root, apErr(CodePathNotSupportedYet, fmt.Sprintf(
				"Path '%s' on node '%s' is not yet supported by the apply engine.", path, target))
		default:
			return root, apErr(CodeKindMismatch, fmt.Sprintf(
				"UpdateProp value for '%s' on '%s' does not match the field's expected type: %s",
				path, target, outcome.detail))
		}
	}

	// Nested path (WIRE_FORMAT.md §3.4) — the per-kind typed traversal.
	nested := updateNested(segs, value, node.Kind)
	switch nested.tag {
	case nestedUpdated:
		return finish(nested.kind)
	case nestedMissingIndex:
		return root, apErr(CodePathInvalid, fmt.Sprintf(
			"Field '%s' on node '%s' is a list — address an element with a 0-based index (the list has %d element(s)).",
			nested.listField, target, nested.count))
	case nestedIndexOutOfRange:
		bounds := "the list is empty"
		if nested.count > 0 {
			bounds = fmt.Sprintf("valid: 0..%d", nested.count-1)
		}
		return root, apErr(CodePositionOutOfRange, fmt.Sprintf(
			"Index %d is out of range for '%s' on node '%s' (%s).",
			nested.requested, nested.listField, target, bounds))
	case nestedFieldNotFound:
		return root, apErr(CodeFieldNotFound, fmt.Sprintf(
			"Field '%s' (in path '%s') not found on node '%s'. Available at this segment: %s.",
			nested.segment, path, target, joinComma(nested.available)))
	case nestedNotSupported:
		return root, apErr(CodePathNotSupportedYet, fmt.Sprintf(
			"Path '%s' on node '%s' is not yet supported by the apply engine.", path, target))
	default:
		return root, apErr(CodeKindMismatch, fmt.Sprintf(
			"UpdateProp value for '%s' on '%s' does not match the field's expected type: %s",
			path, target, nested.detail))
	}
}

func joinComma(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
