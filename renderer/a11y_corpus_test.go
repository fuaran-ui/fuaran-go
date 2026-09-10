package renderer

// The a11y projection, driven by the SHARED CORPUS rather than by nodes this
// package authored for itself.
//
// a11y_placement_test.go already asserts WHERE the projection lands, but every
// node in it is hand-built here — so it measures this host against this host's
// own idea of the trait. The corpus fixtures are the oracle: they exercise all
// six slots, both role classes (a named lower-case `region` and a
// deliberately-cased custom `doc-pageFooter`), both binding forms (Static and
// State), all three liveRegion tokens, and both placement shapes.
//
// The assertions are placement-sensitive for the reason the placement suite
// records: a `role` on a wrapper `<div>` is not associated by assistive
// technology with the interactive element inside it, and a substring check over
// the whole markup cannot tell the two apart.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// a11yCorpusCase is one fixture's expectation: which element carries the
// projection, what it must carry, and what must NOT have leaked onto the
// wrapper when the projection forwards.
type a11yCorpusCase struct {
	fixture string
	// element is "" when the projection stays on the wrapper, else the tag of
	// the semantic element the kind body renders (D4).
	element string
	// want are substrings the carrying element's own OPEN TAG must contain.
	want []string
	// absentFromCarrier are attributes the carrying element must NOT emit.
	absentFromCarrier []string
	// absentFromWrapper are attributes that must NOT have stayed on the wrapper
	// (only meaningful for a forwarding kind).
	absentFromWrapper []string
}

// projectionAttributes are the six attribute names the accessibility projection
// can emit, in the wire's slot order. The complement of a vector's own list is
// what that vector forbids: the contract declares its attribute list EXHAUSTIVE
// for the projection.
var projectionAttributes = []string{
	"aria-label",
	"aria-labelledby",
	"aria-describedby",
	"role",
	"aria-live",
	"aria-hidden",
}

// forwardingTag is the element THIS host's body renders for each forwarding
// fixture's kind — the host-local half of a contract vector. A forwarding vector
// with no entry here fails loudly rather than falling back to the wrapper: a
// silent fallback would assert the projection landed where the contract says it
// must not.
var forwardingTag = map[string]string{
	"a11y-link-labelled":    "a",
	"a11y-button-named":     "button",
	"a11y-image-decorative": "img",
}

// a11yContractVector is one behaviour vector out of the corpus's a11y contract.
type a11yContractVector struct {
	Fixture    string      `json:"fixture"`
	Forwards   bool        `json:"forwards"`
	Attributes [][2]string `json:"attributes"`
}

// a11yCorpusCases reads the cases out of the corpus's own a11y contract.
//
// Phase 1665 — this table used to be hand-written here, and the same table was
// hand-written again in four sibling hosts. Five copies of one cross-host claim
// is exactly the arrangement that let accessibility.label resolve five different
// ways with every conformance gate green: each host measured itself against its
// own idea of the trait, and no copy could contradict another. The claim now
// lives once, in a11y-contract.json's `behaviour` section, and every host reads
// it. What stays host-local is the one thing the contract deliberately does not
// state: which element this host renders for a forwarding kind.
//
// Returns nil with no corpus present; the caller skips rather than passing
// vacuously.
func a11yCorpusCases(t *testing.T) []a11yCorpusCase {
	t.Helper()
	corpus := findFixtureCorpus()
	if corpus == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "a11y-contract.json"))
	if err != nil {
		return nil
	}
	var contract struct {
		Behaviour struct {
			Vectors []a11yContractVector `json:"vectors"`
		} `json:"behaviour"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parsing a11y-contract.json: %v", err)
	}
	cases := make([]a11yCorpusCase, 0, len(contract.Behaviour.Vectors))
	for _, v := range contract.Behaviour.Vectors {
		names := map[string]bool{}
		want := make([]string, 0, len(v.Attributes))
		for _, pair := range v.Attributes {
			names[pair[0]] = true
			want = append(want, pair[0]+`="`+pair[1]+`"`)
		}
		var absentFromCarrier []string
		for _, name := range projectionAttributes {
			if !names[name] {
				absentFromCarrier = append(absentFromCarrier, name)
			}
		}
		element := ""
		var absentFromWrapper []string
		if v.Forwards {
			tag, ok := forwardingTag[v.Fixture]
			if !ok {
				t.Fatalf("%s: the contract says the projection forwards, and this host has not said "+
					"which element it renders for that kind - add it to forwardingTag", v.Fixture)
			}
			element = tag
			for _, name := range names2sorted(names) {
				absentFromWrapper = append(absentFromWrapper, name)
			}
		}
		cases = append(cases, a11yCorpusCase{
			fixture:           v.Fixture,
			element:           element,
			want:              want,
			absentFromCarrier: absentFromCarrier,
			absentFromWrapper: absentFromWrapper,
		})
	}
	return cases
}

// names2sorted keeps the emitted order stable for a deterministic failure
// message; the set itself is what the assertion needs.
func names2sorted(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for _, name := range projectionAttributes {
		if names[name] {
			out = append(out, name)
		}
	}
	return out
}

func TestA11yCorpusProjectionLandsOnTheRightElement(t *testing.T) {
	for _, c := range a11yCorpusCases(t) {
		t.Run(c.fixture, func(t *testing.T) {
			node := loadFixtureNode(t, c.fixture)
			html := renderHTML(t, node, nil)

			wrapper := wrapperTag(html)
			carrier := wrapper
			if c.element != "" {
				carrier = openTagOf(html, c.element)
			}

			for _, want := range c.want {
				if !strings.Contains(carrier, want) {
					t.Errorf("%s: carrier <%s> missing %q:\n%s", c.fixture, c.element, want, carrier)
				}
			}
			for _, absent := range c.absentFromCarrier {
				if strings.Contains(carrier, absent) {
					t.Errorf("%s: carrier must not emit %q:\n%s", c.fixture, absent, carrier)
				}
			}
			for _, absent := range c.absentFromWrapper {
				if strings.Contains(wrapper, absent) {
					t.Errorf("%s: projection leaked onto the wrapper (%q):\n%s", c.fixture, absent, wrapper)
				}
			}
			// The wrapper always keeps the node's ADDRESS, whichever element
			// carries the projection.
			if !strings.Contains(wrapper, `data-fuaran-node-id="`+node.ID+`"`) {
				t.Errorf("%s: the wrapper must keep the node address:\n%s", c.fixture, wrapper)
			}
		})
	}
}

// TestA11yStateBoundNameResolvesLikeEveryOtherTier is the POSITIVE successor to
// TestA11yStateBoundNameIsThisHostsDeclaredDivergence, which pinned the opposite
// answer until the operator ruled on 2026-08-26 (fuaran#1064).
//
// What it used to record: `a11y-wrapper-state-bound` carries its accessible name
// as a `Binding.State` with a declared `defaultValue` of "Site footer", and all
// five render tiers were measured — the reference host, TypeScript, Python and
// Rust emitted `aria-label="Site footer"`, and this host emitted no `aria-label`
// at all. That was never an a11y defect: `resolveBinding` declined the `State`
// default GENERALLY and the name slot merely inherited it.
//
// Why it flipped, stated here because the old note argued the other way and a
// reader deserves the reason rather than a silent inversion. Not the four-to-one
// count — that measures what implementers find natural, not what is right. Two
// things settled it. First, the carve-out was inconsistent with this host's OWN
// charter: Phase 651's completeness posture already resolved
// `Selection.defaultValue` and `Filter.defaultValue` at render time, so one
// function resolved two of three declared defaults and skipped the third. Second,
// the specification was silent on `State` resolution while normatively fixing the
// behaviour of its own declared mirror, `Binding.Filter.defaultValue` (§1.1) — so
// NEITHER posture was non-conformant, and that was the actual defect. It is now
// stated on the original as WIRE_FORMAT §24, so this leg pins a rule rather
// than a local preference.
//
// The pin stays two-sided, in the shape the rule has rather than the shape the
// divergence had: the name must now be PRESENT and correct, and the rest of the
// trait must still land. A host that regressed `resolveBinding` would lose the
// first; one that broke the trait projection would lose the second; and the two
// have different repairs, which is why they are not one assertion.
func TestA11yStateBoundNameResolvesLikeEveryOtherTier(t *testing.T) {
	node := loadFixtureNode(t, "a11y-wrapper-state-bound")
	wrapper := wrapperTag(renderHTML(t, node, nil))

	if !strings.Contains(wrapper, `aria-label="Site footer"`) {
		t.Errorf("an unwritten State-bound accessible name must resolve to its declared "+
			"default (WIRE_FORMAT §24, operator ruling 2026-08-26):\n%s", wrapper)
	}
	// The rest of the trait was never affected by the posture and must still land.
	for _, want := range []string{`role="doc-pageFooter"`, `aria-live="off"`} {
		if !strings.Contains(wrapper, want) {
			t.Errorf("wrapper missing %q:\n%s", want, wrapper)
		}
	}
}

// The corpus family must actually be present. A table-driven leg that silently
// enumerated nothing would be a gate that checked nothing — the failure mode the
// conformance runner guards against one level up.
func TestA11yContractVectorsCoverTheTransformBoundName(t *testing.T) {
	if findFixtureCorpus() == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	cases := a11yCorpusCases(t)
	if len(cases) == 0 {
		t.Fatal("a11y-contract.json's behaviour.vectors must enumerate the a11y fixture family")
	}
	for _, c := range cases {
		if c.fixture == "a11y-wrapper-transform-label" {
			return
		}
	}
	t.Fatal("the contract must carry the Phase 1665 vector - the Transform-bound accessible name is " +
		"the one every host resolved through its row-shaped generic path")
}
