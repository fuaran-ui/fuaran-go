// Package styleobserver is the Go host of the computed-style observer — the
// PURE tier only. It derives AI-facing legibility flags (contrast-below-AA,
// invisible-text, accent-indistinct + the manifest-aware checks) over SUPPLIED
// resolved-style facts, and the flag + observation JSON encode is byte-identical
// to the F#/TS/Python hosts. The live getComputedStyle / layout read-back (the
// browser/Pyodide tier) is out of a headless host's reach by nature and is NOT
// ported here — this observer consumes supplied style facts, never a live DOM.
// stdlib-only.
package styleobserver

import (
	"math"
	"strconv"
	"strings"
)

// Rgba is a resolved colour. R/G/B are 0–255; A (alpha) is 0–1 — matching the
// browser getComputedStyle convention so a live read-back is a direct field fill.
type Rgba struct {
	R, G, B, A float64
}

// Canonical colours.
var (
	Black       = Rgba{0, 0, 0, 1}
	White       = Rgba{255, 255, 255, 1}
	Transparent = Rgba{0, 0, 0, 0}
)

// RGB constructs an opaque colour from 0–255 channels.
func RGB(r, g, b float64) Rgba { return Rgba{R: r, G: g, B: b, A: 1} }

// RGBA constructs a colour with explicit alpha (0–1).
func RGBA(r, g, b, a float64) Rgba { return Rgba{R: r, G: g, B: b, A: a} }

// IsOpaque reports whether the colour is fully opaque (the composite walk stops).
func IsOpaque(c Rgba) bool { return c.A >= 1.0 }

// SameRGB reports RGB equality after rounding channels to the nearest integer
// (alpha ignored). Round-half-to-even, matching Python's round().
func SameRGB(a, b Rgba) bool {
	return math.RoundToEven(a.R) == math.RoundToEven(b.R) &&
		math.RoundToEven(a.G) == math.RoundToEven(b.G) &&
		math.RoundToEven(a.B) == math.RoundToEven(b.B)
}

func hex2(s string, i int) (int, bool) {
	if i+2 > len(s) {
		return 0, false
	}
	v, err := strconv.ParseInt(s[i:i+2], 16, 32)
	if err != nil {
		return 0, false
	}
	return int(v), true
}

func hex1(s string, i int) (int, bool) {
	if i+1 > len(s) {
		return 0, false
	}
	v, err := strconv.ParseInt(s[i:i+1], 16, 32)
	if err != nil {
		return 0, false
	}
	return int(v)*16 + int(v), true
}

// TryParseHex parses a CSS hex colour (#rgb / #rrggbb / #rrggbbaa), or returns
// (Rgba{}, false) if malformed.
func TryParseHex(raw string) (Rgba, bool) {
	s := strings.TrimLeft(strings.TrimSpace(raw), "#")
	switch len(s) {
	case 3:
		r, ok1 := hex1(s, 0)
		g, ok2 := hex1(s, 1)
		b, ok3 := hex1(s, 2)
		if ok1 && ok2 && ok3 {
			return RGB(float64(r), float64(g), float64(b)), true
		}
	case 6:
		r, ok1 := hex2(s, 0)
		g, ok2 := hex2(s, 2)
		b, ok3 := hex2(s, 4)
		if ok1 && ok2 && ok3 {
			return RGB(float64(r), float64(g), float64(b)), true
		}
	case 8:
		r, ok1 := hex2(s, 0)
		g, ok2 := hex2(s, 2)
		b, ok3 := hex2(s, 4)
		a, ok4 := hex2(s, 6)
		if ok1 && ok2 && ok3 && ok4 {
			return RGBA(float64(r), float64(g), float64(b), float64(a)/255.0), true
		}
	}
	return Rgba{}, false
}

// f2 formats a float to 2 decimals, byte-identical to Python's f"{x:.2f}"
// (round-half-to-even on the IEEE double).
func f2(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }

// EncodeRgba encodes as compact JSON — {"r":R,"g":G,"b":B,"a":A} (2-decimal).
func EncodeRgba(c Rgba) string {
	return `{"r":` + f2(c.R) + `,"g":` + f2(c.G) + `,"b":` + f2(c.B) + `,"a":` + f2(c.A) + `}`
}

// FontRole is a coarse font-family classification; the value is the wire string.
type FontRole string

const (
	FontSansSerif FontRole = "SansSerif"
	FontSerif     FontRole = "Serif"
	FontMonospace FontRole = "Monospace"
	FontUnknown   FontRole = "Unknown"
)
