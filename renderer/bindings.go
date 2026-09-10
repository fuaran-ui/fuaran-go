package renderer

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Text-source, binding, and number-format resolution for the server renderer.
// The decoded tree is structural — bindings and text sources survive decode as
// tagged Obj values. The baseline renderer resolves what it can statically
// (the Static binding, the Literal text source) and falls back to the same
// placeholders the reference SSR renderer uses (an em-dash for an unresolved
// value). A host can supply a BindingSources map (binding key → value) to
// resolve Query / State bindings.
//
// Resolution answers three things, not two (Phase 1667): a value, ABSENCE (nil —
// the slot's empty state, which is what an unwritten Query or an unseeded State
// means), or an ERROR — the document asked for something no decoded tree can
// answer, and there is no value that could stand in without being read as an
// answer. The error is a returned value threaded to the exported entry points,
// which is Go's channel for the distinction; the render still completes a whole
// document, so a caller gets both what could be rendered and what could not.

// BindingSources is what the host furnishes a render pass (Phase 1663).
//
// Three members, and the reference host's shape with its four identity-keyed
// maps collapsed into the one this host carries:
//
//   - Values — the identity-keyed map that WAS this whole type: a State key, a
//     Query or Filter name, a Selection nodeId → the host's resolved value. It
//     doubles as the compute-parameter store, exactly as before.
//   - Now — the host instant as an ISO-8601 UTC string (2026-08-02T06:59:24Z),
//     for Binding.Now and Format.Since.
//   - Locale — the ambient BCP-47 tag a LocaleSource.Ambient reads.
//
// THE CLOCK LIVES HERE, never on the wire and never read during resolution.
// That is what keeps a tree a pure value: a replayed op-stream re-supplies the
// instant it recorded, so a replay reproduces the original render instead of
// drifting to whatever "now" means at replay time. Resolve it ONCE per render
// pass and hold it for the whole pass, or two Now slots in one tree can
// disagree.
//
// The zero value is the headless baseline and behaves exactly as the old nil
// map did: a nil Values indexes and lens fine, Static bindings resolve, the
// rest placeholder, and no clock is furnished. Now == "" means THIS HOST
// FURNISHES NO CLOCK — the slot resolves to absence, which a text slot renders
// as the empty string and a numeric one as the em-dash. Deliberately loud: a
// relative time computed against an invented "now" is a confidently wrong
// answer, and it is not the Phase 1667 error channel either — the document is
// answerable, the host simply furnished nothing, which is the same fact as an
// unwritten Query.
//
// Locale == "" means the runtime default. The Format cases this host renders
// (Since / RelativeTime / Duration) are locale-INDEPENDENT by declaration and
// consult no tag; the member exists for the cases a locale-aware host renders
// and this one declines — see localeIndependentFormats.
type BindingSources struct {
	Values map[string]wire.Value
	Now    string
	Locale string
}

// Sources lifts a bare identity-keyed map into BindingSources with no host
// clock and no ambient locale — the shape every caller had before the record
// grew its two host members, so a call site that furnishes only values stays
// one expression.
func Sources(values map[string]wire.Value) BindingSources {
	return BindingSources{Values: values}
}

// MergedWith returns these sources with extra layered over Values; the two host
// members ride along. A caller merging maps by hand would silently drop them.
func (s BindingSources) MergedWith(extra map[string]wire.Value) BindingSources {
	merged := make(map[string]wire.Value, len(s.Values)+len(extra))
	for k, v := range s.Values {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	s.Values = merged
	return s
}

// decodedComputedMessage is the one message a decoded Binding.Computed carries —
// byte-identical to the reference host's, so every host reports the same sentence
// and a test can pin it. A constant rather than a literal at the construction
// site for exactly that reason.
const decodedComputedMessage = "Binding.Computed has no wire projection (decoded from a '<closure>' sentinel) — use Binding.Expr / Transform / State"

// ErrDecodedComputed is the resolution error a decoded Binding.Computed answers
// (WIRE_FORMAT.md §5). A package-level value rather than a fresh error per call
// so a caller can classify it with errors.Is without matching on the message.
var ErrDecodedComputed = errors.New(decodedComputedMessage)

// resolutionFailure marks an error that came from BINDING RESOLUTION rather than
// from the compute evaluator, and the distinction decides whether a seam reports
// it or renders absence.
//
// The two are different facts. A pipeline that could not be EVALUATED — an
// unbound param whose filter step was pruned, an ambiguous non-1x1 result — is
// the RENDERER unable to answer, and every scalar and row seam here has always
// rendered that as the slot's empty state; making those reach a caller would
// change what this host reports for documents this phase is not about (measured:
// three corpus legs render trees with a deliberately unbound param). A
// RESOLUTION error is the DOCUMENT asking for something no decoded tree can
// answer, which is the fact Phase 1667 reports.
//
// A wrapper type rather than a list of sentinels to check, so a second
// resolution error needs no edit anywhere but its own construction site. It
// unwraps to the cause, so errors.Is(err, ErrDecodedComputed) reads through it.
type resolutionFailure struct{ err error }

func (r resolutionFailure) Error() string { return r.err.Error() }

func (r resolutionFailure) Unwrap() error { return r.err }

// asResolutionFailure returns err when it carries a resolution failure and nil
// when it is an evaluation failure — so a seam that renders an evaluation
// failure as absence still reports a resolution one.
func asResolutionFailure(err error) error {
	var rf resolutionFailure
	if errors.As(err, &rf) {
		return rf.err
	}
	return nil
}

// renderText resolves a decoded text source to a plain (un-escaped) string:
// Literal → its text; Bound → the resolved source or ""; I18n → "[i18n:key]".
// The caller escapes the result on the way into HTML.
//
// The error is the resolution error (Phase 1667), reported alongside the string
// rather than instead of it: the slot still renders whatever it can, and the
// caller records why the rest is missing.
func renderText(text wire.Value, sources BindingSources) (string, error) {
	switch t := text.(type) {
	case wire.Str:
		return string(t), nil
	case wire.Obj:
		switch t.Tag {
		case "Literal":
			return strValue(t.Fields["text"]), nil
		case "Bound":
			// Phase 632/651 — a text slot resolves through the scalar path, so a
			// `Bound` `Transform` yields its 1×1 result cell (never the rows
			// list) and a `Selection.defaultValue` renders resolved (Phase 629);
			// any other binding resolves exactly as before. Both the static and
			// islands surfaces share this dispatch.
			s, ok, err := resolveScalarText(t.Fields["binding"], sources)
			if ok {
				return s, err
			}
			return "", err
		case "I18n":
			return "[i18n:" + strValue(t.Fields["key"]) + "]", nil
		}
	}
	return "", nil
}

// resolveBinding resolves a decoded binding to its value, or nil when not
// resolvable: Static → its embedded value; every keyed case → the host sources
// map when the identity key is present; an unwritten Selection / Filter / State
// → its declared defaultValue (Phase 629; State added by fuaran#1064);
// otherwise nil (the "NotResolved" branch).
func resolveBinding(binding wire.Value, sources BindingSources) (wire.Value, error) {
	obj, ok := binding.(wire.Obj)
	if !ok {
		return nil, nil
	}
	if obj.Tag == "Static" {
		return obj.Fields["value"], nil
	}
	// Phase 1534 — the scalar expression in a slot with no coercion. A null
	// result, and a failed evaluation, are both nil here: this seam has no error
	// channel, and a text or numeric slot goes through resolveScalar* above,
	// which distinguishes them.
	if e, ok := exprBinding(obj); ok {
		cell, outcome, err := evalScalarTransform(e, sources)
		if outcome == scalarResolved {
			return cell, err
		}
		return nil, err
	}
	// A decoded `Computed` has nothing to compute WITH: the case's whole payload
	// is a host closure and it crosses the wire as the closure sentinel. So
	// WIRE_FORMAT §5 says it resolves to an ERROR naming its replacements, never
	// to a value — and Phase 1667 gave this seam the channel to say so. Returning
	// nil held the negative half of the rule (no 0 / "" / false, ever) and not the
	// positive one: a reader cannot tell an em-dash here from a query that has not
	// answered yet.
	//
	// The arm is EXPLICIT rather than a fall-through so it stays that way — the
	// case carries no key / name / nodeId today, so the lookup below would miss,
	// but a future member named like one of those would otherwise silently turn a
	// host-only computation into a resolved value.
	if obj.Tag == "Computed" {
		return nil, ErrDecodedComputed
	}
	// Phase 1663 — the host-furnished instant. The clock is NOT read here:
	// sources.Now was resolved once, host-side, for the whole render pass. An
	// unset instant is ABSENCE, so the slot renders its empty surface — a host
	// that forgets to furnish the clock must not silently render a plausible
	// wrong date.
	//
	// The declared GRAIN truncates the instant BEFORE anything projects it.
	// Before, not after: a Transform param projecting this into dateDiffDays
	// reads only the leading YYYY-MM-DD, so the truncation has to be upstream of
	// every projection or it is not the document's declaration at all. Absent
	// grain is Second, the identity.
	if obj.Tag == "Now" {
		if sources.Now == "" {
			return nil, nil
		}
		if grain, ok := obj.Fields["grain"].(wire.Str); ok {
			return wire.Str(truncateToGrain(string(grain), sources.Now)), nil
		}
		return wire.Str(sources.Now), nil
	}
	// Phase 1663 — the locale-aware numeric projection. Only the
	// locale-INDEPENDENT cases render; see formatProjection for which, and why
	// the rest resolve to absence on this host.
	if obj.Tag == "Format" {
		return formatProjection(obj, sources)
	}
	if key, ok := bindingKey(obj); ok {
		if v, found := sources.Values[key]; found {
			return v, nil
		}
	}
	// Phase 629 — an unwritten Selection / Filter resolves to its declared
	// defaultValue (resolution-time defaulting IS the preselected mechanism, so
	// preselected master-detail renders resolved without any store seeding).
	//
	// fuaran#1064 — STATE JOINS THEM, by operator ruling (2026-08-26). This arm
	// used to read "State keeps the go host's established em-dash-until-resolved
	// posture", and that carve-out was the one place this host rendered
	// differently from every other tier: `a11y-wrapper-state-bound` carries its
	// accessible name as a `Binding.State` with a declared default, and four
	// render tiers emitted `aria-label="Site footer"` while this one emitted no
	// `aria-label` at all.
	//
	// What settled it was not the four-to-one count — that is evidence about what
	// implementers find natural, not about what is right — but that the carve-out
	// was inconsistent with THIS host's own charter: Phase 651's completeness
	// posture already resolves the two sibling defaults above at render time, so
	// one function was resolving two of three declared defaults and skipping the
	// third. The specification was silent on `State` resolution while normatively
	// fixing the behaviour of its own declared mirror, `Binding.Filter.defaultValue`
	// (§1.1) — so neither posture was non-conformant, which was the actual defect.
	// WIRE_FORMAT §24 now states the rule on the original.
	//
	// A declared default is AUTHORED DATA, not store state, so resolving it costs
	// this host no session state and does not touch the library-not-a-runtime line:
	// the rule is correct-before-hydration, and hydration may re-resolve but never
	// first-fill.
	if obj.Tag == "Selection" || obj.Tag == "Filter" || obj.Tag == "State" {
		if dv, ok := obj.Fields["defaultValue"]; ok {
			return dv, nil
		}
	}
	return nil, nil
}

// bindingKey returns the host-sources lookup key for a decoded binding: State
// keys on `key`, Query / Filter on `name`, Selection on `nodeId`.
func bindingKey(obj wire.Obj) (string, bool) {
	for _, field := range []string{"key", "name", "nodeId"} {
		if k, ok := obj.Fields[field].(wire.Str); ok {
			return string(k), true
		}
	}
	return "", false
}

// displayString renders a resolved value for text interpolation.
func displayString(v wire.Value) string {
	switch t := v.(type) {
	case wire.Str:
		return string(t)
	case wire.Int:
		return strconv.FormatInt(int64(t), 10)
	case wire.Float:
		return plainNumber(float64(t))
	case wire.Bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}

// plainNumber mirrors the reference renderers' plain numeric form: integral
// floats print without a decimal part.
func plainNumber(f float64) string {
	if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Floor(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// numericValue extracts a float64 from a resolved binding value.
func numericValue(v wire.Value) (float64, bool) {
	switch t := v.(type) {
	case wire.Int:
		return float64(t), true
	case wire.Float:
		return float64(t), true
	case wire.Str:
		if f, err := strconv.ParseFloat(string(t), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// ─── The host instant: grain truncation, epoch conversion, Since ────────────
//
// Phase 1663 — the three shared reductions the reference host keeps above its
// own pipeline split, transcribed. All three are pure functions of their
// arguments and consult no clock: the instant is always the caller's.

// grainTruncation is (prefix length, suffix) per TimeGrain. Second is the
// identity and is absent by construction — a grain this map does not carry
// truncates nothing, which is the right answer for the identity and for a token
// this host does not recognise.
var grainTruncation = map[string]struct {
	length int
	suffix string
}{
	"Minute": {16, ":00Z"},
	"Hour":   {13, ":00:00Z"},
	"Day":    {10, ""},
}

// truncateToGrain returns instant truncated to grain. An instant too short to
// slice comes back as it went in: a partial instant is already at or below the
// requested grain, and inventing digits to fill it would be a wrong answer
// rather than a coarse one.
func truncateToGrain(grain, instant string) string {
	slicing, ok := grainTruncation[grain]
	if !ok || len(instant) < slicing.length {
		return instant
	}
	return instant[:slicing.length] + slicing.suffix
}

// epochSecondsOfInstant returns whole Unix-epoch seconds for a canonical
// instant, and false when the leading date is not readable.
//
// Deliberately tolerant of what follows the seconds (a fractional part, a Z, an
// offset suffix) and deliberately INTOLERANT of a missing or non-numeric date:
// truncating to a grain leaves YYYY-MM-DDTHH:MM:00Z and YYYY-MM-DD, both of
// which must parse, and anything shorter is not an instant at all. A false is
// surfaced as unresolved by the caller, never as an invented instant.
//
// Proleptic-Gregorian integer arithmetic (Howard Hinnant's days_from_civil),
// not time.Parse, so this host and every other agree to the second on dates
// outside a platform calendar's range.
func epochSecondsOfInstant(instant string) (float64, bool) {
	digits := func(start, length int) (int, bool) {
		if len(instant) < start+length {
			return 0, false
		}
		n := 0
		for i := start; i < start+length; i++ {
			c := instant[i]
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		return n, true
	}
	year, okY := digits(0, 4)
	month, okM := digits(5, 2)
	day, okD := digits(8, 2)
	if !okY || !okM || !okD || year < 1 || month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, false
	}
	hours, _ := digits(11, 2)
	minutes, _ := digits(14, 2)
	seconds, _ := digits(17, 2)
	y := year
	if month <= 2 {
		y--
	}
	era := y
	if y < 0 {
		era = y - 399
	}
	era /= 400
	yoe := y - era*400
	shift := 9
	if month > 2 {
		shift = -3
	}
	doy := (153*(month+shift)+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := era*146097 + doe - 719468
	return float64(days)*86400 + float64(hours*3600+minutes*60+seconds), true
}

// relativeUnitSeconds is the length of one RelativeTimeUnit. Month and Year are
// the mean Gregorian lengths — FIXED constants rather than calendar arithmetic,
// because "2 months ago" is a rounded human phrase and a calendar-exact answer
// would make the same delta read differently depending on which months it
// spanned, on hosts that must agree to the byte.
var relativeUnitSeconds = map[string]float64{
	"Second": 1,
	"Minute": 60,
	"Hour":   3600,
	"Day":    86400,
	"Week":   604800,
	"Month":  2629746,
	"Year":   31556952,
}

// sinceLadder is the auto-selection ladder: the largest unit whose length does
// not exceed the magnitude of the delta, coarsest threshold last.
var sinceLadder = []struct {
	threshold float64
	unit      string
}{
	{60, "Second"},
	{3600, "Minute"},
	{86400, "Hour"},
	{604800, "Day"},
	{2629746, "Week"},
	{31556952, "Month"},
}

// sinceUnitAndCount is the Format.Since reduction: a signed second-delta becomes
// the (unit, count) pair the relative-time renderer already knows how to say.
//
// An empty declared unit is the AUTO-SELECTION request (not a default): the unit
// is the largest whose length does not exceed the magnitude, from the fixed
// ladder above. The count TRUNCATES toward zero rather than rounding, so 3599
// seconds is "59 minutes" and never "1 hour" — the ladder and the count then
// agree at every boundary, which rounding would break exactly at the point a
// reader is most likely to check.
func sinceUnitAndCount(declared string, deltaSeconds float64) (string, float64) {
	unit := declared
	if unit == "" {
		magnitude := math.Abs(deltaSeconds)
		unit = "Year"
		for _, rung := range sinceLadder {
			if magnitude < rung.threshold {
				unit = rung.unit
				break
			}
		}
	}
	length, ok := relativeUnitSeconds[unit]
	if !ok {
		length = 1
	}
	return unit, float64(int64(deltaSeconds / length))
}

// localeIndependentFormats are the Format cases this host renders (Phase 1663).
// Locale-INDEPENDENT by declaration: Duration is unit glyphs and English words
// with exact cross-pipeline parity, and the relative-time pair reduces to
// (unit, count) through the one shared ladder and then phrases it in the
// deterministic English form — the fallback tier. The four cases NOT here
// (Number / Currency / Percent / Date) take their text from a locale database,
// so a stdlib-only host has no canonical answer to give and resolves them to
// absence exactly as it did before this seam existed. The corpus's render-text
// family enumerates that exclusion with its reason.
var localeIndependentFormats = map[string]bool{"Duration": true, "RelativeTime": true, "Since": true}

// ResolveLocaleTag returns the BCP-47 tag a Binding.Format renders under, and
// false when the binding is not a Format or carries no readable LocaleSource.
//
// LocaleSource.Explicit pins its own tag; LocaleSource.Ambient reads the host's
// BindingSources.Locale, whose "" identity default means "the runtime default
// locale". The precedence is the SEAM's rule rather than each caller's, which is
// why this is exported: a host that wants to render the locale-dependent Format
// cases itself needs the tag the document asked for and must not re-derive it.
func ResolveLocaleTag(binding wire.Value, sources BindingSources) (string, bool) {
	obj, ok := binding.(wire.Obj)
	if !ok || obj.Tag != "Format" {
		return "", false
	}
	locale, ok := obj.Fields["locale"].(wire.Obj)
	if !ok {
		return "", false
	}
	switch locale.Tag {
	case "Explicit":
		tag, ok := locale.Fields["tag"].(wire.Str)
		if !ok {
			return "", false
		}
		return string(tag), true
	case "Ambient":
		return sources.Locale, true
	}
	return "", false
}

// formatProjection resolves a Binding.Format's numeric source and projects it to
// a string, or nil for a case this host does not render (see
// localeIndependentFormats).
//
// Since is the one case whose text is a function of the HOST INSTANT as well as
// of its source, so the delta is taken here, where the instant lives, and the
// phrasing helpers stay pure projections of their arguments. The source is read
// as an instant in whole Unix-epoch seconds (Date's convention) and the sign
// follows Intl.RelativeTimeFormat's: negative is the past. An unset or
// unreadable host instant is ABSENCE, for exactly the reason Binding.Now gives.
func formatProjection(binding wire.Obj, sources BindingSources) (wire.Value, error) {
	fmtObj, ok := binding.Fields["format"].(wire.Obj)
	if !ok || !localeIndependentFormats[fmtObj.Tag] {
		return nil, nil
	}
	source, err := resolveScalarNumber(binding.Fields["source"], sources)
	if err != nil {
		return nil, err
	}
	value, ok := numericValue(source)
	if !ok {
		return nil, nil
	}
	switch fmtObj.Tag {
	case "Duration":
		return wire.Str(formatDuration(strValue(fmtObj.Fields["unit"]), strValue(fmtObj.Fields["style"]), value)), nil
	case "RelativeTime":
		return wire.Str(formatRelativeEnglish(strValue(fmtObj.Fields["unit"]), value)), nil
	}
	nowEpoch, ok := epochSecondsOfInstant(sources.Now)
	if !ok {
		return nil, nil
	}
	unit, count := sinceUnitAndCount(strValue(fmtObj.Fields["unit"]), value-nowEpoch)
	return wire.Str(formatRelativeEnglish(unit, count)), nil
}

// formatNumber formats a resolved numeric value through a decoded CellFormat.
func formatNumber(format, value wire.Value) string {
	num, ok := numericValue(value)
	if !ok {
		return displayString(value)
	}
	fmtObj, ok := format.(wire.Obj)
	if !ok {
		return plainNumber(num)
	}
	switch fmtObj.Tag {
	case "Number":
		if decimals, ok := fmtObj.Fields["decimals"].(wire.Int); ok {
			return strconv.FormatFloat(num, 'f', int(decimals), 64)
		}
		return plainNumber(num)
	case "Currency":
		return strValue(fmtObj.Fields["code"]) + " " + strconv.FormatFloat(num, 'f', 2, 64)
	case "Percent":
		places := 1
		if decimals, ok := fmtObj.Fields["decimals"].(wire.Int); ok {
			places = int(decimals)
		}
		return strconv.FormatFloat(num*100, 'f', places, 64) + "%"
	case "SignificantDigits":
		if digits, ok := fmtObj.Fields["digits"].(wire.Int); ok {
			return strconv.FormatFloat(num, 'g', int(digits), 64)
		}
		return plainNumber(num)
	case "Duration":
		return formatDuration(strValue(fmtObj.Fields["unit"]), strValue(fmtObj.Fields["style"]), num)
	case "RelativeTime":
		return formatRelativeEnglish(strValue(fmtObj.Fields["unit"]), num)
	case "Since":
		// Phase 1663 — `Format.Since` now RENDERS on this host, through
		// formatProjection, where the host instant (BindingSources.Now) lives.
		// This arm is the CELL vocabulary's, and CellFormat carries no `Since`
		// case, so it is unreachable from the wire: the decoder refuses the tag
		// in a cell position. It is kept because its answer is the honest one for
		// a function with no sources — the empty string, NOT the plain number,
		// because an epoch integer rendered where a reader expects "3 hours ago"
		// is a silently wrong answer. A caller that wants the phrase resolves the
		// binding rather than the format.
		return ""
	default:
		// None / Date / Custom: the plain numeric form.
		return plainNumber(num)
	}
}

// ─── Duration / relative-time rendering (Phase 819) ─────────────────────────
//
// Mirrors the reference host's shared `formatDuration` / `formatRelativeEnglish`
// exactly (hand-rolled, shared across its pipelines): a duration is
// deliberately LOCALE-INDEPENDENT — "1h 20m" / "1:20:00" / "1 hour 20 minutes"
// are unit glyphs and English words, not CLDR-driven forms — and the cell
// vocabulary has no locale dimension, so the English relative form IS the
// canonical cell rendering. Rounding is half-to-even, matching the reference
// host's `round`.

// durationUnitSeconds maps a DurationUnit case to its length in seconds.
func durationUnitSeconds(unit string) float64 {
	switch unit {
	case "Minutes":
		return 60
	case "Hours":
		return 3600
	}
	return 1 // Seconds
}

// formatDuration renders `value` (a signed count of `unit`s) per the bounded
// DurationStyle. Negatives render with a leading "-" once the rounded
// magnitude is nonzero.
func formatDuration(unit, style string, value float64) string {
	totalSeconds := value * durationUnitSeconds(unit)
	total := int(math.RoundToEven(math.Abs(totalSeconds)))
	sign := ""
	if totalSeconds < 0 && total > 0 {
		sign = "-"
	}
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	var body string
	switch style {
	case "Clock":
		// "h:mm:ss" from one hour up, "m:ss" below it.
		if hours >= 1 {
			body = strconv.Itoa(hours) + ":" + pad2(minutes) + ":" + pad2(seconds)
		} else {
			body = strconv.Itoa(minutes) + ":" + pad2(seconds)
		}
	case "Long":
		// English words, singular/plural, zero components omitted;
		// zero → "0 minutes".
		var parts []string
		for _, p := range []struct {
			n    int
			word string
		}{{hours, "hour"}, {minutes, "minute"}, {seconds, "second"}} {
			switch {
			case p.n == 0:
			case p.n == 1:
				parts = append(parts, "1 "+p.word)
			default:
				parts = append(parts, strconv.Itoa(p.n)+" "+p.word+"s")
			}
		}
		if len(parts) == 0 {
			body = "0 minutes"
		} else {
			body = strings.Join(parts, " ")
		}
	default: // Compact
		// Largest two grains, zero tails omitted: "1h 20m" / "2h" /
		// "5m 30s" / "42s"; zero → "0s".
		switch {
		case hours >= 1 && minutes > 0:
			body = strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
		case hours >= 1:
			body = strconv.Itoa(hours) + "h"
		case minutes >= 1 && seconds > 0:
			body = strconv.Itoa(minutes) + "m " + strconv.Itoa(seconds) + "s"
		case minutes >= 1:
			body = strconv.Itoa(minutes) + "m"
		default:
			body = strconv.Itoa(seconds) + "s"
		}
	}
	return sign + body
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// formatRelativeEnglish renders a signed count of `unit` — "in 2 hours" /
// "3 minutes ago" / "this minute".
func formatRelativeEnglish(unit string, value float64) string {
	n := int(math.RoundToEven(value))
	unitWord := strings.ToLower(unit)
	if n == 0 {
		return "this " + unitWord
	}
	magnitude := n
	if n < 0 {
		magnitude = -n
	}
	plural := unitWord
	if magnitude != 1 {
		plural += "s"
	}
	if n < 0 {
		return strconv.Itoa(magnitude) + " " + plural + " ago"
	}
	return "in " + strconv.Itoa(magnitude) + " " + plural
}
