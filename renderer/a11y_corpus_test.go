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

func a11yCorpusCases() []a11yCorpusCase {
	return []a11yCorpusCase{
		{
			// All six slots at once on an ordinary wrapper kind. `hidden` is an
			// explicit Static FALSE, which is distinct on the wire from omitted
			// and must emit nothing.
			fixture: "a11y-wrapper-all-slots",
			want: []string{
				`aria-label="Channel performance summary"`,
				`aria-labelledby="a11y-wrapper-heading"`,
				`aria-describedby="a11y-wrapper-note"`,
				`role="region"`,
				`aria-live="polite"`,
			},
			absentFromCarrier: []string{`aria-hidden`},
		},
		{
			// The custom role's case is carried VERBATIM — this is the exact
			// spelling a fold bug once rewrote. `off` is a real liveRegion
			// token, not an absence.
			//
			// `aria-label` is here because the name is a `Binding.State` whose
			// declared default now RESOLVES at the render floor (WIRE_FORMAT
			// §24, operator ruling 2026-08-26 / fuaran#1064). It was absent
			// from this list until then, as a measured cross-tier divergence;
			// TestA11yStateBoundNameResolvesLikeEveryOtherTier below carries the
			// account of why it flipped.
			fixture: "a11y-wrapper-state-bound",
			want: []string{
				`role="doc-pageFooter"`,
				`aria-live="off"`,
				`aria-label="Site footer"`,
			},
			absentFromCarrier: []string{`aria-hidden`},
		},
		{
			fixture: "a11y-alert-assertive",
			want:    []string{`role="alert"`, `aria-live="assertive"`},
		},
		{
			// D4 forwarding: the body IS the semantic element.
			fixture:           "a11y-link-labelled",
			element:           "a",
			want:              []string{`aria-label="Read the 2026 annual report (PDF)"`},
			absentFromWrapper: []string{`aria-label`, `role=`},
		},
		{
			fixture:           "a11y-button-named",
			element:           "button",
			want:              []string{`aria-label="Refresh revenue figures"`, `role="button"`},
			absentFromWrapper: []string{`aria-label`, `role=`},
		},
		{
			// The decorative shape: empty alt + hidden Static TRUE — the slot
			// this host dropped entirely before Phase 951's port.
			fixture:           "a11y-image-decorative",
			element:           "img",
			want:              []string{`aria-hidden="true"`},
			absentFromWrapper: []string{`aria-hidden`},
		},
	}
}

func TestA11yCorpusProjectionLandsOnTheRightElement(t *testing.T) {
	for _, c := range a11yCorpusCases() {
		t.Run(c.fixture, func(t *testing.T) {
			node := loadFixtureNode(t, c.fixture)
			html := RenderHTML(node, nil)

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
	wrapper := wrapperTag(RenderHTML(node, nil))

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
func TestA11yCorpusFamilyIsNonEmpty(t *testing.T) {
	if len(a11yCorpusCases()) < 6 {
		t.Fatalf("the a11y corpus family covers %d fixtures; the Phase 955 family is six", len(a11yCorpusCases()))
	}
}
