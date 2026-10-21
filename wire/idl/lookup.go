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

// OrderedChildren returns the field holding the kind's ordered child list (the
// one list-of-nodes slot, which the structural ops InsertChild / RemoveNode /
// MoveNode / ReorderChildren address), or false for a kind with none. The set
// of kinds answering true is the one layout set every package shares.
func OrderedChildren(tag string) (string, bool) {
	k, ok := kindByTag[tag]
	if !ok {
		return "", false
	}
	for _, f := range k.Fields {
		if f.Optionality != HostOnly && f.Type.IsNodeList() {
			return f.Name, true
		}
	}
	return "", false
}

// NodeArms returns the kind's single-node slots (ErrorBoundary's child and
// fallback, FragmentDecl's body, Switch's default), in table order.
func NodeArms(tag string) []string {
	k, ok := kindByTag[tag]
	if !ok {
		return nil
	}
	var out []string
	for _, f := range k.Fields {
		if f.Optionality != HostOnly && f.Type.Kind == TNode {
			out = append(out, f.Name)
		}
	}
	return out
}
