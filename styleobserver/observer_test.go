package styleobserver

import (
	"math"
	"testing"
)

func sp(s string) *string { return &s }

func input(fg Rgba, layers []Rgba, font *string, tone *string) StyleInput {
	return StyleInput{Foreground: fg, BackgroundLayers: layers, FontFamily: font, EmittedTone: tone}
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func TestRgbaPrimitives(t *testing.T) {
	if !SameRGB(RGB(255, 128, 0), RGBA(255.4, 127.6, 0.2, 0.3)) {
		t.Fatal("same_rgb should hold after rounding")
	}
	if SameRGB(RGB(255, 128, 0), RGB(254, 128, 0)) {
		t.Fatal("same_rgb should not hold")
	}
	if got := EncodeRgba(White); got != `{"r":255.00,"g":255.00,"b":255.00,"a":1.00}` {
		t.Fatalf("encode white: %s", got)
	}
	if got := EncodeRgba(Transparent); got != `{"r":0.00,"g":0.00,"b":0.00,"a":0.00}` {
		t.Fatalf("encode transparent: %s", got)
	}
}

func TestCompositingAndContrast(t *testing.T) {
	if EffectiveBackground(nil) != White {
		t.Fatal("empty layers -> white")
	}
	if EffectiveBackground([]Rgba{Transparent, White}) != White {
		t.Fatal("transparent over white -> white")
	}
	if got := EffectiveBackground([]Rgba{RGB(10, 20, 30), RGB(99, 99, 99)}); got != RGB(10, 20, 30) {
		t.Fatalf("first opaque wins: %v", got)
	}
	if round2(ContrastRatio(Black, White)) != 21.0 {
		t.Fatal("black/white contrast != 21")
	}
	if round2(ContrastRatio(White, White)) != 1.0 {
		t.Fatal("white/white contrast != 1")
	}
}

func TestFontRole(t *testing.T) {
	cases := []struct {
		font string
		want FontRole
	}{
		{"ui-monospace, Menlo", FontMonospace},
		{"Inter, sans-serif", FontSansSerif},
		{"Georgia, serif", FontSerif},
		{"Wingdings", FontUnknown},
	}
	for _, c := range cases {
		if got := FontRoleOf(input(Black, nil, sp(c.font), nil)); got != c.want {
			t.Fatalf("font %q: got %s want %s", c.font, got, c.want)
		}
	}
	if got := FontRoleOf(input(Black, nil, nil, nil)); got != FontUnknown {
		t.Fatalf("no font: %s", got)
	}
}

func TestPredicates(t *testing.T) {
	o := DefaultOptions
	if f := InvisibleTextFlag(o.InvisibleTextThreshold, input(White, []Rgba{White}, nil, nil)); f == nil {
		t.Fatal("white-on-white should be invisible")
	} else if it, ok := f.(InvisibleText); !ok || it.Ratio != 1.0 {
		t.Fatalf("invisible flag: %v", f)
	}
	if f := InvisibleTextFlag(o.InvisibleTextThreshold, input(Black, []Rgba{White}, nil, nil)); f != nil {
		t.Fatalf("black-on-white should not be invisible: %v", f)
	}
	aa := ContrastBelowAAFlag(o.InvisibleTextThreshold, o.ContrastAAThreshold, input(RGB(150, 150, 150), []Rgba{White}, nil, nil))
	cbaa, ok := aa.(ContrastBelowAA)
	if !ok || !(o.InvisibleTextThreshold <= cbaa.Ratio && cbaa.Ratio < o.ContrastAAThreshold) {
		t.Fatalf("aa flag: %v", aa)
	}
	accent := AccentIndistinctFlag(o.AccentIndistinctThreshold, input(Black, []Rgba{RGB(240, 240, 240), White}, nil, sp("brand")))
	if _, ok := accent.(AccentIndistinct); !ok {
		t.Fatalf("accent flag: %v", accent)
	}
	if f := AccentIndistinctFlag(o.AccentIndistinctThreshold, input(Black, []Rgba{RGB(240, 240, 240), White}, nil, nil)); f != nil {
		t.Fatalf("untoned should not flag accent: %v", f)
	}
}

func kinds(flags []StyleFlag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = FlagKind(f)
	}
	return out
}

func eqStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDerivePartitionsContrastAxis(t *testing.T) {
	flags := DeriveStyleFlags(DefaultOptions, input(White, []Rgba{White}, nil, nil))
	if !eqStr(kinds(flags), []string{"InvisibleText"}) {
		t.Fatalf("invisible only: %v", kinds(flags))
	}
	combined := DeriveStyleFlags(DefaultOptions, input(White, []Rgba{White, White}, nil, sp("brand")))
	if !eqStr(kinds(combined), []string{"InvisibleText", "AccentIndistinct"}) {
		t.Fatalf("combined: %v", kinds(combined))
	}
	if got := DeriveStyleFlags(DefaultOptions, input(Black, []Rgba{White}, nil, nil)); len(got) != 0 {
		t.Fatalf("legible should have no flags: %v", got)
	}
}

func TestEncodeFlagsByteIdentical(t *testing.T) {
	cases := []struct {
		flag StyleFlag
		want string
	}{
		{ContrastBelowAA{3.21}, `{"kind":"ContrastBelowAA","ratio":3.21}`},
		{InvisibleText{1.02}, `{"kind":"InvisibleText","ratio":1.02}`},
		{AccentIndistinct{2.5}, `{"kind":"AccentIndistinct","ratio":2.50}`},
		{TokenResolutionFailed{"Brand"}, `{"kind":"TokenResolutionFailed","slot":"Brand"}`},
		{OffPaletteColour{"rgb(1, 2, 3)"}, `{"kind":"OffPaletteColour","value":"rgb(1, 2, 3)"}`},
		{UsageBudgetExceeded{"color.brand.base", 9.0, 28.0}, `{"kind":"UsageBudgetExceeded","token":"color.brand.base","declaredPct":9.00,"observedPct":28.00}`},
		{ContrastBelowDeclaredFloor{"Brand", 5.0, 7.0}, `{"kind":"ContrastBelowDeclaredFloor","role":"Brand","ratio":5.00,"floor":7.00}`},
	}
	for _, c := range cases {
		if got := EncodeStyleFlag(c.flag); got != c.want {
			t.Fatalf("encode %s:\n got: %s\nwant: %s", FlagKind(c.flag), got, c.want)
		}
	}
}

func TestEncodeObservationByteIdentical(t *testing.T) {
	obs := ToStyleObservation(DefaultOptions, "node-1", input(Black, []Rgba{White}, nil, nil))
	want := `{"nodeId":"node-1","foreground":{"r":0.00,"g":0.00,"b":0.00,"a":1.00},` +
		`"effectiveBackground":{"r":255.00,"g":255.00,"b":255.00,"a":1.00},` +
		`"fontRole":"Unknown","emittedTone":null,"contrastRatio":21.00,"flags":[]}`
	if got := EncodeStyleObservation(obs); got != want {
		t.Fatalf("encode observation:\n got: %s\nwant: %s", got, want)
	}
	toned := ToStyleObservation(DefaultOptions, "n2", input(Black, []Rgba{White}, nil, sp("brand")))
	enc := EncodeStyleObservation(toned)
	if !containsSub(enc, `"emittedTone":"brand"`) {
		t.Fatalf("toned emittedTone missing: %s", enc)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func invisible() StyleInput { return input(White, []Rgba{White}, nil, nil) }
func legible() StyleInput   { return input(Black, []Rgba{White}, nil, nil) }

func TestInMemoryObserver(t *testing.T) {
	obs := NewInMemoryStyleObserver(DefaultOptions, nil)
	obs.RegisterFixture("a", invisible(), nil)
	snap := obs.Observe("a")
	if snap == nil || !eqStr(kinds(snap.Flags), []string{"InvisibleText"}) {
		t.Fatalf("observe a: %v", snap)
	}
	if obs.Observe("missing") != nil {
		t.Fatal("missing should be nil")
	}
}

func TestInMemoryChangeOnlyEmission(t *testing.T) {
	obs := NewInMemoryStyleObserver(DefaultOptions, nil)
	obs.RegisterFixture("a", legible(), nil)
	var emissions []StyleObservation
	obs.Subscribe(func(_ string, o StyleObservation) { emissions = append(emissions, o) })
	obs.Update("a", legible())
	if len(emissions) != 0 {
		t.Fatalf("no-change should not emit: %d", len(emissions))
	}
	obs.Update("a", invisible())
	if len(emissions) != 1 {
		t.Fatalf("change should emit once: %d", len(emissions))
	}
	obs.Update("a", invisible())
	if len(emissions) != 1 {
		t.Fatalf("repeat no-change should not emit: %d", len(emissions))
	}
}

func TestInMemoryObserveTreeBFS(t *testing.T) {
	obs := NewInMemoryStyleObserver(DefaultOptions, nil)
	obs.RegisterFixture("root", legible(), nil)
	obs.RegisterFixture("a", legible(), sp("root"))
	obs.RegisterFixture("b", legible(), sp("root"))
	obs.RegisterFixture("a1", legible(), sp("a"))
	got := []string{}
	for _, o := range obs.ObserveTree("root") {
		got = append(got, o.NodeID)
	}
	if !eqStr(got, []string{"root", "a", "b", "a1"}) {
		t.Fatalf("bfs order: %v", got)
	}
	if obs.ObserveTree("unknown") != nil {
		t.Fatal("unknown tree should be nil")
	}
}

func TestInMemoryUnsubscribeBaselineIsolation(t *testing.T) {
	obs := NewInMemoryStyleObserver(DefaultOptions, nil)
	count := 0
	unsub := obs.Subscribe(func(_ string, _ StyleObservation) { count++ })
	obs.RegisterFixture("a", invisible(), nil)
	unsub()
	obs.Update("a", legible())
	if count != 1 {
		t.Fatalf("unsubscribe: count=%d", count)
	}

	obs.Register("baseline")
	base := obs.Observe("baseline")
	if base == nil || len(base.Flags) != 0 || round2(base.ContrastRatio) != 21.0 {
		t.Fatalf("baseline: %+v", base)
	}
	obs.Unregister("baseline")
	if obs.Observe("baseline") != nil {
		t.Fatal("baseline should be gone")
	}

	reached := false
	obs.Subscribe(func(_ string, _ StyleObservation) { panic("boom") })
	obs.Subscribe(func(_ string, _ StyleObservation) { reached = true })
	obs.RegisterFixture("c", invisible(), nil)
	if !reached {
		t.Fatal("throwing subscriber poisoned a sibling")
	}
}
