package renderer

// Conditional visibility, predicate cases and the scalar selector (Phase 1535).
//
// Driven by the SHARED CORPUS rather than by nodes this package authored for
// itself: node-visible, switch-predicate, switch-predicate-only and
// switch-on-transform-scalar are the oracle every host answers to, and rendering
// the same bytes here is what makes "this host agrees with the others" a
// measurement rather than a claim.
//
// The codec round-trip is covered by the conformance package. What is asserted
// here is the RENDERING — which no round-trip can see, and which is the whole of
// what this phase changed on a host that already decoded these bytes fine.

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func renderFixture(t *testing.T, fixture string, sources BindingSources) string {
	t.Helper()
	return renderHTML(t, loadFixtureNode(t, fixture), sources)
}

func assertContains(t *testing.T, html, needle, why string) {
	t.Helper()
	if !strings.Contains(html, needle) {
		t.Errorf("%s: expected %q in the output", why, needle)
	}
}

func assertAbsent(t *testing.T, html, needle, why string) {
	t.Helper()
	if strings.Contains(html, needle) {
		t.Errorf("%s: expected %q NOT to be in the output", why, needle)
	}
}

// ── node-visible: presence, not concealment ─────────────────────────────────

func TestVisibleFalseRemovesTheNodeEntirely(t *testing.T) {
	html := renderFixture(t, "node-visible", Sources(map[string]wire.Value{"banner.shown": wire.Bool(false)}))

	assertAbsent(t, html, "Shown while the flag is set", "a resolved false removes the node")
	// Removal is not concealment: the node contributes no id and no aria-hidden.
	assertAbsent(t, html, `id="visible-state-flag"`, "no placeholder carries the id")
}

func TestVisibleTrueRendersTheNode(t *testing.T) {
	html := renderFixture(t, "node-visible", Sources(map[string]wire.Value{"banner.shown": wire.Bool(true)}))
	assertContains(t, html, "Shown while the flag is set", "a resolved true renders")
}

func TestVisibleUnresolvedRendersTheNode(t *testing.T) {
	// `visible-unresolved` reads a query result no host has furnished. Rendering
	// it is the rule: a missing source silently hiding content is the one failure
	// a reader cannot see, cannot report and cannot work around.
	html := renderFixture(t, "node-visible", BindingSources{})
	assertContains(t, html, "Rendered, because nothing could say whether to hide it",
		"an unresolved predicate renders")
}

func TestVisibleComputedPredicateResolvesThroughTheScalarPath(t *testing.T) {
	// An Expr over a State param, both directions from one fixture — a host that
	// ignored the predicate entirely fails one of the two.
	const shown = "Shown once the basket has more than three things in it"

	assertContains(t, renderFixture(t, "node-visible", Sources(map[string]wire.Value{"cart.itemCount": wire.Int(9)})),
		shown, "9 > 3")
	assertAbsent(t, renderFixture(t, "node-visible", Sources(map[string]wire.Value{"cart.itemCount": wire.Int(1)})),
		shown, "1 is not > 3")
}

func TestVisibleAndAriaHiddenStayDistinct(t *testing.T) {
	// The contrast the fixture carries on sibling nodes: the aria-hidden node is
	// RENDERED and marked, where a visible:false node is not there at all.
	html := renderFixture(t, "node-visible", Sources(map[string]wire.Value{"banner.shown": wire.Bool(false)}))

	assertContains(t, html, "aria-hidden-decoration", "the decoration is rendered")
	assertContains(t, html, `aria-hidden="true"`, "and marked hidden from assistive technology")
	assertAbsent(t, html, `id="visible-state-flag"`, "where the removed node is simply not there")
}

func TestVisibleDefaultVisibleSpellingNeedsNoHostState(t *testing.T) {
	// `visible-until-dismissed` declares defaultValue: true. That spelling is
	// what an author needs, because a DEFAULT-LESS State predicate follows the
	// shared Binding.State rule and resolves false — the FUARAN148 shape.
	const shown = "Shown until the reader dismisses it"

	assertContains(t, renderFixture(t, "node-visible", BindingSources{}), shown, "the declared default")
	assertAbsent(t, renderFixture(t, "node-visible", Sources(map[string]wire.Value{"banner.dismissed": wire.Bool(false)})),
		shown, "and a host write overrides it")
}

// ── switch-predicate: first-match-wins over a mixed case list ───────────────

func TestSwitchPredicateCaseTakenOnResolvedTrue(t *testing.T) {
	html := renderFixture(t, "switch-predicate", Sources(map[string]wire.Value{"cart.empty": wire.Bool(true)}))
	assertContains(t, html, "Your basket is empty", "the predicate case")
}

func TestSwitchFirstMatchWinsRunsOverTheAuthoredOrder(t *testing.T) {
	// The fixture's two predicate cases precede its `match` case. With BOTH the
	// first predicate true and the selector equal to the match value, a host that
	// checked matches ahead of predicates would render "Summary view".
	html := renderFixture(t, "switch-predicate", Sources(map[string]wire.Value{
		"cart.empty": wire.Bool(true),
		"view":       wire.Str("summary"),
	}))

	assertContains(t, html, "Your basket is empty", "authored order decides")
	assertAbsent(t, html, "Summary view", "the later match does not pre-empt it")
}

func TestSwitchMatchCaseWinsOnceThePredicatesDecline(t *testing.T) {
	html := renderFixture(t, "switch-predicate", Sources(map[string]wire.Value{
		"cart.empty":     wire.Bool(false),
		"cart.itemCount": wire.Int(1),
		"view":           wire.Str("summary"),
	}))
	assertContains(t, html, "Summary view", "the match case")
}

func TestSwitchFallsThroughToDefaultWhenNothingSelects(t *testing.T) {
	html := renderFixture(t, "switch-predicate", Sources(map[string]wire.Value{
		"cart.empty":     wire.Bool(false),
		"cart.itemCount": wire.Int(1),
		"view":           wire.Str("no-such-case"),
	}))
	assertContains(t, html, "A few things in your basket", "the default")
}

func TestSwitchWithNoSelectorSelectsByPredicateAlone(t *testing.T) {
	assertContains(t, renderFixture(t, "switch-predicate-only", Sources(map[string]wire.Value{"form.valid": wire.Bool(true)})),
		"Ready to send", "a when-only switch needs no selector")
	assertContains(t, renderFixture(t, "switch-predicate-only", Sources(map[string]wire.Value{"form.valid": wire.Bool(false)})),
		"Fill in the form to continue", "and falls through to its default")
}

// ── the wire-neutral fix ────────────────────────────────────────────────────

func TestSwitchComputedSelectorSelectsTheMatchingCase(t *testing.T) {
	// Before Phase 1535 this rendered `Default`: the generic resolver's Transform
	// arm is row-shaped and cannot serve a string slot. The default is asserted
	// ABSENT as well as the case present, so a host rendering both would fail
	// rather than satisfy a contains-check on the case alone.
	html := renderFixture(t, "switch-on-transform-scalar", BindingSources{})

	assertContains(t, html, "Plenty of rows", "the computed selector matched")
	assertAbsent(t, html, "Could not tell", "and the default did not render")
}
