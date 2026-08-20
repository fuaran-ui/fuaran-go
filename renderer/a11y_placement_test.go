package renderer

// WHERE the a11y projection lands.
//
// A node's accessibility projection is emitted on the node's SEMANTIC ELEMENT —
// the single element the kind body renders, when that element rather than the
// wrapper carries the node's semantics: Link (<a>), Button (<button>), Image
// (<img>). Every other kind keeps the projection on the wrapper <div>, which
// always keeps the node's address (data-fuaran-node-id).
//
// These assertions are placement-sensitive on purpose. Every other renderer
// check in this package asserts that a substring appears SOMEWHERE in the
// emitted HTML, which cannot tell a role="link" on the wrapper from one on the
// anchor — and that difference is the entire point: assistive technology does
// not associate a role on a non-interactive container with the interactive
// element inside it.

import (
	"strings"
	"testing"
)

// openTagOf returns the open tag of the first `<tag …>` in the markup.
func openTagOf(html, tag string) string {
	from := html[strings.Index(html, "<"+tag):]
	return from[:strings.Index(from, ">")+1]
}

// wrapperTag returns the wrapper's own open tag — everything up to its first `>`.
func wrapperTag(html string) string { return html[:strings.Index(html, ">")+1] }

const a11ySection = `"accessibility":{"label":"Home","role":"Link"}`

func TestLinkA11yLandsOnTheAnchor(t *testing.T) {
	node := mustDecode(t, `{"id":"lk","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"/home"},"label":"Home"},`+a11ySection+`}`)
	html := RenderHTML(node, nil)

	wrapper := wrapperTag(html)
	if strings.Contains(wrapper, "role=") || strings.Contains(wrapper, "aria-label") {
		t.Errorf("the projection must not sit on the wrapper div:\n%s", wrapper)
	}
	if !strings.Contains(wrapper, `data-fuaran-node-id="lk"`) {
		t.Errorf("the wrapper keeps the node address:\n%s", wrapper)
	}

	anchor := openTagOf(html, "a")
	for _, want := range []string{`role="link"`, `aria-label="Home"`} {
		if !strings.Contains(anchor, want) {
			t.Errorf("anchor missing %q:\n%s", want, anchor)
		}
	}
}

func TestButtonA11yLandsOnTheButton(t *testing.T) {
	node := mustDecode(t, `{"id":"btn","kind":{"$type":"Button","label":"Go","onClick":{"$type":"Navigate","route":"/x"},"variant":"Primary"},`+a11ySection+`}`)
	html := RenderHTML(node, nil)

	if strings.Contains(wrapperTag(html), "aria-label") {
		t.Errorf("the projection must not sit on the wrapper div:\n%s", wrapperTag(html))
	}
	if !strings.Contains(openTagOf(html, "button"), `aria-label="Home"`) {
		t.Errorf("button missing the projection:\n%s", openTagOf(html, "button"))
	}
}

func TestImageA11yLandsOnTheImg(t *testing.T) {
	node := mustDecode(t, `{"id":"img","kind":{"$type":"Image","alt":"Alt","src":{"$type":"Static","value":"/a.png"},"variant":"Default"},`+a11ySection+`}`)
	html := RenderHTML(node, nil)

	if strings.Contains(wrapperTag(html), "aria-label") {
		t.Errorf("the projection must not sit on the wrapper div:\n%s", wrapperTag(html))
	}
	if !strings.Contains(openTagOf(html, "img"), `aria-label="Home"`) {
		t.Errorf("img missing the projection:\n%s", openTagOf(html, "img"))
	}
}

// The other half of the rule — and the assertion that makes the three above
// non-vacuous: a kind whose body is NOT the semantic element keeps the whole
// projection on the wrapper. No single uniform placement can satisfy both.
func TestNonForwardingKindKeepsTheProjectionOnTheWrapper(t *testing.T) {
	node := mustDecode(t, `{"id":"md","kind":{"$type":"Markdown","text":"x"},`+a11ySection+`}`)
	wrapper := wrapperTag(RenderHTML(node, nil))

	for _, want := range []string{`role="link"`, `aria-label="Home"`} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("wrapper missing %q:\n%s", want, wrapper)
		}
	}
}

// The protected-email Link builds its anchor as an entity-encoded opaque
// string, so the projection lands on the wrap <span> — the only element that
// arm owns in every tier. A stated limit, pinned so it stays deliberate.
func TestProtectedEmailLinkA11yLandsOnTheWrapSpan(t *testing.T) {
	node := mustDecode(t, `{"id":"plk","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"mailto:u@e.com"},"label":"u@e.com","protection":"email"},`+a11ySection+`}`)
	html := RenderHTML(node, nil)

	if strings.Contains(wrapperTag(html), "aria-label") {
		t.Errorf("the projection must not sit on the wrapper div:\n%s", wrapperTag(html))
	}
	if !strings.Contains(openTagOf(html, "span"), `aria-label="Home"`) {
		t.Errorf("wrap span missing the projection:\n%s", openTagOf(html, "span"))
	}
}
