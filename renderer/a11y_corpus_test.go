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
			// `aria-label` is a MEASURED DIVERGENCE, not an omission — see
			// TestA11yStateBoundNameIsThisHostsDeclaredDivergence below.
			fixture: "a11y-wrapper-state-bound",
			want: []string{
				`role="doc-pageFooter"`,
				`aria-live="off"`,
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

// TestA11yStateBoundNameIsThisHostsDeclaredDivergence pins the one place the
// corpus family shows this host emitting something different from every other.
//
// `a11y-wrapper-state-bound`'s accessible name is a `Binding.State` carrying a
// declared `defaultValue` of "Site footer". Measured 2026-08-26 across all five
// render tiers: the reference host, TypeScript, Python and Rust all resolve an
// unwritten `State` to its declared default and emit
// `aria-label="Site footer"`; this host emits NO `aria-label` at all.
//
// It is NOT an a11y defect, which is why it is pinned here rather than fixed.
// `resolveBinding` in bindings.go declines the `State` default GENERALLY — its
// comment records the choice ("State keeps the go host's established
// em-dash-until-resolved posture") and the a11y name slot merely inherits it.
// Changing it would move every bound text slot this host renders, which is a
// binding-resolution parity question rather than an accessibility one.
//
// Asserted in BOTH directions on purpose. Losing `role` / `aria-live` here
// would be a regression; gaining `aria-label` would mean the host-wide posture
// changed and this note has gone stale. Either way the leg goes red and
// somebody reads this comment, which is the whole point of writing a divergence
// down rather than leaving the slot unasserted.
func TestA11yStateBoundNameIsThisHostsDeclaredDivergence(t *testing.T) {
	node := loadFixtureNode(t, "a11y-wrapper-state-bound")
	wrapper := wrapperTag(RenderHTML(node, nil))

	if strings.Contains(wrapper, "aria-label") {
		t.Errorf("this host now resolves a State-bound accessible name — the recorded "+
			"divergence is stale and the posture note in bindings.go needs revisiting:\n%s", wrapper)
	}
	// The rest of the trait is unaffected by the posture and must still land.
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
