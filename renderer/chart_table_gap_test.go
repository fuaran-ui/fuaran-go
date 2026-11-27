package renderer

import (
	"strings"
	"testing"
)

// TestChartDataTableIsADeclaredGap pins Phase 2804's decision for this host (see
// the chart arm in render.go): neither a raw Chart, which renders the marked
// placeholder, nor a pre-lowered chart, which arrives as a Drawing, offers the
// data-table disclosure the lowering hosts draw. A change that starts emitting
// one turns this red, so the gap is revisited rather than half-closed.
func TestChartDataTableIsADeclaredGap(t *testing.T) {
	raw := mustDecode(t, `{"id":"c","kind":{"$type":"Chart","kind":"Bar","source":{"$type":"Static","value":[{"q":"Q1","v":1},{"q":"Q2","v":2}]},"xField":"q","yFields":["v"]}}`)
	drawn := mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[],"style":{},"viewBox":{"height":400,"minX":0,"minY":0,"width":640}}}`)
	for name, html := range map[string]string{
		"raw chart":         renderHTML(t, raw, BindingSources{}),
		"pre-lowered chart": renderHTML(t, drawn, BindingSources{}),
	} {
		for _, forbidden := range []string{`fuaran-chart-table`, `aria-details`, `<details`} {
			if strings.Contains(html, forbidden) {
				t.Errorf("%s: found %q, but the data table is a declared gap on this host:\n%s", name, forbidden, html)
			}
		}
	}
	// Not vacuous: the pre-lowered chart did render as a drawing.
	if html := renderHTML(t, drawn, BindingSources{}); !strings.Contains(html, `class="fuaran-drawing"`) {
		t.Errorf("the pre-lowered chart did not render as a drawing:\n%s", html)
	}
}
