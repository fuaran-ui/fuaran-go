package renderer

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The served form field and Map, in the reference host's ruled shape (Phase
// 2391). The reference settled both under its 0.98.0 rulings: a form field is
// `div.fuaran-form-field > label.fuaran-form-label[for] + control[id,
// data-fuaran-field] + help` (R4), and a Map is the served placeholder carrying,
// once its markers resolve, one list item per marker (R12). The old
// `span.fuaran-form-field-label` inside a wrapping `<label>` is retired, so a host
// still emitting it serves a class the reference vocabulary no longer spells.

func TestARequiredFieldIsTheRuledWrapperLabelAndControl(t *testing.T) {
	html := formWith(t, `{"id":"x","kind":{"$type":"Text","onChange":"<closure>"},"label":"Name","required":true}`)
	mustContain(t, html,
		`<div class="fuaran-form-field"><label class="fuaran-form-label" for="x">Name *</label>`+
			`<input class="fuaran-form-field-control" id="x" data-fuaran-field="x" /></div>`)
	mustNotContain(t, html, "fuaran-form-field-label", `<label class="fuaran-form-field"`)
}

func TestAnOptionalFieldHasNoAsterisk(t *testing.T) {
	html := formWith(t, `{"id":"x","kind":{"$type":"Text","onChange":"<closure>"},"label":"Name"}`)
	mustContain(t, html, `<label class="fuaran-form-label" for="x">Name</label>`)
}

func TestTheHelpTextFollowsTheControl(t *testing.T) {
	html := formWith(t,
		`{"id":"x","kind":{"$type":"Text","onChange":"<closure>"},"label":"Name","help":"As on your passport"}`)
	mustContain(t, html,
		`<input class="fuaran-form-field-control" id="x" data-fuaran-field="x" />`+
			`<div class="fuaran-form-help">As on your passport</div></div>`)
}

func TestEveryBespokeControlCarriesTheFieldID(t *testing.T) {
	cases := map[string]string{
		"Combobox": `{"id":"c","kind":{"$type":"Combobox","onChange":"<closure>","options":{"$type":"Static","value":[]}},"label":"C"}`,
		"Color":    `{"id":"c","kind":{"$type":"Color","value":{"$type":"State","key":"c"}},"label":"C"}`,
		"Tokens":   `{"id":"c","kind":{"$type":"Tokens","value":{"$type":"State","key":"c"}},"label":"C"}`,
		"Rating":   `{"id":"c","kind":{"$type":"Rating","max":5,"value":{"$type":"State","key":"c"}},"label":"C"}`,
	}
	for name, field := range cases {
		t.Run(name, func(t *testing.T) {
			html := formWith(t, field)
			mustContain(t, html, `id="c" data-fuaran-field="c"`, `<label class="fuaran-form-label" for="c">C</label>`)
		})
	}
}

func TestAColorFieldIsClassedAsTheReferenceClassesIt(t *testing.T) {
	// R5 — a Color input is `fuaran-form-input fuaran-color-input`.
	html := formWithSources(t,
		`{"id":"brand","kind":{"$type":"Color","value":{"$type":"State","key":"brand"}},"label":"Brand"}`,
		map[string]wire.Value{"brand": wire.Str("#3366FF")})
	mustContain(t, html,
		`<input class="fuaran-form-input fuaran-color-input" id="brand" data-fuaran-field="brand" type="color" value="#3366FF" />`)
}

func TestAMapWhoseMarkersResolveServesTheMarkerList(t *testing.T) {
	node := mustDecode(t, `{"id":"map-1","kind":{"$type":"Map","centreLatitude":51.5,"centreLongitude":-0.12,`+
		`"source":{"$type":"Static","value":[{"label":"London","latitude":51.5,"longitude":-0.12}]},"zoom":6}}`)
	html := renderHTML(t, node, BindingSources{})
	mustContain(t, html,
		`<div class="fuaran-map fuaran-map-ssr-placeholder" data-fuaran-ssr-placeholder="Map" `+
			`data-fuaran-marker-count="1">[Map: 1 markers — hydrates client-side]`+
			`<ul class="fuaran-map-marker-list"><li class="fuaran-map-marker">London @ (51.5000, -0.1200)</li></ul></div>`)
}

func TestAMapWithNoMarkersIsTheCaptionAlone(t *testing.T) {
	node := mustDecode(t, `{"id":"m","kind":{"$type":"Map","centreLatitude":0,"centreLongitude":0,`+
		`"source":{"$type":"Static","value":[]},"zoom":3}}`)
	html := renderHTML(t, node, BindingSources{})
	mustContain(t, html, `data-fuaran-marker-count="0">[Map: 0 markers — hydrates client-side]</div>`)
	mustNotContain(t, html, "fuaran-map-marker-list")
}
