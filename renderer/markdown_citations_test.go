package renderer

import (
	"strings"
	"testing"
)

// Phase 2591 — a figure cited inside a sentence (WIRE_FORMAT.md §3.6.32). The render
// bytes are pinned by the shared markdown corpus; this reaches the node path.
func TestACitedFigureIsASpanInTheSentence(t *testing.T) {
	node := mustDecode(t, `{"id":"m","kind":{"$type":"Markdown","citations":[{"label":"Online share","name":"share","reference":"fact:share"},{"name":"spend","reference":"fact:spend"}],"text":"It took [23%](cite:share) of [£412k](cite:spend)."}}`)
	html := renderHTML(t, node, BindingSources{})
	want := `<p>It took <span class="fuaran-cited" data-fact="fact:share"><span class="fuaran-cited-label">Online share </span>23%</span> of <span class="fuaran-cited" data-fact="fact:spend">£412k</span>.</p>`
	if !strings.Contains(html, want) {
		t.Fatalf("no %s in %s", want, html)
	}
	if strings.Contains(html, "fuaran-kind-fact") || strings.Contains(html, "<a ") {
		t.Fatalf("a citation drew a tile or a link: %s", html)
	}
}
