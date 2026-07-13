package ops

import "github.com/fuaran-ui/fuaran-go/wire"

// Structural traversal over the flat generic model the codec produces. The
// wire is flat, so a layout's children are just the "children" array hoisted
// under the kind discriminator; every other sub-node position (an
// ErrorBoundary's child/fallback, a Switch's default + case children, a
// FragmentDecl's body, the state surfaces) is enumerated by childSlots.

// layoutKinds are the kinds that carry an ordered children array — the only
// kinds the structural child ops (InsertChild / RemoveNode / MoveNode /
// ReorderChildren) address.
var layoutKinds = map[string]bool{
	"Box":         true,
	"SplitPanel":  true,
	"Tabs":        true,
	"Stepper":     true,
	"SummaryList": true,
	"Disclosure":  true,
}

func setKindField(n wire.Node, key string, value wire.Value) wire.Node {
	fields := make(map[string]wire.Value, len(n.Kind.Fields)+1)
	for k, v := range n.Kind.Fields {
		fields[k] = v
	}
	fields[key] = value
	return wire.Node{ID: n.ID, Kind: wire.Obj{Tag: n.Kind.Tag, Fields: fields}, Extras: n.Extras}
}

func setExtra(n wire.Node, key string, value wire.Value) wire.Node {
	extras := make(map[string]wire.Value, len(n.Extras)+1)
	for k, v := range n.Extras {
		extras[k] = v
	}
	extras[key] = value
	return wire.Node{ID: n.ID, Kind: n.Kind, Extras: extras}
}

func setStateChild(n wire.Node, key string, child wire.Node) wire.Node {
	fields := make(map[string]wire.Value)
	if state, ok := n.Extras["state"].(wire.Obj); ok {
		for k, v := range state.Fields {
			fields[k] = v
		}
	}
	fields[key] = child
	return setExtra(n, "state", wire.Obj{Fields: fields})
}

// layoutChildren returns the ordered child list for a layout node, or
// (nil, false) for a childless kind.
func layoutChildren(n wire.Node) ([]wire.Node, bool) {
	if !layoutKinds[n.Kind.Tag] {
		return nil, false
	}
	arr, ok := n.Kind.Fields["children"].(wire.Arr)
	if !ok {
		return nil, false
	}
	children := make([]wire.Node, 0, len(arr))
	for _, item := range arr {
		if child, ok := item.(wire.Node); ok {
			children = append(children, child)
		}
	}
	return children, true
}

func withLayoutChildren(n wire.Node, children []wire.Node) wire.Node {
	arr := make(wire.Arr, len(children))
	for i, c := range children {
		arr[i] = c
	}
	return setKindField(n, "children", arr)
}

// childSlot is one immediate sub-node position, with a rebuild that swaps it.
type childSlot struct {
	child   wire.Node
	rebuild func(wire.Node) wire.Node
}

func childSlots(n wire.Node) []childSlot {
	var slots []childSlot
	fields := n.Kind.Fields

	if children, ok := layoutChildren(n); ok {
		for i, child := range children {
			slots = append(slots, childSlot{child, func(c wire.Node) wire.Node {
				swapped := append([]wire.Node(nil), children...)
				swapped[i] = c
				return withLayoutChildren(n, swapped)
			}})
		}
	} else {
		switch n.Kind.Tag {
		case "ErrorBoundary":
			if child, ok := fields["child"].(wire.Node); ok {
				slots = append(slots, childSlot{child, func(c wire.Node) wire.Node {
					return setKindField(n, "child", c)
				}})
			}
			if fallback, ok := fields["fallback"].(wire.Node); ok {
				slots = append(slots, childSlot{fallback, func(c wire.Node) wire.Node {
					return setKindField(n, "fallback", c)
				}})
			}
		case "Switch":
			// The default child + each case child are editable slots; a case's
			// match is preserved on rebuild.
			if def, ok := fields["default"].(wire.Node); ok {
				slots = append(slots, childSlot{def, func(c wire.Node) wire.Node {
					return setKindField(n, "default", c)
				}})
			}
			if cases, ok := fields["cases"].(wire.Arr); ok {
				for i, item := range cases {
					caseObj, ok := item.(wire.Obj)
					if !ok {
						continue
					}
					caseChild, ok := caseObj.Fields["child"].(wire.Node)
					if !ok {
						continue
					}
					slots = append(slots, childSlot{caseChild, func(c wire.Node) wire.Node {
						newItems := append(wire.Arr(nil), cases...)
						newFields := make(map[string]wire.Value, len(caseObj.Fields))
						for k, v := range caseObj.Fields {
							newFields[k] = v
						}
						newFields["child"] = c
						newItems[i] = wire.Obj{Tag: caseObj.Tag, Fields: newFields}
						return setKindField(n, "cases", newItems)
					}})
				}
			}
		case "FragmentDecl":
			if body, ok := fields["body"].(wire.Node); ok {
				slots = append(slots, childSlot{body, func(c wire.Node) wire.Node {
					return setKindField(n, "body", c)
				}})
			}
		}
	}

	if state, ok := n.Extras["state"].(wire.Obj); ok {
		if onLoading, ok := state.Fields["onLoading"].(wire.Node); ok {
			slots = append(slots, childSlot{onLoading, func(c wire.Node) wire.Node {
				return setStateChild(n, "onLoading", c)
			}})
		}
		if onEmpty, ok := state.Fields["onEmpty"].(wire.Node); ok {
			slots = append(slots, childSlot{onEmpty, func(c wire.Node) wire.Node {
				return setStateChild(n, "onEmpty", c)
			}})
		}
	}

	return slots
}

func findNode(target string, n wire.Node) (wire.Node, bool) {
	if n.ID == target {
		return n, true
	}
	for _, slot := range childSlots(n) {
		if found, ok := findNode(target, slot.child); ok {
			return found, true
		}
	}
	return wire.Node{}, false
}

// mapNode rewrites the target node via f, rebuilding the spine above it.
// Returns (tree, false) when the target is absent.
func mapNode(target string, f func(wire.Node) wire.Node, n wire.Node) (wire.Node, bool) {
	if n.ID == target {
		return f(n), true
	}
	for _, slot := range childSlots(n) {
		if mapped, ok := mapNode(target, f, slot.child); ok {
			return slot.rebuild(mapped), true
		}
	}
	return wire.Node{}, false
}

func allIDs(n wire.Node) []string {
	ids := []string{n.ID}
	for _, slot := range childSlots(n) {
		ids = append(ids, allIDs(slot.child)...)
	}
	return ids
}

// findLayoutParent finds the layout node whose children list contains target.
func findLayoutParent(target string, n wire.Node) (wire.Node, bool) {
	if children, ok := layoutChildren(n); ok {
		for _, c := range children {
			if c.ID == target {
				return n, true
			}
		}
	}
	for _, slot := range childSlots(n) {
		if found, ok := findLayoutParent(target, slot.child); ok {
			return found, true
		}
	}
	return wire.Node{}, false
}

func isAncestor(ancestorID, descendantID string, root wire.Node) bool {
	ancestor, ok := findNode(ancestorID, root)
	if !ok {
		return false
	}
	for _, slot := range childSlots(ancestor) {
		for _, id := range allIDs(slot.child) {
			if id == descendantID {
				return true
			}
		}
	}
	return false
}
