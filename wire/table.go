package wire

import (
	"strconv"
	"strings"

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

func caseRows(u *idl.Union, tag string) []idl.Field {
	if c, ok := u.Case(tag); ok {
		return c.Fields
	}
	panic("wire: idl.json's " + u.Name + " declares no case " + tag)
}

// requiredRowsPresent reports whether obj carries every row the table declares
// required — for a hand decoder whose shape test (an object form beside a
// lenient array form) refuses an incomplete object as a whole rather than at
// the missing member.
func requiredRowsPresent(rows []idl.Field, obj map[string]any) bool {
	for _, f := range rows {
		if _, ok := obj[f.Name]; !ok && f.Optionality == idl.Required {
			return false
		}
	}
	return true
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
// table declares for it: a required row refuses its absence (unless its
// declared leniency admits it), an omit-at-default row drops a value equal to
// its declared default (on the decode boundary, so an explicit default
// normalises to the omitted canonical form), and an optional row carries
// whatever is present. A key the table does not declare is read as optional —
// TestHandArmsReadOnlyDeclaredRows and TestHandDecodersReadOnlyDeclaredRows keep
// that path empty.
func (s *spec) field(key string, dec fieldDecoder, aliases ...string) {
	raw, ok := s.take(key, aliases...)
	if !ok {
		s.absent(key)
		return
	}
	v := dec(s.w, raw, s.path+"."+key)
	if f, declared := s.row(key); declared && f.Optionality == idl.OmitDefault && matchesDefault(v, f.Default) {
		return
	}
	s.fields[key] = v
}

// has reads a row's raw value for a hand reading that carries behaviour over it
// (a relational check, a shape coercion, a bound): present or not, with the
// table's answer to absence — a required row refuses it here, exactly as field
// would, so the hand code after it never states required-ness itself.
func (s *spec) has(key string, aliases ...string) (any, bool) {
	raw, ok := s.take(key, aliases...)
	if !ok {
		s.absent(key)
	}
	return raw, ok
}

// absent answers a key missing from the object. Only a required row refuses
// it; a row whose absence the decode policy admits (absentLeniency) takes its
// declared value instead (a fresh one per decode: decoded values are not
// shared).
func (s *spec) absent(key string) {
	f, ok := s.row(key)
	if !ok || f.Optionality != idl.Required {
		return
	}
	if v, lenient := absentLeniency[s.owner+"."+key]; lenient {
		s.fields[key] = v()
		return
	}
	fail(CodeMissingField, s.path+"."+key, "missing required field '"+key+"'")
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

// readRowsOrdinal decodes every row not yet read, by its declared type, in
// Ordinal key order — the order the first of several defects is reported at
// (WIRE_FORMAT.md section 29.3). For a hand decoder with no behaviour of its
// own on those fields.
func (s *spec) readRowsOrdinal() {
	names := make([]string, 0, len(s.rows))
	for _, f := range s.rows {
		if f.Optionality != idl.HostOnly && !s.consumed[f.Name] {
			if _, decoded := s.fields[f.Name]; !decoded {
				names = append(names, f.Name)
			}
		}
	}
	canonical.SortKeys(names)
	for _, name := range names {
		f, _ := s.row(name)
		s.field(name, typeDecoder(f.Type))
	}
}

// decodeKindByTable is the table arm: the kind decoded entirely from its row.
func decodeKindByTable(w *walkState, obj map[string]any, path string, k *idl.Kind) Obj {
	return newKindSpec(w, obj, path, k).build(k.Tag)
}

// newCaseSpec starts one union case's decode against the case's row set.
func newCaseSpec(w *walkState, obj map[string]any, path string, u *idl.Union, tag string) *spec {
	s := newSpec(w, obj, path)
	s.rows = caseRows(u, tag)
	s.owner = u.Name + "." + tag
	return s
}

// newRecordSpec starts one record object's decode against the record's rows.
func newRecordSpec(w *walkState, obj map[string]any, path string, r *idl.Record) *spec {
	s := newSpec(w, obj, path)
	s.rows = r.Fields
	s.owner = r.Name
	return s
}

// tableCase decodes a union case a hand decoder has no arm for — one idl.json
// added after the hand decoder was written — from its table row. Without it a
// generated case set would admit the new discriminator into a switch whose last
// arm reads some OTHER case's fields, or whose strict build drops them all.
func tableCase(w *walkState, obj map[string]any, path string, u *idl.Union, tag string) Value {
	return newCaseSpec(w, obj, path, u, tag).build(tag)
}

// absentLeniency is decode policy, not vocabulary (Phase 2330): the required
// rows whose ABSENCE this host admits on decode, each with the value absence
// decodes to. The table says the canonical encoder always writes these fields;
// the reference decoder reads a missing one as the value below, so this host
// does too, and every other required row refuses its absence. Keyed
// "<Union>.<Case>.<field>" or "<Record>.<field>". Each entry is checked against
// the table at start-up: one naming a row the table does not declare REQUIRED
// panics, so a leniency cannot outlive the disagreement it records.
var absentLeniency = map[string]func() Value{
	// The closure slots: the wire cannot carry a closure, so the reference
	// decoder never reads these keys and a missing one is the sentinel.
	"Binding.Computed.fn":                absentClosure,
	"Binding.Local.format":               absentClosure,
	"Binding.Local.parse":                absentClosure,
	"CellFormat.Custom.fn":               absentClosure,
	"CellKindErased.Checkbox.get":        absentClosure,
	"CellKindErased.Custom.fn":           absentClosure,
	"CellKindErased.Link.hrefFn":         absentClosure,
	"CellKindErased.Link.labelFn":        absentClosure,
	"CellKindErased.Pill.labelFn":        absentClosure,
	"CellKindErased.Pill.toneFn":         absentClosure,
	"CellKindErased.Progress.fractionFn": absentClosure,
	// (`FormFieldKind.SegmentedChoice.orientation` left this table with Phase 2300:
	// the IDL declares its default, so the generated row restores `Horizontal`.)
	// The buffer's flush trigger defaults to on-blur.
	"Binding.Local.flushOn": func() Value { return Obj{Tag: "OnBlur", Fields: map[string]Value{}} },
	// A message with no arguments.
	"TextSource.I18n.args": absentEmpty,
	// An unstyled shape: the empty DrawStyle.
	"Shape.Group.style":     absentEmpty,
	"Shape.Rectangle.style": absentEmpty,
	"Shape.Line.style":      absentEmpty,
	"Shape.Polyline.style":  absentEmpty,
	"Shape.Polygon.style":   absentEmpty,
	"Shape.Curve.style":     absentEmpty,
	"Shape.Circle.style":    absentEmpty,
	"Shape.Ellipse.style":   absentEmpty,
	"Shape.Label.style":     absentEmpty,
}

func absentClosure() Value { return Str(closureSentinel) }

func absentEmpty() Value { return Obj{Fields: map[string]Value{}} }

func init() {
	for key := range absentLeniency {
		if f, ok := leniencyRow(key); !ok || f.Optionality != idl.Required {
			panic("wire: absentLeniency names " + key + ", which idl.json does not declare as a required row")
		}
	}
}

// leniencyRow resolves an absentLeniency key to its table row.
func leniencyRow(key string) (*idl.Field, bool) {
	parts := strings.Split(key, ".")
	switch len(parts) {
	case 2:
		if r, ok := idl.RecordByName(parts[0]); ok {
			return r.Field(parts[1])
		}
	case 3:
		if u, ok := idl.UnionByName(parts[0]); ok {
			if c, ok := u.Case(parts[1]); ok {
				return c.Field(parts[2])
			}
		}
	}
	return nil, false
}

// decodeClosure reads a closure slot. It carries nothing but its presence: the
// canonical form is the sentinel, whatever was written.
func decodeClosure(*walkState, any, string) Value { return Str(closureSentinel) }

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
		return expectNumber
	case idl.TNode:
		return decodeSingleNode
	case idl.TKind:
		return decodeKindField
	case idl.TOp:
		return func(w *walkState, raw any, path string) Value { return decodeOpValue(w, raw, path) }
	case idl.TFn:
		return decodeClosure
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
		return decodeBoxLayout
	}
	u, ok := idl.UnionByName(t.Name)
	if !ok {
		return decodeJSONValue
	}
	cases := unionCases(u)
	return func(w *walkState, raw any, path string) Value {
		obj := expectObject(raw, path)
		tag := dispatch(obj, path, cases, CodeUnknownDUCase)
		return tableCase(w, obj, path, u, tag)
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
	case "ButtonGroupItem":
		return decodeButtonGroupItem
	case "DrawPoint":
		return decodeDrawPoint
	case "DrawStyle":
		return decodeDrawStyle
	case "ViewBox":
		return decodeViewBox
	}
	r, ok := idl.RecordByName(t.Name)
	if !ok {
		return decodeJSONValue
	}
	return func(w *walkState, raw any, path string) Value {
		return newRecordSpec(w, expectObject(raw, path), path, r).build("")
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
