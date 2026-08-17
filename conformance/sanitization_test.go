package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/renderer"
)

// The shared `sanitization/` corpus family, run against this host's render-time
// safety floor (WIRE_FORMAT.md §22; §19 for the URL group).
//
// Unlike every other corpus family this one is NOT byte-parity: the markup a host
// wraps around a payload differs legitimately between hosts, so comparing those
// bytes would pin accidents rather than the contract. Each case states an
// INVARIANT instead — reject, accept, or inert — and this test asserts that THIS
// host satisfies it.
//
// The url-floor group's claims are verified by the corpus itself against a real
// WHATWG parser (sanitization/verify-against-url-parser.mjs), so what is checked
// here is agreement with an invariant established independently, rather than
// agreement between two of our own assertions.

// Groups whose seam does not exist on this host, with the reason. DECLARED rather
// than omitted: a group silently skipped would read as covered in the family,
// which is the shape §22.2 refuses.
var notApplicable = map[string]string{
	"extra-attributes": "The ExtraAttributes seam does not exist on a decoded tree in this host — " +
		"the wire format omits the field, and every attribute name this renderer emits is " +
		"renderer-controlled. There is no untrusted attribute key to gate.",
}

type sanitizationCase struct {
	ID               string   `json:"id"`
	Input            string   `json:"input"`
	Invariant        string   `json:"invariant"`
	Expected         string   `json:"expected"`
	ForbiddenPattern []string `json:"forbiddenPattern"`
	Required         []string `json:"required"`
	Target           string   `json:"target"`
}

type sanitizationManifest struct {
	Groups []struct {
		ID    string             `json:"id"`
		Cases []sanitizationCase `json:"cases"`
	} `json:"groups"`
}

func loadSanitizationManifest(t *testing.T) *sanitizationManifest {
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
			return &m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

func sanitizationCases(m *sanitizationManifest, id string) []sanitizationCase {
	for _, g := range m.Groups {
		if g.ID == id {
			return g.Cases
		}
	}
	return nil
}

// The `inert` check. A PATTERN rather than a substring, deliberately: an escaped
// payload still contains the text `onclick=`, harmlessly, so a substring check
// would fail a CORRECT host. What must not exist is a live tag carrying the
// handler. `required` is the other half, catching a host that satisfies every
// forbidden pattern by discarding the content entirely.
func assertInert(t *testing.T, rendered string, c sanitizationCase) {
	t.Helper()
	for _, p := range c.ForbiddenPattern {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			t.Fatalf("%s: forbidden pattern %q does not compile: %v", c.ID, p, err)
		}
		if re.MatchString(rendered) {
			t.Errorf("%s: output matches forbidden pattern %q — payload %q survived as live markup",
				c.ID, p, c.Input)
		}
	}
	for _, r := range c.Required {
		if !strings.Contains(rendered, r) {
			t.Errorf("%s: output is missing required %q — the payload was stripped rather than escaped",
				c.ID, r)
		}
	}
}

func TestSanitizationCorpus(t *testing.T) {
	m := loadSanitizationManifest(t)
	if m == nil {
		t.Skip("wire-format-fixtures/sanitization not found")
	}

	t.Run("every group is claimed", func(t *testing.T) {
		known := map[string]bool{"url-floor": true, "markdown-body": true, "text-source": true}
		for id := range notApplicable {
			known[id] = true
		}
		for _, g := range m.Groups {
			if !known[g.ID] {
				t.Errorf("the corpus carries group %q, which this host neither runs nor declares not-applicable", g.ID)
			}
		}
	})

	t.Run("url-floor", func(t *testing.T) {
		cases := sanitizationCases(m, "url-floor")
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
	})

	t.Run("markdown-body", func(t *testing.T) {
		cases := sanitizationCases(m, "markdown-body")
		t.Logf("sanitization/markdown-body: %d cases", len(cases))
		for _, c := range cases {
			t.Run(c.ID, func(t *testing.T) {
				// `MarkdownToHTML` already applies both layers on this host — the
				// deterministic GFM renderer, which escapes by construction, then
				// the defence-in-depth sweep — so it IS the obligation's surface.
				assertInert(t, renderer.MarkdownToHTML(c.Input), c)
			})
		}
	})

	t.Run("text-source", func(t *testing.T) {
		cases := sanitizationCases(m, "text-source")
		t.Logf("sanitization/text-source: %d cases", len(cases))
		for _, c := range cases {
			t.Run(c.ID, func(t *testing.T) {
				// The markdown renderer is the seam a text-bearing string reaches on
				// this host, and it escapes by construction — which is what makes the
				// legitimate `a < b && c > d` case survive intact rather than stripped.
				assertInert(t, renderer.MarkdownToHTML(c.Input), c)
			})
		}
	})

	// The not-applicable declarations are logged rather than silently skipped, so a
	// reader of the run sees WHY a group did not execute here.
	for id, reason := range notApplicable {
		t.Logf("sanitization/%s: NOT APPLICABLE on this host — %s", id, reason)
	}
}
