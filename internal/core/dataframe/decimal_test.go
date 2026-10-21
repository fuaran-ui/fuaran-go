package dataframe

import (
	"encoding/json"
	"strings"
	"testing"
)

// The seven rounding modes over the well-known table (the one
// java.math.RoundingMode documents), at scale 0 — every mode, both signs, a tie
// on an even and an odd neighbour, and values either side of the half.
func TestRoundingModesTable(t *testing.T) {
	inputs := []string{"5.5", "2.5", "1.6", "1.1", "1.0", "-1.0", "-1.1", "-1.6", "-2.5", "-5.5"}
	want := map[string][]string{
		RoundUp:       {"6", "3", "2", "2", "1", "-1", "-2", "-2", "-3", "-6"},
		RoundDown:     {"5", "2", "1", "1", "1", "-1", "-1", "-1", "-2", "-5"},
		RoundCeiling:  {"6", "3", "2", "2", "1", "-1", "-1", "-1", "-2", "-5"},
		RoundFloor:    {"5", "2", "1", "1", "1", "-1", "-2", "-2", "-3", "-6"},
		RoundHalfUp:   {"6", "3", "2", "1", "1", "-1", "-1", "-2", "-3", "-6"},
		RoundHalfDown: {"5", "2", "2", "1", "1", "-1", "-1", "-2", "-2", "-5"},
		RoundHalfEven: {"6", "2", "2", "1", "1", "-1", "-1", "-2", "-2", "-6"},
	}
	if len(want) != len(roundingModes) {
		t.Fatalf("the table covers %d modes; the vocabulary has %d", len(want), len(roundingModes))
	}
	for mode, outs := range want {
		for i, in := range inputs {
			got, ok := decimalQuantize(mode, 0, in)
			if !ok || got != outs[i] {
				t.Errorf("%s(%s) = %q, want %q", mode, in, got, outs[i])
			}
		}
	}
}

// A quotient is ONE rounding of the exact value: 2/3 at 4 places, -2/3, a zero
// divisor (null), and a result that rounds to zero carries no sign.
func TestDecimalDivide(t *testing.T) {
	cases := []struct {
		mode, a, b string
		scale      int
		want       string
		null       bool
	}{
		{RoundHalfEven, "2", "3", 4, "0.6667", false},
		{RoundDown, "2", "3", 4, "0.6666", false},
		{RoundHalfEven, "-2", "3", 4, "-0.6667", false},
		{RoundCeiling, "-2", "3", 0, "0", false},
		{RoundHalfUp, "1", "0", 2, "", true},
		{RoundHalfEven, "1.25", "0.5", 0, "2", false},
		{RoundHalfEven, "0.125", "1", 2, "0.12", false},
	}
	for _, c := range cases {
		got, isNull, ok := decimalDivide(c.mode, c.scale, c.a, c.b)
		if !ok || isNull != c.null || got != c.want {
			t.Errorf("%s/%s @%d %s = (%q, null=%v, ok=%v), want %q null=%v", c.a, c.b, c.scale, c.mode, got, isNull, ok, c.want, c.null)
		}
	}
}

// The codec reads all seven mode spellings and refuses any other by name.
func TestRoundingModeSpellings(t *testing.T) {
	parse := func(text string) any {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for mode := range roundingModes {
		e, ce := DecodeExpr(parse(`{"$type":"rounded","expr":{"$type":"col","name":"m"},"rounding":{"mode":"` + mode + `","scale":2}}`))
		if ce != nil {
			t.Fatalf("mode %s: %v", mode, ce)
		}
		if r := e.(Rounded).Rounding; r.Mode != mode || r.Scale != 2 {
			t.Fatalf("mode %s decoded as %+v", mode, r)
		}
	}
	_, ce := DecodeExpr(parse(`{"$type":"rounded","expr":{"$type":"col","name":"m"},"rounding":{"mode":"half-odd","scale":2}}`))
	if ce == nil || ce.Code != UnknownType {
		t.Fatalf("an unknown mode decoded: %v", ce)
	}
}
