package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/renderer"
)

// The shared `sanitization/` corpus family, run against this host's URL floor.
//
// Unlike every other corpus family this one is NOT byte-parity: the markup a host
// wraps around a URL differs legitimately between hosts, so comparing those bytes
// would pin accidents rather than the contract. Each case states an INVARIANT
// instead — "reject" (refuse it) or "accept" (take it, and emit the normalised
// form) — plus the reason the URL parser gives, which is what makes the case
// meaningful.
//
// The corpus verifies its own reason claims against a real WHATWG parser
// (sanitization/verify-against-url-parser.mjs); this test verifies that THIS host
// agrees with the resulting invariants.

type sanitizationCase struct {
	ID        string `json:"id"`
	Input     string `json:"input"`
	Invariant string `json:"invariant"`
	Expected  string `json:"expected"`
}

type sanitizationManifest struct {
	Groups []struct {
		ID    string             `json:"id"`
		Cases []sanitizationCase `json:"cases"`
	} `json:"groups"`
}

func loadSanitizationCases(t *testing.T) []sanitizationCase {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "wire-format-fixtures", "sanitization", "manifest.json")
		if _, err := os.Stat(candidate); err == nil {
			raw, err := os.ReadFile(candidate)
			if err != nil {
				t.Fatalf("read %s: %v", candidate, err)
			}
			var m sanitizationManifest
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("parse %s: %v", candidate, err)
			}
			var cases []sanitizationCase
			for _, g := range m.Groups {
				cases = append(cases, g.Cases...)
			}
			return cases
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func TestSanitizationCorpusURLFloor(t *testing.T) {
	cases := loadSanitizationCases(t)
	if len(cases) == 0 {
		t.Skip("wire-format-fixtures/sanitization not found")
	}
	// Logged so the scanned count is visible under -v: a loader that silently
	// parsed zero cases would otherwise read exactly as green as one that ran
	// them all.
	t.Logf("sanitization/url-floor: %d cases", len(cases))

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			got, ok := renderer.SanitizeURL(c.Input)
			switch c.Invariant {
			case "reject":
				if ok {
					t.Errorf("expected REJECT, got %q", got)
				}
				// §19 rule 6 — the or-blank variant substitutes about:blank.
				if blank := renderer.SanitizeURLOrBlank(c.Input); blank != "about:blank" {
					t.Errorf("rejected, but SanitizeURLOrBlank gave %q", blank)
				}
			case "accept":
				if !ok {
					t.Errorf("expected ACCEPT, was rejected")
				} else if got != c.Expected {
					t.Errorf("expected %q, got %q", c.Expected, got)
				}
			default:
				t.Errorf("unknown invariant %q", c.Invariant)
			}
		})
	}
}
