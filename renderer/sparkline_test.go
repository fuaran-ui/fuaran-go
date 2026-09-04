package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// TestSparklineLoweringMatchesGoldens is this host's certification against the
// Phase 1098 `sparkline-lowering/*` contract: for every vector, the series in
// `<name>.input.json` lowers to bytes IDENTICAL to `<name>.expected.json`.
//
// The comparison is on the canonical wire encoding of the lowered Drawing node,
// not on a structural walk, because the goldens ARE bytes and a structural
// comparison would quietly tolerate a coordinate that prints differently — which
// is exactly the class of drift a hand-copied lowering produces.
//
// `empty.expected.json` is the JSON literal `null`: the vector asserts the host
// drew NO drawing and fell back, which is the one outcome the lowering cannot
// express as a Drawing (the em-dash hook is a host element, not a Shape).
func TestSparklineLoweringMatchesGoldens(t *testing.T) {
	corpus := findFixtureCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	dir := filepath.Join(corpus, "sparkline-lowering")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("sparkline-lowering goldens not found: %v", err)
	}

	ran := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".input.json") {
			continue
		}
		vector := strings.TrimSuffix(name, ".input.json")
		ran++
		t.Run(vector, func(t *testing.T) {
			series := loadSparklineGoldenSeries(t, dir, vector)
			want := readGoldenBytes(t, filepath.Join(dir, vector+".expected.json"))

			lowered, ok := tryLowerSparkline(series)
			if !ok {
				if want != "null" {
					t.Fatalf("lowering reported nothing to draw, but the golden expects a drawing:\n%s", want)
				}
				return
			}
			if want == "null" {
				t.Fatalf("golden expects NO drawing, but the lowering produced one")
			}

			got, err := wire.EncodeNode(wire.Node{
				ID:   "sparkline-" + vector,
				Kind: wire.Obj{Tag: "Drawing", Fields: lowered},
			})
			if err != nil {
				t.Fatalf("encoding the lowered drawing: %v", err)
			}
			if got != want {
				t.Errorf("not byte-identical to the golden\n got: %s\nwant: %s", got, want)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no sparkline-lowering input vectors found")
	}
}

// loadSparklineGoldenSeries reads `<vector>.input.json` — `{"series":[…]}` — and
// resolves it through the HOST'S OWN decoder rather than a bespoke JSON read, so
// the non-finite sentinels reach the lowering exactly as they reach it from a
// real document. That is deliberate: it makes this test a joint proof of the
// decode path and the lowering, which is precisely the seam Phase 1099 closes.
func loadSparklineGoldenSeries(t *testing.T, dir, vector string) []float64 {
	t.Helper()
	raw := readGoldenBytes(t, filepath.Join(dir, vector+".input.json"))
	// `{"series":[…]}` → a Sparkline document over the same array, decoded by the
	// host's own float-sequence path.
	inner := strings.TrimSpace(raw)
	const prefix = `{"series":`
	if !strings.HasPrefix(inner, prefix) || !strings.HasSuffix(inner, "}") {
		t.Fatalf("unexpected input vector shape: %s", raw)
	}
	arr := strings.TrimSuffix(strings.TrimPrefix(inner, prefix), "}")
	node, err := wire.DecodeNode(`{"id":"s","kind":{"$type":"Sparkline","source":{"$type":"Static","value":` + arr + `}}}`)
	if err != nil {
		t.Fatalf("decoding the input series %s: %v", arr, err)
	}
	source, _ := node.Kind.Fields["source"].(wire.Obj)
	series, ok := sparklineSeries(source.Fields["value"])
	if !ok {
		t.Fatalf("input series did not resolve to floats: %s", arr)
	}
	return series
}

func readGoldenBytes(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return strings.TrimRight(string(raw), "\r\n")
}

// TestSparklineRendersLoweredDrawing pins the emitted markup: the
// `fuaran-sparkline` hook element wrapping the lowered Drawing's SVG as a DIRECT
// child. The reference stylesheet sizes it with `.fuaran-sparkline >
// .fuaran-drawing`, so an extra wrapper would leave the picture unsized while
// every byte-level assertion still passed.
func TestSparklineRendersLoweredDrawing(t *testing.T) {
	node := mustDecode(t, `{"id":"spark-1","kind":{"$type":"Sparkline","source":{"$type":"Static","value":[1,2,3,2,4]}}}`)
	html := RenderHTML(node, nil)

	for _, want := range []string{
		`<div class="fuaran-sparkline"><svg class="fuaran-drawing"`,
		`viewBox="0 0 100 30"`,
		`<polyline class="fuaran-drawing-polyline" points="0,29 25,19.67 50,10.33 75,19.67 100,1"`,
		`stroke="currentColor"`,
		`stroke-width="1.5"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("lowered sparkline missing %q:\n%s", want, html)
		}
	}
	// The retired placeholder must not survive for a RESOLVED series.
	if strings.Contains(html, "fuaran-sparkline-empty") || strings.Contains(html, emDash) {
		t.Errorf("a resolved series still emitted the em-dash placeholder:\n%s", html)
	}
}

// TestSparklineEmptyKeepsEmDashFallback pins the OTHER half of the contract, the
// half `render-fidelity.json` is explicit about: an unresolved or empty series
// keeps the em-dash hook element — a readable, deterministic stand-in rather
// than a blank region or an empty canvas.
func TestSparklineEmptyKeepsEmDashFallback(t *testing.T) {
	cases := map[string]string{
		"empty series":     `{"id":"s","kind":{"$type":"Sparkline","source":{"$type":"Static","value":[]}}}`,
		"unresolved query": `{"id":"s","kind":{"$type":"Sparkline","source":{"$type":"Query","accessor":"<closure>","deps":[],"name":"series"}}}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			html := RenderHTML(mustDecode(t, doc), nil)
			if want := `<div class="fuaran-sparkline fuaran-sparkline-empty">` + emDash + `</div>`; !strings.Contains(html, want) {
				t.Errorf("missing the em-dash fallback %q:\n%s", want, html)
			}
			if strings.Contains(html, "<svg") {
				t.Errorf("nothing to draw, but SVG was emitted:\n%s", html)
			}
		})
	}
}
