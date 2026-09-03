package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/renderer"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// LIST-valued `Binding.Transform` params — WIRE_FORMAT.md, "LIST-valued
// `Binding.Transform` params (Phase 610)" and its §11.0 per-host adoption table.
//
// This is a RENDER-parity leg, not a codec one, in exactly the shape
// state_seeding_test.go takes and for the same reason: the bytes of
// nodes/multiselect-chip-list-param.json round-trip identically on a host that
// resolves list params and on one that does not, so every codec family here
// passes either way. What separates the two is the DERIVED VALUE — which rows a
// static render draws — so that is what this file asserts.
//
// Measured on this host before the adoption, against the same fixture:
//
//	nothing selected  → 3 rows (already correct: the param never bound, so the
//	                    pre-existing unbound-prune already showed the unfiltered
//	                    table — the one behaviour that was right by accident)
//	{"eng","ops"}     → 0 rows and the hydration placeholder (a bound LIST param
//	                    was a "non-scalar value" error that failed the whole
//	                    transform)
//	[] (deselect all) → 0 rows, same error, where the rule says UNFILTERED
//
// The static-emission posture is what makes this leg meaningful here: go
// resolves compute at render time (Phase 651) and holds no UI session state, so
// "what does a static render emit with nothing selected" is a total question
// this host can answer, and its answer is the unfiltered table.

const listParamFixture = "multiselect-chip-list-param"

// loadListParamFixture decodes the shared corpus fixture, skipping on a
// standalone checkout (the corpus is a sibling repo, not vendored).
func loadListParamFixture(t *testing.T) wire.Node {
	t.Helper()
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "nodes", listParamFixture+".json"))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", listParamFixture, err)
	}
	node, derr := wire.DecodeNode(string(raw))
	if derr != nil {
		t.Fatalf("decoding fixture %s: %v", listParamFixture, derr)
	}
	return node
}

// renderedDepts names, in row order, the department cell of every rendered grid
// row. Narrow on purpose: it reads the emitted table rather than the decoded
// tree, because the obligation is a claim about output.
func renderedDepts(html string) []string {
	var out []string
	const rowOpen = `<tr class="fuaran-grid-row">`
	const cellOpen = `<td class="fuaran-grid-cell"><span>`
	rest := html
	for {
		i := strings.Index(rest, rowOpen)
		if i < 0 {
			return out
		}
		rest = rest[i+len(rowOpen):]
		j := strings.Index(rest, cellOpen)
		if j < 0 {
			return out
		}
		cell := rest[j+len(cellOpen):]
		end := strings.Index(cell, "<")
		if end < 0 {
			return out
		}
		out = append(out, cell[:end])
	}
}

func assertDepts(t *testing.T, html string, want ...string) {
	t.Helper()
	got := renderedDepts(html)
	if len(got) != len(want) {
		t.Fatalf("expected rows %v, got %v:\n%s", want, got, html)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected rows %v, got %v:\n%s", want, got, html)
		}
	}
}

// Behaviour 2, the acceptance criterion 610 pinned as output: with NOTHING
// selected a static render emits the UNFILTERED table. Asserted twice, because
// there are two distinct spellings of "nothing selected" and only one of them
// was ever right on this host — no host value at all, and an explicitly empty
// selection. The rule makes them the same answer.
func TestListParamNothingSelectedRendersTheUnfilteredTable(t *testing.T) {
	node := loadListParamFixture(t)

	t.Run("no host value", func(t *testing.T) {
		assertDepts(t, renderer.RenderHTML(node, nil), "eng", "sales", "ops")
	})

	t.Run("explicitly deselected to empty", func(t *testing.T) {
		html := renderer.RenderHTML(node, renderer.BindingSources{"depts": wire.Arr{}})
		assertDepts(t, html, "eng", "sales", "ops")
		if strings.Contains(html, `data-fuaran-row-count="0"`) {
			t.Errorf("deselecting everything must not fail the transform to an empty grid:\n%s", html)
		}
	})
}

// Behaviour 1 as OUTPUT: a bound list param substitutes into the pipeline before
// evaluation, so the render carries exactly the selected rows in frame order.
func TestListParamSelectionScopesTheRenderedRows(t *testing.T) {
	node := loadListParamFixture(t)

	t.Run("two selected", func(t *testing.T) {
		html := renderer.RenderHTML(node, renderer.BindingSources{
			"depts": wire.Arr{wire.Str("eng"), wire.Str("ops")},
		})
		assertDepts(t, html, "eng", "ops")
	})

	t.Run("one selected", func(t *testing.T) {
		html := renderer.RenderHTML(node, renderer.BindingSources{
			"depts": wire.Arr{wire.Str("sales")},
		})
		assertDepts(t, html, "sales")
	})

	t.Run("a selected value matching no row", func(t *testing.T) {
		// A genuine constraint that nothing satisfies is an EMPTY table — which
		// is the case the empty-selection rule above must never be confused
		// with, so it is pinned beside it.
		html := renderer.RenderHTML(node, renderer.BindingSources{
			"depts": wire.Arr{wire.Str("legal")},
		})
		assertDepts(t, html)
	})
}

// The islands emission carries the same resolved rows as the full static render
// (correct-before-hydration: hydration may re-resolve, never first-fill).
func TestListParamResolutionCarriesToTheIslandsSkeleton(t *testing.T) {
	node := loadListParamFixture(t)
	sources := renderer.BindingSources{"depts": wire.Arr{wire.Str("eng"), wire.Str("ops")}}

	html, err := renderer.RenderWithIslands(node, sources, map[string]string{"dept-chip": "chip-island"})
	if err != nil {
		t.Fatalf("RenderWithIslands: %v", err)
	}
	if !strings.Contains(html, `data-fuaran-island="chip-island"`) {
		t.Fatalf("the island boundary was not emitted:\n%s", html)
	}
	assertDepts(t, html, "eng", "ops")
}

// Behaviour 3 as OUTPUT: a kind mismatch is REFUSED, never silently substituted.
// The discriminating assertion is not "no rows" — a pruned-away or failed
// transform both draw nothing — it is that the render never shows the WRONGLY
// SCOPED answer a lenient host would produce.
func TestListParamKindMismatchIsRefusedNotSilentlyScoped(t *testing.T) {
	node := loadListParamFixture(t)

	// A SCALAR bound to a name the pipeline reads as an in/param. A host that
	// coerced it would emit the single "eng" row; a host that ignored the
	// mismatch and pruned would emit all three. Neither is admissible.
	html := renderer.RenderHTML(node, renderer.BindingSources{"depts": wire.Str("eng")})
	assertDepts(t, html)
	if strings.Contains(html, `<td class="fuaran-grid-cell"><span>eng</span></td>`) {
		t.Errorf("a scalar bound to an in/param must not silently scope the rows:\n%s", html)
	}

	// A LIST holding a non-scalar item has no membership reading at all.
	nested := renderer.RenderHTML(node, renderer.BindingSources{
		"depts": wire.Arr{wire.Str("eng"), wire.Arr{wire.Str("ops")}},
	})
	assertDepts(t, nested)
}
