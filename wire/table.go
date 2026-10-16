package wire

import (
	"strconv"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// The table-driven half of the kind decoders (Phase 2297).
//
// Every kind decodes through a spec bound to its generated field table
// (wire/idl). A hand arm decodes the fields whose reading carries behaviour
// this host owns — an alias, a shape coercion, a relational check, a bound —
// and consults the table for each field's optionality and omit-at-default
// value through spec.field. Whatever the hand arm did not read, the table
// reads: build decodes every remaining row by its declared type, so a kind
// with no hand arm (or a field a hand arm predates) is still decoded field by
// field, required rows still refuse their absence, and every node beneath it
// is a wire.Node the walk counters, the apply engine and the diff all see.

// enumAliases are the lenient-ingest spellings of a bare enum's cases, keyed
// by enum name (decode-only; never encoded). idl.json declares no alias rows,
// so this is hand data — faithful same-concept mappings only.
var enumAliases = map[string]map[string]string{
	"ToneVariant":    toneAliases,
	"Emphasis":       emphasisAliases,
	"Orientation":    orientationAliases,
	"BadgeVariant":   badgeVariantAliases,
	"ButtonVariant":  buttonVariantAliases,
	"HeadingVariant": headingVariantAliases,
}

var (
	enumSets  = make(map[string]caseSet, len(idl.Enums))
	unionSets = make(map[string]caseSet, len(idl.Unions))
)

func init() {
	for _, e := range idl.Enums {
		enumSets[e.Name] = newCaseSet(e.Cases...)
	}
	for _, u := range idl.Unions {
		unionSets[u.Name] = newCaseSet(u.CaseTags()...)
	}
}

// enumCases is the generated case set of a bare enum.
func enumCases(e *idl.Enum) caseSet { return enumSets[e.Name] }

// unionCases is the generated discriminator set of a union.
func unionCases(u *idl.Union) caseSet { return unionSets[u.Name] }

// The omit-at-default tests of the record and union-case rows that a hand
// decoder reads field by field. Each is resolved from its table row once, at
// initialisation, so a row the table stops declaring omit-at-default fails
// every run at start-up instead of one decode at a time.
var (
	dropComboboxFreeText = declaredDefault(caseRows(&idl.UnionFormFieldKind, "Combobox"), "allowFreeText")
	dropRatingHalf       = declaredDefault(caseRows(&idl.UnionFormFieldKind, "Rating"), "allowHalf")
	dropTonedPillDefault = declaredDefault(caseRows(&idl.UnionCellKindErased, "TonedPill"), "default")
	dropVideoAutoplay    = declaredDefault(caseRows(&idl.UnionMediaKind, "Video"), "autoplay")
	dropColumnFormat     = declaredDefault(idl.RecordColumnErased.Fields, "format")
	dropColumnWidth      = declaredDefault(idl.RecordColumnErased.Fields, "width")
	dropTrackDefault     = declaredDefault(idl.RecordTrackEntry.Fields, "default")
	dropTreeItemChildren = declaredDefault(idl.RecordTreeItem.Fields, "children")
)

func caseRows(u *idl.Union, tag string) []idl.Field {
	if c, ok := u.Case(tag); ok {
		return c.Fields
	}
	panic("wire: idl.json's " + u.Name + " declares no case " + tag)
}

// declaredDefault is the omit-at-default test of one table row.
func declaredDefault(rows []idl.Field, key string) func(Value) bool {
	for _, f := range rows {
		if f.Name == key && f.Optionality == idl.OmitDefault {
			def := f.Default
			return func(v Value) bool { return matchesDefault(v, def) }
		}
	}
	panic("wire: idl.json declares no omit-at-default row " + key)
}

// newKindSpec starts a kind object's decode against its table.
func newKindSpec(w *walkState, obj map[string]any, path string, k *idl.Kind) *spec {
	s := newSpec(w, obj, path)
	s.rows = k.Fields
	return s
}

func (s *spec) row(key string) (*idl.Field, bool) {
	for i := range s.rows {
		if s.rows[i].Name == key {
			return &s.rows[i], true
		}
	}
	return nil, false
}

// field reads one row with the optionality and omit-at-default value the
// table declares for it: a required row refuses its absence, an omit-at-default
// row drops a value equal to its declared default (on the decode boundary, so
// an explicit default normalises to the omitted canonical form), and an
// optional row carries whatever is present. A key the table does not declare
// is read as optional — TestHandArmsReadOnlyDeclaredRows keeps that path empty.
func (s *spec) field(key string, dec fieldDecoder, aliases ...string) {
	f, ok := s.row(key)
	if !ok {
		s.opt(key, dec, aliases...)
		return
	}
	switch f.Optionality {
	case idl.Required:
		s.req(key, dec, aliases...)
	case idl.OmitDefault:
		def := f.Default
		s.optDrop(key, dec, func(v Value) bool { return matchesDefault(v, def) }, aliases...)
	default:
		s.opt(key, dec, aliases...)
	}
}

// matchesDefault reports whether v is the declared omit-at-default value,
// comparing canonical encodings — the generated default IS the canonical wire
// form, so one comparison serves every row whatever its type.
func matchesDefault(v Value, def string) bool {
	enc, err := encodeValue(v)
	return err == nil && enc == def
}

// decodeRemainingRows decodes every table row the hand arm did not read.
func (s *spec) decodeRemainingRows() {
	for i := range s.rows {
		f := &s.rows[i]
		if f.Optionality == idl.HostOnly || s.consumed[f.Name] {
			continue
		}
		if _, decoded := s.fields[f.Name]; decoded {
			continue // read through an alias
		}
		s.field(f.Name, typeDecoder(f.Type))
	}
}

// decodeKindByTable is the table arm: the kind decoded entirely from its row.
func decodeKindByTable(w *walkState, obj map[string]any, path string, k *idl.Kind) Obj {
	return newKindSpec(w, obj, path, k).build(k.Tag)
}

// decodeRowsByTable decodes a union case's or a record's object from its rows;
// tag is the case discriminator, or "" for a record.
func decodeRowsByTable(w *walkState, obj map[string]any, path string, rows []idl.Field, tag string) Obj {
	s := newSpec(w, obj, path)
	s.rows = rows
	return s.build(tag)
}

// tableCase decodes a union case a hand decoder has no arm for — one idl.json
// added after the hand decoder was written — from its table row. Without it a
// generated case set would admit the new discriminator into a switch whose last
// arm reads some OTHER case's fields, or whose strict build drops them all.
func tableCase(w *walkState, obj map[string]any, path string, u *idl.Union, tag string) Value {
	c, _ := u.Case(tag)
	return decodeRowsByTable(w, obj, path, c.Fields, tag)
}

// typeDecoder is the decoder a row's declared type implies. Where this host
// already reads a type through a hand decoder (TextSource's bare-string
// canonical form, a Binding's lenient shapes, the Action vocabulary) the table
// uses it, so a table-read field is read exactly as a hand-read one would be.
func typeDecoder(t idl.Type) fieldDecoder {
	switch t.Kind {
	case idl.TStr:
		return decodeString
	case idl.TBool:
		return decodeBool
	case idl.TInt:
		return decodeInt
	case idl.TFloat:
		return expectNumberField
	case idl.TNode:
		return decodeSingleNode
	case idl.TKind:
		return decodeKindField
	case idl.TOp:
		return func(w *walkState, raw any, path string) Value { return decodeOpValue(w, raw, path) }
	case idl.TFn:
		// A closure slot carries nothing but its presence: the canonical form is
		// the sentinel, whatever was written.
		return func(*walkState, any, string) Value { return Str(closureSentinel) }
	case idl.THosted:
		if t.Elem != nil {
			return typeDecoder(*t.Elem)
		}
		return decodeJSONValue
	case idl.TList:
		return listDecoder(t)
	case idl.TMap:
		return mapDecoder(t)
	case idl.TEnum:
		if e, ok := idl.EnumByName(t.Name); ok {
			return enumDecoder(enumCases(e), t.Name, enumAliases[t.Name])
		}
	case idl.TUnion:
		return unionDecoder(t)
	case idl.TRecord:
		return recordDecoder(t)
	}
	return decodeJSONValue
}

func listDecoder(t idl.Type) fieldDecoder {
	if t.Elem == nil {
		return decodeJSONValue
	}
	switch t.Elem.Kind {
	case idl.TNode:
		return decodeChildren
	case idl.TStr:
		return decodeStringArrayField
	}
	elem := typeDecoder(*t.Elem)
	return func(w *walkState, raw any, path string) Value {
		arr := expectArray(raw, path)
		out := make(Arr, len(arr))
		for i, item := range arr {
			out[i] = elem(w, item, path+"["+strconv.Itoa(i)+"]")
		}
		return out
	}
}

func mapDecoder(t idl.Type) fieldDecoder {
	if t.Elem == nil {
		return decodeJSONValue
	}
	elem := typeDecoder(*t.Elem)
	return func(w *walkState, raw any, path string) Value {
		obj := expectObject(raw, path)
		fields := make(map[string]Value, len(obj))
		for _, k := range ordinalKeys(obj) {
			fields[k] = elem(w, obj[k], path+"."+k)
		}
		return Obj{Fields: fields}
	}
}

func unionDecoder(t idl.Type) fieldDecoder {
	switch t.Name {
	case "TextSource":
		return decodeTextSource
	case "Binding":
		return bindingDecoder(t)
	case "Action":
		return decodeAction
	case "CellFormat":
		return decodeCellFormat
	case "ColumnWidth":
		return decodeColumnWidth
	case "ChartAnnotation":
		return decodeChartAnnotation
	case "Shape":
		return decodeShape
	case "CurveCommand":
		return decodeCurveCommand
	case "BoxLayout":
		return func(_ *walkState, raw any, path string) Value { return decodeBoxLayout(raw, path) }
	}
	u, ok := idl.UnionByName(t.Name)
	if !ok {
		return decodeJSONValue
	}
	cases := unionCases(u)
	return func(w *walkState, raw any, path string) Value {
		obj := expectObject(raw, path)
		tag := dispatch(obj, path, cases, CodeUnknownDUCase)
		c, _ := u.Case(tag)
		return decodeRowsByTable(w, obj, path, c.Fields, tag)
	}
}

// bindingDecoder picks the typed Binding decoder for the row's type argument.
func bindingDecoder(t idl.Type) fieldDecoder {
	if len(t.Args) != 1 {
		return decodeBinding
	}
	arg := t.Args[0]
	switch arg.Kind {
	case idl.TBool:
		return decodeBindingBool
	case idl.TInt:
		return decodeBindingInt
	case idl.TFloat:
		return decodeBindingFloat
	case idl.TStr:
		return decodeBindingString
	case idl.TList:
		if arg.Elem != nil && arg.Elem.Kind == idl.TStr {
			return decodeBindingStringList
		}
	}
	return decodeBinding
}

func recordDecoder(t idl.Type) fieldDecoder {
	switch t.Name {
	case "SemanticStyle":
		return decodeStyleField
	case "StyleDelta":
		return decodeStyleDeltaField
	case "StateBehaviour":
		return decodeStateField
	case "Accessibility":
		return func(w *walkState, raw any, path string) Value { return decodeAccessibility(w, raw, path) }
	}
	r, ok := idl.RecordByName(t.Name)
	if !ok {
		return decodeJSONValue
	}
	return func(w *walkState, raw any, path string) Value {
		return decodeRowsByTable(w, expectObject(raw, path), path, r.Fields, "")
	}
}

// ordinalKeys returns an open map's keys in Ordinal order — the order a decoder
// walks them in, so the first defect reported among several is fixed
// (WIRE_FORMAT.md section 29.3) rather than whatever Go's map iteration chose.
func ordinalKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	canonical.SortKeys(keys)
	return keys
}
