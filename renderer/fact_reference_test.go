package renderer

import (
	"strings"
	"testing"
)

// Phase 2535 — a cited figure (WIRE_FORMAT.md §3.6.31): `data-fact` on the tile's
// root, through the ordinary attribute encoder; an uncited tile carries none.
func TestACitedFactStampsDataFactOnItsTile(t *testing.T) {
	cited := mustDecode(t, `{"id":"f","kind":{"$type":"Fact","label":"Revenue","reference":"fact:a\"b","value":"1"}}`)
	html := renderHTML(t, cited, BindingSources{})
	if want := `<div class="fuaran-fact fuaran-fact-default" data-fact="fact:a&quot;b">`; !strings.Contains(html, want) {
		t.Fatalf("no %s in %s", want, html)
	}
	uncited := mustDecode(t, `{"id":"f","kind":{"$type":"Fact","label":"Revenue","value":"1"}}`)
	if html := renderHTML(t, uncited, BindingSources{}); strings.Contains(html, "data-fact") {
		t.Fatalf("an uncited tile carries data-fact: %s", html)
	}
}
