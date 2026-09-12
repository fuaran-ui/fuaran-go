package renderer

import (
	"math"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Real SSR controls for `Rating`, `Color` and `Tokens` (Phase 1677).
//
// Until this file, every form-field kind except `Combobox` rendered through ONE
// generic floor — a bare `<input class="fuaran-form-field-control">` with no
// type, no value and no per-kind class. That is not an austere floor so much as
// an absent one: a `Color` field rendered a text box that could not show the
// colour, a `Rating` rendered a text box where the reference renders stars, and
// a `Tokens` field rendered a text box holding nothing at all. The
// render-fidelity roster's `Form` fallback asks for "the form and its fields
// with their classes, resolved values and `disabled` state, carrying no event
// handlers", and one untyped input answers none of the three.
//
// Each control below is ported from the reference host's own server renderer —
// the same classes, the same attributes, the same recorded limits — because the
// point of a floor is that two hosts serving the same document serve the same
// markup. What is NOT ported is anything needing a keystroke: this tier has no
// script, and the reference's floors are shaped by that same constraint.
//
// THE FIDELITY DECLARATION IS NOT HERE. `render-fidelity.json` is the corpus's,
// and the corpus is another phase's write lane — Phase 1674 owns declaring
// these three obligations in the roster so a gate exists for them. This file is
// the behaviour; that declaration is what will make it checkable from the
// artefact rather than from these tests.

// hexColorUnset is the value a `<input type="color">` holds when nothing valid
// was declared. Black, matching the reference host, and deliberately not an
// empty string: the element has no empty state — handed one it substitutes its
// own default silently, so the only honest options are a colour the document
// chose and a colour the whole platform agrees means "unset".
const hexColorUnset = "#000000"

// tokensSeparator is the SSR projection of a token list. Comma-and-space, in
// reader order, matching the reference's `TokensModel`.
//
// RECORDED LIMIT, carried over verbatim: a token containing a comma cannot
// survive this projection — it re-parses as two. The floor is a degraded medium
// and this is what it degrades to; the client tier never uses it.
const tokensSeparator = ", "

// isHexColor is `#rrggbb` and nothing else, either case.
//
// Deliberately STRICT, and for the reason the reference states: the control
// cannot carry `#fff`, `rebeccapurple`, `rgb(0 0 0)` or an alpha channel, and a
// renderer that narrowed one of those to something else would be answering a
// question the author did not ask. Case is accepted and never rewritten.
func isHexColor(text string) bool {
	if len(text) != 7 || text[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := text[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// ── The rating model, ported from the reference host ────────────────────────
//
// These five functions are the reference's `RatingModel`, member for member.
// They live here rather than being re-derived at the call site for its reason:
// the fill row, the enterable positions and the accessible name are three views
// of ONE scale, and a host that computed them separately would eventually show
// four stars while announcing "3.5 out of 5".

func ratingStep(allowHalf bool) float64 {
	if allowHalf {
		return 0.5
	}
	return 1.0
}

func ratingClamp(max int, value float64) float64 {
	switch {
	case math.IsNaN(value):
		return 0.0
	case value < 0.0:
		return 0.0
	case value > float64(max):
		return float64(max)
	default:
		return value
	}
}

func ratingSnap(allowHalf bool, max int, value float64) float64 {
	s := ratingStep(allowHalf)
	return ratingClamp(max, math.Round(value/s)*s)
}

// ratingFills is one fraction per star position, in order.
func ratingFills(max int, value float64) []float64 {
	v := ratingClamp(max, value)
	out := make([]float64, 0, max)
	for i := 1; i <= max; i++ {
		filled := v - float64(i-1)
		switch {
		case filled <= 0.0:
			out = append(out, 0.0)
		case filled >= 1.0:
			out = append(out, 1.0)
		default:
			out = append(out, filled)
		}
	}
	return out
}

// ratingValueText is the accessible name — "3 out of 5", "3.5 out of 5".
func ratingValueText(max int, value float64) string {
	v := ratingClamp(max, value)
	var shown string
	if math.Abs(v-math.Round(v)) < 1e-9 {
		shown = strconv.Itoa(int(math.Round(v)))
	} else {
		// The reference formats with "0.##" under the invariant culture: at most
		// two decimals, trailing zeros dropped, always a '.' separator.
		shown = strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	}
	return shown + " out of " + strconv.Itoa(max)
}

func ratingFillClass(fill float64) string {
	switch {
	case fill <= 0.0:
		return "fuaran-rating-star-empty"
	case fill >= 1.0:
		return "fuaran-rating-star-full"
	default:
		return "fuaran-rating-star-partial"
	}
}

// isWriteBackTarget mirrors the reference's binding walk: a `State`, a `Filter`
// with no default, or a `Local` is a slot a control can commit back into.
// Anything else — a `Static`, a `Query`, an `Expr` — is a READ, so a rating over
// it has no interaction to floor.
func isWriteBackTarget(binding wire.Value) bool {
	obj, ok := binding.(wire.Obj)
	if !ok {
		return false
	}
	switch obj.Tag {
	case "State", "Local":
		return true
	case "Filter":
		_, hasDefault := obj.Fields["defaultValue"]
		return !hasDefault
	default:
		return false
	}
}

// fieldValueBinding is the value slot a field's control reads, with the auto-bind
// substituted when the slot is ABSENT.
//
// An absent slot is not "no value": §3.6 binds it to the field's own id, so the
// control reads and writes `State(<field id>)`. Substituting the binding here
// rather than special-casing absence at three call sites keeps the read path and
// the write-back test looking at the same thing — which is what decides whether a
// rating is static or enterable.
func fieldValueBinding(kind wire.Obj, fieldID string) wire.Value {
	if b, ok := kind.Fields["value"]; ok && b != nil {
		return b
	}
	return wire.Obj{Tag: "State", Fields: map[string]wire.Value{"key": wire.Str(fieldID)}}
}

// colorField — a native `<input type="color">` IS the control, so there is no
// approximation here at all: the user agent supplies the picker, the keyboard
// interaction and the accessible semantics.
//
// A value the input could not hold falls back to the unset black rather than
// being passed through, matching the reference: a native colour input
// substitutes its own default silently, so handing it a bad literal would show
// a colour the document did not choose while looking as though it had.
func (r *renderer) colorField(field wire.Obj, fieldID string, kind wire.Obj) string {
	value := hexColorUnset
	if v, ok := r.resolve(fieldValueBinding(kind, fieldID)).(wire.Str); ok && isHexColor(string(v)) {
		value = string(v)
	}
	attrs := []attr{
		{"class", "fuaran-form-field-control"},
		{"data-fuaran-field", fieldID},
		{"type", "color"},
	}
	if req, ok := field.Fields["required"].(wire.Bool); ok && bool(req) {
		attrs = append(attrs, attr{"required", ""})
	}
	attrs = append(attrs, attr{"value", value})
	return voidElement("input", attrs)
}

// tokensField — THE SSR FLOOR FOR A TOKEN FIELD IS ONE TEXT INPUT, and it is
// less an approximation of the client's chip row than the only honest thing
// this medium can render. A chip row is BUILT by a keystroke handler: with no
// script there is no gesture that adds a chip and none that removes one, and a
// row of static chips with dead remove buttons would be an affordance the markup
// cannot honour — the failure the whole floor family exists to avoid. So the
// floor is a single `<input type="text">` carrying the tokens comma-separated,
// which a reader CAN edit and which submits with the form.
//
// The `<datalist>` is a real gain rather than decoration when a suggestion
// source resolves: it lets the user agent suggest tokens as the reader types,
// with no script — the same trade the combobox floor makes.
//
// RECORDED KNOWN LIMIT, not claimed as coverage: `allowFreeText = false` is not
// enforced here and cannot be. A `<datalist>` is a suggestion list, not a
// constraint, and HTML offers no native membership check. The declaration rides
// as `data-fuaran-tokens-constrained` so a reader can see it was not silently
// dropped; nothing in this host reads that attribute. Enforcement is the
// server-side re-check §22 requires of any host that accepts submissions.
func (r *renderer) tokensField(field wire.Obj, fieldID string, kind wire.Obj) string {
	listID := fieldID + "-suggestions"

	var tokens []string
	if resolved, ok := r.resolve(fieldValueBinding(kind, fieldID)).(wire.Arr); ok {
		for _, t := range resolved {
			if s, ok := t.(wire.Str); ok {
				tokens = append(tokens, string(s))
			}
		}
	}

	// A suggestion source is OPTIONAL, and an absent one emits no `list`
	// attribute at all rather than one pointing at an empty datalist: an empty
	// popup that opens on focus is worse than no popup.
	var options strings.Builder
	hasSuggestions := false
	if raw, ok := kind.Fields["suggestions"]; ok && raw != nil {
		if resolved, ok := r.resolve(raw).(wire.Arr); ok {
			hasSuggestions = true
			for _, opt := range resolved {
				optObj, ok := opt.(wire.Obj)
				if !ok {
					continue
				}
				optValue := strValue(optObj.Fields["value"])
				optLabel := optValue
				if l, ok := optObj.Fields["label"]; ok {
					optLabel = r.text(l)
				}
				options.WriteString(textElement("option", []attr{{"value", optValue}}, optLabel))
			}
		}
	}

	constrained := "true"
	if v, ok := kind.Fields["allowFreeText"].(wire.Bool); ok && bool(v) {
		constrained = "false"
	} else if _, declared := kind.Fields["allowFreeText"]; !declared {
		// `allowFreeText` is omit-at-default TRUE on this kind — the opposite
		// polarity to `Combobox`'s. An absent key therefore means free text is
		// ALLOWED, so the marker is "false", and reading the absence as the
		// combobox's default would emit the reverse of what the document says.
		constrained = "false"
	}

	inputAttrs := []attr{
		{"class", "fuaran-form-field-control fuaran-tokens-input"},
		{"data-fuaran-field", fieldID},
		{"type", "text"},
	}
	if hasSuggestions {
		inputAttrs = append(inputAttrs, attr{"list", listID})
	}
	inputAttrs = append(inputAttrs,
		// The browser's own history dropdown would compete with the datalist
		// popup for the same gesture.
		attr{"autocomplete", "off"},
		attr{"data-fuaran-tokens-constrained", constrained},
	)
	if req, ok := field.Fields["required"].(wire.Bool); ok && bool(req) {
		inputAttrs = append(inputAttrs, attr{"required", ""})
	}
	inputAttrs = append(inputAttrs, attr{"value", strings.Join(tokens, tokensSeparator)})

	inner := voidElement("input", inputAttrs)
	if hasSuggestions {
		inner += element("datalist", []attr{{"id", listID}}, options.String())
	}
	return element("span", []attr{{"class", "fuaran-tokens"}}, inner)
}

// ratingField — THE SSR FLOOR FOR A RATING, and it is deliberately not one
// markup but two, chosen by what the document can honour.
//
// A rating that CANNOT be written (no handler, and a value binding no write-back
// can reach — the bound-average display case) has no interaction to floor, so
// this emits the star row as an image with the figure as its accessible name.
// There is nothing for hydration to add, so the two tiers agree.
//
// A WRITABLE rating floors on native RADIOS. With no script a `<span
// role="slider">` can be neither adjusted nor submitted; radios are
// keyboard-adjustable and submit with the form, and the user agent supplies the
// group semantics itself. NO HAND-WRITTEN ARIA IS EMITTED on that arm — the
// combobox floor's rule, for its reason: a static `aria-valuenow` that can never
// change is worse than the native semantics it replaced.
//
// RECORDED KNOWN LIMIT, not claimed as coverage: a writable rating whose current
// value is a FRACTION landing on no enterable position (a bound 4.3 a reader may
// overwrite) checks no radio here. The floor shows the positions a reader can
// choose, not the average; the exact figure rides as `data-fuaran-rating-value`
// so it is visibly not dropped. Hydration restores the fraction.
func (r *renderer) ratingField(field wire.Obj, fieldID string, kind wire.Obj) string {
	// `max` is REQUIRED by the IDL, so a document reaching here has one; the
	// fallback is for a tree built by something other than the decoder.
	max := 5
	if n, ok := kind.Fields["max"].(wire.Int); ok && int(n) > 0 {
		max = int(n)
	}
	allowHalf := false
	if v, ok := kind.Fields["allowHalf"].(wire.Bool); ok {
		allowHalf = bool(v)
	}

	binding := fieldValueBinding(kind, fieldID)
	current := 0.0
	switch v := r.resolve(binding).(type) {
	case wire.Int:
		current = float64(v)
	case wire.Float:
		current = float64(v)
	}
	shown := ratingClamp(max, current)

	var stars strings.Builder
	for _, fill := range ratingFills(max, shown) {
		stars.WriteString(element("span", []attr{
			{"class", "fuaran-rating-star " + ratingFillClass(fill)},
			{"aria-hidden", "true"},
		}, ""))
	}

	_, hasHandler := kind.Fields["onChange"]
	if !hasHandler && !isWriteBackTarget(binding) {
		return element("span", []attr{
			{"class", "fuaran-form-field-control fuaran-rating fuaran-rating-static"},
			{"role", "img"},
			{"aria-label", ratingValueText(max, shown)},
			{"data-fuaran-field", fieldID},
		}, stars.String())
	}

	positions := int(math.Round(float64(max) / ratingStep(allowHalf)))
	var choices strings.Builder
	for i := 1; i <= positions; i++ {
		target := ratingSnap(allowHalf, max, float64(i)*ratingStep(allowHalf))
		radioAttrs := []attr{
			{"type", "radio"},
			{"name", fieldID},
			{"value", strconv.FormatFloat(target, 'f', -1, 64)},
		}
		if math.Abs(target-shown) < 1e-9 {
			radioAttrs = append(radioAttrs, attr{"checked", ""})
		}
		caption := textElement("span", []attr{{"class", "fuaran-rating-choice-label"}}, ratingValueText(max, target))
		choices.WriteString(element("label", []attr{{"class", "fuaran-rating-choice"}},
			voidElement("input", radioAttrs)+caption))
	}

	return element("span", []attr{
		{"class", "fuaran-form-field-control fuaran-rating fuaran-rating-choices"},
		{"data-fuaran-field", fieldID},
		{"data-fuaran-rating-value", ratingValueText(max, shown)},
	}, stars.String()+choices.String())
}
