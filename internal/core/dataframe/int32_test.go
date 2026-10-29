package dataframe

import (
	"encoding/json"
	"testing"
)

// Phase 2484 — an int column value is a signed 32-bit integer, read by value
// (WIRE_FORMAT §3.6.28). The corpus certifies the wire path; this pins the Core
// twin's own column reader and its inference to the same rule.
func TestIntColumnIsInt32(t *testing.T) {
	accepted := map[string]int64{
		"2147483647":  2147483647,
		"-2147483648": -2147483648,
		"2.0":         2,
		"-3.0":        -3,
		"0":           0,
	}
	for tok, want := range accepted {
		c, err := decodeCellValue("n", TypeInt, json.Number(tok))
		if err != nil {
			t.Fatalf("%s: refused: %v", tok, err)
		}
		if got := c.Value.(int64); got != want {
			t.Fatalf("%s: got %d, want %d", tok, got, want)
		}
	}
	for _, tok := range []string{"2147483648", "-2147483649", "2.5", "1e10", "9223372036854775808"} {
		if _, err := decodeCellValue("n", TypeInt, json.Number(tok)); err == nil || err.Code != TypeMismatch {
			t.Fatalf("%s: want a TypeMismatch refusal, got %v", tok, err)
		}
	}
}

func TestIntInferenceIsInt32(t *testing.T) {
	cases := []struct {
		values []any
		want   string
	}{
		{[]any{json.Number("1"), json.Number("2147483648")}, TypeFloat},
		{[]any{json.Number("2.0"), json.Number("-2147483648")}, TypeInt},
		{[]any{json.Number("1"), json.Number("2.5")}, TypeFloat},
	}
	for _, c := range cases {
		got, err := inferColumnType("n", c.values)
		if err != nil || got != c.want {
			t.Fatalf("%v: got %q (%v), want %q", c.values, got, err, c.want)
		}
	}
}
