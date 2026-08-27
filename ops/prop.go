package ops

import (
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The UpdateProp surface: the per-kind field-coercion tables, the
// WIRE_FORMAT.md §3.4 path grammar, and the nested typed-traversal legs —
// built to match the sibling engines' semantics exactly.

func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

var (
	textSourceSet = nameSet(wire.TextSourceCases())
	bindingSet    = nameSet(wire.BindingCases())
	cellFormatSet = nameSet(wire.CellFormatCases())

	toneSet           = nameSet(wire.ToneVariants())
	iconSizeSet       = nameSet(wire.IconSizes())
	weightSet         = nameSet(wire.StyleWeights())
	emphasisSet       = nameSet(wire.EmphasisLevels())
	orientationSet    = nameSet(wire.Orientations())
	badgeVariantSet   = nameSet(wire.BadgeVariants())
	headingVariantSet = nameSet(wire.HeadingVariants())
	trendPolaritySet  = nameSet(wire.TrendPolarities())

	columnWidthSet = map[string]bool{"Auto": true, "Fixed": true, "Flex": true}
)

// ── Value coercion ──────────────────────────────────────────────────────────

type coerced struct {
	ok     bool
	value  wire.Value
	detail string
}

type coercer func(wire.Value) coerced

func coerceTextSource(v wire.Value) coerced {
	// 0.2.0 — the bare string IS the canonical Literal form. Both op-value
	// spellings decode to the same typed value at apply time (hosts MUST
	// accept both); the verbose Literal envelope collapses so the applied
	// tree re-encodes canonical.
	if s, ok := v.(wire.Str); ok {
		return coerced{ok: true, value: s}
	}
	if o, ok := v.(wire.Obj); ok && textSourceSet[o.Tag] {
		if o.Tag == "Literal" {
			if t, ok := o.Fields["text"].(wire.Str); ok {
				return coerced{ok: true, value: t}
			}
		}
		return coerced{ok: true, value: o}
	}
	return coerced{detail: "expected a string or TextSource"}
}

// coerceBindingOf accepts a scalar of the accepted shapes (wrapped as a Static
// binding) or any recognised Binding object.
func coerceBindingOf(expected string, acceptsScalar func(wire.Value) bool) coercer {
	return func(v wire.Value) coerced {
		if acceptsScalar(v) {
			return coerced{ok: true, value: wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": v}}}
		}
		if o, ok := v.(wire.Obj); ok && bindingSet[o.Tag] {
			return coerced{ok: true, value: o}
		}
		return coerced{detail: "expected a " + expected + " or Binding"}
	}
}

func isNumber(v wire.Value) bool {
	switch v.(type) {
	case wire.Int, wire.Float:
		return true
	}
	return false
}

var (
	coerceBindingNumber = coerceBindingOf("number", isNumber)
	coerceBindingString = coerceBindingOf("string", func(v wire.Value) bool { _, ok := v.(wire.Str); return ok })
	coerceBindingInt    = coerceBindingOf("integer", func(v wire.Value) bool { _, ok := v.(wire.Int); return ok })
	coerceBindingBool   = coerceBindingOf("boolean", func(v wire.Value) bool { _, ok := v.(wire.Bool); return ok })
)

func coerceCellFormat(v wire.Value) coerced {
	if o, ok := v.(wire.Obj); ok && cellFormatSet[o.Tag] {
		return coerced{ok: true, value: o}
	}
	return coerced{detail: "expected a CellFormat object"}
}

func coerceColumnWidth(v wire.Value) coerced {
	if o, ok := v.(wire.Obj); ok && columnWidthSet[o.Tag] {
		return coerced{ok: true, value: o}
	}
	return coerced{detail: "expected a ColumnWidth object (Auto | Fixed | Flex)"}
}

func coerceEnum(allowed map[string]bool, sorted []string) coercer {
	hint := "one of: " + strings.Join(sorted, ", ")
	return func(v wire.Value) coerced {
		if s, ok := v.(wire.Str); ok && allowed[string(s)] {
			return coerced{ok: true, value: s}
		}
		return coerced{detail: hint}
	}
}

func coerceInt(v wire.Value) coerced {
	if i, ok := v.(wire.Int); ok {
		return coerced{ok: true, value: i}
	}
	return coerced{detail: "expected an integer"}
}

func coerceFloat(v wire.Value) coerced {
	switch t := v.(type) {
	case wire.Int:
		return coerced{ok: true, value: wire.Float(t)}
	case wire.Float:
		return coerced{ok: true, value: t}
	}
	return coerced{detail: "expected a number"}
}

func coerceBool(v wire.Value) coerced {
	if b, ok := v.(wire.Bool); ok {
		return coerced{ok: true, value: b}
	}
	return coerced{detail: "expected a boolean"}
}

func coerceString(v wire.Value) coerced {
	if s, ok := v.(wire.Str); ok {
		return coerced{ok: true, value: s}
	}
	return coerced{detail: "expected a string"}
}

// ── Per-kind flat field tables ──────────────────────────────────────────────

type fieldEntry struct {
	wireKey      string
	coerce       coercer // nil == the field exists but is not prop-addressable
	notSupported bool
}

var (
	coerceTone           = coerceEnum(toneSet, wire.ToneVariants())
	coerceIconSize       = coerceEnum(iconSizeSet, wire.IconSizes())
	coerceWeight         = coerceEnum(weightSet, wire.StyleWeights())
	coerceEmphasisEnum   = coerceEnum(emphasisSet, wire.EmphasisLevels())
	coerceOrientation    = coerceEnum(orientationSet, wire.Orientations())
	coerceBadgeVariant   = coerceEnum(badgeVariantSet, wire.BadgeVariants())
	coerceHeadingVariant = coerceEnum(headingVariantSet, wire.HeadingVariants())
	coerceTrendPolarity  = coerceEnum(trendPolaritySet, wire.TrendPolarities())
)

// propFields maps (PascalCase op path) -> (camelCase wire key, coercer) per
// kind. Children entries are notSupported (use the structural child ops); a
// kind absent from this table has no field-level surface
// (PathNotSupportedYet). Box is NOT here — its legacy-compat surface mutates
// the nested layout object and is layout-mode-sensitive (updateBox).
var propFields = map[string]map[string]fieldEntry{
	"Metric": {
		"Label": {wireKey: "label", coerce: coerceTextSource},
		// 0.2.0 rename law — the scalar displayed value is `value`; the
		// pre-rename "Source" path stays accepted (read-compat: persisted op
		// logs replay), targeting the same slot.
		"Value":       {wireKey: "value", coerce: coerceBindingNumber},
		"Source":      {wireKey: "value", coerce: coerceBindingNumber},
		"Format":      {wireKey: "format", coerce: coerceCellFormat},
		"Tone":        {wireKey: "tone", coerce: coerceTone},
		"Weight":      {wireKey: "weight", coerce: coerceWeight},
		"Emphasis":    {wireKey: "emphasis", coerce: coerceEmphasisEnum},
		"Trend":       {wireKey: "trend", coerce: coerceBindingNumber},
		"TrendFormat": {wireKey: "trendFormat", coerce: coerceCellFormat},
		// Phase 867 — the direction-of-good declaration, settable through the
		// same UpdateProp surface the reference engine exposes it on. Closed
		// vocabulary: an unrecognised polarity is refused here rather than
		// written into the tree, which is what keeps rejection parity with the
		// reference's `tryTrendPolarity` coercion.
		"TrendPolarity": {wireKey: "trendPolarity", coerce: coerceTrendPolarity},
		"Icon":          {wireKey: "icon", coerce: coerceString},
		"Subtext":       {wireKey: "subtext", coerce: coerceTextSource},
	},
	"Heading": {
		"Level":   {wireKey: "level", coerce: coerceInt},
		"Text":    {wireKey: "text", coerce: coerceTextSource},
		"Variant": {wireKey: "variant", coerce: coerceHeadingVariant},
	},
	"Markdown": {
		"Text": {wireKey: "text", coerce: coerceTextSource},
	},
	"Badge": {
		"Label":   {wireKey: "label", coerce: coerceTextSource},
		"Variant": {wireKey: "variant", coerce: coerceBadgeVariant},
	},
	"Skeleton": {
		"Rows": {wireKey: "rows", coerce: coerceInt},
	},
	// Phase 821 — the standalone Icon display kind's field surface.
	"Icon": {
		"Icon":  {wireKey: "icon", coerce: coerceString},
		"Size":  {wireKey: "size", coerce: coerceIconSize},
		"Tone":  {wireKey: "tone", coerce: coerceTone},
		"Label": {wireKey: "label", coerce: coerceString},
	},
	"Callout": {
		"Tone":        {wireKey: "tone", coerce: coerceTone},
		"Body":        {wireKey: "body", coerce: coerceTextSource},
		"Dismissable": {wireKey: "dismissable", coerce: coerceBool},
		"Heading":     {wireKey: "heading", coerce: coerceTextSource},
		"Icon":        {wireKey: "icon", coerce: coerceString},
	},
	"Progress": {
		"Fraction":      {wireKey: "fraction", coerce: coerceBindingNumber},
		"Indeterminate": {wireKey: "indeterminate", coerce: coerceBool},
		"Tone":          {wireKey: "tone", coerce: coerceTone},
		"Label":         {wireKey: "label", coerce: coerceTextSource},
		"Caveat":        {wireKey: "caveat", coerce: coerceTextSource},
	},
	"LabelValueRow": {
		"Label": {wireKey: "label", coerce: coerceTextSource},
		// 0.2.0 rename law — see Metric above.
		"Value":    {wireKey: "value", coerce: coerceBindingNumber},
		"Source":   {wireKey: "value", coerce: coerceBindingNumber},
		"Format":   {wireKey: "format", coerce: coerceCellFormat},
		"Emphasis": {wireKey: "emphasis", coerce: coerceBool},
		"Help":     {wireKey: "help", coerce: coerceTextSource},
	},
	"Link": {
		"Href":     {wireKey: "href", coerce: coerceBindingString},
		"Label":    {wireKey: "label", coerce: coerceTextSource},
		"Rel":      {wireKey: "rel", coerce: coerceString},
		"Target":   {wireKey: "target", coerce: coerceString},
		"Download": {wireKey: "download", coerce: coerceBool},
	},
	"SplitPanel": {
		"Weight":   {wireKey: "weight", coerce: coerceFloat},
		"Children": {wireKey: "children", notSupported: true},
	},
	"Tabs": {
		"Orientation": {wireKey: "orientation", coerce: coerceOrientation},
		"Children":    {wireKey: "children", notSupported: true},
	},
	"Stepper": {
		"ActiveStep": {wireKey: "activeStep", coerce: coerceBindingInt},
		"Children":   {wireKey: "children", notSupported: true},
	},
	"SummaryList": {
		"Heading":  {wireKey: "heading", coerce: coerceTextSource},
		"Children": {wireKey: "children", notSupported: true},
	},
	"Disclosure": {
		"Heading":     {wireKey: "heading", coerce: coerceTextSource},
		"Open":        {wireKey: "open", coerce: coerceBindingBool},
		"DefaultOpen": {wireKey: "defaultOpen", coerce: coerceBool},
		"Children":    {wireKey: "children", notSupported: true},
	},
	"FragmentDecl": {
		"Name": {wireKey: "name", coerce: coerceString},
	},
	"FragmentRef": {
		"Name": {wireKey: "name", coerce: coerceString},
	},
}

type outcomeTag int

const (
	outcomeUpdated outcomeTag = iota
	outcomeUnknownField
	outcomeNotSupported
	outcomeTypeMismatch
)

type fieldOutcome struct {
	tag    outcomeTag
	kind   wire.Obj
	detail string
}

func withKindField(kind wire.Obj, key string, value wire.Value) wire.Obj {
	fields := make(map[string]wire.Value, len(kind.Fields)+1)
	for k, v := range kind.Fields {
		fields[k] = v
	}
	fields[key] = value
	return wire.Obj{Tag: kind.Tag, Fields: fields}
}

// updateBox is the Box UpdateProp surface: the retired container kinds' field
// names stay addressable so pre-merge op-streams replaying against an upgraded
// Box keep working — Orientation / Wrap mutate a Flex box's nested layout,
// Cols / TemplateColumns a Grid box's, Heading the top-level heading. A field
// that does not match the box's current layout mode is UnknownField.
func updateBox(field string, value wire.Value, kind wire.Obj) fieldOutcome {
	layout, _ := kind.Fields["layout"].(wire.Obj)
	mode := layout.Tag

	withLayoutField := func(camel string, v wire.Value) fieldOutcome {
		newLayout := make(map[string]wire.Value, len(layout.Fields)+1)
		for k, lv := range layout.Fields {
			newLayout[k] = lv
		}
		newLayout[camel] = v
		return fieldOutcome{tag: outcomeUpdated, kind: withKindField(kind, "layout", wire.Obj{Tag: layout.Tag, Fields: newLayout})}
	}

	switch {
	case field == "Heading":
		result := coerceTextSource(value)
		if !result.ok {
			return fieldOutcome{tag: outcomeTypeMismatch, detail: result.detail}
		}
		return fieldOutcome{tag: outcomeUpdated, kind: withKindField(kind, "heading", result.value)}
	case field == "Orientation" && mode == "Flex":
		result := coerceOrientation(value)
		if !result.ok {
			return fieldOutcome{tag: outcomeTypeMismatch, detail: result.detail}
		}
		return withLayoutField("direction", result.value)
	case field == "Wrap" && mode == "Flex":
		result := coerceBool(value)
		if !result.ok {
			return fieldOutcome{tag: outcomeTypeMismatch, detail: result.detail}
		}
		return withLayoutField("wrap", result.value)
	case field == "Cols" && mode == "Grid":
		result := coerceInt(value)
		if !result.ok {
			return fieldOutcome{tag: outcomeTypeMismatch, detail: result.detail}
		}
		return withLayoutField("cols", result.value)
	case field == "TemplateColumns" && mode == "Grid":
		result := coerceString(value)
		if !result.ok {
			return fieldOutcome{tag: outcomeTypeMismatch, detail: result.detail}
		}
		return withLayoutField("templateColumns", result.value)
	case field == "Children":
		return fieldOutcome{tag: outcomeNotSupported}
	default:
		return fieldOutcome{tag: outcomeUnknownField}
	}
}

func updateField(field string, value wire.Value, kind wire.Obj) fieldOutcome {
	if kind.Tag == "Box" {
		return updateBox(field, value, kind)
	}
	table, ok := propFields[kind.Tag]
	if !ok {
		return fieldOutcome{tag: outcomeNotSupported}
	}
	entry, ok := table[field]
	if !ok {
		return fieldOutcome{tag: outcomeUnknownField}
	}
	if entry.notSupported {
		return fieldOutcome{tag: outcomeNotSupported}
	}
	result := entry.coerce(value)
	if !result.ok {
		return fieldOutcome{tag: outcomeTypeMismatch, detail: result.detail}
	}
	return fieldOutcome{tag: outcomeUpdated, kind: withKindField(kind, entry.wireKey, result.value)}
}

// ── The §3.4 path grammar (hand-rolled, no regex — reference-parser parity) ──
//
//	path     := segment ( "." segment )*
//	segment  := field ( "[" index "]" )?
//	field    := [A-Za-z_][A-Za-z0-9_]*
//	index    := "0" | [1-9][0-9]*

type pathSeg struct {
	field    string
	index    int
	hasIndex bool
}

func isFieldStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isFieldChar(c byte) bool {
	return isFieldStart(c) || (c >= '0' && c <= '9')
}

// parseSegment parses one path segment, returning the segment or an
// error-reason string.
func parseSegment(raw string) (pathSeg, string) {
	if raw == "" {
		return pathSeg{}, "empty segment"
	}
	bracket := strings.IndexByte(raw, '[')
	fieldPart := raw
	if bracket >= 0 {
		fieldPart = raw[:bracket]
	}
	if fieldPart == "" || !isFieldStart(fieldPart[0]) {
		return pathSeg{}, "segment '" + raw + "' is not a field name"
	}
	for i := 1; i < len(fieldPart); i++ {
		if !isFieldChar(fieldPart[i]) {
			return pathSeg{}, "segment '" + raw + "' is not a field name"
		}
	}
	if bracket < 0 {
		return pathSeg{field: fieldPart}, ""
	}
	indexPart := raw[bracket:]
	if len(indexPart) < 3 || indexPart[len(indexPart)-1] != ']' {
		return pathSeg{}, "malformed index in segment '" + raw + "'"
	}
	digits := indexPart[1 : len(indexPart)-1]
	index := 0
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return pathSeg{}, "index in segment '" + raw + "' must be a non-negative decimal integer"
		}
		index = index*10 + int(c-'0')
	}
	if len(digits) > 1 && digits[0] == '0' {
		return pathSeg{}, "index in segment '" + raw + "' has a leading zero"
	}
	return pathSeg{field: fieldPart, index: index, hasIndex: true}, ""
}

// parsePath parses a full path, returning the segments or an error-reason
// string.
func parsePath(path string) ([]pathSeg, string) {
	if strings.TrimSpace(path) == "" {
		return nil, "empty path"
	}
	var segs []pathSeg
	for _, raw := range strings.Split(path, ".") {
		seg, reason := parseSegment(raw)
		if reason != "" {
			return nil, reason
		}
		segs = append(segs, seg)
	}
	return segs, ""
}

// ── Nested typed-traversal legs (§3.4 v1 surface) ───────────────────────────

type nestedTag int

const (
	nestedUpdated nestedTag = iota
	nestedFieldNotFound
	nestedMissingIndex
	nestedIndexOutOfRange
	nestedNotSupported
	nestedTypeMismatch
)

type nestedOutcome struct {
	tag       nestedTag
	kind      wire.Obj
	detail    string
	listField string
	count     int
	requested int
	segment   string
	available []string
}

type nestedLeaf struct {
	wireKey string
	coerce  coercer
}

type nestedList struct {
	wireKey       string
	leaves        map[string]nestedLeaf
	leafOrder     []string // for the available-fields hint
	closureLeaves map[string]bool
}

// nestedRecordLists mirrors the reference engines' nested legs: grid
// Columns[i].{Label,Format,Width}, tabs TabHeaders[i].{Label,Icon,Disabled},
// form Fields[i].{Label,Required,Help}. Chart YFields[i] is the indexed
// scalar-leaf special case handled inline. Closure-bearing sub-fields are
// never addressable.
var nestedRecordLists = map[string]map[string]nestedList{
	"DataGrid": {
		"Columns": {
			wireKey: "columns",
			leaves: map[string]nestedLeaf{
				"Label":  {"label", coerceString},
				"Format": {"format", coerceCellFormat},
				"Width":  {"width", coerceColumnWidth},
			},
			leafOrder:     []string{"Label", "Format", "Width"},
			closureLeaves: map[string]bool{"Value": true, "Kind": true},
		},
	},
	"Tabs": {
		"TabHeaders": {
			wireKey: "tabHeaders",
			leaves: map[string]nestedLeaf{
				"Label":    {"label", coerceTextSource},
				"Icon":     {"icon", coerceString},
				"Disabled": {"disabled", coerceBindingBool},
			},
			leafOrder: []string{"Label", "Icon", "Disabled"},
		},
	},
	"Form": {
		"Fields": {
			wireKey: "fields",
			leaves: map[string]nestedLeaf{
				"Label":    {"label", coerceTextSource},
				"Required": {"required", coerceBool},
				"Help":     {"help", coerceTextSource},
			},
			leafOrder:     []string{"Label", "Required", "Help"},
			closureLeaves: map[string]bool{"Id": true, "Kind": true},
		},
	},
}

func updateNested(segs []pathSeg, value wire.Value, kind wire.Obj) nestedOutcome {
	head, rest := segs[0], segs[1:]

	// Chart YFields — the indexed scalar-leaf case (the element IS the string).
	if kind.Tag == "Chart" {
		if head.field != "YFields" {
			return nestedOutcome{tag: nestedFieldNotFound, segment: head.field, available: []string{"YFields"}}
		}
		var items wire.Arr
		if arr, ok := kind.Fields["yFields"].(wire.Arr); ok {
			items = append(wire.Arr(nil), arr...)
		}
		if !head.hasIndex {
			return nestedOutcome{tag: nestedMissingIndex, listField: "YFields", count: len(items)}
		}
		if head.index >= len(items) {
			return nestedOutcome{tag: nestedIndexOutOfRange, listField: "YFields", count: len(items), requested: head.index}
		}
		if len(rest) > 0 {
			return nestedOutcome{tag: nestedNotSupported}
		}
		result := coerceString(value)
		if !result.ok {
			return nestedOutcome{tag: nestedTypeMismatch, detail: result.detail}
		}
		items[head.index] = result.value
		return nestedOutcome{tag: nestedUpdated, kind: withKindField(kind, "yFields", items)}
	}

	table, ok := nestedRecordLists[kind.Tag]
	if !ok {
		return nestedOutcome{tag: nestedNotSupported}
	}
	entry, ok := table[head.field]
	if !ok {
		available := make([]string, 0, len(table))
		for name := range table {
			available = append(available, name)
		}
		return nestedOutcome{tag: nestedFieldNotFound, segment: head.field, available: available}
	}
	// An absent optional list (e.g. tabHeaders) addresses like an empty one.
	var items wire.Arr
	if arr, ok := kind.Fields[entry.wireKey].(wire.Arr); ok {
		items = append(wire.Arr(nil), arr...)
	}
	if !head.hasIndex {
		return nestedOutcome{tag: nestedMissingIndex, listField: head.field, count: len(items)}
	}
	if head.index >= len(items) {
		return nestedOutcome{tag: nestedIndexOutOfRange, listField: head.field, count: len(items), requested: head.index}
	}
	if len(rest) != 1 || rest[0].hasIndex {
		return nestedOutcome{tag: nestedNotSupported}
	}
	leaf := rest[0].field
	if entry.closureLeaves[leaf] {
		return nestedOutcome{tag: nestedNotSupported}
	}
	leafEntry, ok := entry.leaves[leaf]
	if !ok {
		return nestedOutcome{tag: nestedFieldNotFound, segment: leaf, available: entry.leafOrder}
	}
	result := leafEntry.coerce(value)
	if !result.ok {
		return nestedOutcome{tag: nestedTypeMismatch, detail: result.detail}
	}
	element, ok := items[head.index].(wire.Obj)
	if !ok {
		return nestedOutcome{tag: nestedNotSupported}
	}
	elementFields := make(map[string]wire.Value, len(element.Fields)+1)
	for k, v := range element.Fields {
		elementFields[k] = v
	}
	elementFields[leafEntry.wireKey] = result.value
	items[head.index] = wire.Obj{Tag: element.Tag, Fields: elementFields}
	return nestedOutcome{tag: nestedUpdated, kind: withKindField(kind, entry.wireKey, items)}
}

// ── ReplaceBinding slot surface ─────────────────────────────────────────────

type bindingSlot struct {
	kind string
	slot string
}

// bindingSlots maps (kind tag, op slot) -> the camelCase binding-bearing key.
var bindingSlots = map[bindingSlot]string{
	// 0.2.0 rename law — Metric / LabelValueRow bind their scalar `value`
	// slot; the pre-rename "Source" slot name stays accepted (read-compat:
	// persisted op logs replay), targeting the same wire key.
	{"Metric", "Value"}:         "value",
	{"Metric", "Source"}:        "value",
	{"Metric", "Trend"}:         "trend",
	{"Sparkline", "Source"}:     "source",
	{"Progress", "Fraction"}:    "fraction",
	{"LabelValueRow", "Value"}:  "value",
	{"LabelValueRow", "Source"}: "value",
	{"Stepper", "ActiveStep"}:   "activeStep",
	{"Button", "Disabled"}:      "disabled",
	{"Select", "Disabled"}:      "disabled",
	{"Form", "Disabled"}:        "disabled",
	{"FileUpload", "Disabled"}:  "disabled",
	{"DataGrid", "Source"}:      "source",
	{"Chart", "Source"}:         "source",
	{"Map", "Source"}:           "source",
}

func replaceBindingSlot(slot string, binding wire.Value, kind wire.Obj) (wire.Obj, bool) {
	camel, ok := bindingSlots[bindingSlot{kind.Tag, slot}]
	if !ok {
		return wire.Obj{}, false
	}
	return withKindField(kind, camel, binding), true
}
