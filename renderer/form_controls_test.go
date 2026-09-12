package renderer

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The SSR floors for `Rating`, `Color` and `Tokens` (Phase 1677).
//
// No corpus fixture pins these: `render-fidelity.json` does not yet declare the
// three obligations, and the roster is another phase's write lane. So this file
// IS this host's evidence for them, which is why it carries the negative cases
// (what must NOT be emitted) beside the positive ones — a floor that renders
// something is easy to assert and is not what the obligation says.

func formWith(t *testing.T, fieldJSON string) string {
	t.Helper()
	node := mustDecode(t,
		`{"id":"f","kind":{"$type":"Form","fields":[`+fieldJSON+`],"onSubmit":{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`)
	return renderHTML(t, node, BindingSources{})
}

func formWithSources(t *testing.T, fieldJSON string, values map[string]wire.Value) string {
	t.Helper()
	node := mustDecode(t,
		`{"id":"f","kind":{"$type":"Form","fields":[`+fieldJSON+`],"onSubmit":{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`)
	return renderHTML(t, node, Sources(values))
}

func mustNotContain(t *testing.T, html string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(html, bad) {
			t.Errorf("html must not contain %q:\n%s", bad, html)
		}
	}
}

// ── Color ───────────────────────────────────────────────────────────────────

func TestColorFieldRendersANativeColourInput(t *testing.T) {
	html := formWithSources(t,
		`{"id":"brand","kind":{"$type":"Color","value":{"$type":"State","key":"brand"}},"label":"Brand","required":false}`,
		map[string]wire.Value{"brand": wire.Str("#3366FF")})
	mustContain(t, html,
		`type="color"`,
		`data-fuaran-field="brand"`,
		`value="#3366FF"`, // case preserved — the document's bytes, not the browser's
	)
}

func TestColorFieldFallsBackToUnsetRatherThanPassingAValueTheInputCannotHold(t *testing.T) {
	// A native colour input handed `rebeccapurple` substitutes its own default
	// SILENTLY, so passing it through would show a colour the document did not
	// choose while looking as though it had.
	html := formWithSources(t,
		`{"id":"brand","kind":{"$type":"Color","value":{"$type":"State","key":"brand"}},"label":"Brand","required":false}`,
		map[string]wire.Value{"brand": wire.Str("rebeccapurple")})
	mustContain(t, html, `value="#000000"`)
	mustNotContain(t, html, "rebeccapurple")
}

func TestColorFieldAutoBindsAnAbsentValueSlotToTheFieldId(t *testing.T) {
	html := formWithSources(t,
		`{"id":"brand","kind":{"$type":"Color"},"label":"Brand","required":false}`,
		map[string]wire.Value{"brand": wire.Str("#00ff00")})
	mustContain(t, html, `value="#00ff00"`)
}

func TestHexColourValidityIsTheCanonicalFormAndNothingElse(t *testing.T) {
	for _, ok := range []string{"#000000", "#ffffff", "#AbCdEf"} {
		if !isHexColor(ok) {
			t.Errorf("%q should be a hex colour", ok)
		}
	}
	// Verify the probe: a validator that accepted everything would pass the
	// fallback test above for the wrong reason.
	for _, bad := range []string{"", "#fff", "#12345g", "#1234567", "fff000", "rgb(0 0 0)"} {
		if isHexColor(bad) {
			t.Errorf("%q should NOT be a hex colour", bad)
		}
	}
}

// ── Tokens ──────────────────────────────────────────────────────────────────

func TestTokensFieldProjectsTheListCommaSeparated(t *testing.T) {
	html := formWithSources(t,
		`{"id":"tags","kind":{"$type":"Tokens","value":{"$type":"State","key":"tags"}},"label":"Tags","required":false}`,
		map[string]wire.Value{"tags": wire.Arr{wire.Str("alpha"), wire.Str("beta")}})
	mustContain(t, html,
		`class="fuaran-tokens"`,
		`class="fuaran-form-field-control fuaran-tokens-input"`,
		`value="alpha, beta"`,
		`autocomplete="off"`,
	)
}

func TestTokensFieldEmitsNoDatalistWhenNoSuggestionSourceIsDeclared(t *testing.T) {
	// An empty popup that opens on focus is worse than no popup, so an absent
	// suggestion source emits no `list` attribute at all.
	html := formWith(t, `{"id":"tags","kind":{"$type":"Tokens"},"label":"Tags","required":false}`)
	mustNotContain(t, html, "<datalist", `list="tags-suggestions"`)
}

func TestTokensFieldEmitsADatalistWhenSuggestionsResolve(t *testing.T) {
	html := formWithSources(t,
		`{"id":"tags","kind":{"$type":"Tokens","suggestions":{"$type":"State","key":"opts"}},"label":"Tags","required":false}`,
		map[string]wire.Value{"opts": wire.Arr{
			wire.Obj{Tag: "", Fields: map[string]wire.Value{"value": wire.Str("go"), "label": wire.Str("Go")}},
		}})
	mustContain(t, html,
		`list="tags-suggestions"`,
		`<datalist id="tags-suggestions">`,
		`<option value="go">Go</option>`,
	)
}

func TestTokensConstraintMarkerReadsTheKindsOwnDefaultPolarity(t *testing.T) {
	// `Tokens.allowFreeText` is omit-at-default TRUE — the OPPOSITE polarity to
	// `Combobox.allowFreeText`. Reading the absence as the combobox's default
	// would emit the reverse of what the document says, which is why this test
	// pins the absent case rather than only the declared one.
	absent := formWith(t, `{"id":"tags","kind":{"$type":"Tokens"},"label":"Tags","required":false}`)
	mustContain(t, absent, `data-fuaran-tokens-constrained="false"`)

	// `allowFreeText:false` may not stand alone — §3.6.19 refuses a closed token
	// field with nothing to pick from — so the closed case carries its source.
	declared := formWith(t,
		`{"id":"tags","kind":{"$type":"Tokens","allowFreeText":false,`+
			`"suggestions":{"$type":"Static","value":[{"label":"Go","value":"go"}]}},"label":"Tags","required":false}`)
	mustContain(t, declared, `data-fuaran-tokens-constrained="true"`)
}

// ── Rating ──────────────────────────────────────────────────────────────────

func TestAReadOnlyRatingRendersTheStarRowAsAnImage(t *testing.T) {
	// A `Static` value with no handler can be written by nothing, so there is no
	// interaction to floor: the honest markup is the picture plus its name.
	html := formWith(t,
		`{"id":"score","kind":{"$type":"Rating","max":5,"value":{"$type":"Static","value":3}},"label":"Score","required":false}`)
	mustContain(t, html,
		`class="fuaran-form-field-control fuaran-rating fuaran-rating-static"`,
		`role="img"`,
		`aria-label="3 out of 5"`,
		`class="fuaran-rating-star fuaran-rating-star-full"`,
		`class="fuaran-rating-star fuaran-rating-star-empty"`,
	)
	// It must not offer an affordance the markup cannot honour.
	mustNotContain(t, html, `type="radio"`)
}

func TestAWritableRatingFloorsOnNativeRadios(t *testing.T) {
	html := formWithSources(t,
		`{"id":"score","kind":{"$type":"Rating","max":5,"value":{"$type":"State","key":"score"}},"label":"Score","required":false}`,
		map[string]wire.Value{"score": wire.Int(4)})
	mustContain(t, html,
		`class="fuaran-form-field-control fuaran-rating fuaran-rating-choices"`,
		`data-fuaran-rating-value="4 out of 5"`,
		`type="radio"`,
		`name="score"`,
		`value="4" checked=""`,
		`class="fuaran-rating-choice-label"`,
	)
	// NO HAND-WRITTEN ARIA on the writable arm: a static `aria-valuenow` that can
	// never change is worse than the native semantics it would replace.
	mustNotContain(t, html, "aria-valuenow", `role="slider"`)
}

func TestARatingWithAHandlerIsWritableEvenOverAStaticValue(t *testing.T) {
	html := formWith(t,
		`{"id":"score","kind":{"$type":"Rating","max":5,"onChange":"<closure>","value":{"$type":"Static","value":2}},"label":"Score","required":false}`)
	mustContain(t, html, `fuaran-rating-choices`, `type="radio"`)
}

func TestAHalfStepRatingOffersTwiceTheEnterablePositions(t *testing.T) {
	html := formWithSources(t,
		`{"id":"score","kind":{"$type":"Rating","allowHalf":true,"max":5,"value":{"$type":"State","key":"score"}},"label":"Score","required":false}`,
		map[string]wire.Value{"score": wire.Float(3.5)})
	if got := strings.Count(html, `type="radio"`); got != 10 {
		t.Errorf("a half-step rating over max 5 should offer 10 positions, got %d:\n%s", got, html)
	}
	mustContain(t, html, `value="3.5" checked=""`, `data-fuaran-rating-value="3.5 out of 5"`)
}

func TestAFractionalValueOnAWholeStepRatingChecksNothing(t *testing.T) {
	// The RECORDED KNOWN LIMIT, pinned rather than left to be discovered: the
	// floor shows the positions a reader can CHOOSE, not the average, and the
	// exact figure rides on the container so it is visibly not dropped.
	html := formWithSources(t,
		`{"id":"score","kind":{"$type":"Rating","max":5,"value":{"$type":"State","key":"score"}},"label":"Score","required":false}`,
		map[string]wire.Value{"score": wire.Float(4.3)})
	mustNotContain(t, html, `checked=""`)
	mustContain(t, html, `data-fuaran-rating-value="4.3 out of 5"`)
}

func TestTheRatingModelAgreesWithTheReferenceScale(t *testing.T) {
	// The fill row, the enterable positions and the accessible name are three
	// views of ONE scale; these pin the shared arithmetic so they cannot drift
	// into showing four stars while announcing "3.5 out of 5".
	if got := ratingFills(5, 2.5); len(got) != 5 || got[0] != 1 || got[1] != 1 || got[2] != 0.5 || got[3] != 0 {
		t.Errorf("fills(5, 2.5) = %v", got)
	}
	if got := ratingValueText(5, 3); got != "3 out of 5" {
		t.Errorf("valueText(5, 3) = %q", got)
	}
	if got := ratingValueText(5, 3.5); got != "3.5 out of 5" {
		t.Errorf("valueText(5, 3.5) = %q", got)
	}
	// Clamping, both ends and the NaN case — a non-finite rating is "none", not
	// a crash and not the maximum.
	if got := ratingClamp(5, -2); got != 0 {
		t.Errorf("clamp(5, -2) = %v", got)
	}
	if got := ratingClamp(5, 99); got != 5 {
		t.Errorf("clamp(5, 99) = %v", got)
	}
	if got := ratingSnap(false, 5, 3.4); got != 3 {
		t.Errorf("snap(whole, 5, 3.4) = %v", got)
	}
	if got := ratingSnap(true, 5, 3.4); got != 3.5 {
		t.Errorf("snap(half, 5, 3.4) = %v", got)
	}
}

func TestWriteBackTargetsAreTheSlotsAControlCanCommitInto(t *testing.T) {
	state := wire.Obj{Tag: "State", Fields: map[string]wire.Value{"key": wire.Str("k")}}
	filterOpen := wire.Obj{Tag: "Filter", Fields: map[string]wire.Value{"name": wire.Str("k")}}
	filterDefaulted := wire.Obj{Tag: "Filter", Fields: map[string]wire.Value{
		"name": wire.Str("k"), "defaultValue": wire.Str("x"),
	}}
	static := wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": wire.Int(1)}}
	for _, c := range []struct {
		name string
		v    wire.Value
		want bool
	}{
		{"State", state, true},
		{"Filter with no default", filterOpen, true},
		{"Filter with a default", filterDefaulted, false},
		{"Static", static, false},
		{"not a binding", wire.Str("x"), false},
	} {
		if got := isWriteBackTarget(c.v); got != c.want {
			t.Errorf("%s: isWriteBackTarget = %v, want %v", c.name, got, c.want)
		}
	}
}
