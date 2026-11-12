package thememanifest

// Phase 2133 — unknown invariant kinds are kept, and the validation pass checks
// what a tolerantly decoded manifest means. The shared vectors live in the
// corpus (style-observer/manifest-validation-*.json, certified in
// conformance/styleobserver_corpus_test.go); these are this host's go-red
// partners, one per rule, with the inputs the code table names.

import (
	"reflect"
	"testing"
)

func decodeInvariants(t *testing.T, invariants string) ThemeManifest {
	t.Helper()
	m, err := Decode(`{"tokens":{},"invariants":[` + invariants + `]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func defects(m ThemeManifest) []string {
	out := []string{}
	for _, d := range Validate(m) {
		out = append(out, EncodeManifestDefect(d))
	}
	return out
}

func expectDefects(t *testing.T, m ThemeManifest, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := defects(m); !reflect.DeepEqual(got, want) {
		t.Errorf("defects differ:\n got %v\nwant %v", got, want)
	}
}

func TestUnknownInvariantKindRoundTripsWithItsMembersAndWeight(t *testing.T) {
	m, err := Decode(`{"invariants":[{"kind":"FocusRing","minWidthPx":2,"nested":{"b":1,"a":[true,"s"]},"weight":3}],"tokens":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Invariants) != 1 {
		t.Fatalf("want one invariant, got %v", m.Invariants)
	}
	inv := m.Invariants[0]
	u, ok := inv.Kind.(UnknownInvariant)
	if !ok || u.Kind != "FocusRing" || inv.Weight != 3 || len(u.Members) != 2 {
		t.Fatalf("want the unknown invariant kept with two members and weight 3, got %#v", inv)
	}
	if InvariantKindName(inv) != "FocusRing" {
		t.Errorf("kind name: %q", InvariantKindName(inv))
	}
	want := `{"invariants":[{"kind":"FocusRing","minWidthPx":2,"nested":{"a":[true,"s"],"b":1},"weight":3}],"tokens":{}}`
	if got := Encode(m); got != want {
		t.Errorf("re-encode:\n got %s\nwant %s", got, want)
	}
}

func TestAnObjectWithNoStringKindIsStillNotAnInvariant(t *testing.T) {
	for _, src := range []string{`{"role":"x","minRatio":7}`, `{"kind":"","role":"x"}`} {
		if m := decodeInvariants(t, src); len(m.Invariants) != 0 {
			t.Errorf("%s: want no invariant, got %v", src, m.Invariants)
		}
	}
}

func TestTheOmittedDefaultFloorIsOutOfRangeAndUnbound(t *testing.T) {
	expectDefects(t, decodeInvariants(t, `{"kind":"ContrastFloor","role":"x"}`),
		`{"code":"CONTRAST_FLOOR_OUT_OF_RANGE","index":0,"subject":"x"}`,
		`{"code":"CONTRAST_FLOOR_ROLE_UNBOUND","index":0,"subject":"x"}`)
}

func TestFloorRangeIsOneToTwentyOneInclusive(t *testing.T) {
	floor := func(ratio string) ThemeManifest {
		m := decodeInvariants(t, `{"kind":"ContrastFloor","role":"Brand","minRatio":`+ratio+`}`)
		m.Roles = []RoleBinding{{Role: ToneRole{Tone: "Brand"}, TokenName: "t"}}
		return m
	}
	out := `{"code":"CONTRAST_FLOOR_OUT_OF_RANGE","index":0,"subject":"Brand"}`
	expectDefects(t, floor("0.5"), out)
	expectDefects(t, floor("21.5"), out)
	expectDefects(t, floor("1"))
	expectDefects(t, floor("21"))
}

func TestEmptyRoleIsEmptyAndUnboundRoleIsUnbound(t *testing.T) {
	expectDefects(t, decodeInvariants(t, `{"kind":"ContrastFloor","minRatio":7}`),
		`{"code":"CONTRAST_FLOOR_ROLE_EMPTY","index":0,"subject":""}`)
	m := decodeInvariants(t, `{"kind":"ContrastFloor","minRatio":7,"role":"body-text"}`)
	expectDefects(t, m, `{"code":"CONTRAST_FLOOR_ROLE_UNBOUND","index":0,"subject":"body-text"}`)
	m.Roles = []RoleBinding{{Role: NamedRole{Name: "body-text"}, TokenName: "t"}}
	expectDefects(t, m)
}

func TestUndeclaredBudgetTokenAndUnknownKindAreReported(t *testing.T) {
	expectDefects(t, decodeInvariants(t, `{"kind":"UsageBudget","targetPct":10,"token":"color.ghost"}`),
		`{"code":"USAGE_BUDGET_TOKEN_UNDECLARED","index":0,"subject":"color.ghost"}`)
	expectDefects(t, decodeInvariants(t, `{"kind":"MotionVoice"},{"kind":"FocusRing","minWidthPx":2}`),
		`{"code":"INVARIANT_KIND_UNKNOWN","index":1,"subject":"FocusRing"}`)
}

func TestMergeKeepsUnknownInvariantsAndDedupesThemByBytes(t *testing.T) {
	a := decodeInvariants(t, `{"kind":"FocusRing","minWidthPx":2}`)
	b := decodeInvariants(t, `{"kind":"FocusRing","minWidthPx":2},{"kind":"FocusRing","minWidthPx":3}`)
	if got := len(Merge(a, b).Invariants); got != 2 {
		t.Errorf("want 2 invariants after merge, got %d", got)
	}
}
