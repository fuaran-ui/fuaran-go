// Style-observer conformance: the pure-tier flag + observation encode is
// certified byte-for-byte against the sibling hosts in the styleobserver /
// thememanifest package tests (which mirror the fuaran-py reference vectors).
// This leg adds the shared a11y-contract.json check — the canonical rule set +
// severity threshold both hosts read — and documents the headless boundary: the
// static gate disables `color-contrast` precisely because live contrast needs
// real layout metrics, which is why the Go observer's contrast tier operates on
// SUPPLIED resolved-style facts, never a live DOM.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/styleobserver"
	tm "github.com/fuaran-ui/fuaran-go/thememanifest"
)

type a11yContract struct {
	Version           int               `json:"version"`
	SeverityThreshold []string          `json:"severityThreshold"`
	DisabledRules     map[string]string `json:"disabledRules"`
}

func TestA11yContract(t *testing.T) {
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "a11y-contract.json"))
	if err != nil {
		t.Skipf("a11y-contract.json not present: %v", err)
	}
	var c a11yContract
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parsing a11y-contract.json: %v", err)
	}
	// The rule set + severity threshold both hosts key off must be present.
	if len(c.SeverityThreshold) == 0 {
		t.Fatal("a11y-contract severityThreshold is empty")
	}
	hasCritical := false
	for _, s := range c.SeverityThreshold {
		if s == "critical" {
			hasCritical = true
		}
	}
	if !hasCritical {
		t.Fatalf("severityThreshold missing 'critical': %v", c.SeverityThreshold)
	}
	// The headless boundary: the static gate disables color-contrast (it needs
	// real layout / canvas metrics) — which is exactly why the Go observer's
	// contrast tier consumes supplied resolved-style facts, not a live DOM.
	if _, ok := c.DisabledRules["color-contrast"]; !ok {
		t.Fatal("a11y-contract expected to disable 'color-contrast' for the static gate")
	}

	// The manifest's declared per-role contrast floor aligns with WCAG AA: the
	// observer's supplied-facts contrast tier is the headless-meaningful
	// enforcement of the same intent the disabled live rule would check.
	m := tm.ThemeManifest{
		Meta:       tm.AnonymousMeta,
		Tokens:     []tm.ManifestToken{{Name: "color.brand", Type: "color", Value: "#010203"}},
		Roles:      []tm.RoleBinding{{Role: tm.ToneRole{Tone: "Brand"}, TokenName: "color.brand"}},
		Invariants: []tm.Invariant{tm.NewInvariant(tm.ContrastFloor{Role: "Brand", MinRatio: styleobserver.DefaultOptions.ContrastAAThreshold})},
	}
	obs := styleobserver.StyleObservation{
		NodeID:              "n",
		EffectiveBackground: styleobserver.RGB(1, 2, 3),
		EmittedTone:         strptr("Brand"),
		ContrastRatio:       3.0, // below the AA floor
	}
	flags := styleobserver.PerNodeFlags(m, obs)
	if len(flags) != 1 {
		t.Fatalf("expected one below-floor flag, got %v", flags)
	}
	if _, ok := flags[0].(styleobserver.ContrastBelowDeclaredFloor); !ok {
		t.Fatalf("expected ContrastBelowDeclaredFloor, got %T", flags[0])
	}
}

func strptr(s string) *string { return &s }
