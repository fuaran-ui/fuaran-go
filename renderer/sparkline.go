package renderer

import (
	"math"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Sparkline lowering (Phase 1099) — the Go arm of the cross-host contract
// Phase 1098 authored on the F# reference host and pinned as the
// `wire-format-fixtures/sparkline-lowering/*` goldens.
//
// POSTURE: this host LOWERS. Phase 551 decided the go *chart* posture as
// require-pre-lowered and that decision stands — but a chart and a sparkline are
// different questions at that boundary. A chart needs axes, ticks, a legend and
// scale negotiation, and `render-fidelity.json` gives it `"class": "clientOnly"`,
// which is precisely what licenses a marked server placeholder. A sparkline has
// none of that: it is a bounded arithmetic map from a resolved series onto a
// fixed 100x30 canvas, and its fidelity row reads `"class": "none"` — "no
// client-only tier, the parity-checked fallback IS the whole render". There is no
// sanctioned placeholder tier to defer to, so an em-dash over a resolved series
// was a hole in this host's completeness claim (Phase 651: go resolves computed
// values at render time), not a posture.
//
// The lowering reuses the shipped `drawingSVG` builder rather than emitting
// bespoke SVG, so there is no hand-written sparkline geometry in this host and
// the picture cannot drift from the other tiers' without the goldens going red.
//
// The em-dash fallback SURVIVES for the unresolved or empty series, exactly as
// the fidelity row describes: an empty series has no polyline, and the hook
// element carrying the em-dash is a HOST element rather than a Shape, so the
// lowering cannot express it and does not pretend to (the goldens spell that
// case as the JSON literal `null`).

const (
	// The sparkline canvas — 100 x 30 user units, the viewBox every host has
	// emitted since the kind shipped.
	sparklineWidth  = 100.0
	sparklineHeight = 30.0
	// The shipped stroke width. Corpus-pinned by `sparkline-lowering/*`.
	sparklineStrokeWidth = 1.5
	// The vertical inset kept at each edge, so a peak or trough is not clipped
	// by the stroke's own width; the plotted height is the canvas less one inset
	// at each edge.
	sparklineInset      = 1.0
	sparklinePlotHeight = 28.0
	// The flat-series guard: a range below this is treated as 1.0, which places
	// a constant series on its own line rather than dividing by zero.
	sparklineFlatEpsilon = 1e-9
)

// sparkR2 is round-half-up to 2 decimal places — one deterministic rule every
// host reproduces, chosen over the platform's own rounding so a coordinate
// cannot differ by a banker's-rounding tie or a float-print convention.
func sparkR2(x float64) float64 { return math.Floor(x*100.0+0.5) / 100.0 }

// sparklineSeries extracts the resolved series as floats. A value that is not
// numeric at all stops the read and yields "nothing to draw" rather than a
// silently shortened line — a partial picture is worse than the honest fallback.
func sparklineSeries(v wire.Value) ([]float64, bool) {
	arr, ok := v.(wire.Arr)
	if !ok {
		return nil, false
	}
	out := make([]float64, 0, len(arr))
	for _, item := range arr {
		f, ok := numericValue(item)
		if !ok {
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}

// sparklineExtent returns the series min and max by the SAME naive comparison
// fold the reference host uses — F#'s `Array.min` / `Array.max` are
// `if curr < acc then acc <- curr`, i.e. IEEE `<`, so a NaN element never wins a
// comparison and the finite elements decide the extent.
//
// This is NOT interchangeable with `math.Min` / `math.Max`, which PROPAGATE NaN
// in Go: one NaN element would make the extent NaN and collapse every coordinate
// to NaN. For `[1, NaN, 3]` the two disagree outright — this fold yields
// y = [29, NaN, 1] (the finite points still plot), NaN-propagation yields
// y = [NaN, NaN, NaN].
//
// AND THE SHIPPED GOLDENS DO NOT CATCH THAT. Measured, not assumed: swapping
// this fold for `math.Min`/`math.Max` leaves all seven `sparkline-lowering/*`
// vectors passing, because `nonfinite-sentinel` carries BOTH infinities, so
// NaN-propagation (min=max=NaN, range=NaN) and this fold (min=-Inf, max=+Inf,
// range=+Inf) both reach all-NaN y coordinates by different routes and agree on
// the bytes. A `[1,"NaN",3]` vector — a NaN with no infinity beside it — would
// discriminate them; it is reported to the corpus owner rather than authored
// here, since the expected bytes must come from the reference host.
//
// So the fold is written to mirror the reference deliberately, and the reader is
// told that the corpus is not currently what holds it in place.
func sparklineExtent(series []float64) (float64, float64) {
	minV, maxV := series[0], series[0]
	for _, v := range series[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	return minV, maxV
}

// sparklineViewBox is the fixed canvas, as Drawing `viewBox` fields.
func sparklineViewBox() wire.Value {
	return wire.Obj{Fields: map[string]wire.Value{
		"minX":   wire.Float(0),
		"minY":   wire.Float(0),
		"width":  wire.Float(sparklineWidth),
		"height": wire.Float(sparklineHeight),
	}}
}

// tryLowerSparkline lowers a resolved series to the canonical `Drawing` kind
// fields every conformant host reproduces byte for byte — or reports "nothing to
// draw", which is the caller's cue to render its declared fallback element.
//
// The returned map is exactly a `Drawing` NodeKind's field set, so it feeds
// `drawingSVG` for rendering and `wire.EncodeNode` for certification against the
// goldens without a second shape in between.
func tryLowerSparkline(series []float64) (map[string]wire.Value, bool) {
	if len(series) == 0 {
		return nil, false
	}
	n := len(series)
	minV, maxV := sparklineExtent(series)

	rng := maxV - minV
	if rng < sparklineFlatEpsilon {
		rng = 1.0
	}

	points := make(wire.Arr, n)
	for i, v := range series {
		x := 50.0
		if n > 1 {
			x = float64(i) / float64(n-1) * sparklineWidth
		}
		y := sparklineHeight - (v-minV)/rng*sparklinePlotHeight - sparklineInset
		points[i] = wire.Obj{Fields: map[string]wire.Value{
			"x": wire.Float(sparkR2(x)),
			"y": wire.Float(sparkR2(y)),
		}}
	}

	polyline := wire.Obj{Tag: "Polyline", Fields: map[string]wire.Value{
		"points": points,
		"style": wire.Obj{Fields: map[string]wire.Value{
			"stroke":      wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": wire.Str("currentColor")}},
			"strokeWidth": wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": wire.Float(sparklineStrokeWidth)}},
		}},
	}}

	return map[string]wire.Value{
		"shapes":  wire.Arr{polyline},
		"style":   wire.Obj{Fields: map[string]wire.Value{}},
		"viewBox": sparklineViewBox(),
	}, true
}

// sparklineEmpty is the declared fallback for an unresolved or empty series — a
// readable, deterministic stand-in rather than a blank region. Unchanged from
// what this host emitted before the lowering landed.
func sparklineEmpty() string {
	return textElement("div", []attr{{"class", "fuaran-sparkline fuaran-sparkline-empty"}}, emDash)
}

// sparkline renders the Sparkline kind: the `fuaran-sparkline` hook element
// wrapping the lowered Drawing's SVG as a DIRECT child (the stylesheet's
// `.fuaran-sparkline > .fuaran-drawing` rule sizes it), or the em-dash fallback.
func (r *renderer) sparkline(fields map[string]wire.Value) string {
	resolved := resolveBinding(fields["source"], r.sources)
	series, ok := sparklineSeries(resolved)
	if !ok {
		return sparklineEmpty()
	}
	drawing, ok := tryLowerSparkline(series)
	if !ok {
		return sparklineEmpty()
	}
	return element("div", []attr{{"class", "fuaran-sparkline"}}, r.drawingSVG(drawing))
}
