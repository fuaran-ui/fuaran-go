package renderer

import (
	"testing"
)

// TestMetricComparisonsAreTrendLines — fuaran#2472. Each further comparison on
// a Metric is one more trend line after the primary trend: its own glyph and
// sentiment under its OWN polarity, then the figure in its format, then its
// label. The markup below is the reference server renderer's bytes for the
// corpus fixture metric-comparison-inverted (the F#, TypeScript and Python
// server renderers emit it identically), so this pins cross-host markup.
func TestMetricComparisonsAreTrendLines(t *testing.T) {
	inverted := `{"id":"metric-comparison-inverted","kind":{"$type":"Metric","comparisons":[{"label":"minutes vs last year","polarity":"LowerIsBetter","value":{"$type":"Static","value":12}}],"label":"Avg wait","subtext":"trend is vs last week","trend":{"$type":"Static","value":-0.0734},"trendFormat":{"$type":"Percent","decimals":2},"trendPolarity":"LowerIsBetter","value":{"$type":"Static","value":80}}}`
	mustContain(t, renderHTML(t, mustDecode(t, inverted), BindingSources{}),
		`<div class="fuaran-metric-trend fuaran-metric-trend-improving">`+
			`<span class="fuaran-metric-trend-glyph" role="img" aria-label="improving">▲</span>-7.34%</div>`+
			`<div class="fuaran-metric-trend fuaran-metric-trend-regressing">`+
			`<span class="fuaran-metric-trend-glyph" role="img" aria-label="regressing">▼</span>12 minutes vs last year</div>`+
			`<div class="fuaran-metric-subtext">trend is vs last week</div>`,
	)

	// An unresolved comparison keeps the bare div: no sentiment, no label.
	unresolved := `{"id":"m","kind":{"$type":"Metric","comparisons":[{"label":"vs last week","value":{"$type":"Query","name":"nothing-here"}}],"label":"Orders","value":{"$type":"Static","value":1}}}`
	mustContain(t, renderHTML(t, mustDecode(t, unresolved), BindingSources{}), `<div class="fuaran-metric-trend"></div>`)
}
