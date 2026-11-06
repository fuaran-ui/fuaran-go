package renderer

import (
	"strings"
	"testing"
)

const citedTablePrefix = `{"id":"t","kind":{"$type":"DataGrid","columns":[],"source":{"$type":"Static","value":[]},"staticRows":{`
const citedTableSuffix = `"headers":["A","B"],"rows":[["x","1"]]}}}`

// Phase 2592 — a table cell cites its figure (WIRE_FORMAT.md §3.6.33).
func TestACitedCellStampsTheTdWithTheLabelFirst(t *testing.T) {
	node := mustDecode(t, citedTablePrefix+`"citations":[{"column":1,"label":"One","reference":"fact:one","row":0}],`+citedTableSuffix)
	html := renderHTML(t, node, BindingSources{})
	want := `<td class="fuaran-table-cell" data-fact="fact:one"><span class="fuaran-cited-label">One </span>1</td>`
	if !strings.Contains(html, want) {
		t.Fatalf("no %s in %s", want, html)
	}
	if !strings.Contains(html, `<td class="fuaran-table-cell">x</td>`) {
		t.Fatalf("an uncited cell moved: %s", html)
	}
	if strings.Contains(html, "fuaran-kind-fact") {
		t.Fatalf("a citation drew a tile: %s", html)
	}
}

func TestACitationAddsOnlyTheAttributeAndTheSpan(t *testing.T) {
	plain := renderHTML(t, mustDecode(t, citedTablePrefix+citedTableSuffix), BindingSources{})
	cited := renderHTML(t, mustDecode(t, citedTablePrefix+`"citations":[{"column":1,"label":"One","reference":"fact:one","row":0}],`+citedTableSuffix), BindingSources{})
	stripped := strings.ReplaceAll(cited, ` data-fact="fact:one"`, "")
	stripped = strings.ReplaceAll(stripped, `<span class="fuaran-cited-label">One </span>`, "")
	if stripped != plain {
		t.Fatalf("a citation changed more than its cell:\n%s\n%s", stripped, plain)
	}
}
