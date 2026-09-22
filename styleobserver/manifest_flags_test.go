package styleobserver

import (
	"testing"

	tm "github.com/fuaran-ui/fuaran-go/thememanifest"
)

func tonedObs(tone string, bg Rgba, contrast float64) StyleObservation {
	return StyleObservation{
		NodeID:              "n",
		EffectiveBackground: bg,
		EmittedTone:         sp(tone),
		ContrastRatio:       contrast,
	}
}

// A tone with no manifest binding → TokenResolutionFailed.
func TestPerNodeTokenResolutionFailed(t *testing.T) {
	m := tm.ThemeManifest{Meta: tm.AnonymousMeta}
	flags := PerNodeFlags(m, tonedObs("Brand", RGB(1, 2, 3), 21))
	if !eqStr(kinds(flags), []string{"TokenResolutionFailed"}) {
		t.Fatalf("flags: %v", kinds(flags))
	}
	if trf, ok := flags[0].(TokenResolutionFailed); !ok || trf.Slot != "Brand" {
		t.Fatalf("slot: %v", flags[0])
	}
}

// A resolved tone whose fill is not in the palette → OffPaletteColour.
func TestPerNodeOffPalette(t *testing.T) {
	m := tm.ThemeManifest{
		Meta:   tm.AnonymousMeta,
		Tokens: []tm.ManifestToken{{Name: "color.brand", Type: "color", Value: "#3b5bdb"}},
		Roles:  []tm.RoleBinding{{Role: tm.ToneRole{Tone: "Brand"}, TokenName: "color.brand"}},
	}
	flags := PerNodeFlags(m, tonedObs("Brand", RGB(1, 2, 3), 21))
	if !eqStr(kinds(flags), []string{"OffPaletteColour"}) {
		t.Fatalf("flags: %v", kinds(flags))
	}
	if opc, ok := flags[0].(OffPaletteColour); !ok || opc.Value != "rgb(1, 2, 3)" {
		t.Fatalf("value: %v", flags[0])
	}
}

// An on-palette fill below a declared contrast floor → ContrastBelowDeclaredFloor only.
func TestPerNodeContrastFloor(t *testing.T) {
	m := tm.ThemeManifest{
		Meta:       tm.AnonymousMeta,
		Tokens:     []tm.ManifestToken{{Name: "color.brand", Type: "color", Value: "#010203"}},
		Roles:      []tm.RoleBinding{{Role: tm.ToneRole{Tone: "Brand"}, TokenName: "color.brand"}},
		Invariants: []tm.Invariant{tm.NewInvariant(tm.ContrastFloor{Role: "Brand", MinRatio: 7.0})},
	}
	flags := PerNodeFlags(m, tonedObs("Brand", RGB(1, 2, 3), 5.0))
	if !eqStr(kinds(flags), []string{"ContrastBelowDeclaredFloor"}) {
		t.Fatalf("flags: %v", kinds(flags))
	}
	cbf := flags[0].(ContrastBelowDeclaredFloor)
	if cbf.Role != "Brand" || cbf.Ratio != 5.0 || cbf.Floor != 7.0 {
		t.Fatalf("floor flag: %+v", cbf)
	}
}

// An untoned node is exempt from every manifest check.
func TestPerNodeUntonedExempt(t *testing.T) {
	m := tm.ThemeManifest{Meta: tm.AnonymousMeta}
	obs := StyleObservation{NodeID: "n", EffectiveBackground: RGB(1, 2, 3), ContrastRatio: 1.0}
	if flags := PerNodeFlags(m, obs); len(flags) != 0 {
		t.Fatalf("untoned should be exempt: %v", kinds(flags))
	}
}

// A token whose observed area share breaches its budget → UsageBudgetExceeded.
func TestVerifyUsageBudgets(t *testing.T) {
	m := tm.ThemeManifest{
		Meta:       tm.AnonymousMeta,
		Tokens:     []tm.ManifestToken{{Name: "color.brand", Type: "color", Value: "#010203"}},
		Invariants: []tm.Invariant{tm.NewInvariant(tm.UsageBudget{Token: "color.brand", TargetPct: 10.0, TolerancePct: 5.0})},
	}
	brand := StyleObservation{NodeID: "a", EffectiveBackground: RGB(1, 2, 3)}
	other := StyleObservation{NodeID: "b", EffectiveBackground: RGB(9, 9, 9)}
	// 60px² of brand out of 100px² total = 60% observed vs 10%±5% declared → breach.
	flags := VerifyUsageBudgets(m, []NodeArea{{Obs: brand, Area: 60}, {Obs: other, Area: 40}})
	if !eqStr(kinds(flags), []string{"UsageBudgetExceeded"}) {
		t.Fatalf("flags: %v", kinds(flags))
	}
	ube := flags[0].(UsageBudgetExceeded)
	if ube.Token != "color.brand" || ube.DeclaredPct != 10.0 || ube.ObservedPct != 60.0 {
		t.Fatalf("budget flag: %+v", ube)
	}
	// Within tolerance → no flag.
	if got := VerifyUsageBudgets(m, []NodeArea{{Obs: brand, Area: 12}, {Obs: other, Area: 88}}); len(got) != 0 {
		t.Fatalf("within tolerance should be clean: %v", kinds(got))
	}
}

// Phase 1727 — the palette-attribution tie-break. Two colour tokens carry the
// same value, declared secondary-before-brand; the rule (fuaran-dotnet's
// docs/THEME-BRIDGE-GUIDE.md, "Palette attribution order") attributes the fill
// to the FIRST token in canonical token-path order, so `color.brand` takes the
// 60px² and `color.secondary` takes none. This host meets the rule through its
// DECODER, which walks each DTCG group's keys in sorted order — so the case goes
// through Decode rather than a token-list literal, and a decoder that stopped
// sorting (or an attribution that stopped trusting the list order) goes red
// here rather than silently re-diverging. The corpus vector of the same name is
// the cross-host law; this is its go-red partner.
func TestSameValuedTokensAttributeToThePathFirstToken(t *testing.T) {
	m, err := tm.Decode(`{"meta":{"name":"t","version":"1"},"tokens":{"color":{` +
		`"secondary":{"$type":"color","$value":"#010203"},` +
		`"brand":{"$type":"color","$value":"#010203"}}},"roles":[],"invariants":[` +
		`{"kind":"UsageBudget","token":"color.brand","targetPct":10,"tolerancePct":5},` +
		`{"kind":"UsageBudget","token":"color.secondary","targetPct":0,"tolerancePct":5}]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fill := StyleObservation{NodeID: "a", EffectiveBackground: RGB(1, 2, 3)}
	other := StyleObservation{NodeID: "b", EffectiveBackground: RGB(9, 9, 9)}
	flags := VerifyUsageBudgets(m, []NodeArea{{Obs: fill, Area: 60}, {Obs: other, Area: 40}})
	if !eqStr(kinds(flags), []string{"UsageBudgetExceeded"}) {
		t.Fatalf("a document-order attribution breaches both budgets; got %v", kinds(flags))
	}
	if ube := flags[0].(UsageBudgetExceeded); ube.Token != "color.brand" || ube.ObservedPct != 60.0 {
		t.Fatalf("attributed to the wrong token: %+v", ube)
	}
}

// Phase 1727 — the order is SEGMENT-WISE, not a sort of the dotted string:
// `color.brand.base` precedes `color.brand-alt` because the key `brand`
// precedes `brand-alt`, although `-` sorts before `.` as a character.
func TestTokenPathOrderIsSegmentWise(t *testing.T) {
	m, err := tm.Decode(`{"meta":{"name":"t","version":"1"},"tokens":{"color":{` +
		`"brand-alt":{"$type":"color","$value":"#010203"},` +
		`"brand":{"base":{"$type":"color","$value":"#010203"}}}},"roles":[],"invariants":[` +
		`{"kind":"UsageBudget","token":"color.brand.base","targetPct":10,"tolerancePct":5},` +
		`{"kind":"UsageBudget","token":"color.brand-alt","targetPct":0,"tolerancePct":5}]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	fill := StyleObservation{NodeID: "a", EffectiveBackground: RGB(1, 2, 3)}
	other := StyleObservation{NodeID: "b", EffectiveBackground: RGB(9, 9, 9)}
	flags := VerifyUsageBudgets(m, []NodeArea{{Obs: fill, Area: 60}, {Obs: other, Area: 40}})
	if !eqStr(kinds(flags), []string{"UsageBudgetExceeded"}) {
		t.Fatalf("flags: %v", kinds(flags))
	}
	if ube := flags[0].(UsageBudgetExceeded); ube.Token != "color.brand.base" {
		t.Fatalf("attributed to the wrong token: %+v", ube)
	}
}
