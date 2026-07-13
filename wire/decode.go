package wire

import (
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Decode canonical wire JSON into the structural model (WIRE_FORMAT.md §3,
// §6–§8). The node decoder validates the envelope, the "kind" discriminator,
// and — for the kinds given a typed field schema — every required field,
// surfacing the six canonical DecodeError codes with "$"-rooted paths. Kinds
// the wire spec recognises but that have no typed schema yet are accepted
// structurally (pass-through), so the codec round-trips the full corpus while
// typed validation is filled in incrementally. An unrecognised kind is a
// WRONG_NODE_KIND.
//
// The decoder also implements the two normative decode-only tolerance tiers:
// the §16 lenient AI-ingest shorthand (a bare JSON string in any TextSource
// position IS TextSource.Literal) and the §5 legacy Static-payload read-compat
// (a typed Static position still accepts the pre-typed "<opaque>" sentinel and
// boxes-to-null forms, normalising each to its typed shape on re-encode). Both
// are pinned by the corpus lenient-accept family; the legacy container
// decode-upgrades (Stack / GridLayout / Dashboard / Card → Box, Table →
// DataGrid) are pinned by the same family plus the reject contract.

// opaqueSentinel is the reserved §5 marker for a Binding.Static payload the
// encoder could not decompose; closureSentinel is the §4 marker for a
// function-typed slot. Both are reserved vocabulary, never ordinary data.
const (
	opaqueSentinel  = "<opaque>"
	closureSentinel = "<closure>"
)

// ── Entry-boundary machinery ────────────────────────────────────────────────

// decodeFailure is the internal short-circuit carrying a DecodeError out of
// the recursive decoders; DecodeNode / DecodeOp recover it at the entry
// boundary so malformed input surfaces as a structured error, never a panic.
type decodeFailure struct{ err *DecodeError }

func fail(code DecodeErrorCode, path, message string) {
	panic(decodeFailure{&DecodeError{Code: code, Path: path, Message: message}})
}

func failExpecting(code DecodeErrorCode, path, message, expected string) {
	panic(decodeFailure{&DecodeError{Code: code, Path: path, Message: message, ExpectedShape: expected}})
}

func recoverDecode(err *error) {
	if r := recover(); r != nil {
		f, ok := r.(decodeFailure)
		if !ok {
			panic(r)
		}
		*err = f.err
	}
}

// parseJSON parses exactly one JSON document, preserving the int-vs-float
// distinction via json.Number. Anything else — syntax errors, an empty input,
// trailing content after the document — is INVALID_JSON at "$".
func parseJSON(text string) any {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		fail(CodeInvalidJSON, "$", "input is not syntactically valid JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		fail(CodeInvalidJSON, "$", "input carries content after the JSON document")
	}
	return raw
}

// ── Case sets ───────────────────────────────────────────────────────────────

// caseSet is a closed discriminator / bare-enum vocabulary plus its sorted
// ExpectedShape hint.
type caseSet struct {
	names []string
	set   map[string]struct{}
}

func newCaseSet(names ...string) caseSet {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	set := make(map[string]struct{}, len(sorted))
	for _, n := range sorted {
		set[n] = struct{}{}
	}
	return caseSet{names: sorted, set: set}
}

func (c caseSet) has(name string) bool {
	_, ok := c.set[name]
	return ok
}

func (c caseSet) hint() string {
	return "one of: " + strings.Join(c.names, ", ")
}

// ── Primitive expectations ──────────────────────────────────────────────────

func expectObject(raw any, path string) map[string]any {
	obj, ok := raw.(map[string]any)
	if !ok {
		fail(CodeWrongType, path, "expected an object at "+path)
	}
	return obj
}

func expectString(raw any, path string) string {
	s, ok := raw.(string)
	if !ok {
		fail(CodeWrongType, path, "expected a string at "+path)
	}
	return s
}

func expectBool(raw any, path string) bool {
	b, ok := raw.(bool)
	if !ok {
		fail(CodeWrongType, path, "expected a boolean at "+path)
	}
	return b
}

func expectArray(raw any, path string) []any {
	arr, ok := raw.([]any)
	if !ok {
		fail(CodeWrongType, path, "expected an array at "+path)
	}
	return arr
}

func expectInt(raw any, path string) int64 {
	n, ok := raw.(json.Number)
	if !ok || !isIntegerLiteral(string(n)) {
		fail(CodeWrongType, path, "expected an integer at "+path)
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		fail(CodeWrongType, path, "expected an integer at "+path)
	}
	return i
}

// expectNumber accepts any JSON number (an integer or float literal).
func expectNumber(raw any, path string) Value {
	n, ok := raw.(json.Number)
	if !ok {
		fail(CodeWrongType, path, "expected a number at "+path)
	}
	return numberValue(n)
}

func isIntegerLiteral(lit string) bool {
	return !strings.ContainsAny(lit, ".eE")
}

// numberValue keeps the wire's int-vs-float distinction: an integer literal
// stays Int (plain-decimal re-encode); anything with a decimal point or
// exponent — or an integer too large for int64 — is Float (canonical layout).
func numberValue(n json.Number) Value {
	lit := string(n)
	if isIntegerLiteral(lit) {
		if i, err := strconv.ParseInt(lit, 10, 64); err == nil {
			return Int(i)
		}
	}
	f, _ := n.Float64()
	return Float(f)
}

func require(obj map[string]any, key, path string) any {
	raw, ok := obj[key]
	if !ok {
		fail(CodeMissingField, path+"."+key, "missing required field '"+key+"'")
	}
	return raw
}

// dispatch reads + validates a "$type" discriminator, returning the case name.
func dispatch(obj map[string]any, path string, valid caseSet, unknownCode DecodeErrorCode) string {
	raw, ok := obj["$type"]
	if !ok {
		fail(CodeMissingField, path+".$type", "missing $type discriminator")
	}
	tag, ok := raw.(string)
	if !ok {
		fail(CodeWrongType, path+".$type", "$type must be a string")
	}
	if !valid.has(tag) {
		failExpecting(unknownCode, path+".$type", "unrecognised case '"+tag+"'", valid.hint())
	}
	return tag
}

// enumStr validates a bare-string enum position (WIRE_FORMAT.md §3.5).
func enumStr(raw any, path string, allowed caseSet, name string) string {
	s, ok := raw.(string)
	if !ok {
		fail(CodeWrongType, path, name+" must be a string")
	}
	if !allowed.has(s) {
		failExpecting(CodeUnknownDUCase, path, "unrecognised "+name+" '"+s+"'", allowed.hint())
	}
	return s
}

// ── Structural conversion ───────────────────────────────────────────────────

// fromJSON converts an already-parsed JSON value into the structural model.
// Used for wire positions whose content the codec does not decompose (opaque
// payloads, structural pass-through of not-yet-typed cases). Null-LENIENT: the
// §5 obj-erased Binding.Static seam legitimately carries null.
func fromJSON(raw any) Value {
	switch t := raw.(type) {
	case nil:
		return Null{}
	case bool:
		return Bool(t)
	case string:
		return Str(t)
	case json.Number:
		return numberValue(t)
	case []any:
		arr := make(Arr, len(t))
		for i, item := range t {
			arr[i] = fromJSON(item)
		}
		return arr
	case map[string]any:
		tag, _ := t["$type"].(string)
		fields := make(map[string]Value, len(t))
		for k, v := range t {
			if tag != "" && k == "$type" {
				continue
			}
			fields[k] = fromJSON(v)
		}
		return Obj{Tag: tag, Fields: fields}
	}
	fail(CodeWrongType, "$", "value is not a JSON-shaped value")
	return nil
}

// fromJSONStrict is fromJSON for structured JSON payload positions (rule 12:
// the wire model has no null). A JSON null at ANY depth rejects as WRONG_TYPE
// at the null's exact path. Keys are walked in sorted order so the reported
// path is deterministic.
func fromJSONStrict(raw any, path string) Value {
	switch t := raw.(type) {
	case nil:
		failExpecting(
			CodeWrongType,
			path,
			"null is not representable in the Fuaran wire model — omit the field instead",
			"any JSON value except null (rule 12: the wire model has no null)",
		)
	case bool:
		return Bool(t)
	case string:
		return Str(t)
	case json.Number:
		return numberValue(t)
	case []any:
		arr := make(Arr, len(t))
		for i, item := range t {
			arr[i] = fromJSONStrict(item, path+"["+strconv.Itoa(i)+"]")
		}
		return arr
	case map[string]any:
		tag, _ := t["$type"].(string)
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make(map[string]Value, len(t))
		for _, k := range keys {
			if tag != "" && k == "$type" {
				continue
			}
			fields[k] = fromJSONStrict(t[k], path+"."+k)
		}
		return Obj{Tag: tag, Fields: fields}
	}
	fail(CodeWrongType, path, "value is not a JSON-shaped value")
	return nil
}

// ── Bare-string enum vocabularies (WIRE_FORMAT.md §3.5) ─────────────────────

var (
	toneCases              = newCaseSet("Default", "Subdued", "Brand", "Success", "Warning", "Critical", "Info")
	weightCases            = newCaseSet("Compact", "Standard", "Spacious")
	emphasisCases          = newCaseSet("Quiet", "Normal", "Loud")
	orientationCases       = newCaseSet("Vertical", "Horizontal")
	badgeVariantCases      = newCaseSet("Neutral", "Brand", "Success", "Warning", "Critical", "Info")
	headingVariantCases    = newCaseSet("Standard", "Eyebrow", "Caption", "Lead")
	styleRoleCases         = newCaseSet("None", "Eyebrow", "Data", "Lede", "Caption")
	fontVoiceCases         = newCaseSet("Default", "Display", "Structural")
	imageVariantCases      = newCaseSet("Default", "Avatar", "Rounded")
	scrollOrientationCases = newCaseSet("Vertical", "Horizontal", "Both")
	mathDisplayCases       = newCaseSet("Inline", "Block")
	boxRoleCases           = newCaseSet("Group", "Card", "Dashboard", "Separator")
	boxLayoutCases         = newCaseSet("Flex", "Grid", "Auto")
	channelDirectionCases  = newCaseSet("OutOnly", "TwoWay")

	textSourceCases = newCaseSet("Literal", "Bound", "I18n")
	// The binding vocabulary includes the compute-layer cases (Transform / Data /
	// Invoke) so a data-bound node's source round-trips byte-exactly; every
	// non-Static case decodes structurally (validated discriminator, fields
	// preserved).
	bindingCases = newCaseSet(
		"Static", "Query", "Filter", "Selection", "State", "Computed",
		"I18n", "Local", "Format", "Data", "Transform", "Invoke",
	)
	cellFormatCases = newCaseSet("None", "Number", "Currency", "Percent", "SignificantDigits", "Date", "Custom")
	actionCases     = newCaseSet(
		"Chain", "Dispatch", "Navigate", "SetState", "Notify", "WriteToClipboard",
		"ReadFileBody", "Call", "AiTool", "CommitLocal", "Invoke",
	)
)

// knownKinds is every recognised node-kind discriminator (WIRE_FORMAT.md §3.2),
// including the retired legacy container tags that decode-upgrade on read
// (they never re-encode to their old form). A kind not in this set is
// WRONG_NODE_KIND; a kind in this set but absent from kindSchemas is accepted
// structurally.
var knownKinds = newCaseSet(
	// Layout
	"Box", "SplitPanel", "Tabs", "Stepper", "SummaryList", "Disclosure", "Modal", "ScrollArea",
	// Legacy container tags (decode-upgrade to Box; Table upgrades to DataGrid)
	"Dashboard", "Stack", "GridLayout", "Card", "Table",
	// Display
	"Heading", "Markdown", "Metric", "Badge", "Sparkline", "Callout", "Progress", "Skeleton",
	"LabelValueRow", "Link", "Image", "List", "Toast", "CodeBlock", "Math",
	// Input
	"Form", "Button", "FileUpload", "Select", "Filters",
	// Visualisation
	"DataGrid", "Chart", "Map",
	// Structural
	"Custom", "ErrorBoundary", "Switch", "FragmentDecl", "FragmentRef", "Mount",
)

// KnownNodeKinds returns the sorted set of recognised kind.$type discriminators
// (the conformance exhaustiveness guard keys off it).
func KnownNodeKinds() []string {
	return append([]string(nil), knownKinds.names...)
}

// ── Nested-position decoders ────────────────────────────────────────────────

func decodeTextSource(raw any, path string) Value {
	// §16.1 lenient shorthand (normative — a conformant decoder MUST accept):
	// a bare JSON string in any TextSource position IS TextSource.Literal. It
	// decodes to exactly the value the verbose form denotes and re-encodes to
	// the verbose canonical bytes (the corpus lenient-accept family pins this).
	if s, ok := raw.(string); ok {
		return Obj{Tag: "Literal", Fields: map[string]Value{"text": Str(s)}}
	}
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, textSourceCases, CodeUnknownDUCase)
	switch tag {
	case "Literal":
		text := expectString(require(obj, "text", path), path+".text")
		return Obj{Tag: "Literal", Fields: map[string]Value{"text": Str(text)}}
	case "I18n":
		// I18n args are structured JSON payload positions (rule 12: no null).
		return fromJSONStrict(raw, path)
	default:
		// Bound: structural (validated discriminator). NOT null-strict — a
		// Bound binding may carry a Static whose obj-erased value is null (the
		// deliberate §5 opaque-seam exception).
		return fromJSON(raw)
	}
}

func decodeBinding(raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
	if tag == "Static" {
		v, ok := obj["value"]
		if !ok {
			fail(CodeMissingField, path+".value", "Static binding missing value")
		}
		return Obj{Tag: "Static", Fields: map[string]Value{"value": fromJSON(v)}}
	}
	return fromJSON(raw)
}

// typedStaticBinding decodes a Binding whose Static payload is one of the §5
// typed positions (a SelectOption list, a scalar string option, a string list,
// a float series, a marker list). The two legacy payload forms such a position
// may still carry — the pre-typed "<opaque>" sentinel and the boxes-to-null
// empty — NORMALISE per position (read-compat, indefinite): onOpaque / onNull
// are the §5-pinned replacement values, so a legacy input re-encodes in the
// typed form (the corpus lenient-opaque-static-* / lenient-null-static-*
// fixtures pin each). Every other binding case passes through structurally
// with a validated discriminator, exactly as decodeBinding.
func typedStaticBinding(raw any, path string, onTyped func(any, string) Value, onOpaque, onNull Value) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
	if tag != "Static" {
		return fromJSON(raw)
	}
	v, ok := obj["value"]
	if !ok {
		fail(CodeMissingField, path+".value", "Static binding missing value")
	}
	var payload Value
	switch {
	case v == nil:
		payload = onNull
	case v == any(opaqueSentinel):
		payload = onOpaque
	default:
		payload = onTyped(v, path+".value")
	}
	return Obj{Tag: "Static", Fields: map[string]Value{"value": payload}}
}

func decodeSelectOption(raw any, path string) Value {
	obj := expectObject(raw, path)
	label := decodeTextSource(require(obj, "label", path), path+".label")
	value := expectString(require(obj, "value", path), path+".value")
	return Obj{Fields: map[string]Value{"label": label, "value": Str(value)}}
}

func decodeSelectOptionArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeSelectOption(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeBindingSelectOptions(raw any, path string) Value {
	// "<opaque>" → a tagged one-element placeholder; null → the empty typed array.
	opaquePlaceholder := Arr{Obj{Fields: map[string]Value{
		"label": Obj{Tag: "Literal", Fields: map[string]Value{"text": Str(opaqueSentinel)}},
		"value": Str(opaqueSentinel),
	}}}
	return typedStaticBinding(raw, path, decodeSelectOptionArray, opaquePlaceholder, Arr{})
}

func decodeBindingStringOpt(raw any, path string) Value {
	// "<opaque>" → the scalar sentinel string; null → null (a genuine None option).
	return typedStaticBinding(raw, path, func(v any, p string) Value {
		return Str(expectString(v, p))
	}, Str(opaqueSentinel), Null{})
}

func decodeStringArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Str(expectString(item, path+"["+strconv.Itoa(i)+"]"))
	}
	return out
}

func decodeBindingStringList(raw any, path string) Value {
	// "<opaque>" → a one-element placeholder list; null → the empty typed array.
	return typedStaticBinding(raw, path, decodeStringArray, Arr{Str(opaqueSentinel)}, Arr{})
}

func decodeFloatArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = expectNumber(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeBindingFloatSeq(raw any, path string) Value {
	// Both "<opaque>" and null → the empty typed array (a seq has no placeholder element).
	return typedStaticBinding(raw, path, decodeFloatArray, Arr{}, Arr{})
}

func decodeMapMarker(raw any, path string) Value {
	obj := expectObject(raw, path)
	label := decodeTextSource(require(obj, "label", path), path+".label")
	lat := expectNumber(require(obj, "latitude", path), path+".latitude")
	lon := expectNumber(require(obj, "longitude", path), path+".longitude")
	return Obj{Fields: map[string]Value{"label": label, "latitude": lat, "longitude": lon}}
}

func decodeMarkerArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeMapMarker(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeBindingMarkerSeq(raw any, path string) Value {
	// Both "<opaque>" and null → the empty typed array.
	return typedStaticBinding(raw, path, decodeMarkerArray, Arr{}, Arr{})
}

func decodeCellFormat(raw any, path string) Value {
	obj := expectObject(raw, path)
	dispatch(obj, path, cellFormatCases, CodeUnknownDUCase)
	return fromJSON(raw)
}

// decodeAction validates a wire-survivable Action's discriminator and passes
// the case through structurally — but NULL-STRICT: the action payload
// positions (SetState.value / Notify.payload / AiTool.args) are structured
// JSON payload positions per rule 12, and no action case carries a §5 opaque
// seam, so a null anywhere in an action rejects at its exact path.
func decodeAction(raw any, path string) Value {
	obj := expectObject(raw, path)
	dispatch(obj, path, actionCases, CodeUnknownDUCase)
	return fromJSONStrict(raw, path)
}

// decodeGuestChannel decodes Mount's guest channel: direction is a closed DU
// (OutOnly | TwoWay); messageShape is an optional string riding on TwoWay.
func decodeGuestChannel(raw any, path string) Value {
	obj := expectObject(raw, path)
	direction := expectString(require(obj, "direction", path), path+".direction")
	if !channelDirectionCases.has(direction) {
		failExpecting(
			CodeUnknownDUCase,
			path+".direction",
			"unknown channel direction '"+direction+"'",
			"OutOnly | TwoWay",
		)
	}
	fields := map[string]Value{"direction": Str(direction)}
	if raw, ok := obj["messageShape"]; ok {
		fields["messageShape"] = Str(expectString(raw, path+".messageShape"))
	}
	return Obj{Fields: fields}
}

// decodeJSONValue is the strict structural decoder for structured JSON payload
// positions (Custom props / contentHash / exposedNodeIds, Mount capabilities).
func decodeJSONValue(raw any, path string) Value {
	return fromJSONStrict(raw, path)
}

// decodeJSONPassthrough is the null-LENIENT structural decoder — for positions
// that can legitimately carry a §5 obj-erased opaque seam (Mount inputs embed
// whole node trees, whose Binding.Static values may be null).
func decodeJSONPassthrough(raw any, _ string) Value {
	return fromJSON(raw)
}

func decodeString(raw any, path string) Value {
	return Str(expectString(raw, path))
}

func decodeInt(raw any, path string) Value {
	return Int(expectInt(raw, path))
}

func decodeBool(raw any, path string) Value {
	return Bool(expectBool(raw, path))
}

func enumDecoder(allowed caseSet, name string) fieldDecoder {
	return func(raw any, path string) Value {
		return Str(enumStr(raw, path, allowed, name))
	}
}

func decodeChildren(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeNodeValue(item, path+"."+strconv.Itoa(i))
	}
	return out
}

func decodeSingleNode(raw any, path string) Value {
	return decodeNodeValue(raw, path)
}

func decodeSwitchCase(raw any, path string) Value {
	obj := expectObject(raw, path)
	child := decodeNodeValue(require(obj, "child", path), path+".child")
	match := expectString(require(obj, "match", path), path+".match")
	return Obj{Fields: map[string]Value{"child": child, "match": Str(match)}}
}

func decodeSwitchCases(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeSwitchCase(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeTextSourceArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeTextSource(item, path+"."+strconv.Itoa(i))
	}
	return out
}

func decodeIntArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Int(expectInt(item, path+"."+strconv.Itoa(i)))
	}
	return out
}

// ── Per-kind field schemas ──────────────────────────────────────────────────

type fieldDecoder func(raw any, path string) Value

type fieldSpec struct {
	name     string
	required bool
	dec      fieldDecoder
}

// kindSchemas drives typed field validation per kind: (field, required,
// decoder), walked in declaration order so which defect surfaces first is
// deterministic. Kinds absent here but present in knownKinds decode
// structurally (byte-exact on round-trip, typed validation pending). Box and
// the legacy container tags are handled by dedicated builders below.
//
// Populated in init(): the table refers to decoders that recurse back through
// decodeKind (children embed nodes), which a package-level composite literal
// would report as an initialization cycle.
var kindSchemas map[string][]fieldSpec

func init() {
	kindSchemas = map[string][]fieldSpec{
		"Heading": {
			{"level", true, decodeInt},
			{"text", true, decodeTextSource},
			{"variant", true, enumDecoder(headingVariantCases, "variant")},
		},
		"Markdown": {
			{"text", true, decodeTextSource},
		},
		"Metric": {
			{"emphasis", true, enumDecoder(emphasisCases, "emphasis")},
			{"format", true, decodeCellFormat},
			{"label", true, decodeTextSource},
			{"source", true, decodeBinding},
			{"tone", true, enumDecoder(toneCases, "tone")},
			{"weight", true, enumDecoder(weightCases, "weight")},
			{"icon", false, decodeString},
			{"subtext", false, decodeTextSource},
			{"trend", false, decodeBinding},
			{"trendFormat", false, decodeCellFormat},
		},
		"Badge": {
			{"label", true, decodeTextSource},
			{"variant", true, enumDecoder(badgeVariantCases, "variant")},
		},
		"Callout": {
			{"body", true, decodeTextSource},
			{"dismissable", true, decodeBool},
			{"tone", true, enumDecoder(toneCases, "tone")},
			{"heading", false, decodeTextSource},
			{"icon", false, decodeString},
		},
		"Progress": {
			{"fraction", true, decodeBinding},
			{"indeterminate", true, decodeBool},
			{"tone", true, enumDecoder(toneCases, "tone")},
			{"label", false, decodeTextSource},
			{"caveat", false, decodeTextSource},
		},
		"Skeleton": {
			{"rows", true, decodeInt},
		},
		// source is a §5 typed Static float-series position.
		"Sparkline": {
			{"source", true, decodeBindingFloatSeq},
		},
		// source is a §5 typed Static marker-list position; the numeric envelope
		// fields (centre/zoom) pass through structurally like any unlisted key.
		"Map": {
			{"source", true, decodeBindingMarkerSeq},
		},
		"LabelValueRow": {
			{"emphasis", true, decodeBool},
			{"format", true, decodeCellFormat},
			{"label", true, decodeTextSource},
			{"source", true, decodeBinding},
			{"help", false, decodeTextSource},
		},
		"Link": {
			{"download", true, decodeBool},
			{"href", true, decodeBinding},
			{"label", true, decodeTextSource},
			{"rel", false, decodeString},
			{"target", false, decodeString},
		},
		"Image": {
			{"alt", true, decodeTextSource},
			{"src", true, decodeBinding},
			{"variant", true, enumDecoder(imageVariantCases, "variant")},
		},
		"List": {
			{"items", true, decodeTextSourceArray},
			{"ordered", true, decodeBool},
		},
		"Toast": {
			{"dismissable", true, decodeBool},
			{"message", true, decodeTextSource},
			{"open", true, decodeBinding},
			{"tone", true, enumDecoder(toneCases, "tone")},
		},
		"CodeBlock": {
			{"code", true, decodeString},
			{"copyable", true, decodeBool},
			{"highlightLines", true, decodeIntArray},
			{"language", true, decodeString},
			{"lineNumbers", true, decodeBool},
		},
		"Math": {
			{"display", true, enumDecoder(mathDisplayCases, "display")},
			{"source", true, decodeString},
		},
		// The handler fields are OPTIONAL: omitted on the wire when the control is
		// declarative (an omitted handler arms the renderer's write-back default);
		// present as the "<closure>" sentinel when closure-authored. source / value
		// / values are §5 typed Static positions.
		"Select": {
			{"label", true, decodeTextSource},
			{"onChange", false, decodeString},
			{"onChangeMulti", false, decodeString},
			{"source", true, decodeBindingSelectOptions},
			{"value", true, decodeBindingStringOpt},
			{"disabled", false, decodeBinding},
			{"placeholder", false, decodeTextSource},
			{"multiple", false, decodeBool},
			{"values", false, decodeBindingStringList},
		},
		// onDismiss is optional and — unlike the closure-sentinel handlers — a
		// genuine wire-survivable Action, so it decodes null-strict when present.
		"Modal": {
			{"children", true, decodeChildren},
			{"dismissable", true, decodeBool},
			{"onDismiss", false, decodeAction},
			{"open", true, decodeBinding},
			{"heading", false, decodeTextSource},
		},
		"ScrollArea": {
			{"children", true, decodeChildren},
			{"orientation", true, enumDecoder(scrollOrientationCases, "orientation")},
			{"maxHeight", false, decodeInt},
			{"maxWidth", false, decodeInt},
		},
		// Button's two contract-bearing fields go through the typed decoders (the
		// onClick action is null-strict per rule 12); the remaining fields
		// (variant / icon / disabled / …) pass through structurally.
		"Button": {
			{"label", true, decodeTextSource},
			{"onClick", true, decodeAction},
		},
		"Custom": {
			{"moduleId", true, decodeString},
			{"componentId", true, decodeString},
			{"props", false, decodeJSONValue},
			{"contentHash", false, decodeJSONValue},
			{"exposedNodeIds", false, decodeJSONValue},
		},
		// State-bound conditional child. Duplicate match values are NOT a decode
		// error (first-match-wins; the validator flags them).
		"Switch": {
			{"cases", true, decodeSwitchCases},
			{"default", true, decodeSingleNode},
			{"stateKey", true, decodeString},
		},
		// Isolation/embedding boundary (§4o). inputs passes through WITHOUT
		// null-strictness — it embeds whole node trees whose Binding.Static values
		// are §5 opaque seams.
		"Mount": {
			{"scopeId", true, decodeString},
			{"channel", true, decodeGuestChannel},
			{"capabilities", true, decodeJSONValue},
			{"onBubble", true, decodeString},
			{"inputs", false, decodeJSONPassthrough},
		},
	}
}

// ── Box (the unified container) + legacy decode-upgrade ─────────────────────

func decodeBoxLayout(raw any, path string) Obj {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, boxLayoutCases, CodeUnknownDUCase)
	switch tag {
	case "Flex":
		fields := map[string]Value{
			"direction": Str(enumStr(require(obj, "direction", path), path+".direction", orientationCases, "direction")),
			"wrap":      Bool(expectBool(require(obj, "wrap", path), path+".wrap")),
		}
		if raw, ok := obj["gap"]; ok {
			fields["gap"] = Int(expectInt(raw, path+".gap"))
		}
		return Obj{Tag: "Flex", Fields: fields}
	case "Grid":
		fields := map[string]Value{
			"cols": Int(expectInt(require(obj, "cols", path), path+".cols")),
		}
		if raw, ok := obj["gap"]; ok {
			fields["gap"] = Int(expectInt(raw, path+".gap"))
		}
		if raw, ok := obj["templateColumns"]; ok {
			fields["templateColumns"] = Str(expectString(raw, path+".templateColumns"))
		}
		return Obj{Tag: "Grid", Fields: fields}
	default: // Auto
		return Obj{Tag: "Auto", Fields: map[string]Value{}}
	}
}

func decodeBox(obj map[string]any, path string) Obj {
	children := decodeChildren(require(obj, "children", path), path+".children")
	role := enumStr(require(obj, "role", path), path+".role", boxRoleCases, "role")
	layout := decodeBoxLayout(require(obj, "layout", path), path+".layout")
	fields := map[string]Value{
		"children": children,
		"layout":   layout,
		"role":     Str(role),
	}
	if raw, ok := obj["heading"]; ok {
		fields["heading"] = decodeTextSource(raw, path+".heading")
	}
	return Obj{Tag: "Box", Fields: fields}
}

var legacyContainerTags = map[string]bool{
	"Dashboard":  true,
	"Stack":      true,
	"GridLayout": true,
	"Card":       true,
}

// decodeLegacyContainer decode-upgrades a retired container tag to the
// equivalent Box (permalink / op-stream compatibility — a legacy tag never
// re-encodes to its old form).
func decodeLegacyContainer(tag string, obj map[string]any, path string) Obj {
	children := decodeChildren(require(obj, "children", path), path+".children")
	switch tag {
	case "Dashboard":
		return Obj{Tag: "Box", Fields: map[string]Value{
			"children": children,
			"layout":   Obj{Tag: "Auto", Fields: map[string]Value{}},
			"role":     Str("Dashboard"),
		}}
	case "Stack":
		direction := enumStr(require(obj, "orientation", path), path+".orientation", orientationCases, "orientation")
		wrap := expectBool(require(obj, "wrap", path), path+".wrap")
		return Obj{Tag: "Box", Fields: map[string]Value{
			"children": children,
			"layout":   Obj{Tag: "Flex", Fields: map[string]Value{"direction": Str(direction), "wrap": Bool(wrap)}},
			"role":     Str("Group"),
		}}
	case "GridLayout":
		gridFields := map[string]Value{
			"cols": Int(expectInt(require(obj, "cols", path), path+".cols")),
		}
		if raw, ok := obj["templateColumns"]; ok {
			gridFields["templateColumns"] = Str(expectString(raw, path+".templateColumns"))
		}
		return Obj{Tag: "Box", Fields: map[string]Value{
			"children": children,
			"layout":   Obj{Tag: "Grid", Fields: gridFields},
			"role":     Str("Group"),
		}}
	default: // Card
		fields := map[string]Value{
			"children": children,
			"layout":   Obj{Tag: "Flex", Fields: map[string]Value{"direction": Str("Vertical"), "wrap": Bool(false)}},
			"role":     Str("Card"),
		}
		if raw, ok := obj["heading"]; ok {
			fields["heading"] = decodeTextSource(raw, path+".heading")
		}
		return Obj{Tag: "Box", Fields: fields}
	}
}

// decodeLegacyTable decode-upgrades a retired Table tag to a static read-only
// DataGrid: the static text table becomes the staticRows mode, with an empty
// column set and an opaque Static source. Accepted on read; never re-encodes
// as Table.
func decodeLegacyTable(obj map[string]any, path string) Obj {
	headers := decodeTextSourceArray(require(obj, "headers", path), path+".headers")
	rowsArr := expectArray(require(obj, "rows", path), path+".rows")
	rows := make(Arr, len(rowsArr))
	for i, row := range rowsArr {
		rows[i] = decodeTextSourceArray(row, path+".rows["+strconv.Itoa(i)+"]")
	}
	return Obj{Tag: "DataGrid", Fields: map[string]Value{
		"columns":  Arr{},
		"editable": Bool(false),
		"source":   Obj{Tag: "Static", Fields: map[string]Value{"value": Str(opaqueSentinel)}},
		"staticRows": Obj{Fields: map[string]Value{
			"headers": headers,
			"rows":    rows,
		}},
	}}
}

// ── Kind / style / state / node envelope ────────────────────────────────────

func decodeKind(raw any, path string) Obj {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, knownKinds, CodeWrongNodeKind)
	switch {
	case tag == "Box":
		return decodeBox(obj, path)
	case legacyContainerTags[tag]:
		return decodeLegacyContainer(tag, obj, path)
	case tag == "Table":
		return decodeLegacyTable(obj, path)
	}
	schema, hasSchema := kindSchemas[tag]
	if !hasSchema {
		// Recognised kind without a typed schema yet — accept structurally.
		fields := make(map[string]Value, len(obj))
		for k, v := range obj {
			if k != "$type" {
				fields[k] = fromJSON(v)
			}
		}
		return Obj{Tag: tag, Fields: fields}
	}
	fields := make(map[string]Value, len(obj))
	known := make(map[string]bool, len(schema))
	for _, fs := range schema {
		known[fs.name] = true
		if raw, ok := obj[fs.name]; ok {
			fields[fs.name] = fs.dec(raw, path+"."+fs.name)
		} else if fs.required {
			fail(CodeMissingField, path+"."+fs.name, "missing required field '"+fs.name+"'")
		}
	}
	// Preserve any extra (unknown) keys structurally so the round-trip is
	// lossless and tolerant of fields a later spec revision adds (§2 rule 2).
	for k, v := range obj {
		if k != "$type" && !known[k] {
			fields[k] = fromJSON(v)
		}
	}
	return Obj{Tag: tag, Fields: fields}
}

func decodeStyle(raw any, path string) Obj {
	obj := expectObject(raw, path)
	fields := map[string]Value{
		"emphasis": Str(enumStr(require(obj, "emphasis", path), path+".emphasis", emphasisCases, "emphasis")),
	}
	fields["tone"] = Str(enumStr(require(obj, "tone", path), path+".tone", toneCases, "tone"))
	fields["weight"] = Str(enumStr(require(obj, "weight", path), path+".weight", weightCases, "weight"))
	if raw, ok := obj["role"]; ok {
		fields["role"] = Str(enumStr(raw, path+".role", styleRoleCases, "role"))
	}
	if raw, ok := obj["voice"]; ok {
		fields["voice"] = Str(enumStr(raw, path+".voice", fontVoiceCases, "voice"))
	}
	return Obj{Fields: fields}
}

func decodeState(raw any, path string) Obj {
	obj := expectObject(raw, path)
	fields := make(map[string]Value)
	if raw, ok := obj["onLoading"]; ok {
		fields["onLoading"] = decodeNodeValue(raw, path+".onLoading")
	}
	if raw, ok := obj["onEmpty"]; ok {
		fields["onEmpty"] = decodeNodeValue(raw, path+".onEmpty")
	}
	if raw, ok := obj["onError"]; ok {
		fields["onError"] = fromJSON(raw) // the "<closure>" sentinel
	}
	return Obj{Fields: fields}
}

// decodeNodeValue decodes a node envelope, applying the §8 NodeId invariants.
func decodeNodeValue(raw any, path string) Node {
	obj := expectObject(raw, path)

	rawID, ok := obj["id"]
	if !ok {
		fail(CodeMissingField, path+".id", "missing required field 'id'")
	}
	id, ok := rawID.(string)
	if !ok {
		fail(CodeWrongType, path+".id", "id must be a string")
	}
	if id == "" {
		fail(CodeEmptyNodeID, path+".id", "id must be a non-empty string")
	}

	rawKind, ok := obj["kind"]
	if !ok {
		fail(CodeMissingField, path+".kind", "missing required field 'kind'")
	}
	kind := decodeKind(rawKind, path+".kind")

	extras := make(map[string]Value)
	if raw, ok := obj["state"]; ok {
		extras["state"] = decodeState(raw, path+".state")
	}
	if raw, ok := obj["style"]; ok {
		extras["style"] = decodeStyle(raw, path+".style")
	}
	if raw, ok := obj["accessibility"]; ok {
		extras["accessibility"] = fromJSON(raw)
	}
	return Node{ID: id, Kind: kind, Extras: extras}
}
