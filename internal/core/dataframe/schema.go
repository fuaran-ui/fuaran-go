package dataframe

import (
	canonical "github.com/Fuaran-Core/fuaran-core-go/json"
	"github.com/fuaran-ui/fuaran-go/internal/core/function"
)

// The schema operations a field's metadata moves (WIRE_FORMAT §3.6.26; twin of
// the reference's Fuaran.Core Schema module, fuaran-core Phases 33 and 427): the
// fingerprint, the delta between two schemas (diff, and patch replaying it), and
// the compatibility verdict for a consumer's depended-on columns. The delta's
// wire encoding lowers into the host's structural wire model, so it lives in the
// public dataframe package beside the schema entry's encoder.

// Field metadata members — the `member` an amended entry names.
const (
	MemberUnit        = "unit"
	MemberLabel       = "label"
	MemberDescription = "description"
	MemberExt         = "ext"
)

// FieldChange is one moved metadata member of a column common to both schemas.
// A nil side is one that stated nothing. FromUnit/ToUnit carry a unit member;
// From/To carry a label, description or extension value; Key names the
// extension member.
type FieldChange struct {
	Member   string
	Key      string
	FromUnit *Unit
	ToUnit   *Unit
	From     *string
	To       *string
}

// Amendment is a FieldChange on the named column.
type Amendment struct {
	Name   string
	Change FieldChange
}

// Retype is a column common to both schemas whose type moved.
type Retype struct {
	Name string
	From string
	To   string
}

// SchemaDelta is what moves one schema into another: the added and removed
// columns, the retyped ones, the moved metadata (Amended), whether the common
// columns changed order, and the target order where patch cannot derive it.
type SchemaDelta struct {
	Added     Schema
	Removed   Schema
	Retyped   []Retype
	Amended   []Amendment
	Reordered bool
	Order     []string
}

// Schema patch refusal kinds.
const (
	SchemaDuplicateColumn   = "DuplicateColumn"
	SchemaAbsentColumn      = "AbsentColumn"
	SchemaTypeDisagrees     = "TypeDisagrees"
	SchemaMetadataDisagrees = "MetadataDisagrees"
	SchemaAlreadyPresent    = "AlreadyPresent"
	SchemaOrderMismatch     = "OrderMismatch"
)

// SchemaError is why a delta does not replay over a schema.
type SchemaError struct {
	Kind   string
	Column string
}

func (e *SchemaError) Error() string { return e.Kind + ": " + e.Column }

// Compatibility verdicts.
const (
	CompatCompatible = "compatible"
	CompatBreaking   = "breaking"
	CompatUnknown    = "unknown"
)

// SchemaCompat is a classify verdict and its reasons.
type SchemaCompat struct {
	Verdict string
	Reasons []string
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameUnit(a, b *Unit) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func extValue(ext map[string]string, k string) *string {
	if v, ok := ext[k]; ok {
		return &v
	}
	return nil
}

// sortedExtKeys is the union of the two maps' keys in ordinal (UTF-16) order.
func sortedExtKeys(a, b map[string]string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	canonical.SortKeys(keys)
	return keys
}

// fieldChanges is every moved member, in the order unit, label, description,
// then each extension key in ordinal order.
func fieldChanges(before, after SchemaEntry) []FieldChange {
	var out []FieldChange
	if !sameUnit(before.Unit, after.Unit) {
		out = append(out, FieldChange{Member: MemberUnit, FromUnit: before.Unit, ToUnit: after.Unit})
	}
	if !sameText(before.Label, after.Label) {
		out = append(out, FieldChange{Member: MemberLabel, From: before.Label, To: after.Label})
	}
	if !sameText(before.Description, after.Description) {
		out = append(out, FieldChange{Member: MemberDescription, From: before.Description, To: after.Description})
	}
	for _, k := range sortedExtKeys(before.Ext, after.Ext) {
		b, a := extValue(before.Ext, k), extValue(after.Ext, k)
		if !sameText(b, a) {
			out = append(out, FieldChange{Member: MemberExt, Key: k, From: b, To: a})
		}
	}
	return out
}

func byName(s Schema) map[string]SchemaEntry {
	m := make(map[string]SchemaEntry, len(s))
	for _, f := range s {
		m[f.Name] = f
	}
	return m
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DiffSchema is the delta that moves old into target. Order is recorded only
// where the target's order is not the one patch derives (the survivors in old
// order, then the added columns in target order).
func DiffSchema(old, target Schema) SchemaDelta {
	oldMap, newMap := byName(old), byName(target)
	d := SchemaDelta{Added: Schema{}, Removed: Schema{}, Retyped: []Retype{}, Order: []string{}}
	for _, f := range target {
		if _, ok := oldMap[f.Name]; !ok {
			d.Added = append(d.Added, f)
		}
	}
	for _, f := range old {
		nf, ok := newMap[f.Name]
		if !ok {
			d.Removed = append(d.Removed, f)
			continue
		}
		if nf.Type != f.Type {
			d.Retyped = append(d.Retyped, Retype{Name: f.Name, From: f.Type, To: nf.Type})
		}
		for _, c := range fieldChanges(f, nf) {
			d.Amended = append(d.Amended, Amendment{Name: f.Name, Change: c})
		}
	}
	var commonOld, commonNew, targetOrder []string
	for _, f := range old {
		if _, ok := newMap[f.Name]; ok {
			commonOld = append(commonOld, f.Name)
		}
	}
	for _, f := range target {
		targetOrder = append(targetOrder, f.Name)
		if _, ok := oldMap[f.Name]; ok {
			commonNew = append(commonNew, f.Name)
		}
	}
	derived := append([]string{}, commonOld...)
	for _, f := range d.Added {
		derived = append(derived, f.Name)
	}
	d.Reordered = !sameStrings(commonOld, commonNew)
	if !sameStrings(targetOrder, derived) {
		d.Order = targetOrder
	}
	return d
}

// applyChange replays one change over a field, or ok=false where the field's
// current value is not the change's `from`.
func applyChange(c FieldChange, f SchemaEntry) (SchemaEntry, bool) {
	switch c.Member {
	case MemberUnit:
		if !sameUnit(f.Unit, c.FromUnit) {
			return f, false
		}
		f.Unit = c.ToUnit
	case MemberLabel:
		if !sameText(f.Label, c.From) {
			return f, false
		}
		f.Label = c.To
	case MemberDescription:
		if !sameText(f.Description, c.From) {
			return f, false
		}
		f.Description = c.To
	case MemberExt:
		if !sameText(extValue(f.Ext, c.Key), c.From) {
			return f, false
		}
		ext := make(map[string]string, len(f.Ext)+1)
		for k, v := range f.Ext {
			ext[k] = v
		}
		if c.To != nil {
			ext[c.Key] = *c.To
		} else {
			delete(ext, c.Key)
		}
		if len(ext) == 0 {
			ext = nil
		}
		f.Ext = ext
	default:
		return f, false
	}
	return f, true
}

func indexOf(s Schema, name string) int {
	for i, f := range s {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// PatchSchema replays a delta over old: removals, retypes, amendments, additions,
// then the recorded order. Each step checks the schema it meets agrees with what
// the delta says it was, so PatchSchema(old, DiffSchema(old, target)) is target.
func PatchSchema(old Schema, d SchemaDelta) (Schema, *SchemaError) {
	seen := map[string]bool{}
	for _, f := range old {
		if seen[f.Name] {
			return nil, &SchemaError{Kind: SchemaDuplicateColumn, Column: f.Name}
		}
		seen[f.Name] = true
	}
	s := append(Schema{}, old...)
	for _, r := range d.Removed {
		i := indexOf(s, r.Name)
		if i < 0 {
			return nil, &SchemaError{Kind: SchemaAbsentColumn, Column: r.Name}
		}
		if s[i].Type != r.Type {
			return nil, &SchemaError{Kind: SchemaTypeDisagrees, Column: r.Name}
		}
		s = append(s[:i:i], s[i+1:]...)
	}
	for _, r := range d.Retyped {
		i := indexOf(s, r.Name)
		if i < 0 {
			return nil, &SchemaError{Kind: SchemaAbsentColumn, Column: r.Name}
		}
		if s[i].Type != r.From {
			return nil, &SchemaError{Kind: SchemaTypeDisagrees, Column: r.Name}
		}
		s[i].Type = r.To
	}
	for _, a := range d.Amended {
		i := indexOf(s, a.Name)
		if i < 0 {
			return nil, &SchemaError{Kind: SchemaAbsentColumn, Column: a.Name}
		}
		f, ok := applyChange(a.Change, s[i])
		if !ok {
			return nil, &SchemaError{Kind: SchemaMetadataDisagrees, Column: a.Name}
		}
		s[i] = f
	}
	for _, a := range d.Added {
		if indexOf(s, a.Name) >= 0 {
			return nil, &SchemaError{Kind: SchemaAlreadyPresent, Column: a.Name}
		}
		s = append(s, a)
	}
	if len(d.Order) == 0 {
		return s, nil
	}
	names := make([]string, len(s))
	for i, f := range s {
		names[i] = f.Name
	}
	key := func(xs []string) []string {
		out := append([]string{}, xs...)
		canonical.SortKeys(out)
		return out
	}
	if len(d.Order) != len(names) || !sameStrings(key(d.Order), key(names)) {
		return nil, &SchemaError{Kind: SchemaOrderMismatch}
	}
	out := make(Schema, len(d.Order))
	for i, n := range d.Order {
		out[i] = s[indexOf(s, n)]
	}
	return out, nil
}

// ColumnTypeWidens reports whether a retype from one column type to another is
// a safe widening: int into float or decimal; a timestamp unit into an equal or
// finer one; otherwise only a type into itself.
func ColumnTypeWidens(from, to string) bool {
	if from == TypeInt && (to == TypeFloat || to == TypeDecimal) {
		return true
	}
	a, aok := TimestampUnitDigits[from]
	b, bok := TimestampUnitDigits[to]
	if aok && bok {
		return a <= b
	}
	return from == to
}

func unitText(u *Unit) string {
	if u == nil {
		return "none"
	}
	return u.String()
}

// ClassifySchema is the compatibility verdict a delta carries for a consumer
// depending on the named columns: `breaking` where a depended-on column was
// removed, retyped other than by a safe widening, or had a stated unit replaced
// or withdrawn; else `unknown` where a depended-on column's extension member
// moved (no host interprets one); else `compatible`. A unit stated for the first
// time, and a moved label or description, are compatible.
func ClassifySchema(dependsOn []string, d SchemaDelta) SchemaCompat {
	deps := set(dependsOn...)
	var breaking, unknown []string
	for _, f := range d.Removed {
		if deps[f.Name] {
			breaking = append(breaking, "depended-on column removed: "+f.Name+" ("+f.Type+")")
		}
	}
	for _, r := range d.Retyped {
		if deps[r.Name] && !ColumnTypeWidens(r.From, r.To) {
			breaking = append(breaking, "depended-on column "+r.Name+" retyped "+r.From+" → "+r.To+" (not a safe widening)")
		}
	}
	for _, a := range d.Amended {
		if a.Change.Member == MemberUnit && a.Change.FromUnit != nil && deps[a.Name] {
			breaking = append(breaking, "depended-on column "+a.Name+" unit changed "+a.Change.FromUnit.String()+
				" → "+unitText(a.Change.ToUnit)+" (its values no longer mean what was read)")
		}
	}
	for _, a := range d.Amended {
		if a.Change.Member == MemberExt && deps[a.Name] {
			unknown = append(unknown, "depended-on column "+a.Name+" extension member "+a.Change.Key+
				" changed (not interpreted by the kit)")
		}
	}
	switch {
	case len(breaking) > 0:
		return SchemaCompat{Verdict: CompatBreaking, Reasons: breaking}
	case len(unknown) > 0:
		return SchemaCompat{Verdict: CompatUnknown, Reasons: unknown}
	}
	return SchemaCompat{Verdict: CompatCompatible}
}

// metadataPreimage is a field's metadata as one canonical field: the unit's
// canonical text prefixed `u`, the label prefixed `l`, the description prefixed
// `d` (each the empty field where unstated), then each extension key and value
// in ordinal key order.
func metadataPreimage(f SchemaEntry) string {
	stated := func(tag string, v *string) string {
		if v == nil {
			return ""
		}
		return tag + *v
	}
	var unit *string
	if f.Unit != nil {
		s := f.Unit.String()
		unit = &s
	}
	fields := []string{stated("u", unit), stated("l", f.Label), stated("d", f.Description)}
	keys := make([]string, 0, len(f.Ext))
	for k := range f.Ext {
		keys = append(keys, k)
	}
	canonical.SortKeys(keys)
	for _, k := range keys {
		fields = append(fields, k, f.Ext[k])
	}
	return function.CanonicalFields(fields)
}

// SchemaFingerprint is the schema's FNV-1a fingerprint over the UTF-16 code
// units of its canonical field pre-image: each column's `name:type`, followed,
// where the column states metadata, by its metadata pre-image. A schema without
// metadata keeps the fingerprint it had before field metadata existed.
func SchemaFingerprint(s Schema) string {
	var fields []string
	for _, f := range s {
		fields = append(fields, f.Name+":"+f.Type)
		if f.HasMetadata() {
			fields = append(fields, metadataPreimage(f))
		}
	}
	return function.FNV1a(function.CanonicalFields(fields))
}
