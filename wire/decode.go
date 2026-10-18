package wire

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire/idl"
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
	// §21 syntactic bound FIRST — before encoding/json, whose own nesting cap
	// would otherwise report a too-deep document as a syntax error (rule 2
	// forbids that classification for a well-formed document).
	checkTextDepth(text)

	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		fail(CodeInvalidJSON, "$", "input is not syntactically valid JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		fail(CodeInvalidJSON, "$", "input carries content after the JSON document")
	}
	checkNoRepeatedMembers(text)
	return raw
}

// checkNoRepeatedMembers refuses an object carrying the same member twice
// (WIRE_FORMAT.md §20.2 row 1).
//
// This is one of the two rows that change what a document MEANS rather than
// whether it is accepted, and every other check here is blind to it.
// encoding/json unmarshals into a map, so the LAST occurrence wins and the
// earlier ones are simply gone — while the reference host's parser kept the
// FIRST. Two hosts therefore read different trees from identical bytes with no
// error raised anywhere, which is exactly the failure §20 exists to close.
//
// It is a second pass over an already-validated token stream rather than a hook:
// the standard library offers no per-member callback, and re-implementing its
// parser to get one would trade a bounded cost at a trust boundary for a
// permanent divergence risk. Duplicate DETECTION is a decision about this wire
// format; JSON PARSING is not, and this host should keep exactly one parser.
func checkNoRepeatedMembers(text string) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()

	// One frame per open container. `seen` is nil for an array — an array has no
	// members to repeat — and `expectKey` alternates within an object.
	type frame struct {
		seen      map[string]struct{}
		expectKey bool
	}
	var stack []frame

	for {
		tok, err := dec.Token()
		if err != nil {
			// The document already parsed cleanly above, so the only error
			// reachable here is end-of-input.
			return
		}

		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.seen != nil && top.expectKey {
				if key, ok := tok.(string); ok {
					if _, repeated := top.seen[key]; repeated {
						failExpecting(
							CodeInvalidJSON,
							"$",
							"the object member '"+key+"' appears more than once (WIRE_FORMAT.md §20.2 row 1): "+
								"hosts disagreed on which occurrence wins, so the same bytes meant different trees",
							"each member named at most once within one object",
						)
					}
					top.seen[key] = struct{}{}
					top.expectKey = false
					continue
				}
			}
		}

		if delim, isDelim := tok.(json.Delim); isDelim {
			switch delim {
			case '{':
				stack = append(stack, frame{seen: map[string]struct{}{}, expectKey: true})
				continue
			case '[':
				stack = append(stack, frame{})
				continue
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
		}

		// A value has just been consumed, or a container just closed, so the next
		// token in the enclosing object is a key again.
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.seen != nil {
				top.expectKey = true
			}
		}
	}
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

// The width of every typed integer slot this format declares (§7.1). Not §2
// rule 5's ±(2⁵³−1), which is a different bound answering a different question:
// that one is where integer IDENTITY stops in an untyped payload position, this
// one is the width of the SLOT, and a value the slot cannot hold has nowhere to
// land.
const (
	intSlotMin = -2147483648
	intSlotMax = 2147483647
)

// expectInt is the §7.1 integer-slot accept set: a JSON number that is finite,
// has no fractional part, and lies within the signed 32-bit range.
//
// `2.0` decodes as 2 — a document whose intent is unambiguous should not be
// refused for its spelling, and this host refused it. `2.5` is a WRONG_TYPE
// rather than a truncation to 2, which is the other half of the same
// reconciliation: truncating discards a value at a slot the author chose to type
// as an integer. `1e10` is a WRONG_TYPE too, and that row was measured — the
// same bytes became Int32.MinValue on one runtime and 1410065408 on another, an
// implementation-defined cast where the format needs an answer.
func expectInt(raw any, path string) int64 {
	n, ok := unwrapStaticAny(raw).(json.Number)
	if !ok {
		fail(CodeWrongType, path, "expected an integer at "+path)
	}
	lit := string(n)
	if isIntegerLiteral(lit) {
		i, err := strconv.ParseInt(lit, 10, 64)
		if err != nil {
			// Beyond int64 — necessarily beyond int32 as well.
			failIntSlotRange(path)
		}
		if i < intSlotMin || i > intSlotMax {
			failIntSlotRange(path)
		}
		return i
	}
	// A number written with a fraction or an exponent. It is admissible exactly
	// when its VALUE is an integer the slot can hold.
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		failExpecting(
			CodeWrongType,
			path,
			"expected a finite integer at "+path+" — an integer slot has no non-finite form",
			"a finite integral number within the signed 32-bit range",
		)
	}
	if f != math.Trunc(f) {
		failExpecting(
			CodeWrongType,
			path,
			"expected an integer at "+path+", but the value has a fractional part — an integer slot "+
				"holds no fraction, and truncating it would discard a value the author typed",
			"a finite integral number within the signed 32-bit range",
		)
	}
	if f < intSlotMin || f > intSlotMax {
		failIntSlotRange(path)
	}
	return int64(f)
}

func failIntSlotRange(path string) {
	failExpecting(
		CodeWrongType,
		path,
		"the value at "+path+" is outside the signed 32-bit range a typed integer slot can hold ("+
			itoa(intSlotMin)+" … "+itoa(intSlotMax)+")",
		"a finite integral number within the signed 32-bit range",
	)
}

// nonFiniteSentinel maps the three §5/§7 quoted sentinels to their float value.
// §7 is symmetric with §5: a float slot accepts BOTH a JSON number and the
// quoted spelling this host itself emits for a non-finite. Without it a document
// this host encodes is one this host cannot read back — `decode → encode →
// decode` does not close on any non-finite number, and a peer host's canonical
// output is undecodable here.
//
// It returns the float rather than the sentinel string so decode is idempotent
// at the TREE level and not merely at the byte level: the bare overflowing
// literal `-1e999` already decodes to Float(-Inf) via numberValue, so answering
// a re-decode of its own canonical form with Str would hand a consumer a float
// the first time and a string the second.
func nonFiniteSentinel(s string) (float64, bool) {
	switch s {
	case "NaN":
		return math.NaN(), true
	case "Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	return 0, false
}

// expectNumber accepts any JSON number (an integer or float literal), or one of
// the three §7 non-finite sentinel strings. It is the FLOAT-slot choke point —
// integer slots go through expectInt, which gates on isIntegerLiteral and so is
// unreachable from here: a sentinel at an integer slot stays a WRONG_TYPE.
func expectNumber(w *walkState, raw any, path string) Value {
	raw = unwrapStaticAny(raw)
	if n, ok := raw.(json.Number); ok {
		return numberValue(n)
	}
	if s, ok := raw.(string); ok {
		if f, isSentinel := nonFiniteSentinel(s); isSentinel {
			return Float(f)
		}
	}
	fail(CodeWrongType, path, "expected a number at "+path)
	return nil
}

func isIntegerLiteral(lit string) bool {
	return !strings.ContainsAny(lit, ".eE")
}

// PayloadIntMax is where INTEGER IDENTITY stops in an untyped rule-12 payload
// position (§2 rule 5) — the range in which every conformant host's number
// representation agrees exactly.
//
// It is a boundary of the FORMAT, not of this host. Go can hold an int64 and
// Python an arbitrary-precision integer, while three hosts route every number
// through a double; beyond ±(2⁵³−1) one document therefore canonicalises to
// different bytes, and to different hash-chain and teleport digests, depending
// on which host read it. A conformant encoder MUST NOT emit an integer token
// outside this range — carry a uint64 column or a 19-digit identifier as a
// string — and a conformant decoder MUST take one it receives as a double and
// accept the rounding, which is what the clause below does.
const PayloadIntMax = 9007199254740991

// numberValue keeps the wire's int-vs-float distinction: an integer literal
// within ±(2⁵³−1) stays Int (plain-decimal re-encode); anything with a decimal
// point or exponent, or an integer token outside that range, is Float (canonical
// layout).
func numberValue(n json.Number) Value {
	lit := string(n)
	if isIntegerLiteral(lit) {
		if i, err := strconv.ParseInt(lit, 10, 64); err == nil {
			if i >= -PayloadIntMax && i <= PayloadIntMax {
				return Int(i)
			}
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
func fromJSONStrict(w *walkState, raw any, path string) Value {
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
			arr[i] = fromJSONStrict(w, item, path+"["+strconv.Itoa(i)+"]")
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
			fields[k] = fromJSONStrict(w, t[k], path+"."+k)
		}
		return Obj{Tag: tag, Fields: fields}
	}
	fail(CodeWrongType, path, "value is not a JSON-shaped value")
	return nil
}

// ── Bare-string enum vocabularies (WIRE_FORMAT.md §3.5) ─────────────────────
//
// Every set below is a generated idl.json row (wire/idl), so a case added to
// the IDL reaches this decoder by regeneration alone. The two sets carrying a
// spelling the IDL does not declare say so where they are built: Binding's
// lenient "Bound" unwrap and CellFormat's pre-rename "Date" alias.

var (
	toneCases           = newCaseSet(idl.EnumToneVariant.Cases...)
	weightCases         = newCaseSet(idl.EnumStyleWeight.Cases...)
	emphasisCases       = newCaseSet(idl.EnumEmphasis.Cases...)
	orientationCases    = newCaseSet(idl.EnumOrientation.Cases...)
	badgeVariantCases   = newCaseSet(idl.EnumBadgeVariant.Cases...)
	buttonVariantCases  = newCaseSet(idl.EnumButtonVariant.Cases...)
	headingVariantCases = newCaseSet(idl.EnumHeadingVariant.Cases...)
	// Phase 1811 — DateTimeVariant (was DateVariant); the three cases did not move.
	dateTimeVariantCases = newCaseSet(idl.EnumDateTimeVariant.Cases...)
	styleRoleCases       = newCaseSet(idl.EnumStyleRole.Cases...)
	fontVoiceCases       = newCaseSet(idl.EnumFontVoice.Cases...)
	liveRegionCases      = newCaseSet(idl.EnumLiveRegionKind.Cases...)
	imageVariantCases    = newCaseSet(idl.EnumImageVariant.Cases...)
	// Phase 1077 — §3.6.2. Three closed TOKEN vocabularies, never CSS values:
	// `aspectRatio` names one of four ratios and can never carry "16 / 9",
	// "16:9" or 1.7778, which is the free-form escape the tokens exist to
	// close. Each is omitted-when-default on BOTH boundaries, so a pre-phase
	// document decodes to today's behaviour and re-encodes to the bytes it
	// already had.
	imageFitCases     = newCaseSet(idl.EnumImageFit.Cases...)
	imageAspectCases  = newCaseSet(idl.EnumImageAspect.Cases...)
	imageLoadingCases = newCaseSet(idl.EnumImageLoading.Cases...)
	// Phase 1110 — §3.6.6's `TrackKind`, a BARE enum (§3.5), so an unrecognised
	// token reports at the array element's own path with no `.$type` suffix.
	// Closed at four: `metadata` is deliberately NOT admitted, because its cues
	// are rendered by no user agent and read only by script, so a declarative
	// document naming it would state an intent no conformant host could honour.
	trackKindCases = newCaseSet(idl.EnumTrackKind.Cases...)
	// Phase 1111 — §3.6.8's `EmbedPermission`, likewise a BARE enum. Closed at
	// four, and the two omissions are DESIGN rather than reservation: a
	// top-level-navigation relaxation is the drive-by redirect, and a downloads
	// relaxation puts a file-save prompt in a third party's hands. A decoder
	// MUST NOT silently drop an unrecognised token — that would turn a document
	// asking for something this vocabulary cannot name into one asking for
	// LESS, which reads as success.
	embedPermissionCases = newCaseSet(idl.EnumEmbedPermission.Cases...)
	// Phase 1076 — §3.6.6. ONE kind, two variants: everything a video surface
	// and an audio surface share is stated once on the record, and only the
	// slots that genuinely differ live in the variant. Discriminated by
	// `$type`, so an unknown case reports at `$.kind.kind.$type` — the
	// Binding / TextSource position, not the bare-enum one (§6).
	mediaKindCases         = newCaseSet(idl.UnionMediaKind.CaseTags()...)
	linkProtectionCases    = newCaseSet(idl.EnumLinkProtection.Cases...) // Phase 812 — anti-scraper render strategy
	scrollOrientationCases = newCaseSet(idl.EnumScrollOrientation.Cases...)
	mathDisplayCases       = newCaseSet(idl.EnumMathDisplay.Cases...)
	chartKindCases         = newCaseSet(idl.EnumChartKind.Cases...)
	boxRoleCases           = newCaseSet(idl.EnumBoxRole.Cases...)
	boxLayoutCases         = newCaseSet(idl.UnionBoxLayout.CaseTags()...)
	channelDirectionCases  = newCaseSet(idl.EnumChannelDirection.Cases...)
	textAnchorCases        = newCaseSet(idl.EnumTextAnchor.Cases...)
	// Phase 801 — the closed two-value sort direction on staticRows.defaultSort.
	// Lower-case on the wire, unlike most enums here.
	sortDirectionCases = newCaseSet(idl.EnumSortDirection.Cases...)
	// Phase 867 — Metric.trendPolarity: which way the measured quantity IMPROVES.
	// TWO cases, and the absence of a third is deliberate: `Neutral` is RESERVED
	// and is not admitted, which is the whole reason the slot is an enum rather
	// than an `inverted: bool` — admitting it later is then a bare-string
	// addition here, not a type replacement across every host.
	trendPolarityCases = newCaseSet(idl.EnumTrendPolarity.Cases...)
	// Phase 1472 — §3.1's `TextDirection`, the ONE `SemanticStyle` member that
	// is not presentational: it declares one value's own base direction, which
	// the bidirectional algorithm otherwise infers from the value's first
	// strong character and gets wrong for identifiers. Lower-case, spelled in
	// the values the isolation is ultimately expressed in (the `liveRegion`
	// posture). `"auto"` is the identity and omits at it, so a document that
	// declares nothing is byte-identical to what it was before the member
	// existed. An unrecognised token is REFUSED, never coerced to the default —
	// a misspelled `"rtl"` would otherwise render as reordered digits with
	// nothing said anywhere, which is the failure the member exists to prevent.
	textDirectionCases = newCaseSet(idl.EnumTextDirection.Cases...)
	// Phase 1119 — §3.6.11's `Modality`, a closed PAIR. A sheet, a drawer and a
	// menu are all PRESENTATIONS of one of the two rather than a third, so the
	// answer to an unknown token is to say which of the two was meant, not to
	// widen the enum. `Modal` is the identity and omits at it.
	modalityCases = newCaseSet(idl.EnumModalityKind.Cases...)
	// Phase 1536 — which browsing context an `Action.Navigate` lands in. A bare
	// enum (§3.5), omitted at `Self`. Two cases and NO lenient spelling: HTML's
	// `_self` / `_blank` / `_parent` / `_top` are not accepted as aliases, because
	// two of them are frame-busting gestures a hosted tree must not be able to ask
	// for and accepting the two harmless ones would teach an emitter that the HTML
	// vocabulary is the one in force here.
	navigateTargetCases = newCaseSet(idl.EnumNavigateTarget.Cases...)
	// Phase 1116 — §3.6.10's `CaptureSource`, a closed pair. `Screen` is
	// deliberately absent: the HTML capture attribute cannot ask for a display
	// at all, and a screen capture is a standing grant reaching every window the
	// reader has open rather than a third device behind the same picker
	// permission. A later admission would be an ADDITION, never a re-meaning.
	captureSourceCases = newCaseSet(idl.EnumCaptureSource.Cases...)

	// Drawing (Phase 524) — the closed Shape / CurveCommand DUs. An unrecognised
	// discriminator is UNKNOWN_DU_CASE (the typed-surface default-deny).
	shapeCases        = newCaseSet(idl.UnionShape.CaseTags()...)
	curveCommandCases = newCaseSet(idl.UnionCurveCommand.CaseTags()...)

	textSourceCases = newCaseSet(idl.UnionTextSource.CaseTags()...)
	// The binding vocabulary includes the compute-layer cases (Transform /
	// Invoke) plus "Bound" — the TextSource-wrapper convention transferred to
	// a bare-Binding slot, accepted leniently and unwrapped in place.
	bindingCases = newCaseSet(append(idl.UnionBinding.CaseTags(), "Bound")...)
	// Phase 1811 — DateTime is canonical; Date is its section-16 lenient alias (the
	// pre-rename spelling), admitted here and normalised in decodeCellFormat.
	cellFormatCases = newCaseSet(append(idl.UnionCellFormat.CaseTags(), "Date")...)
	// Phase 819 — the Duration / RelativeTime format enums (shared by the
	// CellFormat vocabulary; the binding-level Format object still decodes
	// structurally).
	durationUnitCases     = newCaseSet(idl.EnumDurationUnit.Cases...)
	durationStyleCases    = newCaseSet(idl.EnumDurationStyle.Cases...)
	relativeTimeUnitCases = newCaseSet(idl.EnumRelativeTimeUnit.Cases...)
	// Phase 1533 — the resolution a `Binding.Now` declares for the
	// host-furnished instant. A strict SUBSET of relativeTimeUnitCases: `Week`
	// / `Month` / `Year` are refused rather than quietly accepted, because this
	// is a truncation of a calendar instant and those three have no truncation
	// every host agrees on (which weekday starts a week; which calendar).
	timeGrainCases = newCaseSet(idl.EnumTimeGrain.Cases...)
	// Phase 821 — the standalone Icon display kind's size enum.
	iconSizeCases    = newCaseSet(idl.EnumIconSize.Cases...)
	columnWidthCases = newCaseSet(idl.UnionColumnWidth.CaseTags()...)
	cellKindCases    = newCaseSet(idl.UnionCellKindErased.CaseTags()...)
	// Phase 1113 — `Combobox` is the searchable form of `Choice`: a LARGE,
	// searchable or asynchronous option set, or one that admits a value not on
	// the list. Its `value` / `onChange` contract IS Choice's, deliberately, so
	// a document migrating between the two changes its `$type` and nothing else.
	//
	// Phase 1121's `Tokens` and Phase 1130's `Rating` / `Color` join them.
	formFieldCases    = newCaseSet(idl.UnionFormFieldKind.CaseTags()...)
	flushTriggerCases = newCaseSet(idl.UnionLocalFlushTrigger.CaseTags()...)
	actionCases       = newCaseSet(idl.UnionAction.CaseTags()...)
	callTargetCases   = newCaseSet(idl.UnionCallResultTarget.CaseTags()...)
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

// knownKinds is every node-kind discriminator idl.json declares
// (WIRE_FORMAT.md §3.2). A kind not in this set is WRONG_NODE_KIND; every kind
// in it has a typed decoder arm (kindarms_gen.go).
var knownKinds = newCaseSet(idl.KindTags()...)

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
func decodeTextSource(w *walkState, raw any, path string) Value {
	if s, ok := raw.(string); ok {
		return Str(s)
	}
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, textSourceCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionTextSource, tag)
	switch tag {
	case "Literal":
		// The transparent case: its canonical form is the bare text itself.
		if raw, ok := s.has("text"); ok {
			return Str(expectString(raw, path+".text"))
		}
	case "I18n":
		s.field("key", decodeString)
		s.field("args", decodeI18nArgs)
	case "Bound":
		s.field("binding", decodeBinding)
	default:
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

// decodeI18nArgs decodes a TextSource.I18n argument bag, discriminated BY
// INSPECTION (WIRE_FORMAT.md §5, Phase 1661): an object carrying a $type member
// is a BINDING and decodes as one, and every other JSON value is the LITERAL
// argument — rule-12 strict, so a null rejects at the null's own path — and is
// emitted BARE, which is why every literal-args document ever emitted is
// byte-identical across the widening.
//
// The whole bag used to take the structural fromJSONStrict pass-through, which
// validated nothing: a bound argument naming an unrecognised case, or a known
// case missing a required member, went unraised here while the typed hosts
// refused the same document, and a tagged Static was re-emitted tagged where
// they collapse it. Both are divergences the widening would have manufactured.
//
// The Static arm is read here rather than through decodeBinding so the two
// spellings of a literal agree: a missing or null value is Phase 677's
// structural absence and stays {"$type":"Static"} (absence has no bare
// spelling), and a PRESENT value collapses to the bare form under the same
// strict decoder the bare spelling takes.
func decodeI18nArgs(w *walkState, raw any, path string) Value {
	argsObj := expectObject(raw, path)
	out := make(map[string]Value, len(argsObj))
	for k, v := range argsObj {
		argPath := path + "." + k
		if m, isObj := v.(map[string]any); isObj {
			if tag, hasTag := m["$type"]; hasTag {
				if s, isStr := tag.(string); isStr && s == "Static" {
					inner, present := m["value"]
					if !present || inner == nil {
						out[k] = Obj{Tag: "Static", Fields: map[string]Value{}}
					} else {
						out[k] = fromJSONStrict(w, inner, argPath+".value")
					}
					continue
				}
				out[k] = decodeBinding(w, v, argPath)
				continue
			}
		}
		out[k] = fromJSONStrict(w, v, argPath)
	}
	return Obj{Fields: out}
}

// ── Bindings ────────────────────────────────────────────────────────────────

// staticParser decodes a Binding.Static payload (and the bare-scalar/array
// shape coercion) for one slot's typed shape.
type staticParser func(w *walkState, raw any, path string) Value

// objStatic is the obj-erased default: structural, null-lenient (§5).
func objStatic(w *walkState, raw any, path string) Value {
	return fromJSON(raw)
}

// floatStatic — a JSON number, the three non-finite sentinels, or the Static
// envelope around either (the inverse-confusion unwrap). Delegates to
// expectNumber so a Binding's float payload and a typed float slot answer the
// same way: this slot already round-tripped before, but it did so by keeping the
// sentinel as a STRING, which would leave a Static float scalar carrying Str
// where a Static float SEQUENCE element (floatSeqStatic, which walks
// expectNumber) carries Float — an inconsistency inside one family.
func floatStatic(w *walkState, raw any, path string) Value {
	return expectNumber(w, raw, path)
}

// intStatic — an integer literal, or the Static envelope around one. The
// deliberate asymmetry with floatStatic above: expectInt gates on
// isIntegerLiteral and never reaches nonFiniteSentinel, so an integer slot has no
// non-finite form at all. §7 confines the sentinels to a float slot, and the
// published schema widened only there for the same reason.
func intStatic(w *walkState, raw any, path string) Value {
	return Int(expectInt(raw, path))
}

func boolStatic(w *walkState, raw any, path string) Value {
	return Bool(expectBool(raw, path))
}

func stringStatic(w *walkState, raw any, path string) Value {
	return Str(expectString(raw, path))
}

// stringOptStatic — a string option: null is a genuine None; the legacy
// "<opaque>" sentinel stays the scalar sentinel string.
func stringOptStatic(w *walkState, raw any, path string) Value {
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
func decodeSelectOption(w *walkState, raw any, path string) Value {
	if s, ok := raw.(string); ok {
		return Obj{Fields: map[string]Value{"label": Str(s), "value": Str(s)}}
	}
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordSelectOption)
	s.field("label", decodeTextSource)
	s.field("value", decodeString)
	return s.buildStrict("")
}

// selectOptionsStatic — the typed option-list payload; "<opaque>" → a tagged
// one-element placeholder; null → the empty typed list (§5 read-compat).
func selectOptionsStatic(w *walkState, raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{Obj{Fields: map[string]Value{"label": Str(opaqueSentinel), "value": Str(opaqueSentinel)}}}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeSelectOption(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// stringListStatic — "<opaque>" → a one-element placeholder list; null → the
// empty typed list.
func stringListStatic(w *walkState, raw any, path string) Value {
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
func floatSeqStatic(w *walkState, raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = expectNumber(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeMapMarker(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordMapMarker)
	s.field("label", decodeTextSource)
	s.field("latitude", expectNumber)
	s.field("longitude", expectNumber)
	return s.buildStrict("")
}

// markerSeqStatic — both "<opaque>" and null → the empty typed array.
func markerSeqStatic(w *walkState, raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeMapMarker(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// rowCell — one cell of a typed row (fuaran#665), the residual-opaque boundary
// narrowed from the whole rows payload to the cell seam. The §2 rule-11
// recognised scalars (string / bool / number) carry faithfully; a nested array
// or object is display-opaque and normalises to the "<opaque>" sentinel, which
// is what the reference hosts re-encode such a cell as — so this host's
// decode-time normalisation keeps the round trip byte-stable in one pass rather
// than two, the same idiom the typed parsers above use.
func rowCell(raw any) Value {
	switch t := raw.(type) {
	case bool:
		return Bool(t)
	case string:
		return Str(t)
	case json.Number:
		return numberValue(t)
	}
	return Str(opaqueSentinel)
}

// decodeRow — one row: an *open* name→value record of scalar cells. A null cell
// is OMITTED (rule 4 — absence is structural, never "k":null), matching what the
// reference encoders emit. Built structurally rather than via fromJSON so a cell
// named "$type" stays a cell, never a discriminator.
func decodeRow(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	fields := make(map[string]Value, len(obj))
	for k, v := range obj {
		if v == nil {
			continue
		}
		fields[k] = rowCell(v)
	}
	return Obj{Fields: fields}
}

// rowSeqStatic — the typed row-source payload (DataGrid / Chart source).
// fuaran#665 moved it off the §5 host-typed opaque seam: rows ride the wire as
// an array of row objects. Both legacy spellings — the "<opaque>" sentinel a
// pre-typed host emitted, and an absent/null payload — normalise to the empty
// feed (read-compat, indefinitely: that *was* the whole value the sentinel
// carried). An empty feed encodes [], never null.
func rowSeqStatic(w *walkState, raw any, path string) Value {
	if raw == nil {
		return Arr{}
	}
	if s, ok := raw.(string); ok && s == opaqueSentinel {
		return Arr{}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeRow(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeLocalFlushTrigger(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, flushTriggerCases, CodeUnknownDUCase)
	// Every case is read from its rows (OnDebounce's milliseconds included);
	// unknown members are dropped.
	return newCaseSpec(w, obj, path, &idl.UnionLocalFlushTrigger, tag).buildStrict(tag)
}

// decodeInvokeArgs — the [{"addr","value"}] scalar pairs of Binding.Invoke /
// Action.Invoke.
func decodeInvokeArgs(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		s := newRecordSpec(w, expectObject(item, p), p, &idl.RecordInvokeArg)
		s.field("addr", decodeString)
		s.field("value", decodeString)
		out[i] = s.buildStrict("")
	}
	return out
}

// decodeBindingWith is the master Binding decoder: it validates the
// discriminator, applies the lenient shape coercions (a bare array / scalar
// IS Static; the Bound wrapper unwraps in place), drops the 0.2.0 wire-omitted
// closure sentinels (Query.accessor / Selection.accessor), renames the
// field-name aliases, and decodes each case to its canonical shape.
func decodeBindingWith(w *walkState, raw any, path string, parse staticParser) Value {
	return decodeBindingTyped(w, raw, path, parse, false)
}

// decodeBindingTyped is decodeBindingWith plus typedDefault, which extends the
// slot's Static parser to the OTHER value-carrying binding arms —
// State/Selection/Filter's defaultValue, which the reference hosts route through
// the same typed parser. Opt-in per slot rather than global: only the rows slot
// (fuaran#665) has a fixture pinning it, and flipping the pre-429 typed slots
// onto it is a behaviour change of its own.
func decodeBindingTyped(w *walkState, raw any, path string, parse staticParser, typedDefault bool) Value {
	decodeDefault := func(raw any, p string) Value {
		if typedDefault {
			return parse(w, raw, p)
		}
		return fromJSON(raw)
	}
	switch raw.(type) {
	case string, json.Number, bool, []any:
		// §3.6 shape coercion: every Binding case is a $type-discriminated
		// object, so a bare array or scalar can only mean Static. Objects stay
		// strict (an object without $type is more plausibly a mistyped
		// binding); null stays strict (ambiguous with absent).
		return Obj{Tag: "Static", Fields: map[string]Value{"value": parse(w, raw, path)}}
	}
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
	if tag == "Bound" {
		// The TextSource-wrapper convention transferred to a bare-Binding
		// slot: Bound carries exactly one payload field, so the unwrap is
		// one-to-one — decode the inner binding in place. Decode-only: Bound is
		// a section-16 lenient spelling, not a Binding case, so it has no row.
		return decodeBindingWith(w, require(obj, "binding", path), path+".binding", parse)
	}
	// Every case reads its fields' required-ness from its table row (Phase 2330).
	s := newCaseSpec(w, obj, path, &idl.UnionBinding, tag)
	switch tag {
	case "Static":
		// Phase 677 — absence is structural: a MISSING `value` means the binding
		// carries none, and the legacy `"value": null` spelling normalises to the
		// same thing (§16 shorthand). Where the slot's parser yields Null the key
		// is omitted entirely; where it yields a typed empty (options normalise to
		// []) that empty is emitted, so "no selection" and "selected nothing" stay
		// distinguishable.
		raw, _ := s.has("value")
		decoded := parse(w, raw, path+".value")
		if _, isNull := decoded.(Null); !isNull {
			s.set("value", decoded)
		}
	case "Query":
		s.field("name", decodeString)
		// Field aliases: deps / dependencies. The accessor closure sentinel is
		// wire-omitted (0.2.0) — dropped on decode. dependsOn is
		// omitted-when-empty so the degenerate Query is byte-stable.
		if raw, ok := s.has("dependsOn", "deps", "dependencies"); ok {
			arr := expectArray(raw, path+".dependsOn")
			if len(arr) > 0 {
				out := make(Arr, len(arr))
				for i, item := range arr {
					out[i] = Str(expectString(item, path+".dependsOn["+strconv.Itoa(i)+"]"))
				}
				s.set("dependsOn", out)
			}
		}
	case "Filter":
		s.field("name", decodeString)
		if raw, ok := s.has("defaultValue"); ok {
			s.set("defaultValue", decodeDefault(raw, path+".defaultValue"))
		}
	case "Selection":
		s.field("nodeId", decodeString)
		if raw, ok := s.has("defaultValue"); ok {
			s.set("defaultValue", decodeDefault(raw, path+".defaultValue"))
		}
		s.field("field", decodeString)
	case "State":
		s.field("key", decodeString)
		// Field aliases: initialValue / default — the React useState prior.
		// Phase 677 — an explicit null default is absence, same as omitting it.
		if raw, ok := s.has("defaultValue", "initialValue", "default"); ok && raw != nil {
			s.set("defaultValue", decodeDefault(raw, path+".defaultValue"))
		}
	case "Now":
		// Phase 765 — the host-furnished current INSTANT is never on the wire:
		// the clock lives in the host, never the tree. Phase 1533 — the declared
		// `grain` is the one wire field, optional, absent meaning `Second`, so a
		// grain-less `Now` is still the bare `{"$type":"Now"}`.
		//
		// Present-but-unreadable is a REFUSAL rather than a silent fallback to
		// the default: a document that names a grain the host cannot honour must
		// not be rendered at a neighbouring resolution in silence.
		s.field("grain", enumDecoder(timeGrainCases, "grain", noAliases))
	case "Computed":
		s.field("fn", decodeClosure)
	case "I18n":
		s.field("key", decodeString)
		if raw, ok := s.has("args"); ok {
			argsObj := expectObject(raw, path+".args")
			args := make(map[string]Value, len(argsObj))
			for k, v := range argsObj {
				args[k] = decodeBinding(w, v, path+".args."+k)
			}
			s.set("args", Obj{Fields: args})
		}
	case "Local":
		// The declarative half of the buffer (WIRE_FORMAT.md Section 3.3.3).
		// `format` / `onCommit` / `parse` are still the closure sentinels this
		// host cannot restore; what it CAN do is refuse the two combinations a
		// structural pass-through would admit silently.
		//
		// A `codec` whose Format case has no total, LOCALE-INDEPENDENT inverse.
		// `Binding.Format` carries a LocaleSource because it renders for
		// reading; a Local codec carries none, because whatever it renders it
		// must also parse back from what the reader typed. `Currency` prepends a
		// locale-chosen symbol, `Date`'s four styles are locale renditions, and
		// `RelativeTime` / `Since` / `Duration` render a phrase. `Percent` is
		// refused for a narrower reason worth recording, since it looks
		// admissible: its inverse needs a x100 scale whose IEEE round-trip is not
		// exact.
		s.field("format", decodeClosure)
		s.field("parse", decodeClosure)
		if raw, ok := s.has("codec"); ok {
			codecObj := expectObject(raw, path+".codec")
			tag, _ := codecObj["$type"].(string)
			if tag != "Number" {
				failExpecting(
					CodeWrongType,
					path+".codec",
					"Binding.Local 'codec' must be a Format case with a total, locale-independent inverse - only 'Number' has one",
					`use {"$type":"Number","decimals":2}, or drop the codec and let the buffer use the identity; a locale-rendered format (Currency / Date / RelativeTime / Since / Duration) cannot be parsed back from what the reader typed`,
				)
			}
			s.set("codec", fromJSON(raw))
		}
		// Two commit destinations. Not resolved by a precedence rule, because
		// the wire cannot carry the closure at all: a host honouring `onCommit`
		// and a host honouring `commitTo` would write to different places from
		// identical bytes.
		_, onCommitPresent := s.has("onCommit")
		if raw, ok := s.has("commitTo"); ok {
			if onCommitPresent {
				failExpecting(
					CodeWrongType,
					path+".commitTo",
					"Binding.Local carries both 'onCommit' and 'commitTo' - exactly one commit destination is allowed",
					"either 'onCommit' (a host closure, which crosses the wire only as the closure sentinel) or 'commitTo' (the State key the flush writes); a decoding host can honour only the second, so keeping both makes the same document commit to two different places depending on who read it",
				)
			}
			s.set("commitTo", Str(expectString(raw, path+".commitTo")))
		}
		// PRESENCE, not a constant: this arm used to emit the sentinel
		// unconditionally, so a document that never wrote the key gained one on
		// re-encode — invisible while every Local carried a closure, and a
		// byte divergence the moment one declared `commitTo` instead.
		if onCommitPresent {
			s.set("onCommit", Str(closureSentinel))
		}
		if raw, ok := s.has("initialFrom"); ok {
			s.set("initialFrom", decodeBindingWith(w, raw, path+".initialFrom", parse))
		}
		s.field("flushOn", decodeLocalFlushTrigger)
	case "Format":
		if raw, ok := s.has("format"); ok {
			format := fromJSON(raw)
			if o, ok := format.(Obj); ok && o.Tag == "Date" {
				// Phase 1811 — Format.DateTime is canonical; Date (the pre-rename
				// spelling) is a section-16 lenient alias and re-encodes canonical.
				o.Tag = "DateTime"
				format = o
			}
			s.set("format", format)
		}
		if raw, ok := s.has("locale"); ok {
			s.set("locale", fromJSON(raw))
		}
		if raw, ok := s.has("source"); ok {
			s.set("source", decodeBindingWith(w, raw, path+".source", floatStatic))
		}
	case "Transform":
		// Phase 815 — normalise the two observed organic shapes (Static/Bound
		// wrapper; row-major rows) to canonical columnar before the frame
		// codec sees the value.
		// Phase 818 — a binding-shaped source (State / Selection / Query
		// `$type`) is PRESERVED as the live source: the decoded binding sits
		// in the `source` slot verbatim (canonical re-encode is byte-for-byte
		// — one wire dialect) and the renderer derives the initial snapshot
		// from its carried default data at evaluation time. A State wrapper
		// carrying NO data — the bare `{"$type":"State","key":k}` — is a live
		// source over the EMPTY initial snapshot (§16), and a State wrapper's
		// carried data is snapshot-VALIDATED at decode so
		// the ragged-rows didactic stays byte-identical to the snapshot era.
		srcRawOrig, hasSource := s.has("source")
		liveTag := ""
		if m, ok := srcRawOrig.(map[string]any); ok {
			if t, ok := m["$type"].(string); ok && (t == "State" || t == "Selection" || t == "Query") {
				liveTag = t
			}
		}
		var source Value
		switch {
		case !hasSource:
			// Reachable only if the table stops requiring a source.
		case liveTag != "":
			b := decodeBinding(w, srcRawOrig, path+".source")
			carried, carriedPresent := srcRawOrig.(map[string]any)["defaultValue"]
			// Phase 1656 — a `null` member is a SPELLING OF ABSENCE (§5), so it
			// carries nothing. It did not read that way before: a JSON null
			// decodes to a present `nil`, which is neither missing nor an empty
			// array, so it fell to the snapshot branch and was decoded as carried
			// data. The reference host never had the defect because it reads the
			// DECODED binding's default, where every spelling of absence has
			// already collapsed to one value; this reads the raw member, so it has
			// to name them.
			hasCarried := carriedPresent && carried != nil
			emptyCarried := false
			if rows, ok := carried.([]any); ok && len(rows) == 0 {
				emptyCarried = true
			}
			if liveTag == "State" {
				switch {
				case emptyCarried:
					// WIRE_FORMAT.md §16 / §24.4 — an EMPTY carried array is the
					// EMPTY TABLE, not a malformed source. An initially-empty
					// live collection ("count the requests in an empty log") is a
					// complete intent with zero rows and no columns to infer, and
					// under §24.4 it is also how a reader spells "I read this key
					// and carry no data of my own" while a SIBLING reader's
					// declaration seeds the slot.
					//
					// Sending it through the columnar codec instead ACCUSES it: a
					// bare `[]` has no first row to take a key set from, so
					// `normaliseTransformSource` passes the array through
					// untouched and `decodeFrameSource` reports
					// `WRONG_TYPE … expected object` against a document the
					// reference hosts decode. That refusal is what kept
					// `nodes/shared-source-seeded-pair` red on this host.
					//
					// The preserved binding stays the stored source, exactly as
					// the carried-data arm below; only the snapshot VALIDATION is
					// skipped, because an empty array has nothing to validate.
					source = b
				case hasCarried:
					// Validate the carried data as the initial snapshot
					// (didactics byte-identical to the 815 snapshot decode);
					// the preserved binding stays the stored source.
					srcRaw := normaliseTransformSource(srcRawOrig)
					atComputePath(path+".source", func() Value { return decodeFrameSource(srcRaw) })
					source = b
				default:
					// WIRE_FORMAT.md §16 — a State wrapper carrying NO payload
					// member is a LIVE source over the EMPTY initial snapshot,
					// exactly as the empty-array spelling above and as a
					// Selection / Query source already were. It used to surface
					// the columnar codec's missing-field didactic, which was
					// correct while nothing else could fill the slot; under §24.4
					// a SIBLING reader's declaration fills it, so the refusal was
					// rejecting the most direct spelling of "I read this key and
					// carry no data of my own" — the one FUARAN106's remedy text
					// tells an author to write.
					//
					// The two spellings are ONE dialect and decode to the same
					// live source; neither is normalised into the other, so each
					// re-encodes to its own bytes — the decoded binding keeps its
					// `defaultValue` ABSENT, which is what makes the bare form
					// round-trip verbatim.
					source = b
				}
			} else {
				// Selection / Query — the initial snapshot derives from the
				// carried default (or the empty table) at evaluation time; a
				// non-tabular default stays loud at evaluation, never here.
				source = b
			}
		default:
			srcRaw := normaliseTransformSource(srcRawOrig)
			source = atComputePath(path+".source", func() Value { return decodeFrameSource(srcRaw) })
		}
		if source != nil {
			s.set("source", source)
		}
		if pipeRaw, ok := s.has("pipeline"); ok {
			pipeline := atComputePath(path+".pipeline", func() Value { return decodeComputePipeline(pipeRaw) })
			// Phase 1662 — §21.8's expression-node bound over the pipeline's own
			// embedded expressions, at DECODE and not at validation: a document that
			// decodes must not be able to name an unbounded evaluation.
			checkPipelineExprBound(pipeline, path)
			s.set("pipeline", pipeline)
		}
		if raw, ok := s.has("params"); ok {
			params := decodeTransformParams(w, raw, path+".params")
			if len(params) > 0 {
				s.set("params", params)
			}
		}
	case "Expr":
		// Phase 1534 — the scalar expression binding (WIRE_FORMAT §3.3.2). One
		// ColExpr in the SAME encoding the pipeline's steps carry — the case
		// mints no operator — plus the same optional `params` list Transform
		// carries, decoded by the same helper.
		//
		// Three refusals, all here because each wants a $-rooted path and a
		// code: a `col` reference (an Expr has no row, so `col` names nothing —
		// and the remedy is a different BINDING, which the message says), a
		// `param` this binding's own `params` does not bind (decidable
		// statically here where it is NOT for Transform, whose unbound filter
		// params are pruned under the deliberate unset-chip leniency), and an
		// expression over MaxExprNodes.
		var names []string
		if exprRaw, ok := s.has("expr"); ok {
			expr := atComputePath(path+".expr", func() Value { return decodeComputeExpr(exprRaw) })
			count := 0
			if exprWalk(expr, &names, &count) {
				failExpecting(CodeWrongType, path+".expr",
					"a `col` reference is not admitted inside an Expr binding — an Expr evaluates against its params alone and has no row for a column name to read. Use `Binding.Transform`, whose source supplies the frame, and put the column expression in a `derive` step",
					"a ColExpr over `param` / `lit` / operators only (no `col`)")
			}
			if count > MaxExprNodes {
				failExpecting(CodeLimitExceeded, path+".expr",
					fmt.Sprintf("expression exceeds the maximum of %d expression nodes (WIRE_FORMAT 21)", MaxExprNodes),
					fmt.Sprintf("at most %d ColExpr nodes in one Expr binding", MaxExprNodes))
			}
			s.set("expr", expr)
		}
		bound := map[string]bool{}
		if raw, ok := s.has("params"); ok {
			params := decodeTransformParams(w, raw, path+".params")
			for _, p := range params {
				if po, ok := p.(Obj); ok {
					if n, ok := po.Fields["name"].(Str); ok {
						bound[string(n)] = true
					}
				}
			}
			if len(params) > 0 {
				s.set("params", params)
			}
		}
		var missing []string
		for _, n := range names {
			if !bound[n] {
				missing = append(missing, "'"+n+"'")
			}
		}
		if len(missing) > 0 {
			failExpecting(CodeWrongType, path+".expr",
				"the expression reads param(s) "+strings.Join(missing, ", ")+" that this binding's `params` does not bind — an Expr has no rows and no filter to prune, so an unbound param has no value to take; add a params entry naming each, or drop the reference",
				`{"$type":"Expr","expr":{…},"params":[{"name":"<name>","from":<Binding>}]}`)
		}
	case "Invoke":
		s.field("capabilityId", decodeString)
		s.field("args", decodeInvokeArgs)
	default:
		// A case idl.json declares that has no hand arm above: read from its
		// table row, so a new Binding case decodes typed on regeneration.
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

// decodeTransformParams — Transform's [{ "from": <Binding>, "name": <string> }]
// param list, with the §3.6 map coercion: params are a NAME-KEYED SET
// (ColExpr.Param lookup), so the name→binding map form coerces to the
// canonical array (sorted by name); `value` aliases `from` at the element.
func decodeTransformParams(w *walkState, raw any, path string) Arr {
	if m, ok := raw.(map[string]any); ok {
		if _, tagged := m["$type"]; !tagged {
			names := make([]string, 0, len(m))
			for k := range m {
				names = append(names, k)
			}
			sort.Strings(names)
			out := make(Arr, len(names))
			for i, name := range names {
				from := decodeBinding(w, m[name], path+"."+name+".from")
				out[i] = Obj{Fields: map[string]Value{"from": from, "name": Str(name)}}
			}
			return out
		}
	}
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		s := newRecordSpec(w, expectObject(item, p), p, &idl.RecordTransformParam)
		s.field("name", decodeString)
		if raw, ok := s.has("from", "value"); ok {
			name, _ := s.fields["name"].(Str)
			s.set("from", decodeBinding(w, raw, path+"."+string(name)+".from"))
		}
		out[i] = s.buildStrict("")
	}
	return out
}

func decodeBinding(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, objStatic)
}

func decodeBindingFloat(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, floatStatic)
}

// decodeBindingInt — the INTEGER-slot Binding, and the reason it is not
// decodeBindingFloat with a narrower name. §7's two accept sets differ: a float
// slot takes a JSON number OR one of the three non-finite sentinel spellings, an
// integer slot takes an integer literal and nothing else. `intStatic` therefore
// routes to expectInt, which gates on isIntegerLiteral and never consults
// nonFiniteSentinel — so `"NaN"` at Tabs.activeIndex is WRONG_TYPE, which is what
// `reject-binding-int-sentinel-string` pins.
func decodeBindingInt(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, intStatic)
}

func decodeBindingBool(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, boolStatic)
}

func decodeBindingString(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, stringStatic)
}

func decodeBindingStringOpt(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, stringOptStatic)
}

func decodeBindingSelectOptions(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, selectOptionsStatic)
}

func decodeBindingStringList(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, stringListStatic)
}

func decodeBindingFloatSeq(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, floatSeqStatic)
}

func decodeBindingMarkerSeq(w *walkState, raw any, path string) Value {
	return decodeBindingWith(w, raw, path, markerSeqStatic)
}

// decodeBindingRows — the grid/chart rows slot. typedDefault is set because the
// editable-grid authoring shape is a State-sourced rows array (the write-back
// floor), so defaultValue carries rows exactly as value does and must take the
// same normalisation.
func decodeBindingRows(w *walkState, raw any, path string) Value {
	return decodeBindingTyped(w, raw, path, rowSeqStatic, true)
}

// decodeRangePair reads a {min, max} pair (object or lenient two-element
// array) into the canonical bare pair object.
func decodeRangePair(w *walkState, raw any, path string) Value {
	if m, ok := raw.(map[string]any); ok {
		// An incomplete pair object is refused as a whole (the reference
		// host's verdict), which rows it needs is the table's answer.
		if !requiredRowsPresent(idl.RecordRangePair.Fields, m) {
			fail(CodeWrongType, path, "expected an object with min and max numbers at "+path)
		}
		s := newRecordSpec(w, m, path, &idl.RecordRangePair)
		s.field("max", expectNumber)
		s.field("min", expectNumber)
		return s.buildStrict("")
	}
	if arr, ok := raw.([]any); ok && len(arr) == 2 {
		return Obj{Fields: map[string]Value{
			"max": expectNumber(w, arr[1], path+"[1]"),
			"min": expectNumber(w, arr[0], path+"[0]"),
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
func decodeRangeValue(w *walkState, raw any, path string) Value {
	if m, ok := raw.(map[string]any); ok {
		if _, tagged := m["$type"]; !tagged {
			return decodeRangePair(w, raw, path)
		}
		obj := expectObject(raw, path)
		tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
		if tag == "Static" {
			v, ok := obj["value"]
			if !ok {
				fail(CodeMissingField, path+".value", "Static binding missing value")
			}
			return decodeRangePair(w, v, path+".value")
		}
		return decodeBindingWith(w, raw, path, decodeRangePair)
	}
	if _, ok := raw.([]any); ok {
		return decodeRangePair(w, raw, path)
	}
	return decodeBindingWith(w, raw, path, decodeRangePair)
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
func decodeDateRangePair(w *walkState, raw any, path string) Value {
	checkOrdered := func(from, to Value) {
		f, hasFrom := from.(Str)
		t, hasTo := to.(Str)
		if hasFrom && hasTo && strings.Compare(string(f), string(t)) > 0 {
			failExpecting(
				CodeWrongType,
				path,
				"date-range start '"+string(f)+"' is after end '"+string(t)+"' — a DateRange pair is ordered "+
					"(from <= to); ISO-8601 strings of one variant compare lexicographically, so swap the two values",
				dateRangePairShape,
			)
		}
	}
	if m, ok := raw.(map[string]any); ok {
		// An incomplete pair object is refused as a whole (the reference
		// host's verdict), which rows it needs is the table's answer.
		if !requiredRowsPresent(idl.RecordDateTimeRangePair.Fields, m) {
			fail(CodeWrongType, path, "expected an object with from and to ISO-8601 strings at "+path)
		}
		// A malformed `from` surfaces before a malformed `to` — the F# host's order.
		s := newRecordSpec(w, m, path, &idl.RecordDateTimeRangePair)
		s.field("from", decodeString)
		s.field("to", decodeString)
		checkOrdered(s.fields["from"], s.fields["to"])
		return s.buildStrict("")
	}
	if arr, ok := raw.([]any); ok && len(arr) == 2 {
		from := Str(expectString(arr[0], path+"[0]"))
		to := Str(expectString(arr[1], path+"[1]"))
		checkOrdered(from, to)
		return Obj{Fields: map[string]Value{"from": from, "to": to}}
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
func decodeDateRangeValue(w *walkState, raw any, path string) Value {
	if m, ok := raw.(map[string]any); ok {
		if _, tagged := m["$type"]; !tagged {
			return decodeDateRangePair(w, raw, path)
		}
		obj := expectObject(raw, path)
		tag := dispatch(obj, path, bindingCases, CodeUnknownDUCase)
		if tag == "Static" {
			v, ok := obj["value"]
			if !ok {
				fail(CodeMissingField, path+".value", "Static binding missing value")
			}
			return decodeDateRangePair(w, v, path+".value")
		}
		return decodeBindingWith(w, raw, path, decodeDateRangePair)
	}
	if _, ok := raw.([]any); ok {
		return decodeDateRangePair(w, raw, path)
	}
	return decodeBindingWith(w, raw, path, decodeDateRangePair)
}

// ── Actions ─────────────────────────────────────────────────────────────────

// decodeAction decodes a wire-survivable Action to its canonical shape. The
// payload positions (SetState.value / Notify.payload / AiTool.args) are
// structured JSON payload positions per rule 12 — null-strict at the null's
// exact path. Closure slots normalise to the "<closure>" sentinel; the
// Dispatch msg sentinel is wire-omitted (0.2.0).
func decodeAction(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, actionCases, CodeUnknownDUCase)
	// Every case reads its fields' required-ness from its table row (Phase 2330).
	s := newCaseSpec(w, obj, path, &idl.UnionAction, tag)
	switch tag {
	case "Dispatch":
		// The msg closure is host-only: never on the wire.
	case "Call":
		// Field alias: url — the fetch prior for the same concept.
		s.field("endpoint", decodeString, "url")
		s.field("onResult", decodeClosure)
		s.field("into", decodeCallResultTarget)
	case "Notify":
		s.field("channel", decodeString)
		s.field("payload", decodeJSONValue)
	case "Navigate":
		// Field aliases: href / url / to — the HTML / router prior.
		//
		// Phase 1536 — the route is a `TextSource`, not a bare string, so a tree
		// can name a destination it computes from what the reader is looking at.
		// The bare JSON string IS `Literal`'s canonical form, so every document
		// written before the widening decodes exactly as it did — including one
		// using an alias, since the aliases are resolved before the value is
		// decoded and there is still exactly one canonical field a router can be
		// reached through.
		//
		// `target` is omitted at `Self`, so absence is the pre-1536 behaviour.
		s.field("route", decodeTextSource, "href", "url", "to")
		s.field("target", enumDecoder(navigateTargetCases, "target", noAliases))
	case "SetState":
		// Phase 818 — `value` (a literal JSON value, written verbatim) XOR
		// `valueFrom` (a Binding evaluated at dispatch time inside the
		// existing gate). Exactly one must be present; both / neither error
		// didactically naming both fields.
		s.field("key", decodeString)
		rawValue, hasValue := s.has("value")
		rawFrom, hasFrom := s.has("valueFrom")
		if hasValue && hasFrom {
			fail(CodeWrongType, path+".valueFrom",
				"SetState carries both 'value' and 'valueFrom' — exactly one is allowed: 'value' is a literal JSON value written verbatim; 'valueFrom' derives the written value from a Binding at dispatch time; remove one")
		}
		if !hasValue && !hasFrom {
			fail(CodeMissingField, path+".value",
				"missing required field 'value' — provide 'value' (a literal JSON value) or 'valueFrom' (a Binding evaluated at dispatch time)")
		}
		if hasValue {
			s.set("value", fromJSONStrict(w, rawValue, path+".value"))
		}
		if hasFrom {
			s.set("valueFrom", decodeBinding(w, rawFrom, path+".valueFrom"))
		}
	case "AiTool":
		s.field("toolName", decodeString)
		s.field("args", decodeJSONValue)
	case "Chain":
		if raw, ok := s.has("ops"); ok {
			arr := expectArray(raw, path+".ops")
			ops := make(Arr, len(arr))
			for i, item := range arr {
				ops[i] = decodeAction(w, item, path+".ops["+strconv.Itoa(i)+"]")
			}
			s.set("ops", ops)
		}
	case "CommitLocal":
		s.field("nodeId", decodeString)
	case "WriteToClipboard":
		// Phase 1126 — the payload is a `TextSource`. The bare JSON string IS
		// `Literal`'s canonical form, so every document written before the
		// widening decodes exactly as it did, and the explicit `Literal`
		// envelope normalises down to it here as at every other text slot
		// (§16). A `text` that is neither a string nor a `$type`-tagged
		// `TextSource` is WRONG_TYPE and is never coerced: a host that read the
		// widening as "this member is now open" would put a JSON literal on the
		// reader's clipboard, and a clipboard is a channel the reader later
		// pastes somewhere with authority.
		s.field("text", decodeTextSource)
	case "Print":
		// Phase 1124 — the payload-free case. This is the ONE `Action` arm that
		// is strict about unrecognised members, and the asymmetry is
		// deliberate: everywhere else in this format an unknown member is one
		// the reading host has not learned yet, and dropping it is the
		// forward-compatible answer. Here there is nothing to learn — page
		// range, size, margins and copies are the host's page setup and the
		// reader's dialogue — so a host accepting
		// `{"$type":"Print","pageRange":"1-3"}` would leave the emitter
		// believing it had constrained a printing it had not, with no error
		// anywhere saying otherwise. The refusal names the offending member's
		// own path; the keys are sorted so WHICH member is named is
		// deterministic rather than a function of Go's map iteration order.
		extras := make([]string, 0, len(obj))
		for key := range obj {
			if key != "$type" {
				extras = append(extras, key)
			}
		}
		if len(extras) > 0 {
			sort.Strings(extras)
			fail(CodeWrongType, path+"."+extras[0],
				"no member beside $type — Print takes no payload (WIRE_FORMAT.md §3.6.14)")
		}
	case "Confirm":
		// Phase 1537 — ask, then act. `prompt` is a `TextSource` (so the question
		// can name what the reader selected), `onConfirm` is required and
		// `onCancel` optional; an author who declares no cancel branch means
		// "nothing happens", which an absent action already expresses.
		//
		// THE DEPTH-ONE REFUSAL is the substance of this arm. A `Confirm`
		// reachable from either continuation is refused, and the check walks the
		// DECODED continuation rather than its immediate `$type`, so a nested
		// confirm inside a `Chain` is caught by the same line that catches a
		// bare one. A dialogue that answers a dialogue is a modal stack the
		// reader cannot escape, and it says nothing one question does not.
		//
		// WRONG_TYPE follows the `SetState` value/valueFrom and Print-with-
		// payload precedents: a decoder POLICY refusal reuses it rather than
		// minting a code every host in the roster would owe an adoption for.
		s.field("prompt", decodeTextSource)
		for _, key := range [...]string{"onConfirm", "onCancel"} {
			if raw, ok := s.has(key); ok {
				continuation := decodeAction(w, raw, path+"."+key)
				refuseNestedConfirm(continuation, path+"."+key)
				s.set(key, continuation)
			}
		}
	case "Focus":
		// Phase 1537 — a bare node id, the `CommitLocal` shape above. It
		// addresses a node in THIS document, so there is nothing for a binding
		// to compute and no `TextSource` here.
		s.field("nodeId", decodeString)
	case "ReadFileBody":
		s.field("fileRef", decodeString)
		s.field("encoding", enumDecoder(enumCases(&idl.EnumFileReadEncoding), "encoding", noAliases))
		// onRead is an optional row, but the reference decoder restores the
		// continuation on every decoded tree, so it re-encodes as the sentinel
		// whether or not it was written. Decode policy, not required-ness.
		s.has("onRead")
		s.set("onRead", Str(closureSentinel))
	case "Invoke":
		s.field("capabilityId", decodeString)
		s.field("args", decodeInvokeArgs)
	default:
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

// decodeCallResultTarget — Action.Call's `into`, read from its case's rows.
func decodeCallResultTarget(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, callTargetCases, CodeUnknownDUCase)
	return newCaseSpec(w, obj, path, &idl.UnionCallResultTarget, tag).buildStrict(tag)
}

// refuseNestedConfirm fails when a `Confirm` is reachable from `action`
// (Phase 1537). Confirmation is bounded at ONE question: a dialogue that
// answers a dialogue is a modal stack the reader cannot escape, and it
// expresses no intent a single question does not.
//
// It walks the DECODED action rather than raw JSON, and descends `Chain`,
// because a chain is otherwise a hiding place — a check written against the
// continuation's immediate `$type` passes a nested confirm one level down.
func refuseNestedConfirm(action Value, path string) {
	obj, ok := action.(Obj)
	if !ok {
		return
	}
	switch obj.Tag {
	case "Confirm":
		fail(CodeWrongType, path,
			"a Confirm may not appear inside another Confirm's continuation — confirmation is bounded at one question (WIRE_FORMAT.md §3.6.22)")
	case "Chain":
		ops, isArr := obj.Fields["ops"].(Arr)
		if !isArr {
			return
		}
		for i, inner := range ops {
			refuseNestedConfirm(inner, path+".ops["+strconv.Itoa(i)+"]")
		}
	}
}

// ── Spec builder ────────────────────────────────────────────────────────────

type fieldDecoder func(w *walkState, raw any, path string) Value

// spec accumulates one kind object's decoded fields, tracking which input
// keys were consumed so the unconsumed remainder can be preserved
// structurally (§2 rule 2 tolerance) without resurrecting renamed aliases.
type spec struct {
	// w is this decode call's §21 walk state. It rides on the spec because the
	// node-recursing field decoders (children, cases, default, state) are reached
	// through the fieldDecoder table and have nowhere else to get it from.
	w        *walkState
	obj      map[string]any
	path     string
	fields   map[string]Value
	consumed map[string]bool
	// rows is the generated field table the object is decoded against (a
	// kind's, a union case's or a record's), or nil for a hand-only shape.
	rows []idl.Field
	// owner names the row set for absentLeniency: "<Union>.<Case>" or
	// "<Record>" ("" for a kind, which declares no leniency).
	owner string
}

func newSpec(w *walkState, obj map[string]any, path string) *spec {
	return &spec{w: w, obj: obj, path: path, fields: make(map[string]Value, len(obj)), consumed: map[string]bool{"$type": true}}
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
	v := dec(s.w, raw, s.path+"."+key)
	s.fields[key] = v
	return v
}

// sentinel normalises a present closure-slot key to the "<closure>" sentinel.
func (s *spec) sentinel(key string) {
	if _, ok := s.take(key); ok {
		s.fields[key] = Str(closureSentinel)
	}
}

func (s *spec) set(key string, v Value) { s.fields[key] = v }

// build finishes the object: every table row the hand arm did not read is
// decoded by its declared type, and the keys no row names are preserved
// structurally (rule 2 tolerance).
func (s *spec) build(tag string) Obj {
	s.decodeRemainingRows()
	for k, v := range s.obj {
		if !s.consumed[k] {
			s.fields[k] = fromJSON(v)
		}
	}
	return Obj{Tag: tag, Fields: s.fields}
}

// buildStrict finishes a fully-typed shape: every table row the hand decoder
// did not read is decoded by its declared type, and keys no row names are
// dropped.
func (s *spec) buildStrict(tag string) Obj {
	s.decodeRemainingRows()
	return Obj{Tag: tag, Fields: s.fields}
}

// ── Shared field decoders ───────────────────────────────────────────────────

func decodeCellFormat(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, cellFormatCases, CodeUnknownDUCase)
	if tag == "Date" {
		// Phase 1811 — DateTime is canonical; Date (the pre-rename spelling) is a
		// section-16 lenient alias, decoded to the same value and re-encoded canonical.
		tag = "DateTime"
	}
	s := newCaseSpec(w, obj, path, &idl.UnionCellFormat, tag)
	switch tag {
	case "Duration":
		// Phase 819 — trendable duration cells: raw float counts `unit`s,
		// rendered per `style`. Closed vocabularies.
		s.field("style", enumDecoder(durationStyleCases, "style", noAliases))
		s.field("unit", enumDecoder(durationUnitCases, "unit", noAliases))
	case "RelativeTime":
		// Phase 819 — cell-vocabulary parity with the binding-level Format's
		// RelativeTime case.
		s.field("unit", enumDecoder(relativeTimeUnitCases, "unit", noAliases))
	case "DateTime":
		s.field("format", decodeString)
	default:
		// The remaining cases read from their rows, unknown members preserved.
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

func decodeColumnWidth(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, columnWidthCases, CodeUnknownDUCase)
	return tableCase(w, obj, path, &idl.UnionColumnWidth, tag)
}

// decodeEmphasisEnum — the Emphasis style DU, with the §3.6 value aliases and
// the cross-vocabulary bool coercion (true ⇒ Loud, false ⇒ Normal — the
// same-name collision with the behavioural bool on Fact / LabelValueRow).
func decodeEmphasisEnum(w *walkState, raw any, path string) Value {
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
func decodeEmphasisFlag(w *walkState, raw any, path string) Value {
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

func decodeTone(w *walkState, raw any, path string) Value {
	return Str(enumStr(raw, path, toneCases, "tone", toneAliases))
}

func decodeWeight(w *walkState, raw any, path string) Value {
	return Str(enumStr(raw, path, weightCases, "weight", noAliases))
}

func decodeString(w *walkState, raw any, path string) Value {
	return Str(expectString(raw, path))
}

func decodeInt(w *walkState, raw any, path string) Value {
	return Int(expectInt(raw, path))
}

func decodeBool(w *walkState, raw any, path string) Value {
	return Bool(expectBool(raw, path))
}

// decodeSkeletonRows is `Skeleton.rows`, bounded by WIRE_FORMAT.md §21.9
// (Phase 1666).
//
// expectInt decides FIRST, so §7.1's slot rule is untouched: a fractional,
// non-finite or out-of-32-bit value is still a WRONG_TYPE and never a limit
// breach. The bound then refuses a value the slot CAN hold but the format will
// not carry the work of — a renderer emits one placeholder row per count, so
// {"rows":100000000} names 10^8 rendered rows in a handful of bytes. The two
// codes answer different questions and the ORDER is what keeps them apart.
//
// Upper bound only, deliberately: a negative count is an authoring defect
// (FUARAN152 in the pre-emit family), not a resource breach.
func decodeSkeletonRows(w *walkState, raw any, path string) Value {
	rows := expectInt(raw, path)
	if rows > MaxSkeletonRows {
		failExpecting(
			CodeLimitExceeded,
			path,
			fmt.Sprintf("skeleton rows %d exceeds the maximum of %d (WIRE_FORMAT 21.9)", rows, MaxSkeletonRows),
			fmt.Sprintf("at most %d rows on one Skeleton", MaxSkeletonRows))
	}
	return Int(rows)
}

func enumDecoder(allowed caseSet, name string, aliases map[string]string) fieldDecoder {
	return func(w *walkState, raw any, path string) Value {
		return Str(enumStr(raw, path, allowed, name, aliases))
	}
}

// decodeChildren decodes a node list, naming the offending element by INDEX.
//
// Phase 1677 — the separator was `.` (`$.kind.children.0.kind.text`) where the
// corpus, the reference host and every other indexed slot in this file spell it
// `[0]`. No corpus reject vector discriminated the two, because the reject rule
// is a PREFIX match and every such vector stops at `$.kind.children`; it
// surfaced only when `Tabs`/`Stepper` started routing their children here and a
// host-local test named a path inside one. A §6 path is read by an author
// repairing a document and matched by a host's own tooling, so two spellings of
// the same position is a divergence whichever one is shorter to write.
func decodeChildren(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeNodeValue(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeSingleNode(w *walkState, raw any, path string) Value {
	return decodeNodeValue(w, raw, path)
}

// decodeSwitchCase decodes one Switch case.
//
// Phase 1535 — a case selects on a string `match` XOR a `when` predicate (a
// Binding<bool> evaluated at render time). Exactly one; both and neither are
// refused, naming both fields, on the Phase 818 `value` / `valueFrom`
// precedent.
//
// "Neither" is refused rather than skipped at render because a case that names
// no condition has no rendering that could be right: skipping it renders the
// `default` and reports nothing.
func decodeSwitchCase(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordSwitchCase)
	s.field("child", decodeSingleNode)
	matchRaw, hasMatch := s.has("match")
	whenRaw, hasWhen := s.has("when")
	if hasMatch && hasWhen {
		fail(CodeWrongType, path+".when",
			"Switch case carries both 'match' and 'when' — exactly one is allowed: either 'match' "+
				"(a literal string compared against the switch's `on` selector) or 'when' (a "+
				"Binding<bool> predicate evaluated at render time, needing no selector)")
	}
	if !hasMatch && !hasWhen {
		fail(CodeMissingField, path+".match",
			"Switch case carries neither 'match' nor 'when' — give it a literal string under 'match' "+
				"(compared against the switch's `on` selector), or a Binding<bool> under 'when' (a "+
				"predicate evaluated at render time)")
	}
	if hasMatch {
		s.set("match", Str(expectString(matchRaw, path+".match")))
	} else {
		s.set("when", decodeBinding(w, whenRaw, path+".when"))
	}
	return s.buildStrict("")
}

func decodeSwitchCases(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeSwitchCase(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// ── Chart annotations (Phase 1490/1491/1492 — §4l) ──────────────────────────

var (
	chartAnnotationCases      = newCaseSet(idl.UnionChartAnnotation.CaseTags()...)
	chartAnnotationXCases     = newCaseSet(idl.UnionChartAnnotationX.CaseTags()...)
	chartAnnotationRangeCases = newCaseSet(idl.UnionChartAnnotationRange.CaseTags()...)
)

// isCanonicalISODay reports whether text is a canonical ISO-8601 date the
// temporal axis can place — YYYY-MM-DD, optionally followed by T… whose
// time-of-day is discarded.
//
// STRICT by shape AND by calendar: four digits, two, two, both hyphens, a month
// in 1–12 and a day the month actually has. A locale spelling (15/01/2026) and a
// bare year are both refused — admitting either would be the string-sniffing the
// temporal axis exists to avoid.
func isCanonicalISODay(text string) bool {
	if len(text) < 10 || text[4] != '-' || text[7] != '-' {
		return false
	}
	if len(text) > 10 && text[10] != 'T' {
		return false
	}
	digits := func(start, n int) (int, bool) {
		acc := 0
		for k := start; k < start+n; k++ {
			c := text[k]
			if c < '0' || c > '9' {
				return 0, false
			}
			acc = acc*10 + int(c-'0')
		}
		return acc, true
	}
	y, okY := digits(0, 4)
	m, okM := digits(5, 2)
	d, okD := digits(8, 2)
	if !okY || !okM || !okD || m < 1 || m > 12 {
		return false
	}
	last := 31
	switch m {
	case 2:
		if (y%4 == 0 && y%100 != 0) || y%400 == 0 {
			last = 29
		} else {
			last = 28
		}
	case 4, 6, 9, 11:
		last = 30
	}
	return d >= 1 && d <= last
}

// decodeChartAnnotationX — an annotation's X ADDRESS (Phase 1491, §4l "The three
// addressing forms"). Returns the decoded value and, for a Date address, its ISO
// string so the pair rule below can compare two of them.
//
// THE DATE MUST BE A DATE, and this refusal is the twin of ReferenceLine's
// finite-value narrowing rather than a new posture. A chart's calendar is
// deliberately TOTAL over CELLS — an unparseable x cell reads as 1970-01-01,
// because a non-date COLUMN is loud upstream and refusing per-cell would be
// worse. An annotation has no column to be loud about: the string is authored
// directly, so nothing upstream can catch it. And because §4l rule 3 has a
// temporal address enter the axis extent before the ticks are chosen, a typo does
// not misplace one marker — it drags the domain back to the epoch and rescales
// the whole picture.
func decodeChartAnnotationX(w *walkState, raw any, path string) (Value, string) {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, chartAnnotationXCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionChartAnnotationX, tag)
	iso := ""
	switch tag {
	case "Category":
		s.field("key", decodeString)
	case "Date":
		if raw, ok := s.has("iso"); ok {
			iso = expectString(raw, path+".iso")
			if !isCanonicalISODay(iso) {
				failExpecting(CodeWrongType, path+".iso",
					"expected a canonical ISO-8601 date (YYYY-MM-DD, optionally followed by a time) naming a real calendar day at "+path+".iso",
					"a canonical ISO-8601 date — an event marker's date is the address it is drawn at, and an unreadable one would place the marker at 1970-01-01 and drag the axis back with it")
			}
			s.set("iso", Str(iso))
		}
	}
	return s.buildStrict(tag), iso
}

// decodeChartAnnotationRange — a range band's PAIR (Phase 1492, §4l). The case
// carries the AXIS as well as the pair, so a value axis addressed by category
// keys is not a document this decoder has to refuse — it is one no encoder can
// write.
//
// TWO REFUSALS, and they are the pair rules the WIRE can decide by itself. A
// non-finite endpoint is ReferenceLine's narrowing at two slots instead of one,
// for its reason exactly: §4l rule 3 has both ends enter the value domain, so a
// NaN takes the nice-domain, every gridline and every mark with it. An UNORDERED
// pair is refused at the pair's own slot — the defect is the pair's, not either
// end's — rather than silently swapped.
//
// A CATEGORY pair's order is NOT decided here: the order of two band keys is the
// ROWS' order, a cross-reference rather than a local property of the address.
func decodeChartAnnotationRange(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, chartAnnotationRangeCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionChartAnnotationRange, tag)
	switch tag {
	case "ValueRange":
		var from, to float64
		fromRaw, hasFrom := s.has("from")
		if hasFrom {
			from = chartAnnotationFinite(w, fromRaw, path+".from")
		}
		toRaw, hasTo := s.has("to")
		if hasTo {
			to = chartAnnotationFinite(w, toRaw, path+".to")
		}
		if hasFrom && hasTo && from > to {
			failExpecting(CodeWrongType, path,
				"expected an ORDERED pair at "+path,
				"a range band runs from its lower value to its upper one, and this pair runs backwards; swapping the ends silently would draw a band the author did not describe")
		}
		if hasFrom {
			s.set("from", expectNumber(w, fromRaw, path+".from"))
		}
		if hasTo {
			s.set("to", expectNumber(w, toRaw, path+".to"))
		}
	case "XRange":
		var fromISO, toISO string
		if fromRaw, ok := s.has("from"); ok {
			var v Value
			v, fromISO = decodeChartAnnotationX(w, fromRaw, path+".from")
			s.set("from", v)
		}
		if toRaw, ok := s.has("to"); ok {
			var v Value
			v, toISO = decodeChartAnnotationX(w, toRaw, path+".to")
			s.set("to", v)
		}
		// Both dates are already known canonical and calendar-valid (the address
		// decoder refused anything else), and a canonical YYYY-MM-DD sorts
		// lexicographically exactly as it sorts chronologically — so no calendar
		// arithmetic is needed to decide the order at this boundary.
		if fromISO != "" && toISO != "" && fromISO > toISO {
			failExpecting(CodeWrongType, path,
				"expected an ORDERED pair at "+path,
				"a range band runs from its earlier date to its later one, and this pair runs backwards; swapping the ends silently would draw a band the author did not describe")
		}
	}
	return s.buildStrict(tag)
}

// chartAnnotationFinite reads a float slot and NARROWS §7 at it. §7 admits the
// quoted "NaN" / "Infinity" / "-Infinity" sentinels at every float slot and
// expectNumber reads them — the widening is deliberate and stays. But an
// annotation's value addresses a place on the VALUE AXIS, and a non-finite value
// names no such place: it would enter the domain computation and put every
// gridline, tick and mark at a NaN coordinate. The picture is not merely wrong at
// the annotation, it is wrong everywhere, and nothing downstream can recover it.
func chartAnnotationFinite(w *walkState, raw any, path string) float64 {
	v := expectNumber(w, raw, path)
	f, ok := chartAnnotationFloatOf(v)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		failExpecting(CodeWrongType, path,
			"expected a FINITE JSON number at "+path,
			"an annotation's value names a place on the value axis, and NaN / Infinity names none; give the value in the axis's own units, or drop the annotation")
	}
	return f
}

func chartAnnotationFloatOf(v Value) (float64, bool) {
	switch n := v.(type) {
	case Float:
		return float64(n), true
	case Int:
		return float64(n), true
	default:
		return 0, false
	}
}

// decodeChartAnnotation — a chart's data-addressed annotation (Phase 1490, §4l).
// One closed $type-discriminated union.
func decodeChartAnnotation(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, chartAnnotationCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionChartAnnotation, tag)
	// Every label rides the Phase 1143 text contract — carried, never resolved.
	s.field("label", decodeTextSource)
	switch tag {
	case "ReferenceLine":
		if raw, ok := s.has("value"); ok {
			chartAnnotationFinite(w, raw, path+".value")
			s.set("value", expectNumber(w, raw, path+".value"))
		}
	case "EventMarker":
		if raw, ok := s.has("at"); ok {
			at, _ := decodeChartAnnotationX(w, raw, path+".at")
			s.set("at", at)
		}
	case "RangeBand":
		s.field("range", decodeChartAnnotationRange)
	default:
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

func decodeChartAnnotationArray(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeChartAnnotation(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeTextSourceArray(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeTextSource(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeIntArray(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Int(expectInt(item, path+"["+strconv.Itoa(i)+"]"))
	}
	return out
}

func decodeStringArrayField(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = Str(expectString(item, path+"["+strconv.Itoa(i)+"]"))
	}
	return out
}

// decodeJSONValue is the strict structural decoder for structured JSON payload
// positions (Custom props / contentHash / exposedNodeIds, Mount capabilities).
func decodeJSONValue(w *walkState, raw any, path string) Value {
	return fromJSONStrict(w, raw, path)
}

// decodeJSONPassthrough is the null-LENIENT structural decoder — for positions
// that can legitimately carry a §5 obj-erased opaque seam (Mount inputs embed
// whole node trees, whose Binding.Static values may be null).
func decodeJSONPassthrough(w *walkState, raw any, _ string) Value {
	return fromJSON(raw)
}

// decodeGuestChannel decodes Mount's guest channel: direction is a closed DU
// (OutOnly | TwoWay); messageShape is an optional string riding on TwoWay.
func decodeGuestChannel(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordGuestChannel)
	if raw, ok := s.has("direction"); ok {
		direction := expectString(raw, path+".direction")
		if !channelDirectionCases.has(direction) {
			failExpecting(
				CodeUnknownDUCase,
				path+".direction",
				"unknown channel direction '"+direction+"'",
				"OutOnly | TwoWay",
			)
		}
		s.set("direction", Str(direction))
	}
	s.field("messageShape", decodeString)
	return s.buildStrict("")
}

// ── Drawing (Phases 524 / 642) — closed Shape / CurveCommand DUs ────────────
//
// Geometry is static numbers (a Drawing is a resolved artefact); only DrawStyle
// carries Bindings. The Shape and CurveCommand DUs are closed + typed — an
// unrecognised discriminator is UNKNOWN_DU_CASE (the typed-surface default-deny).
// Array positions use [i] bracket paths to match the reference reject paths
// ($.kind.shapes[0].$type / $.kind.shapes[0].commands[0].$type).

func decodeViewBox(w *walkState, raw any, path string) Value {
	return decodeRecordStrict(w, raw, path, &idl.RecordViewBox)
}

func decodeDrawPoint(w *walkState, raw any, path string) Value {
	return decodeRecordStrict(w, raw, path, &idl.RecordDrawPoint)
}

// decodeRecordStrict reads a record whose every field decodes by its declared
// type, in Ordinal key order (the order the first of several defects is
// reported at, WIRE_FORMAT.md section 29.3); unknown members are dropped.
func decodeRecordStrict(w *walkState, raw any, path string, r *idl.Record) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, r)
	s.readRowsOrdinal()
	return s.buildStrict("")
}

func decodeDrawPointArray(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeDrawPoint(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// decodeDrawStyle — every field optional, emitted only when present (byte-exact
// {} for an unstyled shape). fill/opacity/stroke/strokeWidth are Bindings; the
// text-only fields (Label) are bare enum / number / string; markId (Phase 642)
// is the keyed mark identity.
func decodeDrawStyle(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordDrawStyle)
	s.field("fill", decodeBindingString)
	s.field("stroke", decodeBindingString)
	s.field("opacity", decodeBindingFloat)
	s.field("strokeWidth", decodeBindingFloat)
	s.field("textAnchor", enumDecoder(textAnchorCases, "textAnchor", noAliases))
	s.field("fontSize", expectNumber)
	s.field("emphasis", decodeEmphasisEnum)
	s.field("fontFamily", decodeString)
	s.field("markId", decodeString)
	// rotation (Phase 877) — Label text rotation in degrees, clockwise. Absent
	// means upright, and an explicit 0 is a distinct present value. tip (Phase
	// 883) — the per-mark hover readout, a full TextSource; an explicitly EMPTY
	// tip is a distinct present value. Both key off presence, which is what a
	// row read does.
	s.field("rotation", expectNumber)
	s.field("tip", decodeTextSource)
	return s.buildStrict("")
}

func decodeCurveCommand(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, curveCommandCases, CodeUnknownDUCase)
	return decodeCaseStrict(w, obj, path, &idl.UnionCurveCommand, tag)
}

// decodeCaseStrict reads a union case whose every field decodes by its
// declared type, in Ordinal key order (the order the first of several defects
// is reported at, WIRE_FORMAT.md section 29.3); unknown members are dropped.
func decodeCaseStrict(w *walkState, obj map[string]any, path string, u *idl.Union, tag string) Value {
	s := newCaseSpec(w, obj, path, u, tag)
	s.readRowsOrdinal()
	return s.buildStrict(tag)
}

func decodeCurveCommandArray(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeCurveCommand(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeShape(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, shapeCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionShape, tag)
	// The style is read first (an absent one is the empty style,
	// absentLeniency); every other field follows in Ordinal key order.
	s.field("style", decodeDrawStyle)
	s.readRowsOrdinal()
	return s.buildStrict(tag)
}

func decodeShapeArray(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeShape(w, item, path+"["+strconv.Itoa(i)+"]")
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
	case "Text", "TextArea", "DateTime":
		return isStr(v, "")
	// `Rating` shares `Number`'s zero placeholder: an auto-bound rating starts
	// unrated, and the commonest one a reader sees is an average arriving
	// through a Query binding.
	case "Number", "RangedNumber", "Rating":
		return isNumericZero(v)
	case "Checkbox", "Toggle":
		return isBool(v, false)
	// The EMPTY LIST (Phase 1121) — the list is ordered and the order is the
	// reader's, so an auto-bound token field starts with no chips rather than
	// with a placeholder one.
	case "Tokens":
		arr, ok := v.(Arr)
		return ok && len(arr) == 0
	// The unset swatch (Phase 1130). A native colour input substitutes its own
	// default when handed nothing, and `#000000` is that default's wire
	// spelling — the one `#rrggbb` form the control can hold.
	case "Color":
		return isStr(v, "#000000")
	// `Combobox` shares `Choice`'s placeholder because §3.6.9 makes the value
	// contract Choice's normatively — a document migrating between the two
	// changes its `$type` and nothing else, which a divergent placeholder here
	// would break. With free text admitted an empty entry is genuinely NO value,
	// so it is null rather than "": one fact, one spelling.
	case "Choice", "SegmentedChoice", "Combobox":
		_, ok := v.(Null)
		return ok
	case "Range":
		o, ok := v.(Obj)
		if !ok || o.Tag != "" || len(o.Fields) != 2 {
			return false
		}
		return isNumericZero(o.Fields["min"]) && isNumericZero(o.Fields["max"])
	case "DateTimeRange":
		// ISO-empty both ends — the pair analogue of DateTime's "" placeholder.
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
// Phase 1811 — the section-16 lenient $type aliases of the two temporal form-field
// kinds: the pre-rename spellings (Date / DateRange, kept by the reference host's D8
// ruling) and the invented time-input spellings (Time / TimeRange, which also fix
// the variant). Admitted at dispatch, normalised to the canonical tag, and
// deliberately NOT part of formFieldCases, which is the attested vocabulary.
var (
	temporalFormFieldAliases = newCaseSet("Date", "Time", "DateRange", "TimeRange")
	formFieldCasesLenient    = newCaseSet(append(append([]string(nil), formFieldCases.names...), temporalFormFieldAliases.names...)...)
)

// canonicalTemporalTag maps a section-16 temporal alias to its canonical tag and
// returns every other tag unchanged.
func canonicalTemporalTag(tag string) string {
	switch tag {
	case "Date", "Time":
		return "DateTime"
	case "DateRange", "TimeRange":
		return "DateTimeRange"
	}
	return tag
}

// temporalVariant reads the variant of a DateTime / DateTimeRange field through
// the Time / TimeRange alias rule: under the canonical tag (or the pre-rename
// alias) the member is required as it always was; under the time-alias tag the
// alias SUPPLIES Time when it is absent, and an explicit member beside it must
// agree — a $type of Time carrying variant Date is refused as ambiguous rather
// than resolved to either, the 0.28.0 column-member posture applied to a $type.
func temporalVariant(s *spec, rawTag, timeAlias string) {
	if rawTag != timeAlias {
		s.field("variant", enumDecoder(dateTimeVariantCases, "variant", noAliases))
		return
	}
	// The alias spelling itself supplies the variant, so its absence here is
	// not the row's absence: read the key directly.
	raw, ok := s.take("variant")
	if !ok {
		s.set("variant", Str("Time"))
		return
	}
	if v := enumStr(raw, s.path+".variant", dateTimeVariantCases, "variant", noAliases); v != "Time" {
		fail(CodeWrongType, s.path+".variant", "a $type of "+timeAlias+" already fixes the variant to Time, and a different one beside it is ambiguous")
	}
	s.set("variant", Str("Time"))
}

func decodeFormFieldKind(w *walkState, raw any, path string, ab controlAutoBind) Value {
	obj := expectObject(raw, path)
	rawTag := dispatch(obj, path, formFieldCasesLenient, CodeUnknownDUCase)
	tag := canonicalTemporalTag(rawTag)
	s := newCaseSpec(w, obj, path, &idl.UnionFormFieldKind, tag)

	handler := func(name string) {
		s.field(name, decodeClosure)
	}
	valueSlot := func(dec fieldDecoder) {
		if raw, ok := s.has("value"); ok {
			v := dec(w, raw, path+".value")
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
	case "Checkbox", "Toggle":
		// Phase 766 — Toggle is Checkbox's data twin (a boolean with the same
		// write-back) under a distinct control / a11y contract (role="switch").
		// Phase 2177 — its change handler is `onChange`, as on every form-field kind.
		handler("onChange")
		valueSlot(decodeBindingBool)
	case "Choice":
		handler("onChange")
		s.field("options", decodeBindingSelectOptions)
		valueSlot(decodeBindingStringOpt)
	// Phase 1113 — the searchable form of `Choice`. `options` is the case's ONLY
	// required member (a combobox with no option source is not a control), and
	// `value` / `onChange` are Choice's, normatively: a document migrating
	// between the two changes its `$type` and nothing else, so the auto-bind and
	// the omitted-value contract are shared rather than re-stated.
	//
	// `allowFreeText` omits at `false` and the polarity is load-bearing: the
	// SHORTEST combobox document is the CONSTRAINED one, so an emitter that says
	// nothing gets the shape a `Select` would have had. A present member of any
	// type other than boolean is `WRONG_TYPE` and is NOT coerced — the slot
	// decides whether off-list values are admitted, and a lenient truthiness
	// read would widen the field on `"no"` and `"false"` alike.
	case "Combobox":
		handler("onChange")
		s.field("options", decodeBindingSelectOptions)
		valueSlot(decodeBindingStringOpt)
		s.field("allowFreeText", decodeBool)
	case "SegmentedChoice":
		handler("onChange")
		s.field("options", decodeBindingSelectOptions)
		valueSlot(decodeBindingStringOpt)
		// An absent orientation restores the language default Horizontal
		// (absentLeniency); the canonical wire always carries it.
		s.field("orientation", enumDecoder(orientationCases, "orientation", orientationAliases))
	// Phase 1121 — the multi-token field. `allowFreeText` omits at TRUE, the
	// OPPOSITE polarity to `Combobox`'s, and it is the one thing about this case
	// a host is most likely to get wrong: the default follows the required-ness
	// of the set, and where `Combobox.options` is required so "constrained" is
	// its resting state, `suggestions` is optional so "open" is this one's.
	case "Tokens":
		handler("onChange")
		// The omit-at-default value (true) is the row's; the refusal below reads
		// the decoded value whether or not it was omitted.
		s.field("allowFreeText", decodeBool)
		allowFreeText, written := s.fields["allowFreeText"]
		if !written {
			allowFreeText = Bool(true)
		}
		_, hasSuggestions := obj["suggestions"]
		s.field("suggestions", decodeBindingSelectOptions)
		// THE ONE DECODE REFUSAL, and it is a cross-member one: a closed field
		// with no suggestion source admits nothing typed and offers nothing to
		// pick, so the document names a control that CANNOT EXIST rather than
		// one with a bad value in it. Under the polarity above it is reachable
		// only deliberately, which is what makes refusing it right rather than
		// hostile. What is NOT refused: duplicate tokens and membership of a
		// token in the suggestion set are properties of the VALUE, invisible to
		// a decoder whenever the slot is bound.
		if isBool(allowFreeText, false) && !hasSuggestions {
			fail(CodeWrongType, path+".allowFreeText",
				"a suggestion source alongside allowFreeText:false — a closed token field with nothing to pick from admits no token at all (WIRE_FORMAT.md §3.6.19)")
		}
		valueSlot(decodeBindingStringList)
	// Phase 1130 — `max` is the case's only REQUIRED member: it is the scale,
	// and a rating with no declared ceiling is not a scale. A `max` below 1 is
	// REFUSED, not clamped — a scale with no positions has nothing to draw,
	// nothing to announce and no keystroke that could change anything.
	//
	// The asymmetry is deliberate: the SCALE is refused and the VALUE is not,
	// because a bound value is invisible to a decoder and a rule enforced only
	// on literals would be two rules wearing one name. The value is a FLOAT even
	// where nothing can type a fraction, because the commonest rating a reader
	// sees is an AVERAGE arriving through a Query binding.
	case "Rating":
		handler("onChange")
		s.field("max", decodeRatingMax)
		// Omits at `false`, and the polarity is load-bearing: the SHORTEST
		// rating document is the whole-star one. It governs ENTRY, never
		// DISPLAY.
		s.field("allowHalf", decodeBool)
		valueSlot(decodeBindingFloat)
	// Phase 1130 — `#rrggbb` and nothing else, because that is the one form a
	// native colour input can hold or return. Only the `Static` case is judged
	// here, and the split is recorded rather than hidden: a State / Query /
	// Selection binding carries its text from outside the document, where a
	// decoder cannot see it, and is re-checked by the pre-emit validator and the
	// server-side submission floor. CASE IS PRESERVED, never normalised — a
	// codec that lower-cased it would fail the round-trip this corpus pins.
	case "Color":
		handler("onChange")
		if raw, ok := s.has("value"); ok {
			v := decodeBindingString(w, raw, path+".value")
			if o, isObj := v.(Obj); isObj && o.Tag == "Static" && !isHexColour(o.Fields["value"]) {
				fail(CodeWrongType, path+".value",
					"a #rrggbb colour literal — six hexadecimal digits after a #, the one form a native colour input can hold (WIRE_FORMAT.md §3.6.17)")
			}
			if !isAutoBoundValue(ab, tag, v) {
				s.set("value", v)
			}
		}
	case "TextArea":
		handler("onChange")
		s.field("rows", decodeInt)
		valueSlot(decodeBindingString)
	case "RangedNumber":
		handler("onChange")
		valueSlot(decodeBindingFloat)
		s.field("min", expectNumber)
		s.field("max", expectNumber)
		s.field("step", expectNumber)
	case "Range":
		handler("onChange")
		if raw, ok := s.has("value"); ok {
			v := decodeRangeValue(w, raw, path+".value")
			if !isAutoBoundValue(ab, tag, v) {
				s.set("value", v)
			}
		}
	case "DateTime":
		// Phase 1811 — DateTime (was Date). rawTag may be the Date or Time alias.
		handler("onChange")
		valueSlot(decodeBindingString)
		temporalVariant(s, rawTag, "Time")
		s.field("min", decodeString)
		s.field("max", decodeString)
		s.field("step", expectNumber)
	case "DateTimeRange":
		// Range's pair mechanics with DateTime's value conventions: the value slot
		// is the bare ordered {from, to} pair, the scalars are DateTime's.
		handler("onChange")
		if raw, ok := s.has("value"); ok {
			v := decodeDateRangeValue(w, raw, path+".value")
			if !isAutoBoundValue(ab, tag, v) {
				s.set("value", v)
			}
		}
		temporalVariant(s, rawTag, "TimeRange")
		s.field("min", decodeString)
		s.field("max", decodeString)
		s.field("step", expectNumber)
	default:
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

// decodeRatingMax — the scale floor (Phase 1130), a DECODE rule rather than a
// validator one, on the same reasoning as `decodePositiveInt`'s `w` descriptor:
// a `max` of zero is not a small scale, it is one no host can draw, announce or
// change.
func decodeRatingMax(w *walkState, raw any, path string) Value {
	n := expectInt(raw, path)
	if n < 1 {
		fail(CodeWrongType, path, "expected an integer scale of 1 or more at "+path)
	}
	return Int(n)
}

// isHexColour reports whether a decoded Static value is a `#rrggbb` literal.
// Case is significant to the ROUND TRIP and not to this check: both cases are
// admitted here and neither is rewritten.
func isHexColour(v Value) bool {
	s, ok := v.(Str)
	if !ok || len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// `FormFieldKind` names the CONTROL; `FormField.rule` names the ACCEPTED SET.
// The two enums plus the cross-field operand, then the policy layer above the
// structural shape. Parity-locked with the reference host's `decodeFieldRule` /
// `formFieldNearMisses` and with the TypeScript tier's.
var (
	textFormatCases = newCaseSet(idl.EnumTextFormat.Cases...)
	compareOpCases  = newCaseSet(idl.EnumCompareOp.Cases...)
)

func decodeTextFormat(w *walkState, raw any, path string) Value {
	return Str(enumStr(raw, path, textFormatCases, "TextFormat", nil))
}

func decodeCompareOp(w *walkState, raw any, path string) Value {
	return Str(enumStr(raw, path, compareOpCases, "CompareOp", nil))
}

// decodeCompareRule — the cross-field operand. `against` is a Binding, and that
// IS the cross-field mechanism rather than an accident of typing: any read slot
// may take a Binding, and the auto-bind rule already puts every form field's
// value in State under the field's own id, so {"$type":"State","key":"<sibling
// id>"} reads the sibling with no coordination vocabulary at all.
func decodeCompareRule(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordCompareRule)
	s.field("op", decodeCompareOp)
	s.field("against", decodeBinding)
	return s.buildStrict("")
}

// decodeFieldRule — a field's declared constraint. Every slot is optional
// structurally, and two shapes are refused here as POLICY:
//
//   - a rule with every slot absent. A rule that constrains nothing is a defect,
//     not a no-op: it decodes, validates and renders while declaring nothing,
//     which is the fake-affordance shape the near-miss table also forecloses,
//     arriving through an empty object instead of a wrong key. `message` alone
//     does not rescue it — the message is the prose shown when some OTHER slot
//     is unmet, so a message-only rule is the help-text failure wearing the new
//     vocabulary's clothes.
//   - `minLength` above `maxLength`. The ordered-pair rule applied to a length
//     pair: an inverted bound admits no value at all, so the field can never be
//     submitted and the form is dead on arrival.
//
// Neither is a shape — both are relations BETWEEN slots — which is why they live
// here rather than in the structural layer.
func decodeFieldRule(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordFieldRule)
	s.field("format", decodeTextFormat)
	s.field("pattern", decodeString)
	s.field("minLength", decodeInt)
	s.field("maxLength", decodeInt)
	s.field("compare", decodeCompareRule)
	s.field("message", decodeTextSource)
	built := s.buildStrict("")

	constrains := false
	for _, k := range []string{"format", "pattern", "minLength", "maxLength", "compare"} {
		if _, ok := built.Fields[k]; ok {
			constrains = true
			break
		}
	}
	if !constrains {
		fail(
			CodeWrongType,
			path,
			"a rule that constrains nothing is a defect, not a no-op — declare at least one of "+
				"format / pattern / minLength / maxLength / compare, or omit 'rule' entirely",
		)
	}

	minV, hasMin := built.Fields["minLength"]
	maxV, hasMax := built.Fields["maxLength"]
	if hasMin && hasMax {
		lo, hi := int64(minV.(Int)), int64(maxV.(Int))
		if lo > hi {
			fail(
				CodeWrongType,
				path,
				"minLength "+strconv.FormatInt(lo, 10)+" is above maxLength "+strconv.FormatInt(hi, 10)+
					" — an inverted length bound admits no value at all, so the field could never be submitted",
			)
		}
	}
	return built
}

// formFieldNearMisses — the rule slot's rejected spellings. Small and enumerated
// for the same reason the grid's set is: tolerance of unknown keys is right for a
// field a future profile may add and wrong for a near miss of one that exists,
// because the tree then decodes and renders while constraining nothing.
var formFieldNearMisses = [][2]string{
	{"validation", "rule"},
	{"constraints", "rule"},
	{"validate", "rule"},
}

func decodeFormField(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	// The near-miss check runs BEFORE the rule decode, so a field carrying both
	// `validation` and a well-formed `rule` still names the ignored key. Named
	// as the FORM vocabulary (Phase 959): this host reported it against the
	// grid's until the check gained a vocabulary parameter, which sent the
	// author to the wrong document — the four other hosts always named it.
	checkNearMissesIn(obj, path, formFieldNearMisses, "form", formFieldNearMissConsequence)
	s := newRecordSpec(w, obj, path, &idl.RecordFormField)
	// Field alias: name — the HTML-forms prior for the field's identity. The
	// id decodes first so the auto-bind context can use it.
	s.field("id", decodeString, "name")
	id, _ := s.fields["id"].(Str)
	if raw, ok := s.has("kind"); ok {
		s.set("kind", decodeFormFieldKind(w, raw, path+".kind", controlAutoBind{formFieldID: true, name: string(id)}))
	}
	s.field("label", decodeTextSource)
	s.field("required", decodeBool)
	s.field("help", decodeTextSource)
	s.field("rule", decodeFieldRule)
	return s.buildStrict("")
}

func decodeFormFields(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeFormField(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

func decodeFilterItem(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordFilterSpec)
	// The chip name decodes first so the Filter auto-bind can use it.
	s.field("name", decodeString)
	name, _ := s.fields["name"].(Str)
	if raw, ok := s.has("kind"); ok {
		s.set("kind", decodeFormFieldKind(w, raw, path+".kind", controlAutoBind{filterChip: true, name: string(name)}))
	}
	s.field("label", decodeTextSource)
	return s.buildStrict("")
}

func decodeFilterItems(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeFilterItem(w, item, path+"["+strconv.Itoa(i)+"]")
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
func decodeToneMap(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	fields := make(map[string]Value, len(obj))
	for _, key := range ordinalKeys(obj) {
		entryPath := path + "." + key
		fields[key] = reissueToneMapEntry(w, obj[key], entryPath, key)
	}
	return Obj{Fields: fields}
}

func reissueToneMapEntry(w *walkState, raw any, entryPath, key string) (out Value) {
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
	return decodeTone(w, raw, entryPath)
}

// decodeTonedPill — the shared body of the canonical TonedPill case and the
// Pill-tagged §16 shorthand below. ONE reader, so the two spellings cannot drift
// apart in what they accept.
func decodeTonedPill(w *walkState, obj map[string]any, path string) Value {
	s := newCaseSpec(w, obj, path, &idl.UnionCellKindErased, "TonedPill")
	// `field` names the row property that is both the pill's label and the map key.
	s.field("field", decodeString)
	s.field("map", decodeToneMap, toneMapKeys[1:]...)
	// `default` is omitted-when-`Default` (the Phase 460 discipline); an absent key
	// restores the identity, and an aliased `Neutral` normalises to `Default` and
	// then omits — two rules composing, in that order.
	s.field("default", decodeTone)
	return s.buildStrict("TonedPill")
}

// decodeCellKindErased — a column's cell kind. Closure-bearing cases normalise
// to their canonical sentinel shapes; TonedPill (Phase 750) is the one case with
// no closure in it, which is exactly why it survives the wire.
func decodeCellKindErased(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, cellKindCases, CodeUnknownDUCase)
	// Lenient-ingest (WIRE_FORMAT.md §16, Phase 750): "pill" is the WORD for the
	// thing, so a declarative tone rule arrives tagged `Pill` more often than tagged
	// `TonedPill`. Before this phase those keys were accepted and DISCARDED — the
	// author's whole intent gone, silently, with no error to notice. Presence of a
	// tone map is the unambiguous tell: a closure `Pill` carries only labelFn/toneFn
	// and can never carry one.
	if tag == "Pill" && hasAnyKey(obj, toneMapKeys[:]) {
		return decodeTonedPill(w, obj, path)
	}
	if tag == "TonedPill" {
		return decodeTonedPill(w, obj, path)
	}
	// Every other case is its rows: closure slots read as the sentinel (a
	// required one admits absence, absentLeniency; an optional one is carried
	// exactly when written), labels as TextSource, ButtonGroup's buttons as
	// ButtonGroupItem records. Unknown members are dropped.
	return decodeCaseStrict(w, obj, path, &idl.UnionCellKindErased, tag)
}

// decodeButtonGroupItem — one ButtonGroup button, read from its record's rows.
func decodeButtonGroupItem(w *walkState, raw any, path string) Value {
	return decodeRecordStrict(w, raw, path, &idl.RecordButtonGroupItem)
}

// nearMiss — decode-time didactics for the grid-behaviour family's NEAR MISSES.
//
// Every shape in the tables below decoded SILENTLY before: §2 rule 2 tolerates
// unknown keys, so a model that reached for the wrong name got a tree that
// decoded, validated and rendered while the declaration did nothing — the
// fake-affordance failure in a new guise, and tolerance is what hid it. The
// narrowing is an ENUMERATED set with an unambiguous canonical form each; rule 2
// holds for everything else.
func nearMiss(path, found, canonical string) {
	nearMissIn(path, found, canonical, "grid", "")
}

// nearMissIn — the near-miss refusal with the vocabulary NAMED, and an optional
// trailing clause naming what the silence costs in that vocabulary. The message
// is didactic, and one that names the wrong vocabulary sends the author to the
// wrong document — which is why the four other hosts parameterise it and this
// one now does too.
func nearMissIn(path, found, canonical, vocabulary, consequence string) {
	failExpecting(
		CodeWrongType,
		path+"."+found,
		"'"+found+"' is not part of the "+vocabulary+" vocabulary — it would be ignored, not honoured"+consequence,
		canonical,
	)
}

// checkNearMisses walks the candidate table in declaration order, so which
// defect surfaces first is deterministic across hosts.
func checkNearMisses(obj map[string]any, path string, candidates [][2]string) {
	checkNearMissesIn(obj, path, candidates, "grid", "")
}

func checkNearMissesIn(obj map[string]any, path string, candidates [][2]string, vocabulary, consequence string) {
	for _, c := range candidates {
		if _, ok := obj[c[0]]; ok {
			nearMissIn(path, c[0], c[1], vocabulary, consequence)
		}
	}
}

// a11yNearMisses — the `Accessibility` trait's near-miss set (Phase 959, the
// Phase 863 discipline applied to the §3.1 trait).
//
// Rule 2's tolerance of unknown keys is right for a slot a future profile may
// add and wrong for a near miss of one that exists. That silence is sharper here
// than anywhere else in the vocabulary, for a reason peculiar to this trait: it
// has NO VISIBLE OUTPUT. A mislabelled column is on screen; an ignored
// `ariaLabel` looks identical to an honoured one from every side, so the refusal
// is the only feedback that can ever arrive.
//
// Refused rather than aliased. `ariaLabel` IS an unambiguous synonym, so
// admission turns on §16's other half — a shorthand earns its place by being a
// genuine assist to the emitting model, and a six-character key rename is not
// one. `live` settles it: the HTML idiom it comes from also spells a BOOLEAN, so
// an alias would bind a possibly-boolean prior onto a closed token set.
//
// `live` and `ariaLabel` are named by MEASURED evidence (6 and 1 emissions
// against `liveRegion`'s 12 and `label`'s 44, across 12,722 language-tier
// emissions); the rest of their families ride in with them.
var a11yNearMisses = [][2]string{
	{"aria-label", "label — the accessible name, a Binding<string> (a bare string is the §3.6 shorthand)"},
	{"ariaLabel", "label — the accessible name, a Binding<string> (a bare string is the §3.6 shorthand)"},
	{"aria-labelledby", "labelledBy — the id of a sibling node whose text carries the name"},
	{"ariaLabelledBy", "labelledBy — the id of a sibling node whose text carries the name"},
	{"labelledby", "labelledBy — the slot name is camelCase on the wire, not the ARIA attribute spelling"},
	{"aria-describedby", "describedBy — the id of a sibling node whose text carries the description"},
	{"ariaDescribedBy", "describedBy — the id of a sibling node whose text carries the description"},
	{"describedby", "describedBy — the slot name is camelCase on the wire, not the ARIA attribute spelling"},
	{"aria-role", "role — the ARIA role NAME as a bare string"},
	{"ariaRole", "role — the ARIA role NAME as a bare string"},
	{"aria-live", `liveRegion — the closed token set "polite" / "assertive" / "off"`},
	{"ariaLive", `liveRegion — the closed token set "polite" / "assertive" / "off"`},
	{"live", `liveRegion — the closed token set "polite" / "assertive" / "off"`},
	{"liveregion", `liveRegion — the closed token set "polite" / "assertive" / "off"`},
	{"aria-hidden", "hidden — a Binding<bool> (a bare bool is the §3.6 shorthand)"},
	{"ariaHidden", "hidden — a Binding<bool> (a bare bool is the §3.6 shorthand)"},
}

const a11yNearMissConsequence = ", and the intent would reach assistive technology as nothing at all"

const formFieldNearMissConsequence = ", and the field would accept anything"

// columnNearMisses — named by the census row itself. Deliberately NOT aliased to
// `editable: false`: an inverting alias that guesses wrong makes a read-only
// column editable.
var columnNearMisses = [][2]string{
	{"readOnly", "editable: false — the column flag NARROWS the grid's editable capability"},
}

// gridNearMisses — the grid-level rejected spellings.
var gridNearMisses = [][2]string{
	// The sharpest of them: a LITERAL page number is not expressible at all,
	// because the position lives in State so a control can move it. Ignoring it
	// is what produced the fake-affordance cluster.
	{"currentPage", "pageStateKey — the page POSITION lives in State as {\"page\": N} so the pager can move it; a literal page number is not expressible"},
	{"page", "pageStateKey — the page POSITION lives in State as {\"page\": N} so the pager can move it; a literal page number is not expressible"},
	{"pageIndex", "pageStateKey — the page POSITION lives in State as {\"page\": N}, 1-based (not a zero-based index)"},
	{"sortable", "sortStateKey on the grid + sortable on each COLUMN — grid-wide sortable is the staticRows spelling; a data-bound grid narrows per column"},
	{"onEdit", "editStateKey — the edit DESTINATION is a State key on the grid; onEdit is a per-cell host closure and carries no destination across the wire"},
	{"behaviour", "sibling fields on the grid (sortStateKey / pageStateKey / pageSize / editStateKey / defaultSort) — grid behaviour is not a nested record"},
	{"behavior", "sibling fields on the grid (sortStateKey / pageStateKey / pageSize / editStateKey / defaultSort) — grid behaviour is not a nested record"},
}

func decodeColumnErased(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	s := newRecordSpec(w, obj, path, &idl.RecordColumnErased)
	// Field aliases: `type` for `kind` (the universal JSON prior); `header` /
	// `title` for `label` (the react-table / antd prior). Phase 460 —
	// format/width omitted-when-default.
	s.field("kind", decodeCellKindErased, "type")
	s.field("label", decodeString, "header", "title")
	s.field("format", decodeCellFormat)
	s.field("width", decodeColumnWidth)
	s.field("value", decodeClosure)
	checkNearMisses(obj, path, columnNearMisses)
	// Phase 861 / 863 — per-column sort and editability NARROWING; absent
	// inherits the grid-level flag, so an explicit `false` is carried rather
	// than omitted-when-default (omitting it would erase the narrowing).
	s.field("sortable", decodeBool)
	s.field("editable", decodeBool)
	s.field("field", decodeString)
	return s.buildStrict("")
}

func decodeColumns(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		out[i] = decodeColumnErased(w, item, path+"["+strconv.Itoa(i)+"]")
	}
	return out
}

// decodeDefaultSort — Phase 801: the {column, direction} initial-order
// declaration on a static table. `column` is a NON-NEGATIVE index into
// `headers`; a negative (or non-integral) value is WRONG_TYPE, which is also
// what schema.json's `minimum: 0` says. An index PAST the end of `headers` is
// deliberately accepted — a relation between sibling values is not something a
// per-object codec judges.
func decodeDefaultSort(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordDefaultSort)
	s.field("column", decodeHeaderIndex)
	s.field("direction", enumDecoder(sortDirectionCases, "direction", noAliases))
	return s.buildStrict("")
}

// decodeHeaderIndex — a DefaultSort column: a non-negative header index.
func decodeHeaderIndex(w *walkState, raw any, path string) Value {
	column := expectInt(raw, path)
	if column < 0 {
		fail(CodeWrongType, path, "expected a non-negative integer header index at "+path)
	}
	return Int(column)
}

// decodeGridPageSize — Phase 862: how many rows a page holds. A page of zero or
// fewer rows names no page at all, so a non-positive (or non-integral) value is
// WRONG_TYPE, which is also what schema.json's `minimum: 1` says.
func decodeGridPageSize(w *walkState, raw any, path string) Value {
	n := expectInt(raw, path)
	if n < 1 {
		fail(CodeWrongType, path, "expected an integer page size of 1 or more at "+path)
	}
	return Int(n)
}

// decodeStaticRows — the {headers, rows} static-rows object of a read-only
// grid (also the shape the legacy Table decode-upgrade reads), plus the
// Phase 801 optional sort-intent slots. Both omitted re-encodes byte-identically
// to the pre-801 wire.
func decodeStaticRows(w *walkState, raw any, path string) Value {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordStaticRows)
	s.field("headers", decodeTextSourceArray)
	s.field("rows", decodeStaticRowCells)
	s.field("sortable", decodeBool)
	s.field("defaultSort", decodeDefaultSort)
	return s.buildStrict("")
}

// decodeStaticRowCells — StaticRows.rows: a list of rows of TextSource cells.
func decodeStaticRowCells(w *walkState, raw any, path string) Value {
	rowsArr := expectArray(raw, path)
	rows := make(Arr, len(rowsArr))
	for i, row := range rowsArr {
		rows[i] = decodeTextSourceArray(w, row, path+"["+strconv.Itoa(i)+"]")
	}
	return rows
}

// ── Per-kind typed decoders ─────────────────────────────────────────────────

// decodeSrcSet — §3.6.4's candidate list. `null` never reaches here as an
// empty list: `expectArray` refuses it with WRONG_TYPE at `$.kind.srcSet`,
// which is the whole of the missing-list-field decode class on this slot.
//
// The ARRAY ORDER is the author's and is preserved verbatim. Canonicalisation
// sorts object KEYS (§2) and never array elements, so a codec that sorted here
// would emit bytes differing from what it decoded for every document whose
// author did not happen to write the entries ascending. Presentation order is
// the RENDERER's obligation (ascending by width), which is why the two rules
// are not in tension.
func decodeSrcSet(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		s := newRecordSpec(w, expectObject(item, p), p, &idl.RecordSrcSetEntry)
		s.field("src", decodeBindingString)
		s.field("width", decodePositiveInt)
		out[i] = s.buildStrict("")
	}
	return out
}

// decodeTracks — §3.6.6's timed-text tracks (Phase 1110).
//
// The STRICTEST record on the wire: four of a `TrackEntry`'s five members are
// required. `srcLang` is required on EVERY kind, where HTML makes `srclang`
// mandatory only on subtitles — the extra strictness costs an author one value
// and buys a menu a user agent can order and a reader can tell apart, and there
// is no value to default to that would not be an invented claim about someone
// else's recording. `default` is the one omitted-at-`false` slot.
//
// The path carries the ARRAY INDEX (`$.kind.tracks[0].srcLang`), so a document
// with four tracks names the one at fault — the `srcSet` convention, and the
// position at which a host decoding array elements with a looser walker than its
// records gets it wrong.
func decodeTracks(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		s := newRecordSpec(w, expectObject(item, p), p, &idl.RecordTrackEntry)
		s.field("kind", enumDecoder(trackKindCases, "kind", noAliases))
		s.field("label", decodeTextSource)
		s.field("src", decodeBindingString)
		s.field("srcLang", decodeString)
		// Refused rather than coerced: two hosts ruling differently on
		// truthiness would disagree about which caption track opens, which is a
		// difference the reader meets on the first frame.
		s.field("default", decodeBool)
		out[i] = s.buildStrict("")
	}
	return out
}

// decodeTabHeaders — §3.6's `TabHeader` records (Phase 1677).
//
// A record nested one level inside an ARRAY, which is exactly the position a
// host walking elements with a looser walker than its records gets wrong: before
// this, every member of every header decoded structurally, so a header with no
// `label` at all, or a non-string `icon`, round-tripped byte-perfectly and
// rendered an unnamed tab.
//
// `label` is a `TextSource` and REQUIRED; `icon` is a BARE string, matching the
// reference host (whose icon read is a plain string) and every other icon slot
// here; `disabled` is a `Binding<bool>`, so a tab may be disabled by state
// rather than only by a literal.
//
// The path carries the ARRAY INDEX (`$.kind.tabHeaders[1].label`), so a document
// with four headers names the one at fault — the `decodeTracks` convention.
//
// `buildStrict` closes the record, dropping an unconsumed key rather than
// carrying it: a `TabHeader` is a closed record in the IDL, and the reference and
// Python hosts both build it closed. Tolerance for a later spec version's
// additions lives on the KIND, one level out.
func decodeTabHeaders(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		s := newRecordSpec(w, expectObject(item, p), p, &idl.RecordTabHeader)
		s.field("label", decodeTextSource)
		s.field("icon", decodeString)
		s.field("disabled", decodeBindingBool)
		out[i] = s.buildStrict("")
	}
	return out
}

// decodeEmbedPermissions — §3.6.8's sandbox relaxations (Phase 1111).
//
// Two refusals at the same path and they are different codes, deliberately: a
// non-string element is `WRONG_TYPE` (a host that read a bare `true` as a
// granted permission would have to invent WHICH one it names), and a recognised
// SHAPE carrying an unrecognised token is `UNKNOWN_DU_CASE`. Both report at the
// element's own path with no `.$type` suffix, because the slot holds bare enum
// strings.
func decodeEmbedPermissions(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		out[i] = Str(enumStr(item, p, embedPermissionCases, "permissions", noAliases))
	}
	return out
}

// decodeTreeItems — §3.6.12's self-referential rows (Phase 1120).
//
// `children` holds the SAME record, which makes this the format's first
// recursive shape, and the recursion is bounded on its own §21.5 axis rather
// than on the node axis, which structurally cannot see it.
//
// `children` omits at the EMPTY LIST and `icon` when absent, so a leaf carries
// exactly two keys — which is most of a real hierarchy, and a host emitting
// `"children":[]` on a leaf produces different bytes for most of a file listing.
//
// Duplicate row ids are NOT refused here. That is §8.1's position for `NodeId`
// and it transfers for §8.1's own reason: duplicate detection is a whole-tree
// property, a decoder streaming a document is not required to carry the id set,
// and there is no error code for it. The obligation sits with the emitter.
func decodeTreeItems(w *walkState, raw any, path string) Value {
	arr := expectArray(raw, path)
	out := make(Arr, len(arr))
	for i, item := range arr {
		p := path + "[" + strconv.Itoa(i) + "]"
		w.enterItem(p)
		s := newRecordSpec(w, expectObject(item, p), p, &idl.RecordTreeItem)
		s.field("id", decodeString)
		s.field("label", decodeTextSource)
		// Recursion. The child walker is the SAME walker as the root one — a
		// host whose child walk is looser than its root walk passes every
		// top-level reject vector and fails `reject-tree-nested-item-missing-id`.
		s.field("children", decodeTreeItems)
		s.field("icon", decodeString)
		out[i] = s.buildStrict("")
		w.exitItem()
	}
	return out
}

// decodePositiveInt — the `w` descriptor floor, a DECODE rule rather than a
// validator one. Zero is refused as firmly as a negative on purpose: a `0w`
// candidate is not a small image, it is one a client can never select, so
// admitting it would let the wire state a rendition no host can render.
func decodePositiveInt(w *walkState, raw any, path string) Value {
	n := expectInt(raw, path)
	if n <= 0 {
		fail(CodeWrongType, path, "expected a positive integer at "+path)
	}
	return Int(n)
}

// decodeUploadDestination — §3.6.10's streamed-upload destination (Phase 1117).
// The empty string is REFUSED rather than admitted: an absent member already
// means "the host's default", so an empty one names no destination at all and
// would be a document asking for something with nothing said anywhere.
func decodeUploadDestination(w *walkState, raw any, path string) Value {
	s := expectString(raw, path)
	if s == "" {
		fail(CodeWrongType, path, "expected a non-empty destination name at "+path)
	}
	return Str(s)
}

// decodeMediaKind — §3.6.6's `$type`-discriminated variant, nested at
// `kind.kind`. Closed at Video | Audio, so admitting a third surface later is
// an ADDITION rather than a re-meaning of shipped bytes.
//
// `Audio` has NO autoplay pathway — in the type, on the wire, or in the
// emission. That is stronger than a default of `false`: a slot that defaults
// to off is one a document can switch on, and there is no document this format
// wants to be able to state in which a page begins making sound unbidden. The
// arm below therefore reads NOTHING for Audio, and `buildStrict` drops a
// stray `autoplay` key rather than carrying it — the value has nowhere to
// land, which is exactly what the spec says happens to it.
func decodeMediaKind(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, mediaKindCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionMediaKind, tag)
	switch tag {
	case "Video":
		// `autoplay` is a DECLARATION whose RENDERING is constrained (never
		// without `muted`) — the wire carries no separate muted slot to fall
		// out of step with it. Omitted at false; refused rather than coerced
		// when stringified.
		s.field("autoplay", decodeBool)
		s.field("poster", decodeBindingString)
	case "Audio":
	default:
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

// kindBuilders holds the typed per-kind decoders. Kinds absent here (and from
// the dedicated Box / legacy handlers) decode structurally.
//
// handArms is this host's hand-written decoder arm set. It embeds the
// generated table arm set, so every kind idl.json declares has an arm; a kind
// listed here overrides its table arm because its reading carries behaviour
// this host owns (an alias, a coercion, a relational check), and consults the
// table for each field's optionality and omit-at-default value. The assertion
// below is the reverse guard: an arm set that is not total does not compile.
type handArms struct{ tableArms }

var _ kindArms = handArms{}

func (handArms) armHeading(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindHeading)
	s.field("level", decodeInt)
	s.field("text", decodeTextSource)
	s.field("variant", enumDecoder(headingVariantCases, "variant", headingVariantAliases))
	return s.build("Heading")
}

func (handArms) armMarkdown(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindMarkdown)
	s.field("text", decodeTextSource)
	return s.build("Markdown")
}

// 0.2.0 rename law — the scalar displayed value is `value` (`source`
// is reserved for collection feeds and is NOT accepted here — clean
// break); `data` stays as the web-prior alias. Phase 460 — the
// stylistic fields are omitted-when-default on both boundaries.
func (handArms) armMetric(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindMetric)
	s.field("label", decodeTextSource)
	s.field("value", decodeBindingFloat, "data")
	s.field("format", decodeCellFormat)
	s.field("tone", decodeTone)
	s.field("weight", decodeWeight)
	s.field("emphasis", decodeEmphasisEnum)
	s.field("trend", decodeBindingFloat)
	s.field("trendFormat", decodeCellFormat)
	// Phase 867 — the direction-of-good declaration. Omitted-when-default
	// on BOTH boundaries (§3.6.1 clause 4: an absent slot IS
	// `HigherIsBetter`), so an explicitly-spelled default normalises away
	// and a tree that never mentions polarity keeps its bytes.
	//
	// It is decoded rather than left to `build`'s structural passthrough,
	// and that is the point of this arm: passthrough round-trips the key
	// byte-for-byte while modelling nothing, so a misspelt case would ride
	// through to the renderer and be read as an unrecognised polarity —
	// green corpus, wrong tree. The closed set refuses it at the boundary
	// the way every other bare enum here is refused.
	s.field("trendPolarity", enumDecoder(trendPolarityCases, "trendPolarity", noAliases))
	s.field("icon", decodeString)
	s.field("subtext", decodeTextSource)
	return s.build("Metric")
}

// A labeled TEXT fact: `value` is a TextSource, so static / Bound /
// I18n values ride the label vocabulary. Only label + value required;
// tone / emphasis omitted-when-default on BOTH boundaries.
func (handArms) armFact(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindFact)
	s.field("label", decodeTextSource)
	s.field("value", decodeTextSource)
	s.field("tone", decodeTone)
	s.field("emphasis", decodeEmphasisFlag)
	s.field("help", decodeTextSource)
	s.field("icon", decodeString)
	return s.build("Fact")
}

func (handArms) armLabelValueRow(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindLabelValueRow)
	s.field("label", decodeTextSource)
	s.field("value", decodeBindingFloat, "data")
	s.field("format", decodeCellFormat)
	// 0.2.2 — the behavioural bool, omitted-when-false (aligning with
	// Fact's identical flag); the cross-vocabulary coercion accepts
	// the Emphasis enum + its aliases.
	s.field("emphasis", decodeEmphasisFlag)
	s.field("help", decodeTextSource)
	return s.build("LabelValueRow")
}

func (handArms) armBadge(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindBadge)
	s.field("label", decodeTextSource)
	s.field("variant", enumDecoder(badgeVariantCases, "variant", badgeVariantAliases))
	return s.build("Badge")
}

func (handArms) armCallout(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindCallout)
	s.field("body", decodeTextSource)
	// 0.2.0 — dismissable omitted-when-false; Phase 460 — tone
	// omitted-when-default. `title` aliases `heading`.
	s.field("dismissable", decodeBool)
	s.field("tone", decodeTone)
	s.field("heading", decodeTextSource, "title")
	s.field("icon", decodeString)
	return s.build("Callout")
}

func (handArms) armProgress(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindProgress)
	s.field("fraction", decodeBindingFloat)
	// 0.2.0 — indeterminate omitted-when-false.
	s.field("indeterminate", decodeBool)
	s.field("tone", decodeTone)
	s.field("label", decodeTextSource)
	s.field("caveat", decodeTextSource)
	return s.build("Progress")
}

func (handArms) armToast(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindToast)
	// 0.2.0 — the one omit-when-TRUE: a toast is dismissable unless
	// said otherwise.
	s.field("dismissable", decodeBool)
	s.field("message", decodeTextSource)
	s.field("open", decodeBindingBool)
	s.field("tone", decodeTone)
	return s.build("Toast")
}

func (handArms) armSkeleton(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindSkeleton)
	// Phase 1666 — the §21.9 row bound rides the field's own decoder, so
	// the spec table stays the single statement of a Skeleton's shape.
	s.field("rows", decodeSkeletonRows)
	return s.build("Skeleton")
}

// Phase 821 — the standalone icon-only display kind. `size`
// omitted-when-`Medium`; `tone` omitted-when-default (the Phase 460
// discipline); `label` omitted-when-decorative.
func (handArms) armIcon(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindIcon)
	s.field("icon", decodeString)
	s.field("size", enumDecoder(iconSizeCases, "size", noAliases))
	s.field("tone", decodeTone)
	s.field("label", decodeString)
	return s.build("Icon")
}

// source is a §5 typed Static float-series position; `data` aliases.
func (handArms) armSparkline(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindSparkline)
	s.field("source", decodeBindingFloatSeq, "data")
	return s.build("Sparkline")
}

// source is a §5 typed Static marker-list position; `data` / `markers`
// alias.
func (handArms) armMap(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindMap)
	s.field("centreLatitude", expectNumber)
	s.field("centreLongitude", expectNumber)
	s.field("source", decodeBindingMarkerSeq, "data", "markers")
	s.field("zoom", decodeInt)
	s.sentinel("onMarkerClick")
	return s.build("Map")
}

func (handArms) armLink(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindLink)
	s.field("download", decodeBool)
	s.field("href", decodeBindingString)
	s.field("label", decodeTextSource)
	// Phase 812 — optional closed enumeration; an unknown case is
	// UNKNOWN_DU_CASE at $.kind.protection.
	s.field("protection", enumDecoder(linkProtectionCases, "protection", noAliases))
	s.field("rel", decodeString)
	s.field("target", decodeString)
	return s.build("Link")
}

// Phases 1077 / 1078 / 1079 / 1080 — §3.6.2–§3.6.5. Five optional
// slots beside `variant`, and they do NOT all take the same posture:
//
//   - fit / aspectRatio / loading are identity-default TOKENS, dropped
//     at their default on the decode boundary so an explicit `Natural`
//     / `Eager` canonicalises to the omitted form (§3.6's table).
//   - caption is CONTENT, not a presentation token — there is no
//     default caption the way there is a default fit — so it takes the
//     ordinary optional-field posture (rule 4). It is a full
//     `TextSource`, NOT a string: narrowing the slot costs nothing
//     until somebody needs a locale, which is why the enveloped
//     `{"$type":"Literal"}` form has its own lenient fixture here.
//   - srcSet IS an omit-at-default field whose identity is the EMPTY
//     LIST rather than a token. Absent and `[]` denote the same
//     document; a present `null` is refused, because absence already
//     has a spelling and a second one would let two conformant hosts
//     emit different canonical bytes for one document.
//   - expandable is a plain bool omitted at `false`, refused rather
//     than coerced when stringified — a truthiness rule would have to
//     rule on `"false"` and `""` too, and two hosts ruling differently
//     would disagree about whether the document declares an affordance
//     at all.
func (handArms) armImage(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindImage)
	s.field("alt", decodeTextSource)
	s.field("aspectRatio", enumDecoder(imageAspectCases, "aspectRatio", noAliases))
	s.field("caption", decodeTextSource)
	s.field("expandable", decodeBool)
	s.field("fit", enumDecoder(imageFitCases, "fit", noAliases))
	s.field("loading", enumDecoder(imageLoadingCases, "loading", noAliases))
	s.field("src", decodeBindingString)
	s.field("srcSet", decodeSrcSet)
	s.field("variant", enumDecoder(imageVariantCases, "variant", noAliases))
	return s.build("Image")
}

// Phase 1076 — §3.6.6. The shared record carries the source, the
// mandatory accessible name, and the two shared declarations;
// `controls` is omitted at TRUE (the second such slot in the
// vocabulary after `Toast.dismissable`, and the polarity is
// deliberate — a media element without a transport cannot be paused,
// seeked or muted by a keyboard user at all, so the accessible setting
// is what a document gets for free and taking it away is the deviation
// that costs a key). `loop` takes the ordinary polarity.
func (handArms) armMedia(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindMedia)
	s.field("controls", decodeBool)
	s.field("kind", decodeMediaKind)
	s.field("label", decodeTextSource)
	s.field("loop", decodeBool)
	s.field("src", decodeBindingString)
	// Phase 1110 — both live on the SPEC rather than on
	// `MediaKind.Video`, and the second placement is the one worth
	// explaining: a transcript is the affordance an AUDIO surface needs
	// most, because a recording with no visual channel has nowhere else
	// to put its words.
	//
	// `tracks` is omitted when EMPTY — an absent list and an empty one
	// denote the same document — so it drops rather than encoding `[]`.
	// `transcript` is an ORDINARY optional: absent means the document
	// offers no transcript, which is a different statement from
	// offering an empty one, so it does not drop at "".
	s.field("tracks", decodeTracks)
	s.field("transcript", decodeTextSource)
	return s.build("Media")
}

// Phase 1111 — §3.6.8. `title` is REQUIRED on `MediaSpec.label`'s
// argument one kind over: a frame is a focus container a reader tabs
// INTO, so there is no decorative embed, and a frame with no accessible
// name is announced as "frame" and nothing else.
//
// `permissions` omits at the EMPTY list and empty means TOTAL DENIAL —
// that polarity is the design rather than a consequence of the omit
// rule, so the wire-cheapest document is also the most locked-down one.
// `aspectRatio` REUSES `ImageAspect` (the cases are pure layout ratios
// with nothing image-specific in them) and omits at `Natural`.
func (handArms) armEmbed(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindEmbed)
	s.field("aspectRatio", enumDecoder(imageAspectCases, "aspectRatio", noAliases))
	s.field("permissions", decodeEmbedPermissions)
	s.field("src", decodeBindingString)
	s.field("title", decodeTextSource)
	return s.build("Embed")
}

// Phase 1120 — §3.6.12. Expansion and selection are named State keys and
// there is no `expandable` / `selectable` boolean: a behaviour the
// reader drives is declared as a key the host both writes and reads, and
// a flag with no key behind it is a decorative control writing state
// nothing reads.
func (handArms) armTree(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindTree)
	s.field("expandedStateKey", decodeString)
	s.field("items", decodeTreeItems)
	s.sentinel("onSelect")
	s.field("selectionStateKey", decodeString)
	return s.build("Tree")
}

func (handArms) armList(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindList)
	s.field("items", decodeTextSourceArray)
	s.field("ordered", decodeBool)
	return s.build("List")
}

func (handArms) armCodeBlock(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindCodeBlock)
	s.field("code", decodeString)
	s.field("copyable", decodeBool)
	s.field("highlightLines", decodeIntArray)
	s.field("language", decodeString)
	s.field("lineNumbers", decodeBool)
	return s.build("CodeBlock")
}

func (handArms) armMath(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindMath)
	s.field("display", enumDecoder(mathDisplayCases, "display", noAliases))
	s.field("source", decodeString)
	return s.build("Math")
}

// Phases 524 / 642 — geometry static; the closed Shape / CurveCommand
// DUs default-deny an unknown discriminator; DrawStyle carries the
// bindings + the optional markId.
func (handArms) armDrawing(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindDrawing)
	s.field("description", decodeTextSource)
	s.field("shapes", decodeShapeArray)
	s.field("style", decodeDrawStyle)
	s.field("title", decodeTextSource)
	s.field("viewBox", decodeViewBox)
	return s.build("Drawing")
}

// decodeSelectValue applies the Phase 1962 `value` rule (WIRE_FORMAT.md, "Select
// multi-select"), a relation between two sibling keys the IDL row cannot state
// (it marks `value` optional): a single-select (`multiple` absent or false)
// REQUIRES `value`; a multi-select carries its selection in `values` and no
// `value`. On a multi-select a present `value` is decoded exactly as before — so
// a malformed binding still refuses as any malformed binding does — and then
// DROPPED (a §16 lenient accept: the control never reads it, and earlier
// documents carrying a placeholder or a real binding there keep reading). A
// malformed `multiple` is its own defect (reported when `multiple` itself
// decodes), so the presence rule is not applied: `value` decodes if present and
// is not demanded. The raw `multiple` is peeked rather than decoded here so the
// field decode order — and with it which defect surfaces first — is unchanged.
func decodeSelectValue(s *spec) {
	rawMultiple, hasMultiple := s.obj["multiple"]
	isBoolMultiple := false
	multiple := false
	if hasMultiple {
		// The same unwrap decodeBool applies (a Static-wrapped bool is a bool).
		multiple, isBoolMultiple = unwrapStaticAny(rawMultiple).(bool)
	}
	switch {
	case !hasMultiple || (isBoolMultiple && !multiple):
		raw, ok := s.has("value")
		if !ok {
			fail(CodeMissingField, s.path+".value", "missing required field 'value'")
		}
		s.fields["value"] = decodeBindingStringOpt(s.w, raw, s.path+".value")
	case !isBoolMultiple:
		s.field("value", decodeBindingStringOpt)
	default:
		raw, ok := s.has("value")
		if !ok {
			return
		}
		// Decode for its defects, then drop: the value is never stored.
		decodeBindingStringOpt(s.w, raw, s.path+".value")
	}
}

// The handler fields are OPTIONAL: omitted on the wire when the
// control is declarative; present as the "<closure>" sentinel when
// closure-authored. `options` / `data` alias `source`. Phase 291 —
// values omitted when absent. Phase 1962 — `value` follows the single/multi
// rule (decodeSelectValue), and `multiple` is an ordinary optional field kept
// AS AUTHORED: an explicit false survives the round trip (it is not
// omit-at-default; nodes/select-multiple-false.json).
func (handArms) armSelect(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindSelect)
	s.field("label", decodeTextSource)
	s.sentinel("onChange")
	s.field("source", decodeBindingSelectOptions, "options", "data")
	decodeSelectValue(s)
	s.field("disabled", decodeBindingBool)
	s.field("placeholder", decodeTextSource)
	s.field("multiple", decodeBool)
	s.field("values", decodeBindingStringList)
	s.sentinel("onChangeMulti")
	return s.build("Select")
}

// onDismiss is optional and — unlike the closure-sentinel handlers — a
// genuine wire-survivable Action, so it decodes null-strict when
// present. `title` aliases `heading`.
func (handArms) armModal(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindModal)
	s.field("children", decodeChildren)
	s.field("dismissable", decodeBool)
	s.field("onDismiss", decodeAction)
	s.field("open", decodeBindingBool)
	s.field("heading", decodeTextSource, "title")
	// Phase 1119 — the closed `Modal | Popover` pair, omitted at
	// `Modal`. A bare enum (§3.5), so an unrecognised token reports at
	// the slot's own path with no `.$type` suffix — there is no
	// discriminator on the wire to name.
	s.field("modality", enumDecoder(modalityCases, "modality", noAliases))
	return s.build("Modal")
}

func (handArms) armScrollArea(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindScrollArea)
	s.field("children", decodeChildren)
	s.field("orientation", enumDecoder(scrollOrientationCases, "orientation", noAliases))
	s.field("maxHeight", decodeInt)
	s.field("maxWidth", decodeInt)
	return s.build("ScrollArea")
}

func (handArms) armButton(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindButton)
	s.field("label", decodeTextSource)
	s.field("onClick", decodeAction)
	s.field("variant", enumDecoder(buttonVariantCases, "variant", buttonVariantAliases))
	s.field("icon", decodeString)
	s.field("disabled", decodeBindingBool)
	return s.build("Button")
}

func (handArms) armForm(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindForm)
	s.field("fields", decodeFormFields)
	s.field("onSubmit", decodeAction)
	s.field("submitLabel", decodeTextSource)
	s.field("disabled", decodeBindingBool)
	return s.build("Form")
}

func (handArms) armFilters(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindFilters)
	s.field("items", decodeFilterItems)
	return s.build("Filters")
}

func (handArms) armFileUpload(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindFileUpload)
	s.field("accept", decodeStringArrayField)
	s.field("label", decodeTextSource)
	s.field("multiple", decodeBool)
	// The select handler is an OPTIONAL closure slot (the IDL row and the
	// §3.2 table have said so since the handler sweep; the spec's prose
	// caught up in Phase 1962): present as the sentinel when closure-authored,
	// absent on a declarative upload — kept absent, never synthesised.
	s.sentinel("onSelect")
	s.field("disabled", decodeBindingBool)
	// Phase 1115 — the two ADDITIONAL ingestion gestures, each omitted
	// at `false`. Neither replaces the picker: the `<input type=file>`
	// and its label are emitted whatever a document declares, which is
	// what keeps the keyboard route intact and the no-script floor a
	// working upload rather than an inert box.
	s.field("dropTarget", decodeBool)
	s.field("acceptPaste", decodeBool)
	// Phase 1116 — the capture source, a closed `Camera | Microphone`
	// pair (a bare enum, so the path carries no `.$type`).
	s.field("capture", enumDecoder(captureSourceCases, "capture", noAliases))
	// Phase 1117 — the streamed upload's destination. A non-empty
	// string: an empty one names no destination at all, so it is
	// refused rather than read as "the default", which is what an
	// absent member already means.
	s.field("destination", decodeUploadDestination)
	// Phase 1548 — the two declared ceilings. Optional: absent declares
	// no ceiling, which is the pre-1548 control and the wire identity.
	// Both go through `decodePositiveInt`, so §7.1's slot rule decides
	// the shape (a fractional value is not truncated, a value beyond the
	// signed 32-bit slot is refused naming the slot's width) before the
	// positive floor decides the sign. Zero is refused as firmly as a
	// negative: a ceiling of zero is a control that can accept nothing,
	// and the author who means "no ceiling" omits the member.
	s.field("maxBytes", decodePositiveInt)
	s.field("maxFiles", decodePositiveInt)
	return s.build("FileUpload")
}

// 0.2.0 — editable omitted-when-false; `data` / `rows` alias `source`.
func (handArms) armDataGrid(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindDataGrid)
	s.field("columns", decodeColumns)
	s.field("editable", decodeBool)
	// Phase 934 — declarative row reorder; omitted-when-false, the same
	// convention as `editable`.
	s.field("reorderable", decodeBool)
	s.field("source", decodeBindingRows, "data", "rows")
	s.sentinel("onRowClick")
	s.sentinel("rowKey")
	s.field("rowKeyField", decodeString)
	// Phase 818 — the grid-sort header affordance: names the State key
	// carrying the sort descriptor `{column, direction}` a data-bound
	// grid's runtime sorts by (and whose headers write it). Typed as a
	// string; encode-omitted when absent.
	s.field("sortStateKey", decodeString)
	// Phase 862 — declarative pagination. `pageStateKey` names the State
	// key carrying `{"page": N}` (1-based); `pageSize` is how many rows a
	// page holds. A page of zero or fewer rows names no page at all, so
	// it is WRONG_TYPE — which is also what schema.json's `minimum: 1`
	// says. The pager that writes the key is renderer-owned, so nothing
	// here decodes a control.
	s.field("pageSize", decodeGridPageSize)
	// Phase 861 — the bound path's declared initial order, decoded by
	// the SAME function the staticRows spelling uses: same record, same
	// bound, same message at a different path.
	s.field("defaultSort", decodeDefaultSort)
	checkNearMisses(obj, path, gridNearMisses)
	// Phase 863 — the declared edit destination.
	s.field("editStateKey", decodeString)
	s.field("pageStateKey", decodeString)
	s.field("staticRows", decodeStaticRows)
	// Phase 1125 — the CSV export affordance, omitted at `false`.
	s.field("exportable", decodeBool)
	// Phase 1473 — the grid's half of the print-break vocabulary. There
	// is no `breakBefore` here (that is a wrapper's job); what survives
	// on a grid is whether its rows may fragment and whether the header
	// repeats on each page.
	s.field("keepRowsTogether", decodeBool)
	s.field("repeatHeader", decodeBool)
	// Phase 1123 — cross-container drag. Two grids naming the same key
	// exchange rows; a grid declaring only `transferInKey` receives
	// without giving up its own.
	s.field("transferInKey", decodeString)
	s.field("transferOutKey", decodeString)
	// Phase 1892 — the row window and the declared total. `windowStateKey`
	// names the State key carrying the window descriptor
	// `{"offset": N, "count": M}`; `rowTotal` is an int Binding decoded
	// exactly as the other int Binding slots (Tabs.activeIndex /
	// Stepper.activeStep). Both are encode-omitted when absent. The
	// renderer applies the window (Phase 1912: renderer.windowedGrid).
	s.field("rowTotal", decodeBindingInt)
	s.field("windowStateKey", decodeString)
	return s.build("DataGrid")
}

// Phase 1585 — `stacked` is OMITTED-WHEN-FALSE on both boundaries. It
// used to be set unconditionally, so absence and an explicit `false`
// both re-encoded as `"stacked":false`; the IDL now declares the member
// `omitDefault false`, which makes the omitted form canonical and the
// explicit one a §3.6 lenient accept that normalises to it. `optDrop`
// is the seam every other omitted-when-default flag already uses.
func (handArms) armChart(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindChart)
	s.field("kind", enumDecoder(chartKindCases, "kind", noAliases))
	s.field("source", decodeBindingRows, "data")
	s.field("stacked", decodeBool)
	s.field("xField", decodeString)
	s.field("yFields", decodeStringArrayField)
	s.field("title", decodeTextSource)
	// Phase 1490 — `annotations` (§4l): the data-addressed attachments —
	// reference lines, event markers and range bands — as one closed
	// union. Absent OMITS on the wire, so every pre-1490 document decodes
	// and re-encodes byte-for-byte as it did. An EMPTY list is a different
	// document from an absent field and is carried as such.
	s.field("annotations", decodeChartAnnotationArray)
	s.sentinel("onPointClick")
	return s.build("Chart")
}

func (handArms) armCustom(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindCustom)
	s.field("moduleId", decodeString)
	s.field("componentId", decodeString)
	s.field("props", decodeJSONValue)
	s.field("contentHash", decodeJSONValue)
	s.field("exposedNodeIds", decodeJSONValue)
	return s.build("Custom")
}

// Tabs / Stepper — BOTH SPECS ARE NOW COMPLETE against the corpus IDL's
// `kinds` entry for each, every member typed to the reference host's own
// reading (Phase 1677; the Python host took the same step at the same
// scope in Phase 1654, and this is the Go half of that pair).
//
// The history is worth keeping, because it is what the completion is
// measured against. Both kinds were WHOLLY structural before Phase 1064,
// which is why this host answered `reject-binding-int-bool` and
// `reject-binding-int-sentinel-string` by ACCEPTING them while it refused
// every float vector: the typed float parsers existed and were routed,
// and these two kinds had no typed decoder to route anything through.
// Phase 1064 typed the one `Binding<int>` slot and nothing else,
// deliberately — the blast radius was the slot that phase was about — and
// recorded the rest as residue. This is that residue.
//
// What each member's treatment comes from, and nothing invented on top:
//   - `activeIndex` — omit-at-default `Static 0` (Phase 1585). This
//     host's model is structural, so DROPPING the member on decode is
//     what stops it being emitted; an explicit `{"$type":"Static",
//     "value":0}` carried through would make the pre-1585 spelling a
//     SECOND canonical form.
//   - `orientation` — omit-at-default `Horizontal`, the ordinary §3.6
//     enum treatment, with the `Row`/`Column` aliases every other
//     orientation slot in this host accepts.
//   - `children` — REQUIRED on both, and a list of nodes. Leaving a
//     node-valued position structural is the one omission that costs more
//     than it saves: the nodes inside it never reach the node decoder at
//     all, so nothing in them is counted, bounded or typed.
//   - `tabHeaders` — an array of `TabHeader` records whose `label` is
//     REQUIRED, so a header with no label is a MISSING_FIELD rather than a
//     document that round-trips perfectly and renders an unnamed tab.
//   - `tabTags` — an array of plain strings, refused by index.
//   - `activeTag` — the tag-side selection, a `Binding<string>`; the
//     second way a `Tabs` is live, which the validator already reads.
//   - `onSelect` / `onSelectTag` — `fn` slots, presence-only and
//     normalised to the closure sentinel.
//
// `Stepper.activeStep` is OMIT-AT-DEFAULT `Static{value=0}` since Phase 2300
// (the declared-default rule), the `Tabs.activeIndex` shape: the IDL row says
// so, the generated table applies it, and this host reads the decision from
// the row rather than recording one. (Until Phase 2300 the row was `required`
// and this host held it so on the argument that a stepper with no declared
// step has no position; the vocabulary now declares the position, and
// `lenient/lenient-2300-declared-default-bindings.json` pins the explicit
// form normalising away. `wire/tabs_stepper_test.go` carries the pin.)
//
// `buildStrict` is deliberately NOT used on the kind: an unknown key there
// is still preserved structurally (§2 rule 2 tolerance). Strictness is
// applied one level in, inside `TabHeader`, where the reference and the
// Python host both build a closed record.
func (handArms) armTabs(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindTabs)
	s.field("activeIndex", decodeBindingInt)
	s.field("children", decodeChildren)
	s.field("orientation", enumDecoder(orientationCases, "orientation", orientationAliases))
	s.field("tabHeaders", decodeTabHeaders)
	s.field("tabTags", decodeStringArrayField)
	s.field("activeTag", decodeBindingString)
	s.sentinel("onSelect")
	s.sentinel("onSelectTag")
	return s.build("Tabs")
}

func (handArms) armStepper(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindStepper)
	s.field("activeStep", decodeBindingInt)
	s.field("children", decodeChildren)
	s.sentinel("onSelect")
	return s.build("Stepper")
}

// State-bound conditional child. Duplicate match values are NOT a
// decode error (first-match-wins; the validator flags them).
// Phase 768 — the selector is any Binding: a non-State selector rides
// an `on` key holding the canonical Binding wire form, while the
// State(key) form keeps the compact `stateKey` spelling (the reference
// encoders' collapse rule, applied on the decode boundary here since
// this host's encoder is generic). Both absent keeps the stateKey
// MISSING_FIELD, so the reject fixture's error is unchanged; `on` wins
// when both are present.
func (handArms) armSwitch(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindSwitch)
	s.field("cases", decodeSwitchCases)
	s.field("default", decodeSingleNode)
	if raw, ok := s.take("on"); ok {
		s.take("stateKey") // consumed — `on` is authoritative
		on := decodeBinding(w, raw, path+".on")
		if o, isObj := on.(Obj); isObj && o.Tag == "State" && len(o.Fields) == 1 {
			s.set("stateKey", o.Fields["key"])
		} else {
			s.set("on", on)
		}
	} else {
		// Relational, so hand: the table declares `on` and `stateKey` each
		// optional, and exactly one of the pair names the selector.
		s.req("stateKey", decodeString)
	}
	// Phase 1122 — the timed advance, in whole milliseconds. Zero, a
	// negative and a fraction are all REFUSED rather than clamped or
	// rounded: none of them names an interval, and a carousel that
	// advanced on a rounded guess would be a reading pace the document
	// never asked for.
	s.field("autoAdvanceMs", decodePositiveInt)
	return s.build("Switch")
}

// Phase 1670 — the parameterised fragment's two OMIT-AT-DEFAULT slots.
//
// `WIRE_FORMAT.md`'s parameterised-fragment section has always said a
// zero-hole declaration omits `holes` and a pure-deterministic one omits
// `effect`, and since Phase 1670 it says so as a MUST: the redundant
// `"holes":[]` and `{"determinism":"Deterministic","hostEffect":"Pure"}`
// are decode-accepted input, never a second canonical spelling. This
// host decoded the kind with no builder at all, so it echoed whatever it
// was handed and one document re-encoded to two different byte sequences
// depending on which host read it — invisible to this host's own
// round-trip gate, which compares its re-encode against bytes it
// produced itself.
//
// DELIBERATELY the whole builder, and deliberately structural otherwise:
// `build` preserves every unconsumed key exactly as the fall-through
// did, so `body`, `name` and any key a later spec version adds are
// untouched. This arm narrows the canonical form and changes nothing
// about what is accepted or carried.
func (handArms) armFragmentDecl(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindFragmentDecl)
	s.field("holes", decodeJSONPassthrough)
	s.field("effect", decodeJSONPassthrough)
	return s.build("FragmentDecl")
}

// Isolation/embedding boundary (§4o). inputs passes through WITHOUT
// null-strictness — it embeds whole node trees whose Binding.Static
// values are §5 opaque seams.
func (handArms) armMount(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindMount)
	s.field("scopeId", decodeString)
	s.field("channel", decodeGuestChannel)
	s.field("capabilities", decodeJSONValue)
	s.field("inputs", decodeJSONPassthrough)
	return s.build("Mount")
}

// ── Box (the unified container) + legacy decode-upgrade ─────────────────────

func decodeBoxLayout(w *walkState, raw any, path string) Value {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, boxLayoutCases, CodeUnknownDUCase)
	s := newCaseSpec(w, obj, path, &idl.UnionBoxLayout, tag)
	switch tag {
	case "Flex":
		s.field("direction", enumDecoder(orientationCases, "direction", orientationAliases))
		s.field("wrap", decodeBool)
		s.field("gap", decodeInt)
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
		if !hasCols && !hasColumns {
			// templateColumns present ⇒ cols is documented-ignored, so an
			// absent cols is 1 — relational to templateColumns, so hand.
			s.set("cols", Int(1))
		} else {
			// `columns` aliases `cols`.
			s.field("cols", decodeInt, "columns")
		}
		s.field("gap", decodeInt)
		s.field("templateColumns", decodeString)
	case "Masonry":
		// WIRE_FORMAT §3.6.7 — column-fill. `cols` is REQUIRED and POSITIVE, on
		// the §3.6.4 srcSet width-floor pattern: `column-count: 0` is invalid
		// CSS, so a container declaring it would fall back to whatever the host
		// stylesheet last said and the wire would be carrying a host-defined
		// layout.
		//
		// No auto-column leniency here, unlike Grid above, and the asymmetry is
		// deliberate rather than an omission: Grid canonicalises a column-less
		// spec to Auto because the language already owns that concept, whereas
		// Auto is a ROW-fill mode — rewriting a masonry into it would discard
		// the author's intent rather than recover it.
		s.field("cols", decodeColumnCount, "columns")
		s.field("gap", decodeInt)
	case "Auto":
	default:
		return s.build(tag)
	}
	return s.buildStrict(tag)
}

// decodeColumnCount — Masonry's cols: a positive integer.
func decodeColumnCount(w *walkState, raw any, path string) Value {
	cols := expectInt(raw, path)
	if cols <= 0 {
		fail(CodeWrongType, path, "expected a positive integer column count at "+path)
	}
	return Int(cols)
}

func decodeBox(w *walkState, obj map[string]any, path string) Obj {
	s := newKindSpec(w, obj, path, &idl.KindBox)
	s.field("children", decodeChildren)
	s.field("role", enumDecoder(boxRoleCases, "role", noAliases))
	s.field("layout", decodeBoxLayout)
	// `title` aliases `heading` (the web prior for a card's caption).
	s.field("heading", decodeTextSource, "title")
	// Phase 1473 — the two print-break booleans (§ "Print break control"), each
	// omitted at `false` on BOTH boundaries: `keepTogether` says this container
	// and its whole subtree stay on one page, `breakBefore` that it starts at
	// the top of a fresh one. A non-boolean is WRONG_TYPE at the member's own
	// path rather than coerced — these are page-layout instructions, and a
	// truthiness read would honour `"no"`.
	s.field("breakBefore", decodeBool)
	s.field("keepTogether", decodeBool)
	return s.buildStrict("Box")
}

// armBox — Box decodes with its own strict reader: an unknown key is dropped,
// not preserved.
func (handArms) armBox(w *walkState, obj map[string]any, path string) Obj {
	return decodeBox(w, obj, path)
}

// ── Kind / style / state / node envelope ────────────────────────────────────

func decodeKind(w *walkState, raw any, path string) Obj {
	obj := expectObject(raw, path)
	tag := dispatch(obj, path, knownKinds, CodeWrongNodeKind)
	return dispatchKindArm(handArms{}, w, tag, obj, path)
}

// styleMember is one SemanticStyle member: its wire name, its per-field
// decoder and its identity default (filled from the table at start-up). styleMembers lists them in the order the
// decoders read them, so which defect surfaces first is fixed.
type styleMember struct {
	name     string
	dec      func(w *walkState, raw any, path string) Value
	identity string
}

var styleMembers = []styleMember{
	{"emphasis", decodeEmphasisEnum, ""},
	{"tone", decodeTone, ""},
	{"weight", decodeWeight, ""},
	{"role", func(w *walkState, raw any, path string) Value {
		return Str(enumStr(raw, path, styleRoleCases, "role", noAliases))
	}, ""},
	{"voice", func(w *walkState, raw any, path string) Value {
		return Str(enumStr(raw, path, fontVoiceCases, "voice", noAliases))
	}, ""},
	// Phase 1472 — the declared direction, on exactly the same omit-at-identity
	// terms as its neighbours (§3.1). It is not presentational: a host that
	// drops it renders a document that says something else, so an unrecognised
	// token is refused here rather than coerced to `"auto"`.
	{"direction", func(w *walkState, raw any, path string) Value {
		return Str(enumStr(raw, path, textDirectionCases, "direction", noAliases))
	}, ""},
}

// Each member's identity is its row's omit-at-default value (Phase 2330), so
// the table, not this file, says what an absent member means. A member the
// table does not declare omit-at-default fails every run at start-up.
func init() {
	for i := range styleMembers {
		m := &styleMembers[i]
		f, ok := idl.RecordSemanticStyle.Field(m.name)
		if !ok || f.Optionality != idl.OmitDefault {
			panic("wire: idl.json declares no omit-at-default SemanticStyle row " + m.name)
		}
		identity, err := strconv.Unquote(f.Default)
		if err != nil {
			panic("wire: SemanticStyle." + m.name + "'s default is not a token: " + f.Default)
		}
		m.identity = identity
	}
}

// decodeStyleMembers decodes every SemanticStyle member present in obj,
// default tokens included. Unknown keys are ignored (rule 2).
func decodeStyleMembers(w *walkState, obj map[string]any, path string) map[string]Value {
	fields := map[string]Value{}
	for _, m := range styleMembers {
		if raw, ok := obj[m.name]; ok {
			fields[m.name] = m.dec(w, raw, path+"."+m.name)
		}
	}
	return fields
}

// decodeStyle — SemanticStyle. Every field is omitted-when-default on BOTH
// boundaries (Phase 147 role/voice; Phase 460 emphasis/tone/weight): an
// absent field restores the identity default, and a present explicit default
// normalises to the omitted form.
func decodeStyle(w *walkState, raw any, path string) Obj {
	fields := decodeStyleMembers(w, expectObject(raw, path), path)
	for _, m := range styleMembers {
		if isStr(fields[m.name], m.identity) {
			delete(fields, m.name)
		}
	}
	return Obj{Fields: fields}
}

// decodeStyleDelta — Phase 2291, the PatchStyle payload (§3.4). The same six
// members and the same per-field decoders as SemanticStyle, but each member is
// OPTIONAL and absence means "leave this field as it is": a present member sets
// the field, a default token included, so nothing is normalised away and the
// encoder emits every member the delta sets. A delta naming no member is not
// an edit and is refused.
func decodeStyleDelta(w *walkState, raw any, path string) Obj {
	fields := decodeStyleMembers(w, expectObject(raw, path), path)
	if len(fields) == 0 {
		failExpecting(
			CodeWrongType,
			path,
			"a StyleDelta must name at least one field — an empty patch is not an edit",
			"a StyleDelta naming at least one field (direction | emphasis | role | tone | voice | weight)",
		)
	}
	return Obj{Fields: fields}
}

// StyleIdentity returns the identity (default) token of each SemanticStyle
// member, keyed by wire name — what an absent member of a SemanticStyle means.
func StyleIdentity() map[string]string {
	out := make(map[string]string, len(styleMembers))
	for _, m := range styleMembers {
		out[m.name] = m.identity
	}
	return out
}

// decodeAccessibility decodes the §3.1 Accessibility trait. It used to be
// `fromJSON` — a structural pass-through, so every malformed payload decoded
// with its value preserved verbatim and this host answered 0 of the corpus's
// six a11y reject vectors. `label` / `hidden` are ordinary `Binding` slots
// (the 2026-08-25 §3.1 ruling), so they route through the typed binding
// decoders and the §3.6 bare-scalar coercion applies to their SHAPE while the
// slot's own type still governs the value; `liveRegion` is a closed token set;
// `role` is open to any role NAME but not to any VALUE.
func decodeAccessibility(w *walkState, raw any, path string) Obj {
	obj := expectObject(raw, path)
	// Phase 959 — the near-miss check runs BEFORE the slot reads, matching the
	// `FormField` ordering, so a trait carrying both `ariaLabel` and a
	// well-formed `label` still names the ignored key rather than decoding half
	// the author's intent in silence.
	checkNearMissesIn(obj, path, a11yNearMisses, "accessibility", a11yNearMissConsequence)
	s := newRecordSpec(w, obj, path, &idl.RecordAccessibility)
	s.field("label", decodeBindingString)
	s.field("labelledBy", decodeString)
	s.field("describedBy", decodeString)
	s.field("role", decodeString)
	s.field("liveRegion", enumDecoder(liveRegionCases, "liveRegion", noAliases))
	s.field("hidden", decodeBindingBool)
	// Phase 1812 — the node's SPOKEN rendering for a voice surface: an ordinary
	// TextSource (bare-string canonical for a literal, as tooltip). Inert to every
	// visual renderer — it must reach no attribute and no visible text.
	s.field("speak", decodeTextSource)
	return s.buildStrict("")
}

func decodeState(w *walkState, raw any, path string) Obj {
	s := newRecordSpec(w, expectObject(raw, path), path, &idl.RecordStateBehaviour)
	s.field("onLoading", decodeSingleNode)
	s.field("onEmpty", decodeSingleNode)
	// The rendering closure is opaque — the canonical form is the sentinel.
	s.field("onError", decodeClosure)
	return s.buildStrict("")
}

// decodeNodeValue decodes a node envelope, applying the §8 NodeId invariants.
// `state` and `style` are omitted when empty / all-default (§3.1) — a decoded
// empty/default section normalises to the omitted form.
func decodeNodeValue(w *walkState, raw any, path string) Node {
	// §21 node-depth + total-node bounds, on the way DOWN (rule 4).
	w.enterNode(path)
	defer w.exitNode()

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
	kind := decodeKind(w, rawKind, path+".kind")

	extras := make(map[string]Value)
	if raw, ok := obj["state"]; ok {
		if state := decodeState(w, raw, path+".state"); len(state.Fields) > 0 {
			extras["state"] = state
		}
	}
	if raw, ok := obj["style"]; ok {
		if style := decodeStyle(w, raw, path+".style"); len(style.Fields) > 0 {
			extras["style"] = style
		}
	}
	if raw, ok := obj["accessibility"]; ok {
		extras["accessibility"] = decodeAccessibility(w, raw, path+".accessibility")
	}
	// Phase 1112 — the node-level tooltip TRAIT. It sits on the envelope beside
	// `accessibility`, not inside any kind: a hint is uniform across kinds, so
	// there is nothing about "a short supplementary description of this thing"
	// that varies with whether the thing is a button or a metric.
	//
	// It is an ordinary `TextSource` slot, which is what refuses `42` at
	// `$.tooltip` with WRONG_TYPE: the bare STRING is the canonical encoding of
	// a literal hint (Literal is TextSource's transparent case), so a number is
	// one lenient `toString` away from minting hint text out of a JSON type —
	// which no downstream check could ever catch.
	//
	// A `tooltip` inside a `kind` object is NOT this trait: `ButtonSpec` carries
	// a legacy host-only slot of that name (§10.1) which is never emitted and
	// never decoded, and it stays an unknown key tolerated under rule 2.
	if raw, ok := obj["tooltip"]; ok {
		extras["tooltip"] = decodeTextSource(w, raw, path+".tooltip")
	}
	// Phase 1535 — the node-level visibility predicate. An ordinary optional
	// Binding<bool>, decoded by the shared binding decoder for the reason the
	// tooltip above states: the one time a host read a node-envelope slot as its
	// own narrower thing it took two hosts and a ruling to unwind.
	if raw, ok := obj["visible"]; ok {
		extras["visible"] = decodeBinding(w, raw, path+".visible")
	}
	// Phase 1812 — the author-declared degraded rendering: a full node a BEHIND
	// reader renders in place of its Unknown placeholder. A current reader (this
	// one, for any kind it decodes) decodes and preserves it and never renders
	// it; it walks the same policy-gated node decoder as any nested node.
	if raw, ok := obj["fallback"]; ok {
		extras["fallback"] = decodeNodeValue(w, raw, path+".fallback")
	}
	return Node{ID: id, Kind: kind, Extras: extras}
}
