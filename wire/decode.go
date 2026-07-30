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
// and — for the kinds given a typed decoder — every required field, surfacing
// the six canonical DecodeError codes with "$"-rooted paths. Kinds the wire
// spec recognises but that have no typed decoder yet are accepted structurally
// (pass-through), so the codec round-trips the full corpus while typed
// validation is filled in incrementally. An unrecognised kind is a
// WRONG_NODE_KIND.
//
// The decoder also implements the normative decode-only tolerance tiers:
//
//   - the 0.2.0 canonical bare-string TextSource (a Literal IS the bare JSON
//     string on the wire; the {"$type":"Literal"} envelope is accepted
//     indefinitely and normalises to the bare string on re-encode);
//   - the §3.6 omitted-when-default discipline (stylistic + behavioural
//     defaults are omitted on BOTH boundaries: an absent field restores its
//     identity default, and an explicit identity default normalises to the
//     omitted form on re-encode);
//   - the §3.6 lenient-ingest aliases (enum-value synonyms like Danger→
//     Critical, field-name synonyms like href→route — decode-only, never
//     encoded) and shape coercions (a bare array/scalar in a Binding slot IS
//     Static; the Bound wrapper unwraps in a Binding slot; a Static envelope
//     around a plain scalar unwraps; a bare-string SelectOption expands; the
//     Transform params map coerces to the canonical list; a Grid layout with
//     no column spec is Auto);
//   - the §5 legacy Static-payload read-compat ("<opaque>" sentinel and
//     boxes-to-null forms normalise per position) and the legacy container
//     decode-upgrades (Stack / GridLayout / Dashboard / Card → Box, Table →
//     DataGrid).
//
// All are pinned by the corpus round-trip + lenient-accept families.

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

// unwrapStaticAny is the lenient-ingest inverse-confusion unwrap (§3.6,
// generalised): a Static envelope wrapped around a PLAIN scalar unwraps
// before the scalar readers — at a plain-scalar position the envelope has
// exactly one reading. Objects that are NOT a well-formed Static envelope
// pass through untouched and fail with the normal error.
func unwrapStaticAny(raw any) any {
	if m, ok := raw.(map[string]any); ok {
		if t, ok := m["$type"].(string); ok && t == "Static" {
			if inner, ok := m["value"]; ok {
				return inner
			}
		}
	}
	return raw
}

func expectObject(raw any, path string) map[string]any {
	obj, ok := raw.(map[string]any)
	if !ok {
		fail(CodeWrongType, path, "expected an object at "+path)
	}
	return obj
}

func expectString(raw any, path string) string {
	s, ok := unwrapStaticAny(raw).(string)
	if !ok {
		fail(CodeWrongType, path, "expected a string at "+path)
	}
	return s
}

func expectBool(raw any, path string) bool {
	b, ok := unwrapStaticAny(raw).(bool)
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
	n, ok := unwrapStaticAny(raw).(json.Number)
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
	n, ok := unwrapStaticAny(raw).(json.Number)
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

// requireAliased reads the canonical key, else the first present alias (the
// lenient-ingest FIELD-NAME aliases, §3.6 — decode-only; the canonical name
// wins when both are present; error paths always name the canonical key).
func requireAliased(obj map[string]any, key, path string, aliases ...string) any {
	if raw, ok := obj[key]; ok {
		return raw
	}
	for _, a := range aliases {
		if raw, ok := obj[a]; ok {
			return raw
		}
	}
	fail(CodeMissingField, path+"."+key, "missing required field '"+key+"'")
	return nil
}

func optAliased(obj map[string]any, key string, aliases ...string) (any, bool) {
	if raw, ok := obj[key]; ok {
		return raw, true
	}
	for _, a := range aliases {
		if raw, ok := obj[a]; ok {
			return raw, true
		}
	}
	return nil, false
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

// enumStr validates a bare-string enum position (WIRE_FORMAT.md §3.5),
// mapping any lenient-ingest value alias to its canonical case first.
func enumStr(raw any, path string, allowed caseSet, name string, aliases map[string]string) string {
	s, ok := raw.(string)
	if !ok {
		fail(CodeWrongType, path, name+" must be a string")
	}
	if canonical, ok := aliases[s]; ok {
		s = canonical
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
	buttonVariantCases     = newCaseSet("Primary", "Secondary", "Tertiary", "Destructive")
	headingVariantCases    = newCaseSet("Standard", "Eyebrow", "Caption", "Lead")
	dateVariantCases       = newCaseSet("Date", "Time", "DateTime")
	styleRoleCases         = newCaseSet("None", "Eyebrow", "Data", "Lede", "Caption")
	fontVoiceCases         = newCaseSet("Default", "Display", "Structural")
	imageVariantCases      = newCaseSet("Default", "Avatar", "Rounded")
	scrollOrientationCases = newCaseSet("Vertical", "Horizontal", "Both")
	mathDisplayCases       = newCaseSet("Inline", "Block")
	chartKindCases         = newCaseSet("Line", "Bar", "Area", "Pie", "Scatter", "Heatmap")
	boxRoleCases           = newCaseSet("Group", "Card", "Dashboard", "Separator")
	boxLayoutCases         = newCaseSet("Flex", "Grid", "Auto")
	channelDirectionCases  = newCaseSet("OutOnly", "TwoWay")
	textAnchorCases        = newCaseSet("Start", "Middle", "End")

	// Drawing (Phase 524) — the closed Shape / CurveCommand DUs. An unrecognised
	// discriminator is UNKNOWN_DU_CASE (the typed-surface default-deny).
	shapeCases        = newCaseSet("Group", "Rectangle", "Line", "Polyline", "Polygon", "Curve", "Circle", "Ellipse", "Label")
	curveCommandCases = newCaseSet("MoveTo", "LineTo", "CubicTo", "QuadraticTo", "Close")

	textSourceCases = newCaseSet("Literal", "Bound", "I18n")
	// The binding vocabulary includes the compute-layer cases (Transform /
	// Invoke) plus "Bound" — the TextSource-wrapper convention transferred to
	// a bare-Binding slot, accepted leniently and unwrapped in place.
	bindingCases = newCaseSet(
		"Static", "Query", "Filter", "Selection", "State", "Computed",
		"I18n", "Local", "Format", "Data", "Transform", "Invoke", "Bound",
	)
	cellFormatCases   = newCaseSet("None", "Number", "Currency", "Percent", "SignificantDigits", "Date", "Custom")
	columnWidthCases  = newCaseSet("Auto", "Fixed", "Flex")
	cellKindCases     = newCaseSet("Text", "Numeric", "Date", "Editable", "Checkbox", "Button", "ButtonGroup", "Link", "Pill", "TonedPill", "Progress", "Custom")
	formFieldCases    = newCaseSet("Text", "Number", "RangedNumber", "Checkbox", "Choice", "SegmentedChoice", "TextArea", "Range", "Date", "DateRange")
	flushTriggerCases = newCaseSet("OnBlur", "OnSubmit", "OnDebounce", "OnCommitAction")
	actionCases       = newCaseSet(
		"Chain", "Dispatch", "Navigate", "SetState", "Notify", "WriteToClipboard",
		"ReadFileBody", "Call", "AiTool", "CommitLocal", "Invoke",
	)
	callTargetCases = newCaseSet("State", "Query")
)

// Lenient-ingest enum-value aliases (§3.6, decode-only; never encoded —
// re-encode normalises to the canonical case). Faithful same-concept mappings
// only; a name betraying a different concept stays a reject.
var (
	toneAliases = map[string]string{
		"Positive": "Success", "Danger": "Critical", "Negative": "Critical", "Neutral": "Default",
	}
	emphasisAliases = map[string]string{
		"Strong": "Loud", "Bold": "Loud", "Subtle": "Quiet", "Muted": "Quiet",
	}
	orientationAliases = map[string]string{
		"Row": "Horizontal", "row": "Horizontal", "Column": "Vertical", "column": "Vertical",
	}
	badgeVariantAliases = map[string]string{
		"Default": "Neutral", "Danger": "Critical",
	}
	buttonVariantAliases = map[string]string{
		"Danger": "Destructive",
	}
	headingVariantAliases = map[string]string{
		"Default": "Standard",
	}
	noAliases = map[string]string(nil)
)

// knownKinds is every recognised node-kind discriminator (WIRE_FORMAT.md §3.2),
// including the retired legacy container tags that decode-upgrade on read
// (they never re-encode to their old form). A kind not in this set is
// WRONG_NODE_KIND; a kind in this set but absent from the typed decoders is
// accepted structurally.
var knownKinds = newCaseSet(
	// Layout
	"Box", "SplitPanel", "Tabs", "Stepper", "SummaryList", "Disclosure", "Modal", "ScrollArea",
	// Display
	"Heading", "Markdown", "Metric", "Fact", "Badge", "Sparkline", "Callout", "Progress", "Skeleton",
	"LabelValueRow", "Link", "Image", "List", "Toast", "CodeBlock", "Math", "Drawing",
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

// CanonicalNodeKinds returns the emittable NodeKind vocabulary. Since Phase 673
// retired the superseded container tags outright, every recognised kind is also
// emittable, so this is now identical to KnownNodeKinds. It is kept as a distinct
// name because the Phase 548 cross-host kind-set attestation is pinned against it,
// and because a future decode-only tag would re-introduce the distinction.
func CanonicalNodeKinds() []string {
	return append([]string(nil), knownKinds.names...)
}

// CanonicalFormFieldKinds returns the emittable FormFieldKind control vocabulary
// — the Phase 746 attestation twin of CanonicalNodeKinds, pinned against the
// corpus manifest's `formFieldKinds` enumeration. Since the filters/forms
// unification there is ONE control vocabulary, so this covers both carriers.
func CanonicalFormFieldKinds() []string {
	return append([]string(nil), formFieldCases.names...)
}

// ── Value-shape predicates (the omit-when-default seam, §3.6) ──────────────

func isTagOnly(v Value, tag string) bool {
	o, ok := v.(Obj)
	return ok && o.Tag == tag && len(o.Fields) == 0
}

func isStr(v Value, s string) bool {
	t, ok := v.(Str)
	return ok && string(t) == s
}

func isBool(v Value, b bool) bool {
	t, ok := v.(Bool)
	return ok && bool(t) == b
}

func isNumericZero(v Value) bool {
	switch t := v.(type) {
	case Int:
		return t == 0
	case Float:
		return t == 0
	}
	return false
}

// ── TextSource (bare-string canonical since 0.2.0) ──────────────────────────

// decodeTextSource decodes a TextSource position. The bare JSON string IS the
// canonical Literal form; the verbose {"$type":"Literal"} envelope is
// decode-accepted indefinitely and normalises to the bare string. Bound / I18n
// keep their $type objects (a Bound's inner binding decodes through the
// binding normaliser; I18n args are structured JSON payload positions —
// rule 12: no null — and are always present on the canonical wire).
func decodeTextSource(raw any, path string) Value {
	if s, ok := raw.(string); ok {
		return Str(s)
	}
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, textSourceCases, CodeUnknownDUCase)
	switch tag {
	case "Literal":
		return Str(expectString(require(obj, "text", path), path+".text"))
	case "I18n":
		key := expectString(require(obj, "key", path), path+".key")
		args := Value(Obj{Fields: map[string]Value{}})
		if raw, ok := obj["args"]; ok {
			args = fromJSONStrict(raw, path+".args")
		}
		return Obj{Tag: "I18n", Fields: map[string]Value{"args": args, "key": Str(key)}}
	default: // Bound
		binding := decodeBinding(require(obj, "binding", path), path+".binding")
		return Obj{Tag: "Bound", Fields: map[string]Value{"binding": binding}}
	}
}

// ── Bindings ────────────────────────────────────────────────────────────────

// staticParser decodes a Binding.Static payload (and the bare-scalar/array
// shape coercion) for one slot's typed shape.
type staticParser func(raw any, path string) Value

// objStatic is the obj-erased default: structural, null-lenient (§5).
func objStatic(raw any, path string) Value {
	return fromJSON(raw)
}

// floatStatic — a JSON number, the three non-finite sentinels, or the Static
// envelope around either (the inverse-confusion unwrap).
func floatStatic(raw any, path string) Value {
	raw = unwrapStaticAny(raw)
	if n, ok := raw.(json.Number); ok {
		return numberValue(n)
	}
	if s, ok := raw.(string); ok && (s == "NaN" || s == "Infinity" || s == "-Infinity") {
		return Str(s)
	}
	fail(CodeWrongType, path, "expected a number at "+path)
	return nil
}

func boolStatic(raw any, path string) Value {
	return Bool(expectBool(raw, path))
}

func stringStatic(raw any, path string) Value {
	return Str(expectString(raw, path))
}

// stringOptStatic — a string option: null is a genuine None; the legacy
// "<opaque>" sentinel stays the scalar sentinel string.
func stringOptStatic(raw any, path string) Value {
	raw = unwrapStaticAny(raw)
	if raw == nil {
		return Null{}
	}
	if s, ok := raw.(string); ok {
		return Str(s)
	}
	fail(CodeWrongType, path, "expected a string (or null) at "+path)
	return nil
}

// decodeSelectOption — an option object, or the bare-string shorthand
// ("A" IS {"label":"A","value":"A"} — the HTML <select> prior).
func decodeSelectOption(raw any, path string) Value {
	if s, ok := raw.(string); ok {
		return Obj{Fields: map[string]Value{"label": Str(s), "value": Str(s)}}
	}
	obj := expectObject(raw, path)
	label := decodeTextSource(require(obj, "label", path), path+".label")
	value := expectString(require(obj, "value", path), path+".value")
	return Obj{Fields: map[string]Value{"label": label, "value": Str(value)}}
}

// selectOptionsStatic — the typed option-list payload; "<opaque>" → a tagged
// one-element placeholder; null → the empty typed list (§5 read-compat).
func selectOptionsStatic(raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{Obj{Fields: map[string]Value{"label": Str(opaqueSentinel), "value": Str(opaqueSentinel)}}}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeSelectOption(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// stringListStatic — "<opaque>" → a one-element placeholder list; null → the
// empty typed list.
func stringListStatic(raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{Str(opaqueSentinel)}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Str(expectString(item, path+"["+strconv.Itoa(i)+"]"))
	}
	return out
}

// floatSeqStatic — both "<opaque>" and null → the empty typed array.
func floatSeqStatic(raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = expectNumber(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeMapMarker(raw any, path string) Value {
	obj := expectObject(raw, path)
	label := decodeTextSource(require(obj, "label", path), path+".label")
	lat := expectNumber(require(obj, "latitude", path), path+".latitude")
	lon := expectNumber(require(obj, "longitude", path), path+".longitude")
	return Obj{Fields: map[string]Value{"label": label, "latitude": lat, "longitude": lon}}
}

// markerSeqStatic — both "<opaque>" and null → the empty typed array.
func markerSeqStatic(raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeMapMarker(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// objSeqStatic — the host-typed row-sequence payload (DataGrid / Chart
// source). The value is opaque by design: every accepted payload (null,
// "<opaque>", an array of rows) re-encodes as the "<opaque>" sentinel —
// mirroring the reference encoder's obj best-effort collapse.
func objSeqStatic(raw any, path string) Value {
	switch t := raw.(type) {
	case nil:
		return Str(opaqueSentinel)
	case string:
		return Str(opaqueSentinel)
	case []any:
		return Str(opaqueSentinel)
	default:
		_ = t
	}
	fail(CodeWrongType, path, "expected an array (or the \"<opaque>\" sentinel) at "+path)
	return nil
}

func decodeLocalFlushTrigger(raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, flushTriggerCases, CodeUnknownDUCase)
	if tag == "OnDebounce" {
		ms := expectInt(require(obj, "milliseconds", path), path+".milliseconds")
		return Obj{Tag: tag, Fields: map[string]Value{"milliseconds": Int(ms)}}
	}
	return Obj{Tag: tag, Fields: map[string]Value{}}
}

// decodeInvokeArgs — the [{"addr","value"}] scalar pairs of Binding.Invoke /
// Action.Invoke.
func decodeInvokeArgs(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		obj := expectObject(item, p)
		addr := expectString(require(obj, "addr", p), p+".addr")
		value := expectString(require(obj, "value", p), p+".value")
		out[i] = Obj{Fields: map[string]Value{"addr": Str(addr), "value": Str(value)}}
	}
	return out
}

// decodeBindingWith is the master Binding decoder: it validates the
// discriminator, applies the lenient shape coercions (a bare array / scalar
// IS Static; the Bound wrapper unwraps in place), drops the 0.2.0 wire-omitted
// closure sentinels (Query.accessor / Selection.accessor), renames the
// field-name aliases, and decodes each case to its canonical shape.
func decodeBindingWith(raw any, path string, parse staticParser) Value {
	switch raw.(type) {
	case string, json.Number, bool, []any:
		// §3.6 shape coercion: every Binding case is a $type-discriminated
		// object, so a bare array or scalar can only mean Static. Objects stay
		// strict (an object without $type is more plausibly a mistyped
		// binding); null stays strict (ambiguous with absent).
		return Obj{Tag: "Static", Fields: map[string]Value{"value": parse(raw, path)}}
	}
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
	switch tag {
	case "Static":
		// Phase 677 — absence is structural: a MISSING `value` means the binding
		// carries none, and the legacy `"value": null` spelling normalises to the
		// same thing (§16 shorthand). Where the slot's parser yields Null the key
		// is omitted entirely; where it yields a typed empty (options normalise to
		// []) that empty is emitted, so "no selection" and "selected nothing" stay
		// distinguishable.
		raw := obj["value"]
		decoded := parse(raw, path+".value")

		if _, isNull := decoded.(Null); isNull {
			return Obj{Tag: "Static", Fields: map[string]Value{}}
		}

		return Obj{Tag: "Static", Fields: map[string]Value{"value": decoded}}
	case "Query":
		name := expectString(require(obj, "name", path), path+".name")
		fields := map[string]Value{"name": Str(name)}
		// Field aliases: deps / dependencies. The accessor closure sentinel is
		// wire-omitted (0.2.0) — dropped on decode. dependsOn is
		// omitted-when-empty so the degenerate Query is byte-stable.
		if raw, ok := optAliased(obj, "dependsOn", "deps", "dependencies"); ok {
			arr := expectArray(raw, path+".dependsOn")
			if len(arr) > 0 {
				out := make(Arr, len(arr))
				for i, item := range arr {
					out[i] = Str(expectString(item, path+".dependsOn["+strconv.Itoa(i)+"]"))
				}
				fields["dependsOn"] = out
			}
		}
		return Obj{Tag: "Query", Fields: fields}
	case "Filter":
		name := expectString(require(obj, "name", path), path+".name")
		fields := map[string]Value{"name": Str(name)}
		if raw, ok := obj["defaultValue"]; ok {
			fields["defaultValue"] = fromJSON(raw)
		}
		return Obj{Tag: "Filter", Fields: fields}
	case "Selection":
		nodeID := expectString(require(obj, "nodeId", path), path+".nodeId")
		fields := map[string]Value{"nodeId": Str(nodeID)}
		if raw, ok := obj["defaultValue"]; ok {
			fields["defaultValue"] = fromJSON(raw)
		}
		if raw, ok := obj["field"]; ok {
			fields["field"] = Str(expectString(raw, path+".field"))
		}
		return Obj{Tag: "Selection", Fields: fields}
	case "State":
		key := expectString(require(obj, "key", path), path+".key")
		fields := map[string]Value{"key": Str(key)}
		// Field aliases: initialValue / default — the React useState prior.
		// Phase 677 — an explicit null default is absence, same as omitting it.
		if raw, ok := optAliased(obj, "defaultValue", "initialValue", "default"); ok && raw != nil {
			fields["defaultValue"] = fromJSON(raw)
		}
		return Obj{Tag: "State", Fields: fields}
	case "Computed":
		return Obj{Tag: "Computed", Fields: map[string]Value{"fn": Str(closureSentinel)}}
	case "I18n":
		key := expectString(require(obj, "key", path), path+".key")
		fields := map[string]Value{"key": Str(key)}
		if raw, ok := obj["args"]; ok {
			argsObj := expectObject(raw, path+".args")
			args := make(map[string]Value, len(argsObj))
			for k, v := range argsObj {
				args[k] = decodeBinding(v, path+".args."+k)
			}
			fields["args"] = Obj{Fields: args}
		}
		return Obj{Tag: "I18n", Fields: fields}
	case "Local":
		initialFrom := decodeBindingWith(require(obj, "initialFrom", path), path+".initialFrom", parse)
		flushOn := Value(Obj{Tag: "OnBlur", Fields: map[string]Value{}})
		if raw, ok := obj["flushOn"]; ok {
			flushOn = decodeLocalFlushTrigger(raw, path+".flushOn")
		}
		return Obj{Tag: "Local", Fields: map[string]Value{
			"flushOn":     flushOn,
			"format":      Str(closureSentinel),
			"initialFrom": initialFrom,
			"onCommit":    Str(closureSentinel),
			"parse":       Str(closureSentinel),
		}}
	case "Format":
		format := fromJSON(require(obj, "format", path))
		locale := fromJSON(require(obj, "locale", path))
		source := decodeBindingWith(require(obj, "source", path), path+".source", floatStatic)
		return Obj{Tag: "Format", Fields: map[string]Value{"format": format, "locale": locale, "source": source}}
	case "Transform":
		srcRaw := require(obj, "source", path)
		source := atComputePath(path+".source", func() Value { return decodeFrameSource(srcRaw) })
		pipeRaw := require(obj, "pipeline", path)
		pipeline := atComputePath(path+".pipeline", func() Value { return decodeComputePipeline(pipeRaw) })
		fields := map[string]Value{"pipeline": pipeline, "source": source}
		if raw, ok := obj["params"]; ok {
			params := decodeTransformParams(raw, path+".params")
			if len(params) > 0 {
				fields["params"] = params
			}
		}
		return Obj{Tag: "Transform", Fields: fields}
	case "Invoke":
		capabilityID := expectString(require(obj, "capabilityId", path), path+".capabilityId")
		args := decodeInvokeArgs(require(obj, "args", path), path+".args")
		return Obj{Tag: "Invoke", Fields: map[string]Value{"args": args, "capabilityId": Str(capabilityID)}}
	case "Bound":
		// The TextSource-wrapper convention transferred to a bare-Binding
		// slot: Bound carries exactly one payload field, so the unwrap is
		// one-to-one — decode the inner binding in place. Decode-only.
		return decodeBindingWith(require(obj, "binding", path), path+".binding", parse)
	default: // "Data" — structural pass-through with a validated discriminator.
		return fromJSON(raw)
	}
}

// decodeTransformParams — Transform's [{ "from": <Binding>, "name": <string> }]
// param list, with the §3.6 map coercion: params are a NAME-KEYED SET
// (ColExpr.Param lookup), so the name→binding map form coerces to the
// canonical array (sorted by name); `value` aliases `from` at the element.
func decodeTransformParams(raw any, path string) Arr {
	if m, ok := raw.(map[string]any); ok {
		if _, tagged := m["$type"]; !tagged {
			names := make([]string, 0, len(m))
			for k := range m {
				names = append(names, k)
			}
			sort.Strings(names)
			out := make(Arr, len(names))
			for i, name := range names {
				from := decodeBinding(m[name], path+"."+name+".from")
				out[i] = Obj{Fields: map[string]Value{"from": from, "name": Str(name)}}
			}
			return out
		}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		obj := expectObject(item, p)
		name := expectString(require(obj, "name", p), p+".name")
		from := decodeBinding(requireAliased(obj, "from", p, "value"), path+"."+name+".from")
		out[i] = Obj{Fields: map[string]Value{"from": from, "name": Str(name)}}
	}
	return out
}

func decodeBinding(raw any, path string) Value {
	return decodeBindingWith(raw, path, objStatic)
}

func decodeBindingFloat(raw any, path string) Value {
	return decodeBindingWith(raw, path, floatStatic)
}

func decodeBindingBool(raw any, path string) Value {
	return decodeBindingWith(raw, path, boolStatic)
}

func decodeBindingString(raw any, path string) Value {
	return decodeBindingWith(raw, path, stringStatic)
}

func decodeBindingStringOpt(raw any, path string) Value {
	return decodeBindingWith(raw, path, stringOptStatic)
}

func decodeBindingSelectOptions(raw any, path string) Value {
	return decodeBindingWith(raw, path, selectOptionsStatic)
}

func decodeBindingStringList(raw any, path string) Value {
	return decodeBindingWith(raw, path, stringListStatic)
}

func decodeBindingFloatSeq(raw any, path string) Value {
	return decodeBindingWith(raw, path, floatSeqStatic)
}

func decodeBindingMarkerSeq(raw any, path string) Value {
	return decodeBindingWith(raw, path, markerSeqStatic)
}

func decodeBindingObjSeq(raw any, path string) Value {
	return decodeBindingWith(raw, path, objSeqStatic)
}

// decodeRangePair reads a {min, max} pair (object or lenient two-element
// array) into the canonical bare pair object.
func decodeRangePair(raw any, path string) Value {
	if m, ok := raw.(map[string]any); ok {
		mn, hasMin := m["min"]
		mx, hasMax := m["max"]
		if hasMin && hasMax {
			return Obj{Fields: map[string]Value{
				"max": expectNumber(mx, path+".max"),
				"min": expectNumber(mn, path+".min"),
			}}
		}
		fail(CodeWrongType, path, "expected an object with min and max numbers at "+path)
	}
	if arr, ok := raw.([]any); ok && len(arr) == 2 {
		return Obj{Fields: map[string]Value{
			"max": expectNumber(arr[1], path+"[1]"),
			"min": expectNumber(arr[0], path+"[0]"),
		}}
	}
	fail(CodeWrongType, path, "expected a range pair ({min, max} object or [min, max] array) at "+path)
	return nil
}

// decodeRangeValue — the Range control's value slot. The canonical Static
// pair rides as the BARE {min, max} object (no envelope); the Static envelope
// and the two-element array are lenient forms of the same value; every other
// binding case decodes normally (its State.defaultValue pair normalises to
// the bare-pair shape via fromJSON pass-through).
func decodeRangeValue(raw any, path string) Value {
	if m, ok := raw.(map[string]any); ok {
		if _, tagged := m["$type"]; !tagged {
			return decodeRangePair(raw, path)
		}
		obj := expectObject(raw, path)
		tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
		if tag == "Static" {
			v, ok := obj["value"]
			if !ok {
				fail(CodeMissingField, path+".value", "Static binding missing value")
			}
			return decodeRangePair(v, path+".value")
		}
		return decodeBindingWith(raw, path, decodeRangePair)
	}
	if _, ok := raw.([]any); ok {
		return decodeRangePair(raw, path)
	}
	return decodeBindingWith(raw, path, decodeRangePair)
}

// dateRangePairShape is the ExpectedShape hint carried by the ordered-pair
// reject — the didactic half of the error (the message names the rule, this
// names the shape that satisfies it).
const dateRangePairShape = `ordered ISO-8601 pair ({"from": <iso>, "to": <iso>} with from <= to)`

// decodeDateRangePair reads a {from, to} ISO-8601 pair (object or lenient
// two-element array) into the canonical bare pair object — the Range twin,
// differing in exactly three ways: strings not numbers, from/to not min/max,
// and the ordered-pair gate.
//
// The pair is ORDERED: a LITERAL pair whose from sorts after its to is a
// decode error. Same-variant ISO-8601 strings compare lexicographically in
// chronological order, so strings.Compare (byte-wise, i.e. ordinal) is total
// here for every variant — no date parsing, no locale. Only a literal pair is
// checked; a bound pair's ordering is a runtime concern.
func decodeDateRangePair(raw any, path string) Value {
	ordered := func(from, to string) Value {
		if strings.Compare(from, to) > 0 {
			failExpecting(
				CodeWrongType,
				path,
				"date-range start '"+from+"' is after end '"+to+"' — a DateRange pair is ordered "+
					"(from <= to); ISO-8601 strings of one variant compare lexicographically, so swap the two values",
				dateRangePairShape,
			)
		}
		return Obj{Fields: map[string]Value{"from": Str(from), "to": Str(to)}}
	}
	if m, ok := raw.(map[string]any); ok {
		f, hasFrom := m["from"]
		t, hasTo := m["to"]
		if hasFrom && hasTo {
			// Argument evaluation is left-to-right, so a malformed `from`
			// surfaces before a malformed `to` — the F# host's order.
			return ordered(expectString(f, path+".from"), expectString(t, path+".to"))
		}
		fail(CodeWrongType, path, "expected an object with from and to ISO-8601 strings at "+path)
	}
	if arr, ok := raw.([]any); ok && len(arr) == 2 {
		return ordered(expectString(arr[0], path+"[0]"), expectString(arr[1], path+"[1]"))
	}
	fail(CodeWrongType, path, "expected a date-range pair ({from, to} object or [from, to] array) at "+path)
	return nil
}

// decodeDateRangeValue — the DateRange control's value slot, the decodeRangeValue
// twin. The canonical Static pair rides as the BARE {from, to} object (no
// envelope); the Static envelope and the two-element array are lenient forms of
// the same value; every other binding case decodes normally (its
// State/Filter/Selection defaultValue pair passes through structurally, exactly
// as Range does — the spec checks only a literal pair).
func decodeDateRangeValue(raw any, path string) Value {
	if m, ok := raw.(map[string]any); ok {
		if _, tagged := m["$type"]; !tagged {
			return decodeDateRangePair(raw, path)
		}
		obj := expectObject(raw, path)
		tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
		if tag == "Static" {
			v, ok := obj["value"]
			if !ok {
				fail(CodeMissingField, path+".value", "Static binding missing value")
			}
			return decodeDateRangePair(v, path+".value")
		}
		return decodeBindingWith(raw, path, decodeDateRangePair)
	}
	if _, ok := raw.([]any); ok {
		return decodeDateRangePair(raw, path)
	}
	return decodeBindingWith(raw, path, decodeDateRangePair)
}

// ── Actions ─────────────────────────────────────────────────────────────────

// decodeAction decodes a wire-survivable Action to its canonical shape. The
// payload positions (SetState.value / Notify.payload / AiTool.args) are
// structured JSON payload positions per rule 12 — null-strict at the null's
// exact path. Closure slots normalise to the "<closure>" sentinel; the
// Dispatch msg sentinel is wire-omitted (0.2.0).
func decodeAction(raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, actionCases, CodeUnknownDUCase)
	switch tag {
	case "Dispatch":
		return Obj{Tag: tag, Fields: map[string]Value{}}
	case "Call":
		// Field alias: url — the fetch prior for the same concept.
		endpoint := expectString(requireAliased(obj, "endpoint", path, "url"), path+".endpoint")
		fields := map[string]Value{"endpoint": Str(endpoint)}
		if _, ok := obj["onResult"]; ok {
			fields["onResult"] = Str(closureSentinel)
		}
		if raw, ok := obj["into"]; ok {
			intoPath := path + ".into"
			intoObj := expectObject(raw, intoPath)
			intoTag := dispatch(intoObj, intoPath, callTargetCases, CodeUnknownDUCase)
			if intoTag == "State" {
				key := expectString(require(intoObj, "key", intoPath), intoPath+".key")
				fields["into"] = Obj{Tag: "State", Fields: map[string]Value{"key": Str(key)}}
			} else {
				name := expectString(require(intoObj, "name", intoPath), intoPath+".name")
				fields["into"] = Obj{Tag: "Query", Fields: map[string]Value{"name": Str(name)}}
			}
		}
		return Obj{Tag: tag, Fields: fields}
	case "Notify":
		channel := expectString(require(obj, "channel", path), path+".channel")
		payload := fromJSONStrict(require(obj, "payload", path), path+".payload")
		return Obj{Tag: tag, Fields: map[string]Value{"channel": Str(channel), "payload": payload}}
	case "Navigate":
		// Field aliases: href / url / to — the HTML / router prior.
		route := expectString(requireAliased(obj, "route", path, "href", "url", "to"), path+".route")
		return Obj{Tag: tag, Fields: map[string]Value{"route": Str(route)}}
	case "SetState":
		key := expectString(require(obj, "key", path), path+".key")
		value := fromJSONStrict(require(obj, "value", path), path+".value")
		return Obj{Tag: tag, Fields: map[string]Value{"key": Str(key), "value": value}}
	case "AiTool":
		name := expectString(require(obj, "toolName", path), path+".toolName")
		args := fromJSONStrict(require(obj, "args", path), path+".args")
		return Obj{Tag: tag, Fields: map[string]Value{"args": args, "toolName": Str(name)}}
	case "Chain":
		arr := expectArray(require(obj, "ops", path), path+".ops")
		ops := make(Arr, len(arr))
		for i, item := range arr {
			ops[i] = decodeAction(item, path+".ops["+strconv.Itoa(i)+"]")
		}
		return Obj{Tag: tag, Fields: map[string]Value{"ops": ops}}
	case "CommitLocal":
		nodeID := expectString(require(obj, "nodeId", path), path+".nodeId")
		return Obj{Tag: tag, Fields: map[string]Value{"nodeId": Str(nodeID)}}
	case "WriteToClipboard":
		text := expectString(require(obj, "text", path), path+".text")
		return Obj{Tag: tag, Fields: map[string]Value{"text": Str(text)}}
	case "ReadFileBody":
		fileRef := expectString(require(obj, "fileRef", path), path+".fileRef")
		encoding := enumStr(require(obj, "encoding", path), path+".encoding",
			newCaseSet("Text", "Base64", "DataUrl"), "encoding", noAliases)
		return Obj{Tag: tag, Fields: map[string]Value{
			"encoding": Str(encoding),
			"fileRef":  Str(fileRef),
			"onRead":   Str(closureSentinel),
		}}
	default: // Invoke
		capabilityID := expectString(require(obj, "capabilityId", path), path+".capabilityId")
		args := decodeInvokeArgs(require(obj, "args", path), path+".args")
		return Obj{Tag: tag, Fields: map[string]Value{"args": args, "capabilityId": Str(capabilityID)}}
	}
}

// ── Spec builder ────────────────────────────────────────────────────────────

type fieldDecoder func(raw any, path string) Value

// fieldSpec drives simple table-driven decode (the TreeOp schemas): (field,
// required, decoder), walked in declaration order so which defect surfaces
// first is deterministic.
type fieldSpec struct {
	name     string
	required bool
	dec      fieldDecoder
}

// spec accumulates one kind object's decoded fields, tracking which input
// keys were consumed so the unconsumed remainder can be preserved
// structurally (§2 rule 2 tolerance) without resurrecting renamed aliases.
type spec struct {
	obj      map[string]any
	path     string
	fields   map[string]Value
	consumed map[string]bool
}

func newSpec(obj map[string]any, path string) *spec {
	return &spec{obj: obj, path: path, fields: make(map[string]Value, len(obj)), consumed: map[string]bool{"$type": true}}
}

// take reads the canonical key or an alias, marking whichever was read as
// consumed. The canonical name wins when both are present (the alias is still
// consumed so it never leaks into the output).
func (s *spec) take(key string, aliases ...string) (any, bool) {
	for _, a := range aliases {
		if _, ok := s.obj[a]; ok {
			s.consumed[a] = true
		}
	}
	if raw, ok := s.obj[key]; ok {
		s.consumed[key] = true
		return raw, true
	}
	for _, a := range aliases {
		if raw, ok := s.obj[a]; ok {
			return raw, true
		}
	}
	return nil, false
}

func (s *spec) req(key string, dec fieldDecoder, aliases ...string) Value {
	raw, ok := s.take(key, aliases...)
	if !ok {
		fail(CodeMissingField, s.path+"."+key, "missing required field '"+key+"'")
	}
	v := dec(raw, s.path+"."+key)
	s.fields[key] = v
	return v
}

func (s *spec) opt(key string, dec fieldDecoder, aliases ...string) {
	if raw, ok := s.take(key, aliases...); ok {
		s.fields[key] = dec(raw, s.path+"."+key)
	}
}

// optDrop decodes an optional field and DROPS it when it decodes to its
// identity default — the §3.6 omit-when-default seam, applied on the decode
// boundary so an explicit default normalises to the omitted canonical form.
func (s *spec) optDrop(key string, dec fieldDecoder, isDefault func(Value) bool, aliases ...string) {
	if raw, ok := s.take(key, aliases...); ok {
		v := dec(raw, s.path+"."+key)
		if !isDefault(v) {
			s.fields[key] = v
		}
	}
}

// sentinel normalises a present closure-slot key to the "<closure>" sentinel.
func (s *spec) sentinel(key string) {
	if _, ok := s.take(key); ok {
		s.fields[key] = Str(closureSentinel)
	}
}

func (s *spec) set(key string, v Value) { s.fields[key] = v }

// build finishes the kind object, preserving unconsumed keys structurally.
func (s *spec) build(tag string) Obj {
	for k, v := range s.obj {
		if !s.consumed[k] {
			s.fields[k] = fromJSON(v)
		}
	}
	return Obj{Tag: tag, Fields: s.fields}
}

// buildStrict finishes a fully-typed shape, dropping unconsumed keys.
func (s *spec) buildStrict(tag string) Obj {
	return Obj{Tag: tag, Fields: s.fields}
}

// ── Shared field decoders ───────────────────────────────────────────────────

func decodeCellFormat(raw any, path string) Value {
	obj := expectObject(raw, path)
	dispatch(obj, path, cellFormatCases, CodeUnknownDUCase)
	return fromJSON(raw)
}

func decodeColumnWidth(raw any, path string) Value {
	obj := expectObject(raw, path)
	dispatch(obj, path, columnWidthCases, CodeUnknownDUCase)
	return fromJSON(raw)
}

// decodeEmphasisEnum — the Emphasis style DU, with the §3.6 value aliases and
// the cross-vocabulary bool coercion (true ⇒ Loud, false ⇒ Normal — the
// same-name collision with the behavioural bool on Fact / LabelValueRow).
func decodeEmphasisEnum(raw any, path string) Value {
	if b, ok := raw.(bool); ok {
		if b {
			return Str("Loud")
		}
		return Str("Normal")
	}
	return Str(enumStr(raw, path, emphasisCases, "emphasis", emphasisAliases))
}

// decodeEmphasisFlag — the behavioural emphasis BOOL (Fact / LabelValueRow):
// booleans pass through (a Static envelope unwraps); the Emphasis enum and
// its aliases project one-to-one (Loud/Strong/Bold ⇒ true, the rest ⇒ false);
// any other string is a didactic WRONG_TYPE naming both vocabularies.
func decodeEmphasisFlag(raw any, path string) Value {
	if s, ok := raw.(string); ok {
		switch s {
		case "Loud", "Strong", "Bold":
			return Bool(true)
		case "Normal", "Quiet", "Subtle", "Muted":
			return Bool(false)
		}
		failExpecting(
			CodeWrongType,
			path,
			"expected a boolean at "+path+" — this `emphasis` is a BOOL; the Emphasis style enum (Quiet|Normal|Loud) lives on style/Metric.emphasis",
			"JSON boolean",
		)
	}
	return Bool(expectBool(raw, path))
}

// toneVariantNames — the legal ToneVariant names in DECLARATION order, in one
// place because two positions now teach them: a `tone` field and (Phase 750) a
// TonedPill tone-map value. A second inline copy is exactly how one of them comes
// to name six tones. Distinct from toneCases.hint(), which sorts.
const toneVariantNames = "Default | Subdued | Brand | Success | Warning | Critical | Info"

func decodeTone(raw any, path string) Value {
	return Str(enumStr(raw, path, toneCases, "tone", toneAliases))
}

func decodeWeight(raw any, path string) Value {
	return Str(enumStr(raw, path, weightCases, "weight", noAliases))
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

func enumDecoder(allowed caseSet, name string, aliases map[string]string) fieldDecoder {
	return func(raw any, path string) Value {
		return Str(enumStr(raw, path, allowed, name, aliases))
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

func decodeStringArrayField(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Str(expectString(item, path+"["+strconv.Itoa(i)+"]"))
	}
	return out
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

// ── Drawing (Phases 524 / 642) — closed Shape / CurveCommand DUs ────────────
//
// Geometry is static numbers (a Drawing is a resolved artefact); only DrawStyle
// carries Bindings. The Shape and CurveCommand DUs are closed + typed — an
// unrecognised discriminator is UNKNOWN_DU_CASE (the typed-surface default-deny).
// Array positions use [i] bracket paths to match the reference reject paths
// ($.kind.shapes[0].$type / $.kind.shapes[0].commands[0].$type).

func decodeViewBox(raw any, path string) Value {
	obj := expectObject(raw, path)
	return Obj{Fields: map[string]Value{
		"height": expectNumber(require(obj, "height", path), path+".height"),
		"minX":   expectNumber(require(obj, "minX", path), path+".minX"),
		"minY":   expectNumber(require(obj, "minY", path), path+".minY"),
		"width":  expectNumber(require(obj, "width", path), path+".width"),
	}}
}

func decodeDrawPoint(raw any, path string) Value {
	obj := expectObject(raw, path)
	return Obj{Fields: map[string]Value{
		"x": expectNumber(require(obj, "x", path), path+".x"),
		"y": expectNumber(require(obj, "y", path), path+".y"),
	}}
}

func decodeDrawPointArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeDrawPoint(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// decodeDrawStyle — every field optional, emitted only when present (byte-exact
// {} for an unstyled shape). fill/opacity/stroke/strokeWidth are Bindings; the
// text-only fields (Label) are bare enum / number / string; markId (Phase 642)
// is the keyed mark identity.
func decodeDrawStyle(raw any, path string) Value {
	obj := expectObject(raw, path)
	fields := map[string]Value{}
	for _, key := range []string{"fill", "stroke"} {
		if v, ok := obj[key]; ok {
			fields[key] = decodeBindingString(v, path+"."+key)
		}
	}
	for _, key := range []string{"opacity", "strokeWidth"} {
		if v, ok := obj[key]; ok {
			fields[key] = decodeBindingFloat(v, path+"."+key)
		}
	}
	if v, ok := obj["textAnchor"]; ok {
		fields["textAnchor"] = Str(enumStr(v, path+".textAnchor", textAnchorCases, "textAnchor", noAliases))
	}
	if v, ok := obj["fontSize"]; ok {
		fields["fontSize"] = expectNumber(v, path+".fontSize")
	}
	if v, ok := obj["emphasis"]; ok {
		fields["emphasis"] = decodeEmphasisEnum(v, path+".emphasis")
	}
	if v, ok := obj["fontFamily"]; ok {
		fields["fontFamily"] = Str(expectString(v, path+".fontFamily"))
	}
	if v, ok := obj["markId"]; ok {
		fields["markId"] = Str(expectString(v, path+".markId"))
	}
	return Obj{Fields: fields}
}

func decodeCurveCommand(raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, curveCommandCases, CodeUnknownDUCase)
	switch tag {
	case "MoveTo", "LineTo":
		return Obj{Tag: tag, Fields: map[string]Value{
			"to": decodeDrawPoint(require(obj, "to", path), path+".to"),
		}}
	case "CubicTo":
		return Obj{Tag: tag, Fields: map[string]Value{
			"control1": decodeDrawPoint(require(obj, "control1", path), path+".control1"),
			"control2": decodeDrawPoint(require(obj, "control2", path), path+".control2"),
			"to":       decodeDrawPoint(require(obj, "to", path), path+".to"),
		}}
	case "QuadraticTo":
		return Obj{Tag: tag, Fields: map[string]Value{
			"control": decodeDrawPoint(require(obj, "control", path), path+".control"),
			"to":      decodeDrawPoint(require(obj, "to", path), path+".to"),
		}}
	default: // Close
		return Obj{Tag: "Close", Fields: map[string]Value{}}
	}
}

func decodeCurveCommandArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeCurveCommand(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeShape(raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, shapeCases, CodeUnknownDUCase)
	var style Value = Obj{Fields: map[string]Value{}}
	if v, ok := obj["style"]; ok {
		style = decodeDrawStyle(v, path+".style")
	}
	switch tag {
	case "Group":
		return Obj{Tag: tag, Fields: map[string]Value{
			"children": decodeShapeArray(require(obj, "children", path), path+".children"),
			"style":    style,
		}}
	case "Rectangle":
		fields := map[string]Value{
			"height": expectNumber(require(obj, "height", path), path+".height"),
			"style":  style,
			"width":  expectNumber(require(obj, "width", path), path+".width"),
			"x":      expectNumber(require(obj, "x", path), path+".x"),
			"y":      expectNumber(require(obj, "y", path), path+".y"),
		}
		if v, ok := obj["cornerRadius"]; ok {
			fields["cornerRadius"] = expectNumber(v, path+".cornerRadius")
		}
		return Obj{Tag: tag, Fields: fields}
	case "Line":
		return Obj{Tag: tag, Fields: map[string]Value{
			"style": style,
			"x1":    expectNumber(require(obj, "x1", path), path+".x1"),
			"x2":    expectNumber(require(obj, "x2", path), path+".x2"),
			"y1":    expectNumber(require(obj, "y1", path), path+".y1"),
			"y2":    expectNumber(require(obj, "y2", path), path+".y2"),
		}}
	case "Polyline", "Polygon":
		return Obj{Tag: tag, Fields: map[string]Value{
			"points": decodeDrawPointArray(require(obj, "points", path), path+".points"),
			"style":  style,
		}}
	case "Curve":
		return Obj{Tag: tag, Fields: map[string]Value{
			"commands": decodeCurveCommandArray(require(obj, "commands", path), path+".commands"),
			"style":    style,
		}}
	case "Circle":
		return Obj{Tag: tag, Fields: map[string]Value{
			"cx":    expectNumber(require(obj, "cx", path), path+".cx"),
			"cy":    expectNumber(require(obj, "cy", path), path+".cy"),
			"r":     expectNumber(require(obj, "r", path), path+".r"),
			"style": style,
		}}
	case "Ellipse":
		return Obj{Tag: tag, Fields: map[string]Value{
			"cx":    expectNumber(require(obj, "cx", path), path+".cx"),
			"cy":    expectNumber(require(obj, "cy", path), path+".cy"),
			"rx":    expectNumber(require(obj, "rx", path), path+".rx"),
			"ry":    expectNumber(require(obj, "ry", path), path+".ry"),
			"style": style,
		}}
	default: // Label
		return Obj{Tag: tag, Fields: map[string]Value{
			"style": style,
			"text":  decodeTextSource(require(obj, "text", path), path+".text"),
			"x":     expectNumber(require(obj, "x", path), path+".x"),
			"y":     expectNumber(require(obj, "y", path), path+".y"),
		}}
	}
}

func decodeShapeArray(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeShape(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// ── Form fields (0.2.0 filters unification + 0.2.1 symmetric auto-bind) ─────

// controlAutoBind is the auto-bind context for a control's `value` slot: a
// filter chip auto-binds Filter(<chip name>), a form field auto-binds
// State(<field id>, <typed placeholder>) — and the ENCODER symmetrically omits
// a value that is exactly that auto-binding, so the canonical minimal control
// carries no value key at all.
type controlAutoBind struct {
	filterChip  bool
	formFieldID bool
	name        string
}

// placeholderFor is the slot's typed placeholder (the pinned
// control-value defaults: "" / 0 / false / null-choice / {min 0, max 0} /
// ISO-empty date / ISO-empty {from, to} pair), as its decoded wire shape.
func placeholderMatches(kindTag string, v Value) bool {
	switch kindTag {
	case "Text", "TextArea", "Date":
		return isStr(v, "")
	case "Number", "RangedNumber":
		return isNumericZero(v)
	case "Checkbox":
		return isBool(v, false)
	case "Choice", "SegmentedChoice":
		_, ok := v.(Null)
		return ok
	case "Range":
		o, ok := v.(Obj)
		if !ok || o.Tag != "" || len(o.Fields) != 2 {
			return false
		}
		return isNumericZero(o.Fields["min"]) && isNumericZero(o.Fields["max"])
	case "DateRange":
		// ISO-empty both ends — the pair analogue of Date's "" placeholder.
		o, ok := v.(Obj)
		if !ok || o.Tag != "" || len(o.Fields) != 2 {
			return false
		}
		return isStr(o.Fields["from"], "") && isStr(o.Fields["to"], "")
	}
	return false
}

// isAutoBoundValue reports whether a decoded value slot is exactly the
// context's auto-binding, in which case the canonical form omits it.
func isAutoBoundValue(ab controlAutoBind, kindTag string, v Value) bool {
	o, ok := v.(Obj)
	if !ok {
		return false
	}
	if ab.filterChip {
		return o.Tag == "Filter" && len(o.Fields) == 1 && isStr(o.Fields["name"], ab.name)
	}
	if ab.formFieldID {
		if o.Tag != "State" || len(o.Fields) != 2 || !isStr(o.Fields["key"], ab.name) {
			return false
		}
		d, ok := o.Fields["defaultValue"]
		return ok && placeholderMatches(kindTag, d)
	}
	return false
}

// decodeFormFieldKind decodes a FormFieldKind control (forms and, post-
// unification, filter chips — ONE control vocabulary). Handlers ride the wire
// only when present (a present value normalises to the "<closure>" sentinel);
// an absent `value` is the context's auto-binding (left absent — the canonical
// omitted form); a present value that IS exactly the auto-binding drops.
func decodeFormFieldKind(raw any, path string, ab controlAutoBind) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, formFieldCases, CodeUnknownDUCase)
	s := newSpec(obj, path)

	handler := func(name string) {
		s.sentinel(name)
	}
	valueSlot := func(dec fieldDecoder) {
		if raw, ok := s.take("value"); ok {
			v := dec(raw, path+".value")
			if !isAutoBoundValue(ab, tag, v) {
				s.set("value", v)
			}
		}
	}

	switch tag {
	case "Text":
		handler("onChange")
		valueSlot(decodeBindingString)
	case "Number":
		handler("onChange")
		valueSlot(decodeBindingFloat)
	case "Checkbox":
		handler("onToggle")
		valueSlot(decodeBindingBool)
	case "Choice":
		handler("onChange")
		s.req("options", decodeBindingSelectOptions)
		valueSlot(decodeBindingStringOpt)
	case "SegmentedChoice":
		handler("onChange")
		s.req("options", decodeBindingSelectOptions)
		valueSlot(decodeBindingStringOpt)
		// Omitted-when-default on the decode boundary: absent orientation
		// restores the language default Horizontal; the canonical wire always
		// carries it on SegmentedChoice.
		orientation := Value(Str("Horizontal"))
		if raw, ok := s.take("orientation"); ok {
			orientation = Str(enumStr(raw, path+".orientation", orientationCases, "orientation", orientationAliases))
		}
		s.set("orientation", orientation)
	case "TextArea":
		handler("onChange")
		s.req("rows", decodeInt)
		valueSlot(decodeBindingString)
	case "RangedNumber":
		handler("onChange")
		valueSlot(decodeBindingFloat)
		s.opt("min", expectNumberField)
		s.opt("max", expectNumberField)
		s.opt("step", expectNumberField)
	case "Range":
		handler("onChange")
		if raw, ok := s.take("value"); ok {
			v := decodeRangeValue(raw, path+".value")
			if !isAutoBoundValue(ab, tag, v) {
				s.set("value", v)
			}
		}
	case "Date":
		handler("onChange")
		valueSlot(decodeBindingString)
		s.req("variant", enumDecoder(dateVariantCases, "variant", noAliases))
		s.opt("min", decodeString)
		s.opt("max", decodeString)
		s.opt("step", expectNumberField)
	case "DateRange":
		// Range's pair mechanics with Date's value conventions: the value slot
		// is the bare ordered {from, to} pair, the scalars are Date's. Every
		// s.opt below is load-bearing — buildStrict DROPS unconsumed keys, so a
		// forgotten one fails byte-comparison rather than erroring.
		handler("onChange")
		if raw, ok := s.take("value"); ok {
			v := decodeDateRangeValue(raw, path+".value")
			if !isAutoBoundValue(ab, tag, v) {
				s.set("value", v)
			}
		}
		s.req("variant", enumDecoder(dateVariantCases, "variant", noAliases))
		s.opt("min", decodeString)
		s.opt("max", decodeString)
		s.opt("step", expectNumberField)
	}
	return s.buildStrict(tag)
}

func expectNumberField(raw any, path string) Value {
	return expectNumber(raw, path)
}

func decodeFormField(raw any, path string) Value {
	obj := expectObject(raw, path)
	s := newSpec(obj, path)
	// Field alias: name — the HTML-forms prior for the field's identity. The
	// id decodes first so the auto-bind context can use it.
	idV := s.req("id", decodeString, "name")
	id := string(idV.(Str))
	if raw, ok := s.take("kind"); ok {
		s.set("kind", decodeFormFieldKind(raw, path+".kind", controlAutoBind{formFieldID: true, name: id}))
	} else {
		fail(CodeMissingField, path+".kind", "missing required field 'kind'")
	}
	s.req("label", decodeTextSource)
	s.req("required", decodeBool)
	s.opt("help", decodeTextSource)
	return s.buildStrict("")
}

func decodeFormFields(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeFormField(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeFilterItem(raw any, path string) Value {
	obj := expectObject(raw, path)
	s := newSpec(obj, path)
	// The chip name decodes first so the Filter auto-bind can use it.
	nameV := s.req("name", decodeString)
	name := string(nameV.(Str))
	if raw, ok := s.take("kind"); ok {
		s.set("kind", decodeFormFieldKind(raw, path+".kind", controlAutoBind{filterChip: true, name: name}))
	} else {
		fail(CodeMissingField, path+".kind", "missing required field 'kind'")
	}
	s.req("label", decodeTextSource)
	return s.buildStrict("")
}

func decodeFilterItems(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeFilterItem(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// ── DataGrid columns ────────────────────────────────────────────────────────

// toneMapKeys — the tone-map field names a TonedPill cell accepts, canonical
// first. `map` is the shortest honest name for a value→tone dictionary and the
// least descriptive one.
var toneMapKeys = [...]string{"map", "toneMap", "tones"}

func hasAnyKey(obj map[string]any, keys []string) bool {
	for _, k := range keys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

// decodeToneMap — a TonedPill's `map`: a string-keyed object whose VALUES are
// ToneVariants. Routed through decodeTone per entry, so the §3.6 tone aliases
// work inside the map exactly as they do at a `tone` field; a second, private
// tone reader here is precisely how this position would come to accept a
// vocabulary the `tone` field does not.
//
// The refusal is RE-ISSUED rather than passed through. decodeTone reports
// "unrecognised tone '…'" with the sorted enum hint, which does not say WHICH
// map entry is wrong — and "one of your tones is wrong" is not an actionable
// report when the map has nine entries. The re-issue keeps the code, names the
// offending KEY and value in the terms the author wrote them, and teaches the
// seven legal names. A non-string value is a WRONG_TYPE from decodeTone and
// already reports at the right path, so it passes through untouched.
func decodeToneMap(raw any, path string) Value {
	obj := expectObject(raw, path)
	fields := make(map[string]Value, len(obj))
	for key, v := range obj {
		entryPath := path + "." + key
		fields[key] = reissueToneMapEntry(v, entryPath, key)
	}
	return Obj{Fields: fields}
}

func reissueToneMapEntry(raw any, entryPath, key string) (out Value) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		f, ok := r.(decodeFailure)
		if !ok || f.err.Code != CodeUnknownDUCase {
			panic(r)
		}
		got, _ := raw.(string)
		failExpecting(
			CodeUnknownDUCase,
			entryPath,
			"tone-map value '"+got+"' for '"+key+"' is not a ToneVariant",
			toneVariantNames,
		)
	}()
	return decodeTone(raw, entryPath)
}

// decodeTonedPill — the shared body of the canonical TonedPill case and the
// Pill-tagged §16 shorthand below. ONE reader, so the two spellings cannot drift
// apart in what they accept.
func decodeTonedPill(obj map[string]any, path string) Value {
	s := newSpec(obj, path)
	// `field` names the row property that is both the pill's label and the map key.
	s.req("field", decodeString)
	s.req("map", decodeToneMap, toneMapKeys[1:]...)
	// `default` is omitted-when-`Default` (the Phase 460 discipline); an absent key
	// restores the identity, and an aliased `Neutral` normalises to `Default` and
	// then omits — two rules composing, in that order.
	s.optDrop("default", decodeTone, isDefaultTone)
	return s.buildStrict("TonedPill")
}

// decodeCellKindErased — a column's cell kind. Closure-bearing cases normalise
// to their canonical sentinel shapes; TonedPill (Phase 750) is the one case with
// no closure in it, which is exactly why it survives the wire.
func decodeCellKindErased(raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, cellKindCases, CodeUnknownDUCase)
	// Lenient-ingest (WIRE_FORMAT.md §16, Phase 750): "pill" is the WORD for the
	// thing, so a declarative tone rule arrives tagged `Pill` more often than tagged
	// `TonedPill`. Before this phase those keys were accepted and DISCARDED — the
	// author's whole intent gone, silently, with no error to notice. Presence of a
	// tone map is the unambiguous tell: a closure `Pill` carries only labelFn/toneFn
	// and can never carry one.
	if tag == "Pill" && hasAnyKey(obj, toneMapKeys[:]) {
		return decodeTonedPill(obj, path)
	}
	switch tag {
	case "TonedPill":
		return decodeTonedPill(obj, path)
	case "Text", "Numeric", "Date":
		return Obj{Tag: tag, Fields: map[string]Value{}}
	case "Editable":
		return Obj{Tag: tag, Fields: map[string]Value{"onEdit": Str(closureSentinel)}}
	case "Checkbox":
		return Obj{Tag: tag, Fields: map[string]Value{"get": Str(closureSentinel), "onToggle": Str(closureSentinel)}}
	case "Button":
		label := decodeTextSource(require(obj, "label", path), path+".label")
		return Obj{Tag: tag, Fields: map[string]Value{"label": label, "onClick": Str(closureSentinel)}}
	case "ButtonGroup":
		arr := expectArray(require(obj, "buttons", path), path+".buttons")
		buttons := make(Arr, len(arr))
		for i, item := range arr {
			p := path + ".buttons[" + strconv.Itoa(i) + "]"
			bObj := expectObject(item, p)
			label := decodeTextSource(require(bObj, "label", p), p+".label")
			buttons[i] = Obj{Fields: map[string]Value{"label": label, "onClick": Str(closureSentinel)}}
		}
		return Obj{Tag: tag, Fields: map[string]Value{"buttons": buttons}}
	case "Link":
		return Obj{Tag: tag, Fields: map[string]Value{"hrefFn": Str(closureSentinel), "labelFn": Str(closureSentinel)}}
	case "Pill":
		return Obj{Tag: tag, Fields: map[string]Value{"labelFn": Str(closureSentinel), "toneFn": Str(closureSentinel)}}
	case "Progress":
		return Obj{Tag: tag, Fields: map[string]Value{"fractionFn": Str(closureSentinel), "labelFn": Str(closureSentinel)}}
	default: // Custom
		return Obj{Tag: tag, Fields: map[string]Value{"fn": Str(closureSentinel)}}
	}
}

func decodeColumnErased(raw any, path string) Value {
	obj := expectObject(raw, path)
	s := newSpec(obj, path)
	// Field aliases: `type` for `kind` (the universal JSON prior); `header` /
	// `title` for `label` (the react-table / antd prior). Phase 460 —
	// format/width omitted-when-default.
	s.req("kind", decodeCellKindErased, "type")
	s.req("label", decodeString, "header", "title")
	s.optDrop("format", decodeCellFormat, func(v Value) bool { return isTagOnly(v, "None") })
	s.optDrop("width", decodeColumnWidth, func(v Value) bool { return isTagOnly(v, "Auto") })
	s.sentinel("value")
	s.opt("field", decodeString)
	return s.buildStrict("")
}

func decodeColumns(raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeColumnErased(item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// decodeStaticRows — the {headers, rows} static-rows object of a read-only
// grid (also the shape the legacy Table decode-upgrade reads).
func decodeStaticRows(raw any, path string) Value {
	obj := expectObject(raw, path)
	headers := decodeTextSourceArray(require(obj, "headers", path), path+".headers")
	rowsArr := expectArray(require(obj, "rows", path), path+".rows")
	rows := make(Arr, len(rowsArr))
	for i, row := range rowsArr {
		rows[i] = decodeTextSourceArray(row, path+".rows["+strconv.Itoa(i)+"]")
	}
	return Obj{Fields: map[string]Value{"headers": headers, "rows": rows}}
}

// ── Per-kind typed decoders ─────────────────────────────────────────────────

func isDefaultTone(v Value) bool    { return isStr(v, "Default") }
func isStandardWeight(v Value) bool { return isStr(v, "Standard") }
func isNormalEmphasis(v Value) bool { return isStr(v, "Normal") }
func isFalseValue(v Value) bool     { return isBool(v, false) }
func isTrueValue(v Value) bool      { return isBool(v, true) }
func isNoneCellFormat(v Value) bool { return isTagOnly(v, "None") }

// kindBuilders holds the typed per-kind decoders. Kinds absent here (and from
// the dedicated Box / legacy handlers) decode structurally.
//
// Populated in init(): several builders recurse back through decodeNodeValue,
// which a package-level composite literal would report as a cycle.
var kindBuilders map[string]func(obj map[string]any, path string) Obj

// requiredKindFields mirrors the typed decoders' required sets for the
// validator's constructed-tree checks (see RequiredKindFields).
var requiredKindFields = map[string][]string{
	"Heading":       {"level", "text", "variant"},
	"Markdown":      {"text"},
	"Metric":        {"label", "value"},
	"Fact":          {"label", "value"},
	"LabelValueRow": {"label", "value"},
	"Badge":         {"label", "variant"},
	"Callout":       {"body"},
	"Progress":      {"fraction"},
	"Skeleton":      {"rows"},
	"Sparkline":     {"source"},
	"Map":           {"centreLatitude", "centreLongitude", "source", "zoom"},
	"Link":          {"download", "href", "label"},
	"Image":         {"alt", "src", "variant"},
	"List":          {"items", "ordered"},
	"Toast":         {"message", "open"},
	"CodeBlock":     {"code", "copyable", "highlightLines", "language", "lineNumbers"},
	"Math":          {"display", "source"},
	"Drawing":       {"shapes", "style", "viewBox"},
	"Select":        {"label", "source", "value"},
	"Modal":         {"children", "dismissable", "open"},
	"ScrollArea":    {"children", "orientation"},
	"Button":        {"label", "onClick", "variant"},
	"Form":          {"fields", "onSubmit", "submitLabel"},
	"Filters":       {"items"},
	"FileUpload":    {"accept", "label", "multiple"},
	"DataGrid":      {"columns", "source"},
	"Chart":         {"kind", "source", "xField", "yFields"},
	"Custom":        {"moduleId", "componentId"},
	"Switch":        {"cases", "default", "stateKey"},
	"Mount":         {"scopeId", "channel", "capabilities", "onBubble"},
	"Box":           {"children", "layout", "role"},
}

func init() {
	kindBuilders = map[string]func(obj map[string]any, path string) Obj{
		"Heading": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("level", decodeInt)
			s.req("text", decodeTextSource)
			s.req("variant", enumDecoder(headingVariantCases, "variant", headingVariantAliases))
			return s.build("Heading")
		},
		"Markdown": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("text", decodeTextSource)
			return s.build("Markdown")
		},
		// 0.2.0 rename law — the scalar displayed value is `value` (`source`
		// is reserved for collection feeds and is NOT accepted here — clean
		// break); `data` stays as the web-prior alias. Phase 460 — the
		// stylistic fields are omitted-when-default on both boundaries.
		"Metric": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("label", decodeTextSource)
			s.req("value", decodeBindingFloat, "data")
			s.optDrop("format", decodeCellFormat, isNoneCellFormat)
			s.optDrop("tone", decodeTone, isDefaultTone)
			s.optDrop("weight", decodeWeight, isStandardWeight)
			s.optDrop("emphasis", decodeEmphasisEnum, isNormalEmphasis)
			s.opt("trend", decodeBindingFloat)
			s.opt("trendFormat", decodeCellFormat)
			s.opt("icon", decodeString)
			s.opt("subtext", decodeTextSource)
			return s.build("Metric")
		},
		// A labeled TEXT fact: `value` is a TextSource, so static / Bound /
		// I18n values ride the label vocabulary. Only label + value required;
		// tone / emphasis omitted-when-default on BOTH boundaries.
		"Fact": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("label", decodeTextSource)
			s.req("value", decodeTextSource)
			s.optDrop("tone", decodeTone, isDefaultTone)
			s.optDrop("emphasis", decodeEmphasisFlag, isFalseValue)
			s.opt("help", decodeTextSource)
			s.opt("icon", decodeString)
			return s.build("Fact")
		},
		"LabelValueRow": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("label", decodeTextSource)
			s.req("value", decodeBindingFloat, "data")
			s.optDrop("format", decodeCellFormat, isNoneCellFormat)
			// 0.2.2 — the behavioural bool, omitted-when-false (aligning with
			// Fact's identical flag); the cross-vocabulary coercion accepts
			// the Emphasis enum + its aliases.
			s.optDrop("emphasis", decodeEmphasisFlag, isFalseValue)
			s.opt("help", decodeTextSource)
			return s.build("LabelValueRow")
		},
		"Badge": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("label", decodeTextSource)
			s.req("variant", enumDecoder(badgeVariantCases, "variant", badgeVariantAliases))
			return s.build("Badge")
		},
		"Callout": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("body", decodeTextSource)
			// 0.2.0 — dismissable omitted-when-false; Phase 460 — tone
			// omitted-when-default. `title` aliases `heading`.
			s.optDrop("dismissable", decodeBool, isFalseValue)
			s.optDrop("tone", decodeTone, isDefaultTone)
			s.opt("heading", decodeTextSource, "title")
			s.opt("icon", decodeString)
			return s.build("Callout")
		},
		"Progress": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("fraction", decodeBindingFloat)
			// 0.2.0 — indeterminate omitted-when-false.
			s.optDrop("indeterminate", decodeBool, isFalseValue)
			s.optDrop("tone", decodeTone, isDefaultTone)
			s.opt("label", decodeTextSource)
			s.opt("caveat", decodeTextSource)
			return s.build("Progress")
		},
		"Toast": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			// 0.2.0 — the one omit-when-TRUE: a toast is dismissable unless
			// said otherwise.
			s.optDrop("dismissable", decodeBool, isTrueValue)
			s.req("message", decodeTextSource)
			s.req("open", decodeBindingBool)
			s.optDrop("tone", decodeTone, isDefaultTone)
			return s.build("Toast")
		},
		"Skeleton": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("rows", decodeInt)
			return s.build("Skeleton")
		},
		// source is a §5 typed Static float-series position; `data` aliases.
		"Sparkline": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("source", decodeBindingFloatSeq, "data")
			return s.build("Sparkline")
		},
		// source is a §5 typed Static marker-list position; `data` / `markers`
		// alias.
		"Map": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("centreLatitude", expectNumberField)
			s.req("centreLongitude", expectNumberField)
			s.req("source", decodeBindingMarkerSeq, "data", "markers")
			s.req("zoom", decodeInt)
			s.sentinel("onMarkerClick")
			return s.build("Map")
		},
		"Link": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("download", decodeBool)
			s.req("href", decodeBindingString)
			s.req("label", decodeTextSource)
			s.opt("rel", decodeString)
			s.opt("target", decodeString)
			return s.build("Link")
		},
		"Image": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("alt", decodeTextSource)
			s.req("src", decodeBindingString)
			s.req("variant", enumDecoder(imageVariantCases, "variant", noAliases))
			return s.build("Image")
		},
		"List": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("items", decodeTextSourceArray)
			s.req("ordered", decodeBool)
			return s.build("List")
		},
		"CodeBlock": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("code", decodeString)
			s.req("copyable", decodeBool)
			s.req("highlightLines", decodeIntArray)
			s.req("language", decodeString)
			s.req("lineNumbers", decodeBool)
			return s.build("CodeBlock")
		},
		"Math": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("display", enumDecoder(mathDisplayCases, "display", noAliases))
			s.req("source", decodeString)
			return s.build("Math")
		},
		// Phases 524 / 642 — geometry static; the closed Shape / CurveCommand
		// DUs default-deny an unknown discriminator; DrawStyle carries the
		// bindings + the optional markId.
		"Drawing": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.opt("description", decodeTextSource)
			s.req("shapes", decodeShapeArray)
			s.req("style", decodeDrawStyle)
			s.opt("title", decodeTextSource)
			s.req("viewBox", decodeViewBox)
			return s.build("Drawing")
		},
		// The handler fields are OPTIONAL: omitted on the wire when the
		// control is declarative; present as the "<closure>" sentinel when
		// closure-authored. `options` / `data` alias `source`. Phase 291 —
		// multiple omitted-when-false; values omitted when absent.
		"Select": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("label", decodeTextSource)
			s.sentinel("onChange")
			s.req("source", decodeBindingSelectOptions, "options", "data")
			s.req("value", decodeBindingStringOpt)
			s.opt("disabled", decodeBindingBool)
			s.opt("placeholder", decodeTextSource)
			s.optDrop("multiple", decodeBool, isFalseValue)
			s.opt("values", decodeBindingStringList)
			s.sentinel("onChangeMulti")
			return s.build("Select")
		},
		// onDismiss is optional and — unlike the closure-sentinel handlers — a
		// genuine wire-survivable Action, so it decodes null-strict when
		// present. `title` aliases `heading`.
		"Modal": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("children", decodeChildren)
			s.req("dismissable", decodeBool)
			s.opt("onDismiss", decodeAction)
			s.req("open", decodeBindingBool)
			s.opt("heading", decodeTextSource, "title")
			return s.build("Modal")
		},
		"ScrollArea": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("children", decodeChildren)
			s.req("orientation", enumDecoder(scrollOrientationCases, "orientation", noAliases))
			s.opt("maxHeight", decodeInt)
			s.opt("maxWidth", decodeInt)
			return s.build("ScrollArea")
		},
		"Button": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("label", decodeTextSource)
			s.req("onClick", decodeAction)
			s.req("variant", enumDecoder(buttonVariantCases, "variant", buttonVariantAliases))
			s.opt("icon", decodeString)
			s.opt("disabled", decodeBindingBool)
			return s.build("Button")
		},
		"Form": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("fields", decodeFormFields)
			s.req("onSubmit", decodeAction)
			s.req("submitLabel", decodeTextSource)
			s.opt("disabled", decodeBindingBool)
			return s.build("Form")
		},
		"Filters": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("items", decodeFilterItems)
			return s.build("Filters")
		},
		"FileUpload": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("accept", decodeStringArrayField)
			s.req("label", decodeTextSource)
			s.req("multiple", decodeBool)
			// The select handler is a closure slot the canonical encoder
			// always emits as the sentinel.
			s.take("onSelect")
			s.set("onSelect", Str(closureSentinel))
			s.opt("disabled", decodeBindingBool)
			return s.build("FileUpload")
		},
		// 0.2.0 — editable omitted-when-false; `data` / `rows` alias `source`.
		"DataGrid": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("columns", decodeColumns)
			s.optDrop("editable", decodeBool, isFalseValue)
			s.req("source", decodeBindingObjSeq, "data", "rows")
			s.sentinel("onRowClick")
			s.sentinel("rowKey")
			s.opt("rowKeyField", decodeString)
			s.opt("staticRows", decodeStaticRows)
			return s.build("DataGrid")
		},
		// `stacked` is carried on the wire; a legacy wire predating the field
		// decodes to (and re-encodes with) the default false.
		"Chart": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("kind", enumDecoder(chartKindCases, "kind", noAliases))
			s.req("source", decodeBindingObjSeq, "data")
			stacked := Value(Bool(false))
			if raw, ok := s.take("stacked"); ok {
				stacked = decodeBool(raw, path+".stacked")
			}
			s.set("stacked", stacked)
			s.req("xField", decodeString)
			s.req("yFields", decodeStringArrayField)
			s.opt("title", decodeTextSource)
			s.sentinel("onPointClick")
			return s.build("Chart")
		},
		"Custom": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("moduleId", decodeString)
			s.req("componentId", decodeString)
			s.opt("props", decodeJSONValue)
			s.opt("contentHash", decodeJSONValue)
			s.opt("exposedNodeIds", decodeJSONValue)
			return s.build("Custom")
		},
		// State-bound conditional child. Duplicate match values are NOT a
		// decode error (first-match-wins; the validator flags them).
		"Switch": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("cases", decodeSwitchCases)
			s.req("default", decodeSingleNode)
			s.req("stateKey", decodeString)
			return s.build("Switch")
		},
		// Isolation/embedding boundary (§4o). inputs passes through WITHOUT
		// null-strictness — it embeds whole node trees whose Binding.Static
		// values are §5 opaque seams.
		"Mount": func(obj map[string]any, path string) Obj {
			s := newSpec(obj, path)
			s.req("scopeId", decodeString)
			s.req("channel", decodeGuestChannel)
			s.req("capabilities", decodeJSONValue)
			s.req("onBubble", decodeString)
			s.opt("inputs", decodeJSONPassthrough)
			return s.build("Mount")
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
			"direction": Str(enumStr(require(obj, "direction", path), path+".direction", orientationCases, "direction", orientationAliases)),
			"wrap":      Bool(expectBool(require(obj, "wrap", path), path+".wrap")),
		}
		if raw, ok := obj["gap"]; ok {
			fields["gap"] = Int(expectInt(raw, path+".gap"))
		}
		return Obj{Tag: "Flex", Fields: fields}
	case "Grid":
		_, hasCols := obj["cols"]
		_, hasColumns := obj["columns"]
		_, hasTemplate := obj["templateColumns"]
		if !hasCols && !hasColumns && !hasTemplate {
			// §3.6 shape coercion: a Grid with NO column spec is the CSS
			// auto-grid prior — the language's Auto (responsive auto-tile).
			// Accept-and-canonicalise.
			return Obj{Tag: "Auto", Fields: map[string]Value{}}
		}
		var cols Value
		if !hasCols && !hasColumns {
			// templateColumns present ⇒ cols is documented-ignored, so an
			// absent cols defaults to 1 rather than MISSING_FIELD.
			cols = Int(1)
		} else {
			// `columns` aliases `cols`.
			cols = Int(expectInt(requireAliased(obj, "cols", path, "columns"), path+".cols"))
		}
		fields := map[string]Value{"cols": cols}
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
	role := enumStr(require(obj, "role", path), path+".role", boxRoleCases, "role", noAliases)
	layout := decodeBoxLayout(require(obj, "layout", path), path+".layout")
	fields := map[string]Value{
		"children": children,
		"layout":   layout,
		"role":     Str(role),
	}
	// `title` aliases `heading` (the web prior for a card's caption).
	if raw, ok := optAliased(obj, "heading", "title"); ok {
		fields["heading"] = decodeTextSource(raw, path+".heading")
	}
	return Obj{Tag: "Box", Fields: fields}
}

// ── Kind / style / state / node envelope ────────────────────────────────────

func decodeKind(raw any, path string) Obj {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, knownKinds, CodeWrongNodeKind)
	switch {
	case tag == "Box":
		return decodeBox(obj, path)
	}
	if builder, ok := kindBuilders[tag]; ok {
		return builder(obj, path)
	}
	// Recognised kind without a typed decoder yet — accept structurally.
	fields := make(map[string]Value, len(obj))
	for k, v := range obj {
		if k != "$type" {
			fields[k] = fromJSON(v)
		}
	}
	return Obj{Tag: tag, Fields: fields}
}

// decodeStyle — SemanticStyle. Every field is omitted-when-default on BOTH
// boundaries (Phase 147 role/voice; Phase 460 emphasis/tone/weight): an
// absent field restores the identity default, and a present explicit default
// normalises to the omitted form.
func decodeStyle(raw any, path string) Obj {
	obj := expectObject(raw, path)
	fields := map[string]Value{}
	if raw, ok := obj["emphasis"]; ok {
		if v := decodeEmphasisEnum(raw, path+".emphasis"); !isStr(v, "Normal") {
			fields["emphasis"] = v
		}
	}
	if raw, ok := obj["tone"]; ok {
		if v := decodeTone(raw, path+".tone"); !isStr(v, "Default") {
			fields["tone"] = v
		}
	}
	if raw, ok := obj["weight"]; ok {
		if v := decodeWeight(raw, path+".weight"); !isStr(v, "Standard") {
			fields["weight"] = v
		}
	}
	if raw, ok := obj["role"]; ok {
		if v := Str(enumStr(raw, path+".role", styleRoleCases, "role", noAliases)); !isStr(v, "None") {
			fields["role"] = v
		}
	}
	if raw, ok := obj["voice"]; ok {
		if v := Str(enumStr(raw, path+".voice", fontVoiceCases, "voice", noAliases)); !isStr(v, "Default") {
			fields["voice"] = v
		}
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
	if _, ok := obj["onError"]; ok {
		// The rendering closure is opaque — the canonical form is the sentinel.
		fields["onError"] = Str(closureSentinel)
	}
	return Obj{Fields: fields}
}

// decodeNodeValue decodes a node envelope, applying the §8 NodeId invariants.
// `state` and `style` are omitted when empty / all-default (§3.1) — a decoded
// empty/default section normalises to the omitted form.
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
		if state := decodeState(raw, path+".state"); len(state.Fields) > 0 {
			extras["state"] = state
		}
	}
	if raw, ok := obj["style"]; ok {
		if style := decodeStyle(raw, path+".style"); len(style.Fields) > 0 {
			extras["style"] = style
		}
	}
	if raw, ok := obj["accessibility"]; ok {
		extras["accessibility"] = fromJSON(raw)
	}
	return Node{ID: id, Kind: kind, Extras: extras}
}
