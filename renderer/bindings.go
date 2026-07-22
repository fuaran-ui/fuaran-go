package renderer

import (
	"math"
	"strconv"

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
// map when the identity key is present; an unwritten Selection / Filter → its
// declared defaultValue (Phase 629); otherwise nil (the "NotResolved" branch).
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
	// State keeps the go host's established em-dash-until-resolved posture.
	if obj.Tag == "Selection" || obj.Tag == "Filter" {
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
	default:
		// None / Date / Custom: the plain numeric form.
		return plainNumber(num)
	}
}
