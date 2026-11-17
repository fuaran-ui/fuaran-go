package idl

import "sort"

// Lookups over the generated rows. Everything here is DERIVED from idl_gen.go
// at package initialisation; nothing in this file is a second copy of the
// vocabulary, so a regeneration moves every answer below with it.

var (
	kindByTag    = make(map[string]*Kind, len(Kinds))
	enumByName   = make(map[string]*Enum, len(Enums))
	unionByName  = make(map[string]*Union, len(Unions))
	recordByName = make(map[string]*Record, len(Records))
	opByTag      = make(map[string]*Op, len(Ops))
)

func init() {
	for _, k := range Kinds {
		kindByTag[k.Tag] = k
	}
	for _, e := range Enums {
		enumByName[e.Name] = e
	}
	for _, u := range Unions {
		unionByName[u.Name] = u
	}
	for _, r := range Records {
		recordByName[r.Name] = r
	}
	for _, o := range Ops {
		opByTag[o.Tag] = o
	}
}

// KindByTag returns the NodeKind row for a "$type" discriminator.
func KindByTag(tag string) (*Kind, bool) { k, ok := kindByTag[tag]; return k, ok }

// EnumByName returns the enum row of that name.
func EnumByName(name string) (*Enum, bool) { e, ok := enumByName[name]; return e, ok }

// UnionByName returns the union row of that name.
func UnionByName(name string) (*Union, bool) { u, ok := unionByName[name]; return u, ok }

// RecordByName returns the record row of that name.
func RecordByName(name string) (*Record, bool) { r, ok := recordByName[name]; return r, ok }

// OpByTag returns the TreeOp row for a "$type" discriminator.
func OpByTag(tag string) (*Op, bool) { o, ok := opByTag[tag]; return o, ok }

// Field returns the named row of the kind's field table.
func (k *Kind) Field(name string) (*Field, bool) { return fieldIn(k.Fields, name) }

// Field returns the named row of the record's field table.
func (r *Record) Field(name string) (*Field, bool) { return fieldIn(r.Fields, name) }

// Field returns the named row of the case's field table.
func (c *Case) Field(name string) (*Field, bool) { return fieldIn(c.Fields, name) }

func fieldIn(fields []Field, name string) (*Field, bool) {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i], true
		}
	}
	return nil, false
}

// Case returns the union case with that tag.
func (u *Union) Case(tag string) (*Case, bool) {
	for i := range u.Cases {
		if u.Cases[i].Tag == tag {
			return &u.Cases[i], true
		}
	}
	return nil, false
}

// CaseTags returns the union's discriminators, sorted.
func (u *Union) CaseTags() []string {
	out := make([]string, len(u.Cases))
	for i, c := range u.Cases {
		out[i] = c.Tag
	}
	sort.Strings(out)
	return out
}

// KindTags returns every NodeKind discriminator, sorted.
func KindTags() []string {
	out := make([]string, len(Kinds))
	for i, k := range Kinds {
		out[i] = k.Tag
	}
	sort.Strings(out)
	return out
}

// OpTags returns every TreeOp discriminator, sorted.
func OpTags() []string {
	out := make([]string, len(Ops))
	for i, o := range Ops {
		out[i] = o.Tag
	}
	sort.Strings(out)
	return out
}

// RequiredFields returns the kind's required wire fields, in table order.
func (k *Kind) RequiredFields() []string {
	var out []string
	for _, f := range k.Fields {
		if f.Optionality == Required {
			out = append(out, f.Name)
		}
	}
	return out
}

// IsNodeList reports whether t is a list of nodes — an ordered child slot.
func (t Type) IsNodeList() bool { return t.Kind == TList && t.Elem != nil && t.Elem.Kind == TNode }

// SlotShape is how one kind field holds nodes.
type SlotShape uint8

const (
	// SlotList is a list of nodes: the kind's ordered child list, the one slot
	// the structural ops InsertChild / RemoveNode / MoveNode / ReorderChildren
	// address.
	SlotList SlotShape = iota + 1
	// SlotNode is one node in a named arm (ErrorBoundary's child and fallback,
	// a FragmentDecl's body, a Switch's default).
	SlotNode
	// SlotRecords is a list of records, each holding one node in its Inner
	// field (a Switch's cases).
	SlotRecords
	// SlotArgs is a map of union values whose Case holds one node in its Inner
	// field (a FragmentRef's args, a Mount's inputs).
	SlotArgs
)

// NodeSlot is one field of a kind that holds nodes.
type NodeSlot struct {
	Field string
	Shape SlotShape
	// Case is the union case that carries a node (SlotArgs only).
	Case string
	// Inner is the record or case field holding the node (SlotRecords and
	// SlotArgs only).
	Inner string
}

// nodeSlotsByTag is every kind's slot list, derived once from the field
// tables.
var nodeSlotsByTag = func() map[string][]NodeSlot {
	out := make(map[string][]NodeSlot, len(Kinds))
	for _, k := range Kinds {
		var slots []NodeSlot
		for _, f := range k.Fields {
			if f.Optionality == HostOnly {
				continue
			}
			if s, ok := nodeSlotOf(f); ok {
				slots = append(slots, s)
			}
		}
		out[k.Tag] = slots
	}
	return out
}()

// nodeSlotOf classifies one field: a list of nodes, a node, a list of records
// holding a node, or a map of union values one case of which holds a node. It
// reads the record and union tables directly rather than through the lookup
// maps, which init() fills only after variable initialisation.
func nodeSlotOf(f Field) (NodeSlot, bool) {
	t := f.Type
	switch {
	case t.IsNodeList():
		return NodeSlot{Field: f.Name, Shape: SlotList}, true
	case t.Kind == TNode:
		return NodeSlot{Field: f.Name, Shape: SlotNode}, true
	case t.Kind == TList && t.Elem != nil && t.Elem.Kind == TRecord:
		for _, r := range Records {
			if r.Name != t.Elem.Name {
				continue
			}
			if inner, ok := nodeFieldOf(r.Fields); ok {
				return NodeSlot{Field: f.Name, Shape: SlotRecords, Inner: inner}, true
			}
		}
	case t.Kind == TMap && t.Elem != nil && t.Elem.Kind == TUnion:
		for _, u := range Unions {
			if u.Name != t.Elem.Name {
				continue
			}
			for _, c := range u.Cases {
				if inner, ok := nodeFieldOf(c.Fields); ok {
					return NodeSlot{Field: f.Name, Shape: SlotArgs, Case: c.Tag, Inner: inner}, true
				}
			}
		}
	}
	return NodeSlot{}, false
}

func nodeFieldOf(fields []Field) (string, bool) {
	for _, f := range fields {
		if f.Optionality != HostOnly && f.Type.Kind == TNode {
			return f.Name, true
		}
	}
	return "", false
}

// NodeSlots returns the kind's node-holding fields, in table order: the ONE
// slot list the host's child enumeration (ops.Children) reads. Nil for an
// unknown or childless kind.
func NodeSlots(tag string) []NodeSlot { return nodeSlotsByTag[tag] }

// OrderedChildren returns the field holding the kind's ordered child list (its
// SlotList slot, which the structural ops address), or false for a kind with
// none. The set of kinds answering true is the one layout set every package
// shares.
func OrderedChildren(tag string) (string, bool) {
	for _, s := range nodeSlotsByTag[tag] {
		if s.Shape == SlotList {
			return s.Field, true
		}
	}
	return "", false
}
