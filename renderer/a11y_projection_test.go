package renderer

// WHAT the a11y projection contains — the companion to a11y_placement_test.go,
// which pins where it lands.
//
// The projection is one function ported to every host, so a slot one host
// resolves and another drops is a silent divergence in the emitted DOM: the
// same tree renders a decorative subtree hidden from assistive technology on
// one host and exposed on another. `hidden` was exactly that here until these
// assertions landed.
//
// Every fixture below uses a Markdown node, which does NOT forward to a
// semantic element — so the whole projection sits on the wrapper's open tag and
// a wrapper assertion is enough. Placement is the other file's subject.

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// a11yWrapper renders a non-forwarding node carrying the given accessibility
// section and returns the wrapper's open tag.
func a11yWrapper(t *testing.T, section string, sources BindingSources) string {
	t.Helper()
	node := mustDecode(t, `{"id":"md","kind":{"$type":"Markdown","text":"x"},"accessibility":{`+section+`}}`)
	return wrapperTag(RenderHTML(node, sources))
}

func TestHiddenStaticTrueEmitsAriaHidden(t *testing.T) {
	got := a11yWrapper(t, `"hidden":{"$type":"Static","value":true}`, nil)
	if !strings.Contains(got, `aria-hidden="true"`) {
		t.Errorf("a hidden-marked node must be removed from the accessibility tree:\n%s", got)
	}
}

// The author's intent when marking a subtree hidden is REMOVAL, so the failure
// direction that matters is the silent one: no attribute, content exposed.
// `aria-hidden` is not a tri-state — false and unresolved both emit nothing,
// because `aria-hidden="false"` is a claim neither tree makes.
func TestHiddenFalseAbsentOrUnresolvedEmitsNothing(t *testing.T) {
	cases := []struct {
		name    string
		section string
		sources BindingSources
	}{
		{"static false", `"hidden":{"$type":"Static","value":false}`, nil},
		{"absent", `"label":"Decorative"`, nil},
		{"unresolved binding", `"hidden":{"$type":"State","key":"decorative"}`, nil},
		{"resolved false", `"hidden":{"$type":"State","key":"decorative"}`, BindingSources{"decorative": wire.Bool(false)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := a11yWrapper(t, tc.section, tc.sources); strings.Contains(got, "aria-hidden") {
				t.Errorf("must emit no aria-hidden:\n%s", got)
			}
		})
	}
}

// `hidden` is a Binding<bool>, not a bare bool — so a host-resolved binding
// hides the subtree exactly as a Static one does. Resolving it is the whole
// point: a projection that only understood Static would silently expose every
// conditionally-decorative subtree.
func TestHiddenResolvesThroughHostSources(t *testing.T) {
	got := a11yWrapper(t, `"hidden":{"$type":"State","key":"decorative"}`,
		BindingSources{"decorative": wire.Bool(true)})
	if !strings.Contains(got, `aria-hidden="true"`) {
		t.Errorf("a host-resolved hidden binding must hide the subtree:\n%s", got)
	}
}

// The projection emits the six slots in the reference order. Pinned as a
// sequence, not as six independent substring checks, because the order is part
// of the byte-level parity the hosts are held to.
func TestProjectionEmitsTheSixSlotsInReferenceOrder(t *testing.T) {
	got := a11yWrapper(t, `"label":"Home","labelledBy":"lbl","describedBy":"dsc",`+
		`"role":"link","liveRegion":"polite","hidden":{"$type":"Static","value":true}`, nil)
	want := `aria-label="Home" aria-labelledby="lbl" aria-describedby="dsc" ` +
		`role="link" aria-live="polite" aria-hidden="true"`
	if !strings.Contains(got, want) {
		t.Errorf("projection order/content:\nwant substring %s\ngot %s", want, got)
	}
}

// `label` is a Binding<string>, so the CANONICAL authoring — the only form an
// encoder emits — is a Static envelope. This host tested for a bare string, so
// a canonical tree emitted no `aria-label` at all: not a subtle degradation,
// since the node is then announced by its content or not at all. Nothing went
// red because the fixtures authored the bare form the renderer happened to
// accept, which is why both directions are pinned below.
func TestLabelStaticBindingEmitsAriaLabel(t *testing.T) {
	got := a11yWrapper(t, `"label":{"$type":"Static","value":"Home"}`, nil)
	if !strings.Contains(got, `aria-label="Home"`) {
		t.Errorf("a canonical Static label must emit the accessible name:\n%s", got)
	}
}

// The other half: a keyed binding resolved from the host sources. A projection
// that understood only Static would still silently drop every conditionally-
// named node.
func TestLabelResolvesThroughHostSources(t *testing.T) {
	got := a11yWrapper(t, `"label":{"$type":"State","key":"navLabel"}`,
		BindingSources{"navLabel": wire.Str("Primary navigation")})
	if !strings.Contains(got, `aria-label="Primary navigation"`) {
		t.Errorf("a host-resolved label binding must emit the accessible name:\n%s", got)
	}
}

// The bare string stays accepted as a lenient, non-canonical shorthand (see
// `a11yName`). This pins the decision so a later reader cannot mistake it for
// an accident — and so dropping it becomes a deliberate act with a red test,
// not a silent regression in the fixtures that already author it.
func TestLabelAcceptsTheBareStringShorthand(t *testing.T) {
	got := a11yWrapper(t, `"label":"Home"`, nil)
	if !strings.Contains(got, `aria-label="Home"`) {
		t.Errorf("the bare-string shorthand must still emit the accessible name:\n%s", got)
	}
}

// An unresolved binding and an empty name both emit nothing: `aria-label=""`
// suppresses the content that would otherwise have named the node, so it is
// strictly worse than leaving the slot off.
func TestLabelEmptyOrUnresolvedEmitsNothing(t *testing.T) {
	cases := []struct {
		name    string
		section string
		sources BindingSources
	}{
		{"empty bare", `"label":""`, nil},
		{"empty static", `"label":{"$type":"Static","value":""}`, nil},
		{"unresolved binding", `"label":{"$type":"State","key":"navLabel"}`, nil},
		{"resolved empty", `"label":{"$type":"State","key":"navLabel"}`, BindingSources{"navLabel": wire.Str("")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := a11yWrapper(t, tc.section, tc.sources); strings.Contains(got, "aria-label") {
				t.Errorf("must emit no aria-label:\n%s", got)
			}
		})
	}
}

// `role` is an open vocabulary: the named ARIA roles and the custom escape both
// travel as the raw string, so no host can tell them apart and every reference
// host emits what it was handed. This host case-folded it, which silently
// rewrote a custom role the author had cased deliberately.
func TestCustomRoleIsEmittedVerbatim(t *testing.T) {
	got := a11yWrapper(t, `"role":"doc-pageFooter"`, nil)
	if !strings.Contains(got, `role="doc-pageFooter"`) {
		t.Errorf("a custom role must survive verbatim:\n%s", got)
	}
}
