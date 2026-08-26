package renderer

import (
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

// BindingSources maps a binding key (a Query name, a State key, …) to a
// host-resolved value. Nil is the headless baseline: Static bindings resolve,
// the rest placeholder.
type BindingSources map[string]wire.Value

// renderText resolves a decoded text source to a plain (un-escaped) string:
// Literal → its text; Bound → the resolved source or ""; I18n → "[i18n:key]".
// The caller escapes the result on the way into HTML.
func renderText(text wire.Value, sources BindingSources) string {
	switch t := text.(type) {
	case wire.Str:
		return string(t)
	case wire.Obj:
		switch t.Tag {
		case "Literal":
			return strValue(t.Fields["text"])
		case "Bound":
			// Phase 632/651 — a text slot resolves through the scalar path, so a
			// `Bound` `Transform` yields its 1×1 result cell (never the rows
			// list) and a `Selection.defaultValue` renders resolved (Phase 629);
			// any other binding resolves exactly as before. Both the static and
			// islands surfaces share this dispatch.
			if s, ok := resolveScalarText(t.Fields["binding"], sources); ok {
				return s
			}
			return ""
		case "I18n":
			return "[i18n:" + strValue(t.Fields["key"]) + "]"
		}
	}
	return ""
}

// resolveBinding resolves a decoded binding to its value, or nil when not
// resolvable: Static → its embedded value; every keyed case → the host sources
// map when the identity key is present; an unwritten Selection / Filter / State
// → its declared defaultValue (Phase 629; State added by fuaran#1064);
// otherwise nil (the "NotResolved" branch).
func resolveBinding(binding wire.Value, sources BindingSources) wire.Value {
	obj, ok := binding.(wire.Obj)
	if !ok {
		return nil
	}
	if obj.Tag == "Static" {
		return obj.Fields["value"]
	}
	if key, ok := bindingKey(obj); ok {
		if v, found := sources[key]; found {
			return v
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
			return dv
		}
	}
	return nil
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
