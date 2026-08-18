package renderer

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func mustDecode(t *testing.T, canonicalJSON string) wire.Node {
	t.Helper()
	node, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	return node
}

func TestHeadingRendersLevelAndClassVocabulary(t *testing.T) {
	node := mustDecode(t, `{"id":"h1","kind":{"$type":"Heading","level":2,"text":"Revenue & Cost","variant":"Standard"}}`)
	html := RenderHTML(node, nil)
	for _, want := range []string{
		`<div id="h1" data-fuaran-node-id="h1" class="fuaran-kind-heading fuaran-node fuaran-tone-default fuaran-weight-standard fuaran-emphasis-normal">`,
		`<h2 class="fuaran-heading">Revenue &amp; Cost</h2>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q:\n%s", want, html)
		}
	}
}

func TestLinkSanitisesScriptScheme(t *testing.T) {
	node := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"javascript:alert(1)"},"label":"Click"}}`)
	html := RenderHTML(node, nil)
	if !strings.Contains(html, `href="about:blank"`) {
		t.Errorf("script-scheme href not neutralised:\n%s", html)
	}
	safe := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"https://example.org/x"},"label":"Click"}}`)
	if !strings.Contains(RenderHTML(safe, nil), `href="https://example.org/x"`) {
		t.Error("https href was not preserved")
	}
}

func TestLinkProtectedEmailEmitsNoPlaintextAddress(t *testing.T) {
	// Phase 812 — the protected emission: wrapper span + protected anchor with
	// every href/label character a decimal entity; no plaintext address or
	// scheme anywhere in the output.
	node := mustDecode(t, `{"id":"plk","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"mailto:contact@example.com"},"label":"Email us","protection":"email"}}`)
	html := RenderHTML(node, nil)
	for _, want := range []string{
		`<span class="fuaran-link-protected-wrap">`,
		`<a class="fuaran-link fuaran-link-protected" href="&#109;&#97;&#105;&#108;&#116;&#111;&#58;`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q:\n%s", want, html)
		}
	}
	for _, banned := range []string{"mailto:", "contact@example.com", "@example", "Email us"} {
		if strings.Contains(html, banned) {
			t.Errorf("protected output leaks plaintext %q:\n%s", banned, html)
		}
	}
}

func TestButtonRendersInert(t *testing.T) {
	node := mustDecode(t, `{"id":"b","kind":{"$type":"Button","label":"Go","onClick":{"$type":"Navigate","route":"/x"},"variant":"Primary"}}`)
	html := RenderHTML(node, nil)
	if !strings.Contains(html, `<button class="fuaran-button fuaran-button-primary">Go</button>`) {
		t.Errorf("button did not render inert:\n%s", html)
	}
	if strings.Contains(html, "onclick") {
		t.Error("server render must carry no event handlers")
	}
}

func TestModalClosedCarriesHiddenAttribute(t *testing.T) {
	node := mustDecode(t, `{"id":"m","kind":{"$type":"Modal","children":[],"dismissable":true,"open":{"$type":"Static","value":false}}}`)
	html := RenderHTML(node, nil)
	if !strings.Contains(html, `<div class="fuaran-modal-overlay" hidden="">`) {
		t.Errorf("closed modal must stay in the DOM behind [hidden]:\n%s", html)
	}
	if !strings.Contains(html, `role="dialog"`) || !strings.Contains(html, `aria-modal="true"`) {
		t.Error("modal ARIA structure missing")
	}
}

func TestUnresolvedBindingPlaceholdersAndSourcesResolve(t *testing.T) {
	tree := `{"id":"m1","kind":{"$type":"Metric","label":"Users","value":{"$type":"State","defaultValue":0,"key":"users"}}}`
	bare := RenderHTML(mustDecode(t, tree), nil)
	if !strings.Contains(bare, `<div class="fuaran-metric-value">—</div>`) {
		t.Errorf("unresolved binding must placeholder to the em-dash:\n%s", bare)
	}
	resolved := RenderHTML(mustDecode(t, tree), BindingSources{"users": wire.Int(42)})
	if !strings.Contains(resolved, `<div class="fuaran-metric-value">42</div>`) {
		t.Errorf("host source did not resolve:\n%s", resolved)
	}
}

// TestChartRequiresPreLoweredPosture pins the Phase 551 chart-lowering posture
// for the go host: REQUIRE-PRE-LOWERED. A raw Chart at the headless SSR boundary is
// a documented typed passthrough (a marked client-hydration placeholder), never
// lowered in-host to a Drawing and never a silent empty region. If a future change
// makes go lower Chart→Drawing in-host, this test must be revisited deliberately —
// the posture is contract, not accident.
func TestChartRequiresPreLoweredPosture(t *testing.T) {
	node := mustDecode(t, `{"id":"chart-1","kind":{"$type":"Chart","kind":"Line","source":{"$type":"Static","value":"<opaque>"},"stacked":true,"title":{"$type":"Literal","text":"Channel mix"},"xField":"month","yFields":["revenue","cost"]}}`)
	html := RenderHTML(node, nil)

	// The passthrough is a MARKED placeholder — the documented typed outcome.
	for _, want := range []string{
		`data-fuaran-ssr-placeholder="Chart"`, // the typed passthrough marker
		`class="fuaran-chart fuaran-chart-ssr-placeholder"`,
		`hydrates client-side`, // the visible, non-empty fallback text
		`Channel mix`,          // the title survives so the region is never blank
	} {
		if !strings.Contains(html, want) {
			t.Errorf("chart passthrough missing %q:\n%s", want, html)
		}
	}

	// NOT lowered in-host: a raw Chart must never emit inline Drawing SVG on this
	// headless host (that is the fuaran-rs lower-in-host posture, not go's).
	for _, forbidden := range []string{
		`<svg`,           // no inline SVG
		`fuaran-drawing`, // no Drawing class vocabulary
	} {
		if strings.Contains(html, forbidden) {
			t.Errorf("require-pre-lowered posture violated — found %q:\n%s", forbidden, html)
		}
	}
}

func TestStaticDataGridRendersSemanticTable(t *testing.T) {
	node := mustDecode(t, `{"id":"t","kind":{"$type":"DataGrid","columns":[],"editable":false,"source":{"$type":"Static","value":"<opaque>"},"staticRows":{"headers":[{"$type":"Literal","text":"Term"}],"rows":[[{"$type":"Literal","text":"MVU"}]]}}}`)
	html := RenderHTML(node, nil)
	for _, want := range []string{
		`<table class="fuaran-table">`,
		`<th class="fuaran-table-header">Term</th>`,
		`<td class="fuaran-table-cell">MVU</td>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q:\n%s", want, html)
		}
	}
}

func TestSwitchRendersMatchingCaseFromSources(t *testing.T) {
	tree := `{"id":"sw","kind":{"$type":"Switch","cases":[{"child":{"id":"a","kind":{"$type":"Markdown","text":"case A"}},"match":"a"}],"default":{"id":"d","kind":{"$type":"Markdown","text":"default"}},"stateKey":"view"}}`
	withDefault := RenderHTML(mustDecode(t, tree), nil)
	if !strings.Contains(withDefault, "default") || strings.Contains(withDefault, "case A") {
		t.Errorf("unset state must render the default:\n%s", withDefault)
	}
	withCase := RenderHTML(mustDecode(t, tree), BindingSources{"view": wire.Str("a")})
	if !strings.Contains(withCase, "case A") {
		t.Errorf("matching case did not render:\n%s", withCase)
	}
}

func TestFragmentRefResolvesDeclaredBody(t *testing.T) {
	tree := `{"id":"root","kind":{"$type":"Box","children":[{"id":"decl","kind":{"$type":"FragmentDecl","body":{"id":"body-md","kind":{"$type":"Markdown","text":"template body"}},"name":"tpl"}},{"id":"use","kind":{"$type":"FragmentRef","name":"tpl"}}],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
	html := RenderHTML(mustDecode(t, tree), nil)
	if !strings.Contains(html, "template body") {
		t.Errorf("fragment ref did not resolve the declared body:\n%s", html)
	}
	if strings.Count(html, "template body") != 1 {
		t.Errorf("the decl itself must be zero-paint (body rendered once via the ref):\n%s", html)
	}
}

// TestDrawingLabelRotation pins the Phase 877 rotation emission cross-host: the
// transform is anchored at the label's OWN (x, y), so the rotation composes with
// textAnchor rather than fighting it, and the numbers use the shared canonical
// form. These are byte-for-byte the strings the F# reference emitter produces
// for the same shapes — the corpus is the oracle for the codec, and this is the
// emission half it does not cover.
func TestDrawingLabelRotation(t *testing.T) {
	node := mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[`+
		`{"$type":"Label","style":{"rotation":-30},"text":"Q1","x":30,"y":100},`+
		`{"$type":"Label","style":{"rotation":12.34},"text":"F","x":110,"y":100},`+
		`{"$type":"Label","style":{"rotation":0},"text":"Z","x":150,"y":100},`+
		`{"$type":"Label","style":{},"text":"U","x":100,"y":20}`+
		`],"style":{},"viewBox":{"height":120,"minX":0,"minY":0,"width":200}}}`)
	html := RenderHTML(node, nil)

	for _, want := range []string{
		`<text class="fuaran-drawing-label" x="30" y="100" transform="rotate(-30 30 100)"`,
		`transform="rotate(12.34 110 100)"`,
		// An explicit 0 is a PRESENT value and must still emit — absent and zero
		// are different wire shapes, and a renderer that conflates them
		// re-introduces the distinction the codec is careful to preserve.
		`transform="rotate(0 150 100)"`,
		// The unrotated label carries no transform at all — the byte-unchanged
		// guarantee for every pre-877 drawing.
		`<text class="fuaran-drawing-label" x="100" y="20">U</text>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rotation emission missing %q:\n%s", want, html)
		}
	}

	if got := strings.Count(html, `transform="rotate(`); got != 3 {
		t.Errorf("expected exactly 3 rotate transforms, got %d:\n%s", got, html)
	}
}

// TestDrawingRotationInertOffLabel pins that rotation is ignored on non-text
// shapes. This is load-bearing rather than cosmetic: unlike the other text-only
// DrawStyle fields, an SVG transform on a <rect> would MOVE GEOMETRY, so a
// renderer that emitted it uniformly would silently distort drawings.
func TestDrawingRotationInertOffLabel(t *testing.T) {
	node := mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[`+
		`{"$type":"Rectangle","height":10,"style":{"rotation":45},"width":10,"x":0,"y":0},`+
		`{"$type":"Circle","cx":5,"cy":5,"r":2,"style":{"rotation":45}}`+
		`],"style":{},"viewBox":{"height":100,"minX":0,"minY":0,"width":100}}}`)
	html := RenderHTML(node, nil)

	if strings.Contains(html, "transform=") {
		t.Errorf("rotation must be inert on non-Label shapes:\n%s", html)
	}
}

// TestDrawingMarkIdEmission pins the Phase 642 keyed mark identity in the
// emitted SVG. The codec has carried `markId` since 642 and this renderer
// decoded it without ever emitting it, so a Go-served chart's marks were the
// only ones in the estate that a client could not address by key — the object-
// constancy contract the field exists for, silently absent. The other four
// hosts emit `data-fuaran-mark` LAST in the fixed attribute order; this test
// pins the same position, the same escaping, and that an absent markId leaves
// the emitted bytes unchanged (chrome shapes carry no identity).
func TestDrawingMarkIdEmission(t *testing.T) {
	node := mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[`+
		`{"$type":"Rectangle","height":10,"style":{"fill":"#1a86ac","markId":"revenue/Q1"},"width":10,"x":0,"y":0},`+
		`{"$type":"Label","style":{"emphasis":"Loud","markId":"a<b>&\"c\""},"text":"L","x":5,"y":5},`+
		`{"$type":"Line","style":{"stroke":"currentColor"},"x1":0,"y1":0,"x2":9,"y2":9}`+
		`],"style":{},"viewBox":{"height":20,"minX":0,"minY":0,"width":20}}}`)
	html := RenderHTML(node, nil)

	for _, want := range []string{
		// Last in the order: after fill, and after the text-only cluster.
		`fill="#1a86ac" data-fuaran-mark="revenue/Q1"`,
		`font-weight="700" data-fuaran-mark="a&lt;b&gt;&amp;&quot;c&quot;"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("mark-identity emission missing %q:\n%s", want, html)
		}
	}

	// The chrome Line declares no markId, so it gains no attribute — exactly two
	// marks are addressable, not three.
	if got := strings.Count(html, `data-fuaran-mark=`); got != 2 {
		t.Errorf("expected exactly 2 data-fuaran-mark attributes, got %d:\n%s", got, html)
	}
}

// TestDrawingRootAriaLabel pins the Phase 921 root wiring cross-host.
//
// role="img" (the drawing renderer's a11y posture) presents the drawing as ONE
// graphic and does not traverse into it, and <desc> is not uniformly mapped to
// the accessible description (Chromium has never exposed it) — so the
// description the markup carried was a value no reader could reach. The root now
// emits aria-label composing the title and the description. These strings are
// byte-for-byte what the reference emitter produces.
func TestDrawingRootAriaLabel(t *testing.T) {
	drawingRoot := func(extra string) string {
		return RenderHTML(mustDecode(t, `{"id":"d","kind":{"$type":"Drawing","shapes":[],`+
			`"style":{},"viewBox":{"height":100,"minX":0,"minY":0,"width":200}`+extra+`}}`), nil)
	}

	both := drawingRoot(`,"title":"Sales vs target","description":"Bar chart. 2 series: sales, target."`)
	want := `<svg class="fuaran-drawing" role="img" viewBox="0 0 200 100" ` +
		`aria-label="Sales vs target. Bar chart. 2 series: sales, target.">` +
		`<title>Sales vs target</title>` +
		`<desc>Bar chart. 2 series: sales, target.</desc></svg>`
	if !strings.Contains(both, want) {
		t.Errorf("root wiring drifted:\nwant %q\ngot  %s", want, both)
	}

	// The title is terminated only when it needs to be.
	for _, c := range []struct{ title, want string }{
		{"Ends in a period.", `aria-label="Ends in a period. D."`},
		{"Really?", `aria-label="Really? D."`},
		{"Now!", `aria-label="Now! D."`},
		{"Plain", `aria-label="Plain. D."`},
		// An EMPTY title contributes nothing rather than a bare period.
		{"", `aria-label="D."`},
	} {
		got := drawingRoot(`,"title":"` + c.title + `","description":"D."`)
		if !strings.Contains(got, c.want) {
			t.Errorf("title %q: want %q in\n%s", c.title, c.want, got)
		}
	}

	// A title-only or bare root is byte-identical to the pre-921 emission.
	titled := drawingRoot(`,"title":"Bars"`)
	if !strings.Contains(titled, `<svg class="fuaran-drawing" role="img" viewBox="0 0 200 100"><title>Bars</title></svg>`) {
		t.Errorf("a title-only root gained an attribute:\n%s", titled)
	}
	if strings.Contains(drawingRoot(""), "aria-label") {
		t.Error("a bare root must carry no aria-label")
	}

	// A description-only root announces the description alone.
	descOnly := drawingRoot(`,"description":"One filled circle."`)
	if !strings.Contains(descOnly,
		`<svg class="fuaran-drawing" role="img" viewBox="0 0 200 100" aria-label="One filled circle.">`+
			`<desc>One filled circle.</desc></svg>`) {
		t.Errorf("description-only wiring drifted:\n%s", descOnly)
	}

	// Hostile text is inert inside the ATTRIBUTE — the builder emits raw markup,
	// so its own XML escape is the whole defence, and the chart lowering feeds
	// this seam untrusted series and category strings straight off the data feed.
	hostile := drawingRoot(`,"title":"a\"b","description":"<script>alert('x') & \"y\"</script>"`)
	if !strings.Contains(hostile,
		`aria-label="a&quot;b. &lt;script&gt;alert(&#39;x&#39;) &amp; &quot;y&quot;&lt;/script&gt;"`) {
		t.Errorf("hostile text is not fully escaped in the attribute:\n%s", hostile)
	}
	if strings.Contains(hostile, "<script>") {
		t.Errorf("raw markup survived:\n%s", hostile)
	}
}
