// Package aitools is the machine-reads-the-UI surface: read-only tree
// introspection (the static view an agent uses to inspect a tree it emitted —
// kind discriminator, bound binding slots + their canonical accessor,
// structural children), structural search + assertions, and the default-deny
// dispatch gate. It walks the generic decoded model, so a new NodeKind needs no
// change here — a bound slot is any Binding-tagged object, discovered
// structurally. This is the substrate for Unit-Test Your UI, the Pattern Bank,
// Grep Your Apps, Kintsugi (self-repair over the validator), and the Bouncer.
//
// Value resolution against a live host + geometry probing are renderer-side and
// out of scope (this is the source-side static surface). Field walks iterate in
// Ordinal key order (Go maps are unordered), so introspection is deterministic
// and matches the canonical encoder's key order.
package aitools

import (
	"sort"

	"github.com/fuaran-ui/fuaran-go/wire"
)

var bindingCaseSet = func() map[string]bool {
	m := make(map[string]bool)
	for _, c := range wire.BindingCases() {
		m[c] = true
	}
	return m
}()

// BindingSlot is one data-bound slot on a node: its dotted field path within
// the kind, the Binding case that produced it, and the canonical wire-form
// accessor.
type BindingSlot struct {
	Slot       string
	Source     string
	Expression string
}

// NodeIntrospection is the per-node introspection envelope.
type NodeIntrospection struct {
	ID       string
	Kind     string
	Bindings []BindingSlot
	ChildIDs []string
}

// TreeIntrospection is a recursive structural snapshot of a whole tree.
type TreeIntrospection struct {
	NodeIntrospection
	Children []TreeIntrospection
}

// KindName is the wire discriminator for a node's kind.
func KindName(node wire.Node) string {
	if node.Kind.Tag == "" {
		return "<untagged>"
	}
	return node.Kind.Tag
}

func strField(fields map[string]wire.Value, key string) string {
	if s, ok := fields[key].(wire.Str); ok {
		return string(s)
	}
	return ""
}

// bindingExpression classifies a Binding-tagged object into (source,
// expression) — the canonical wire-form accessor ($state.<key>, …).
func bindingExpression(binding wire.Obj) (string, string) {
	fields := binding.Fields
	switch binding.Tag {
	case "Static":
		return "Static", "$static"
	case "Query":
		return "Query", "$queries." + strField(fields, "name")
	case "Filter":
		return "Filter", "$filters." + strField(fields, "name")
	case "Selection":
		id := strField(fields, "nodeId")
		if id == "" {
			id = strField(fields, "sourceId")
		}
		return "Selection", "$selection." + id
	case "State":
		return "State", "$state." + strField(fields, "key")
	case "I18n":
		return "I18n", "$i18n." + strField(fields, "key")
	case "Local":
		return "Computed", "$local"
	case "Format":
		return "Computed", "$format"
	case "Transform":
		return "Computed", "$transform"
	case "Invoke":
		return "Computed", "$invoke"
	default:
		return "Computed", "$computed"
	}
}

func isBinding(value wire.Value) bool {
	o, ok := value.(wire.Obj)
	return ok && bindingCaseSet[o.Tag]
}

func sortedKeys(fields map[string]wire.Value) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BindingSlots returns every data-bound slot in the node's kind, in Ordinal
// field order, with a dotted field path. Recurses through record objects +
// arrays but NOT into a nested Node (those belong to the child).
func BindingSlots(node wire.Node) []BindingSlot {
	var slots []BindingSlot
	var walk func(value wire.Value, path string)
	walk = func(value wire.Value, path string) {
		switch v := value.(type) {
		case wire.Node:
			return // a child node's bindings are the child's
		case wire.Obj:
			if isBinding(v) {
				source, expr := bindingExpression(v)
				slots = append(slots, BindingSlot{Slot: path, Source: source, Expression: expr})
				return
			}
			for _, name := range sortedKeys(v.Fields) {
				next := name
				if path != "" {
					next = path + "." + name
				}
				walk(v.Fields[name], next)
			}
		case wire.Arr:
			for i, item := range v {
				walk(item, path+"["+itoa(i)+"]")
			}
		}
	}
	for _, name := range sortedKeys(node.Kind.Fields) {
		walk(node.Kind.Fields[name], name)
	}
	return slots
}

// ChildNodes returns the immediate structural child nodes — every Node
// reachable within the kind without passing through another Node.
func ChildNodes(node wire.Node) []wire.Node {
	var children []wire.Node
	var walk func(value wire.Value)
	walk = func(value wire.Value) {
		switch v := value.(type) {
		case wire.Node:
			children = append(children, v)
		case wire.Obj:
			for _, name := range sortedKeys(v.Fields) {
				walk(v.Fields[name])
			}
		case wire.Arr:
			for _, item := range v {
				walk(item)
			}
		}
	}
	for _, name := range sortedKeys(node.Kind.Fields) {
		walk(node.Kind.Fields[name])
	}
	return children
}

// ChildIDs returns the immediate child node ids.
func ChildIDs(node wire.Node) []string {
	children := ChildNodes(node)
	out := make([]string, len(children))
	for i, c := range children {
		out[i] = c.ID
	}
	return out
}

// WalkNodes is a depth-first walk of every node, root first.
func WalkNodes(tree wire.Node) []wire.Node {
	var acc []wire.Node
	var visit func(node wire.Node)
	visit = func(node wire.Node) {
		acc = append(acc, node)
		for _, child := range ChildNodes(node) {
			visit(child)
		}
	}
	visit(tree)
	return acc
}

// FindNode returns the first node with nodeID (depth-first), or ok=false.
func FindNode(tree wire.Node, nodeID string) (wire.Node, bool) {
	for _, node := range WalkNodes(tree) {
		if node.ID == nodeID {
			return node, true
		}
	}
	return wire.Node{}, false
}

func introspect(node wire.Node) NodeIntrospection {
	return NodeIntrospection{
		ID:       node.ID,
		Kind:     KindName(node),
		Bindings: BindingSlots(node),
		ChildIDs: ChildIDs(node),
	}
}

// NodeState is the introspection envelope for a single node by id.
func NodeState(tree wire.Node, nodeID string) (NodeIntrospection, bool) {
	node, ok := FindNode(tree, nodeID)
	if !ok {
		return NodeIntrospection{}, false
	}
	return introspect(node), true
}

// InspectTree is a recursive structural snapshot of the whole tree.
func InspectTree(tree wire.Node) TreeIntrospection {
	children := ChildNodes(tree)
	out := TreeIntrospection{NodeIntrospection: introspect(tree)}
	for _, c := range children {
		out.Children = append(out.Children, InspectTree(c))
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
