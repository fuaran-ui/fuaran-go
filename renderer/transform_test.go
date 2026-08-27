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
		// Row context: the Transform source resolves and the grid renders those
		// rows (Phase 668 — a field-projected bound grid is no longer a
		// row-count placeholder).
		`<td class="fuaran-grid-cell"><span>TCK-2043</span></td>`,
	)
	if got := strings.Count(html, `<tr class="fuaran-grid-row">`); got != 3 {
		t.Errorf("expected the Transform source's 3 rows rendered, got %d:\n%s", got, html)
	}
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
		// matching row, rendered (Phase 668).
		`<td class="fuaran-grid-cell"><span>TCK-2041</span></td>`,
	)
	// The master grid renders all 2 rows, the related grid the 1 filtered row.
	if got := strings.Count(html, `<tr class="fuaran-grid-row">`); got != 3 {
		t.Errorf("expected 2 master rows + 1 related row rendered, got %d:\n%s", got, html)
	}
}

func TestFilterableStaticDashboardResolvesStatically(t *testing.T) {
	node := loadFixtureNode(t, "filterable-static-dashboard")
	html := RenderHTML(node, nil)

	// Both filter params are unset (Filter bindings, no default, no host value),
	// so each filter step is pruned (unset choice ⇒ no constraint) and the full
	// 2-row frame reaches both the chart and the grid. The chart is
	// require-pre-lowered here, so it keeps its typed passthrough placeholder
	// carrying the resolved count; the grid (Phase 668) renders the rows.
	if !strings.Contains(html, `data-fuaran-ssr-placeholder="Chart" data-fuaran-row-count="2"`) {
		t.Errorf("expected the chart placeholder to carry 2 resolved rows:\n%s", html)
	}
	if got := strings.Count(html, `<tr class="fuaran-grid-row">`); got != 2 {
		t.Errorf("expected the grid to render 2 resolved rows, got %d:\n%s", got, html)
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

// ── Phase 668 — the bound-grid render posture, pinned against the corpus ─────
//
// Two corpus grids sit either side of the declared boundary, and both are
// pinned so the behaviour is a contract rather than an accident of whichever
// branch happens to be taken: grid-field-named declares `field`-projected
// columns over a Transform source (renders its rows), grid-transform declares
// no columns at all (keeps the placeholder — nothing server-side to draw).

func TestBoundGridRendersItsTransformRows(t *testing.T) {
	node := loadFixtureNode(t, "grid-field-named")
	html := RenderHTML(node, nil)

	mustContain(t, html,
		`<table class="fuaran-grid">`,
		`<th class="fuaran-grid-header">Dept</th>`,
		`<th class="fuaran-grid-header">Amount</th>`,
		`<td class="fuaran-grid-cell"><span>eng</span></td>`,
		`<td class="fuaran-grid-cell"><span>100</span></td>`,
	)
	if strings.Contains(html, "hydrates client-side") {
		t.Errorf("a field-projected bound grid regressed to the hydration placeholder:\n%s", html)
	}
	if got := strings.Count(html, `<tr class="fuaran-grid-row">`); got != 1 {
		t.Errorf("expected 1 rendered row, got %d:\n%s", got, html)
	}
}

func TestBoundGridWithoutDeclaredColumnsKeepsThePlaceholder(t *testing.T) {
	// grid-transform projects every cell through a closure that does not survive
	// serialisation — it decodes with `columns: []`. There is no declarative
	// projection to draw, so the placeholder stands, and its count is the
	// RESOLVED one (filter → groupBy sum → sort leaves one row): the boundary is
	// declared, and even at the boundary the compute ran.
	node := loadFixtureNode(t, "grid-transform")
	html := RenderHTML(node, nil)

	mustContain(t, html,
		`data-fuaran-ssr-placeholder="DataGrid"`,
		`data-fuaran-row-count="1"`,
		`[Grid: 1 rows `+emDash+` hydrates client-side]`,
	)
}

func TestClosureOnlyColumnsKeepThePlaceholder(t *testing.T) {
	// grid-1 has a resolvable Static row source but its single column projects
	// through a closure (`value`, no `field`). No column declares a field, so the
	// grid stays at the declared boundary rather than emitting blank cells.
	node := loadFixtureNode(t, "grid-1")
	html := RenderHTML(node, nil)

	mustContain(t, html,
		`data-fuaran-ssr-placeholder="DataGrid"`,
		`data-fuaran-row-count="2"`,
	)
}

// TestIslandsBoundGridMatchesTheStaticRender pins the islands contract's
// mismatch-freedom property for a bound grid: the island boundary's static
// children are the same resolved table the full static render emits, so a
// hydrating client attaches rather than replacing a placeholder.
func TestIslandsBoundGridMatchesTheStaticRender(t *testing.T) {
	node := loadFixtureNode(t, "grid-field-named")
	static := RenderHTML(node, nil)
	islands, err := RenderWithIslands(node, nil, map[string]string{"grid-field-named": "grid-island"})
	if err != nil {
		t.Fatalf("RenderWithIslands: %v", err)
	}
	mustContain(t, islands,
		`data-fuaran-island="grid-island"`,
		`<td class="fuaran-grid-cell"><span>eng</span></td>`,
	)
	// The extracted table is the real one (it carries a resolved cell), so the
	// comparison below cannot pass vacuously.
	mustContain(t, gridTableOf(t, static), `<td class="fuaran-grid-cell"><span>eng</span></td>`)
	if !strings.Contains(islands, gridTableOf(t, static)) {
		t.Errorf("the island's static children diverged from the full static render:\nstatic:\n%s\nislands:\n%s", static, islands)
	}
}

// gridTableOf extracts the rendered <table class="fuaran-grid">…</table> from a
// render, failing when there is none (so a regression to the placeholder is a
// failure rather than a vacuously-satisfied comparison).
func gridTableOf(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, `<table class="fuaran-grid">`)
	end := strings.Index(html, `</table>`)
	if start < 0 || end < start {
		t.Fatalf("no rendered grid table in:\n%s", html)
	}
	return html[start : end+len(`</table>`)]
}

// TestMetricTrendIsAResolvedScalarSlot — from the Phase 668 sibling-kind sweep.
// Metric's `trend` is a scalar-bound slot exactly like its `value`, and this
// host used to drop it entirely: no trend div was emitted at all, so a bound
// trend rendered nothing and the markup diverged from every other host. Pinned
// here in both directions — resolved, and emitted-empty when it cannot be.
func TestMetricTrendIsAResolvedScalarSlot(t *testing.T) {
	// A Transform ending in a global single-`count` agg → the 1×1 law resolves
	// the lone cell, formatted through `trendFormat` (1 decimal place).
	resolved := `{"id":"m","kind":{"$type":"Metric","label":"Signups","value":{"$type":"Static","value":42},"trend":{"$type":"Transform","pipeline":[{"$type":"groupBy","aggs":[{"fn":"count","name":"n","of":"v"}],"keys":[]}],"source":{"columns":{"v":{"validity":[true,true],"values":["a","b"]}},"schema":[{"name":"v","type":"string"}]}},"trendFormat":{"$type":"Number","decimals":1}}}`
	// Phase 867 — a RESOLVED trend now carries its sentiment. `+2.0` under the
	// default (omitted) polarity is an improvement.
	mustContain(t, RenderHTML(mustDecode(t, resolved), nil),
		`<div class="fuaran-metric-value">42</div>`,
		`<div class="fuaran-metric-trend fuaran-metric-trend-improving">`+
			`<span class="fuaran-metric-trend-glyph" role="img" aria-label="improving">▲</span>2.0</div>`,
	)

	// An unresolvable trend still emits the div, empty — never an em-dash, and
	// never a silently absent element.
	unresolved := `{"id":"m","kind":{"$type":"Metric","label":"Signups","value":{"$type":"Static","value":42},"trend":{"$type":"Query","name":"nothing-here"}}}`
	mustContain(t, RenderHTML(mustDecode(t, unresolved), nil), `<div class="fuaran-metric-trend"></div>`)

	// A Metric that declares no trend emits no trend div (bytes unchanged).
	none := `{"id":"m","kind":{"$type":"Metric","label":"Signups","value":{"$type":"Static","value":42}}}`
	if html := RenderHTML(mustDecode(t, none), nil); strings.Contains(html, "fuaran-metric-trend") {
		t.Errorf("a Metric with no declared trend must emit no trend div:\n%s", html)
	}
}

// TestMetricTrendSentiment — Phase 867's composition rule, WIRE_FORMAT.md §3.6.1.
//
// The defect this closes was not a missing field: `.fuaran-metric-trend` carried
// exactly ONE class and the stylesheet painted it success-green unconditionally,
// so a −7.34% error rate read green (accidentally right) and a −7.34% revenue
// read green (confidently wrong). A polarity slot decoded but not rendered would
// have changed nothing observable, which is why the render leg is the deliverable
// and the codec arm alone is not.
//
// The expected bytes are derived from the reference server renderer's own source
// (attribute order class → role → aria-label; the glyph span first, the numeric
// text after it as a sibling), so this pins CROSS-HOST markup rather than merely
// this host's self-consistency.
func TestMetricTrendSentiment(t *testing.T) {
	metric := func(polarity, trend string) string {
		slot := ""
		if polarity != "" {
			slot = `,"trendPolarity":"` + polarity + `"`
		}
		return `{"id":"m","kind":{"$type":"Metric","label":"Avg wait","tone":"Warning",` +
			`"trend":{"$type":"Static","value":` + trend + `},` +
			`"trendFormat":{"$type":"Percent","decimals":2}` + slot +
			`,"value":{"$type":"Static","value":80}}}`
	}
	trendDiv := func(sentiment, glyph, text string) string {
		return `<div class="fuaran-metric-trend fuaran-metric-trend-` + sentiment + `">` +
			`<span class="fuaran-metric-trend-glyph" role="img" aria-label="` + sentiment + `">` +
			glyph + `</span>` + text + `</div>`
	}

	// The corpus fixture's own case: a FALLING wait time under LowerIsBetter is
	// an improvement. This one node is the whole argument for the slot.
	mustContain(t, RenderHTML(mustDecode(t, metric("LowerIsBetter", "-0.0734")), nil),
		trendDiv("improving", "▲", "-7.34%"))

	// The same number without the declaration is a regression — the pair below is
	// what proves the slot is READ rather than decoded and ignored.
	mustContain(t, RenderHTML(mustDecode(t, metric("", "-0.0734")), nil),
		trendDiv("regressing", "▼", "-7.34%"))

	// Rising, both ways round.
	mustContain(t, RenderHTML(mustDecode(t, metric("", "0.0734")), nil),
		trendDiv("improving", "▲", "7.34%"))
	mustContain(t, RenderHTML(mustDecode(t, metric("LowerIsBetter", "0.0734")), nil),
		trendDiv("regressing", "▼", "7.34%"))

	// Zero is neither, under either declaration (clause 2: a zero trend).
	for _, polarity := range []string{"", "LowerIsBetter"} {
		mustContain(t, RenderHTML(mustDecode(t, metric(polarity, "0")), nil),
			trendDiv("unchanged", "→", "0.00%"))
	}

	// Clause 3 — the numeric text, ITS SIGN INCLUDED, is unchanged by polarity.
	// The cheap trick this rules out is an emitter flipping the sign so up is
	// always good, which would be a false statement about the world.
	inverted := RenderHTML(mustDecode(t, metric("LowerIsBetter", "-0.0734")), nil)
	if strings.Contains(inverted, ">7.34%<") || !strings.Contains(inverted, "-7.34%") {
		t.Errorf("polarity changed the number's sign — it may change how a number READS, never what it SAYS:\n%s", inverted)
	}

	// And nothing writes back to `tone`: the tile stays Warning while the trend
	// reads improving. A single tone slot could never have said both, which is
	// the pair the fixture exists to pin.
	if !strings.Contains(inverted, `class="fuaran-metric fuaran-metric-warning"`) {
		t.Errorf("the tile's tone must be untouched by trend sentiment:\n%s", inverted)
	}
}
