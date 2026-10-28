package dataframe

import "testing"

// The unit algebra's canonical render over spellings the field-law vectors do
// not reach: prefixes, aliases, parentheses, leading quotients, merges to `1`,
// currencies, and exponents at the range's edge.
func TestParseUnitCanonicalText(t *testing.T) {
	cases := map[string]string{
		"m":               "m",
		"m.m":             "m2",
		"m/m":             "1",
		"1":               "1",
		"1/s":             "/s",
		"/min":            "/min",
		"km/h":            "km/h",
		"s-1.m":           "m/s",
		"m/s/s":           "m/s2",
		"kg.m/s2":         "kg.m/s2",
		"kg/(m.s2)":       "kg/m/s2",
		"m+2":             "m2",
		"l":               "L",
		"mL":              "mL",
		"dam":             "dam",
		"c[GBP]":          "c[GBP]",
		"[GBP]/[lb_av]":   "[GBP]/[lb_av]",
		"(/s)":            "/s",
		"m2147483647":     "m2147483647",
		"m-2147483647":    "/m2147483647",
		"%":               "%",
		"[ppm]":           "[ppm]",
		"mol/L":           "mol/L",
		"s.A":             "A.s",
		"Ohm":             "Ohm",
		"m0":              "1",
		"m3/(kg.s2)/m":    "m2/kg/s2",
		"(m/(s/(kg)))":    "kg.m/s",
		"k[GBP]/[USD]":    "k[GBP]/[USD]",
		"ms.m":            "m.ms",
		"uV":              "uV",
		"mbar":            "mbar",
		"Mt":              "Mt",
		"[in_i]2/[ft_i]2": "[in_i]2/[ft_i]2",
	}
	for text, want := range cases {
		u, r := ParseUnit(text)
		if r != nil {
			t.Errorf("ParseUnit(%q) refused: %v", text, r)
			continue
		}
		if got := u.String(); got != want {
			t.Errorf("ParseUnit(%q) = %q, want %q", text, got, want)
		}
		again, r := ParseUnit(u.String())
		if r != nil || !again.Equal(u) {
			t.Errorf("render(%q) = %q does not parse back to the same unit", text, u.String())
		}
	}
}

// Every refusal class, with the token it names.
func TestParseUnitRefusals(t *testing.T) {
	cases := []struct{ text, class, token string }{
		{"", UnitEmpty, ""},
		{"furlong", UnitUnknownAtom, "furlong"},
		{"m{tall}", UnitAnnotation, "{tall}"},
		{"{tall}", UnitAnnotation, "{tall}"},
		{"[IU]", UnitArbitraryUnit, "[IU]"},
		{"k[IU]", UnitArbitraryUnit, "k[IU]"},
		{"Cel", UnitNonRatioUnit, "Cel"},
		{"mCel", UnitNonRatioUnit, "mCel"},
		{"[degF]", UnitNonRatioUnit, "[degF]"},
		{"dB", UnitNonRatioUnit, "dB"},
		{"kmin", UnitPrefixNotAllowed, "kmin"},
		{"k%", UnitPrefixNotAllowed, "k%"},
		{"10*3", UnitNumericFactor, "10*3"},
		{"1000", UnitNumericFactor, "1000"},
		{"m^2", UnitMalformed, "^"},
		{"m.", UnitMalformed, ""},
		{"m s", UnitMalformed, " "},
		{"m//s", UnitMalformed, "/"},
		{"(m)2", UnitMalformed, "2"},
		{"(m", UnitMalformed, ""},
		{"m)", UnitMalformed, ")"},
		{"m-", UnitMalformed, ""},
		{"m2147483648", UnitMalformed, "2147483648"},
		{"m2147483647.m", UnitMalformed, "m"},
		{"[GBP", UnitMalformed, "[GBP"},
		{"m{x", UnitMalformed, "{x"},
		{"é", UnitMalformed, "é"},
	}
	for _, c := range cases {
		_, r := ParseUnit(c.text)
		if r == nil {
			t.Errorf("ParseUnit(%q) accepted; want %s", c.text, c.class)
			continue
		}
		if r.Class != c.class || r.Token != c.token {
			t.Errorf("ParseUnit(%q) = %s %q (%v), want %s %q", c.text, r.Class, r.Token, r, c.class, c.token)
		}
	}
}

// A delta replays: patch(old, diff(old, target)) is target, metadata included.
func TestSchemaPatchReplaysDiff(t *testing.T) {
	kg, _ := ParseUnit("kg")
	label, desc := "Mass", "kept"
	old := Schema{
		{Name: "a", Type: TypeInt, Ext: map[string]string{"v.k": "1", "v.gone": "x"}},
		{Name: "m", Type: TypeFloat, Unit: &kg, Description: &desc},
		{Name: "z", Type: TypeString},
	}
	target := Schema{
		{Name: "m", Type: TypeDecimal, Label: &label},
		{Name: "n", Type: TypeBool, Unit: &kg},
		{Name: "a", Type: TypeFloat, Ext: map[string]string{"v.k": "2", "v.new": "y"}},
	}
	d := DiffSchema(old, target)
	got, err := PatchSchema(old, d)
	if err != nil {
		t.Fatalf("patch refused: %v", err)
	}
	if len(got) != len(target) {
		t.Fatalf("patched %d columns, want %d", len(got), len(target))
	}
	if SchemaFingerprint(got) != SchemaFingerprint(target) {
		t.Fatalf("patched schema's fingerprint differs from the target's")
	}
	if v := ClassifySchema([]string{"m"}, d).Verdict; v != CompatBreaking {
		t.Fatalf("classify = %s, want breaking (unit withdrawn, decimal is no widening of float)", v)
	}
	if _, err := PatchSchema(target, d); err == nil {
		t.Fatalf("a delta replayed over a schema it does not describe was accepted")
	}
}
