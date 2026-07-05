package canonical

import "testing"

// TestFormatFiniteDouble pins the canonical number form against the corpus
// divergence-zone values (WIRE_FORMAT.md §5; the metric-float-* fixtures) plus a
// spread of fixed-point / scientific boundary cases. These are the same vectors
// the fuaran-py decision record verifies.
func TestFormatFiniteDouble(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		// Corpus divergence-zone (metric-float-*).
		{1e21, "1E+21"},
		{1e-7, "1E-07"},
		{0.30000000000000004, "0.30000000000000004"},
		{1.2345678901234568e17, "1.2345678901234568E+17"},
		{5e-324, "5E-324"}, // smallest positive subnormal
		{-0.0, "0"},        // negative zero collapses

		// Fixed-point range [-4, 16].
		{0, "0"},
		{1, "1"},
		{100, "100"},
		{123.5, "123.5"},
		{0.5, "0.5"},
		{0.0001, "0.0001"}, // exp = -4, last fixed-point value
		{-42.25, "-42.25"},

		// Scientific just outside the range.
		{0.00001, "1E-05"}, // exp = -5, first scientific value
	}
	for _, c := range cases {
		if got := FormatFiniteDouble(c.in); got != c.want {
			t.Errorf("FormatFiniteDouble(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
