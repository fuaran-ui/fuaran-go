package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Phase 651 — render-time compute resolution. These are parity-shaped tests: the
// static-HTML (and islands) emission must carry the compute values RESOLVED,
// exactly as the F#/py/rs hosts do, so a Go binary emits complete static output.
// The assertions are strict — a regression to the pre-651 unresolved placeholder
// (an em-dash, an empty slot, or a zero row-count) fails loudly.

// findFixtureCorpus walks up from the working directory to the shared
// wire-format-fixtures corpus (a sibling of the repo). Returns "" when absent,
// so the repo stays standalone-testable.
func findFixtureCorpus() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		manifest := filepath.Join(dir, "wire-format-fixtures", "manifest.json")
		if _, err := os.Stat(manifest); err == nil {
			return filepath.Dir(manifest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadFixtureNode reads and decodes a nodes/<id>.json corpus fixture, skipping
// on a standalone checkout.
func loadFixtureNode(t *testing.T, id string) wire.Node {
	t.Helper()
	corpus := findFixtureCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "nodes", id+".json"))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", id, err)
	}
	node, err := wire.DecodeNode(string(raw))
	if err != nil {
		t.Fatalf("decoding fixture %s: %v", id, err)
	}
	return node
}

// mustContain asserts every wanted substring is present, failing loudly on
// divergence (the pre-651 unresolved forms would be missing).
func mustContain(t *testing.T, html string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(html, want) {
			t.Errorf("static output missing %q:\n%s", want, html)
		}
	}
}

func TestScalarTransformCompositionResolvesStatically(t *testing.T) {
	node := loadFixtureNode(t, "scalar-transform-composition")
	html := RenderHTML(node, nil)

	mustContain(t, html,
		// Badge scalar slot: filter severity==critical + groupBy count → 2.
		`class="fuaran-badge fuaran-badge-critical">2</span>`,
		// Callout scalar slot: Selection.defaultValue-param → filter + project +
		// limit 1 → the pre-selected ticket's alert text.
		`<div class="fuaran-callout-body">TCK-2041 breaches SLA in 2 hours</div>`,
		// Row context: the Transform source's row count reaches the placeholder.
		`data-fuaran-row-count="3"`,
	)
	// Never a silent unresolved slot where a value resolves.
	if strings.Contains(html, `fuaran-badge-critical">`+emDash) {
		t.Errorf("badge scalar slot regressed to the em-dash placeholder:\n%s", html)
	}
}

func TestMasterDetailPreselectedResolvesStatically(t *testing.T) {
	node := loadFixtureNode(t, "master-detail-preselected")
	html := RenderHTML(node, nil)

	mustContain(t, html,
		// Fact scalar slot: an unwritten Selection resolves to its defaultValue
		// (Phase 629) — preselected detail renders the ticket id.
		`<div class="fuaran-fact-value"><span>TCK-2041</span></div>`,
		// related-grid: Transform param from the same Selection default → the one
		// matching row.
		`data-fuaran-row-count="1"`,
	)
}

func TestFilterableStaticDashboardResolvesStatically(t *testing.T) {
	node := loadFixtureNode(t, "filterable-static-dashboard")
	html := RenderHTML(node, nil)

	// Both filter params are unset (Filter bindings, no default, no host value),
	// so each filter step is pruned (unset choice ⇒ no constraint) and the full
	// 2-row frame reaches both the chart and the grid placeholders.
	if strings.Count(html, `data-fuaran-row-count="2"`) < 2 {
		t.Errorf("expected the chart and grid placeholders to each carry 2 resolved rows:\n%s", html)
	}
}

// TestIslandsSkeletonCarriesResolvedValues pins that the islands emission path
// carries the SAME resolved values as the full static render (correct-before-
// hydration): the boundary wrapper's static children are the resolved subtree.
func TestIslandsSkeletonCarriesResolvedValues(t *testing.T) {
	node := loadFixtureNode(t, "scalar-transform-composition")
	html, err := RenderWithIslands(node, nil, map[string]string{"critical-count-badge": "badge-island"})
	if err != nil {
		t.Fatalf("RenderWithIslands: %v", err)
	}
	mustContain(t, html,
		`data-fuaran-island="badge-island"`,                   // the island boundary is emitted
		`class="fuaran-badge fuaran-badge-critical">2</span>`, // and its static children carry the resolved value
	)
}

// TestScalarTransform1x1Law locks the Phase 632 scalar law's edge cases beyond
// the fixtures: a >1×1 result is a loud miss (absence, never a silent first
// cell); an empty non-count result is absence; a trailing global count over an
// empty frame completes to 0.
func TestScalarTransform1x1Law(t *testing.T) {
	// Two source rows, no aggregation → a >1-row result in a scalar slot is
	// ambiguous → the badge renders absence (empty), never the first row.
	ambiguous := `{"id":"b","kind":{"$type":"Badge","label":{"$type":"Bound","binding":{"$type":"Transform","pipeline":[{"$type":"project","cols":[{"a":"v","b":"v"}]}],"source":{"columns":{"v":{"validity":[true,true],"values":["a","b"]}},"schema":[{"name":"v","type":"string"}]}}},"variant":"Neutral"}}`
	html := RenderHTML(mustDecode(t, ambiguous), nil)
	mustContain(t, html, `class="fuaran-badge fuaran-badge-neutral"></span>`)
	if strings.Contains(html, `>a<`) {
		t.Errorf("ambiguous >1×1 slot must not resolve to a silent first cell:\n%s", html)
	}

	// A filter matching nothing, then a trailing global count → the count of
	// nothing is 0 (not absence).
	countZero := `{"id":"b","kind":{"$type":"Badge","label":{"$type":"Bound","binding":{"$type":"Transform","pipeline":[{"$type":"filter","pred":{"$type":"binary","left":{"$type":"col","name":"v"},"op":"eq","right":{"$type":"lit","cell":{"$type":"Str","value":"zzz"}}}},{"$type":"groupBy","aggs":[{"fn":"count","name":"n","of":"v"}],"keys":[]}],"source":{"columns":{"v":{"validity":[true,true],"values":["a","b"]}},"schema":[{"name":"v","type":"string"}]}}},"variant":"Neutral"}}`
	if h := RenderHTML(mustDecode(t, countZero), nil); !strings.Contains(h, `fuaran-badge-neutral">0</span>`) {
		t.Errorf("trailing global count over an empty frame must complete to 0:\n%s", h)
	}

	// A filter matching nothing WITHOUT a trailing count → empty → absence.
	empty := `{"id":"b","kind":{"$type":"Badge","label":{"$type":"Bound","binding":{"$type":"Transform","pipeline":[{"$type":"filter","pred":{"$type":"binary","left":{"$type":"col","name":"v"},"op":"eq","right":{"$type":"lit","cell":{"$type":"Str","value":"zzz"}}}}],"source":{"columns":{"v":{"validity":[true,true],"values":["a","b"]}},"schema":[{"name":"v","type":"string"}]}}},"variant":"Neutral"}}`
	if h := RenderHTML(mustDecode(t, empty), nil); !strings.Contains(h, `fuaran-badge-neutral"></span>`) {
		t.Errorf("an empty non-count result must render absence:\n%s", h)
	}
}
