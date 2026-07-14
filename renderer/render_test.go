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
	node := mustDecode(t, `{"id":"h1","kind":{"$type":"Heading","level":2,"text":{"$type":"Literal","text":"Revenue & Cost"},"variant":"Standard"}}`)
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
	node := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"javascript:alert(1)"},"label":{"$type":"Literal","text":"Click"}}}`)
	html := RenderHTML(node, nil)
	if !strings.Contains(html, `href="about:blank"`) {
		t.Errorf("script-scheme href not neutralised:\n%s", html)
	}
	safe := mustDecode(t, `{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"https://example.org/x"},"label":{"$type":"Literal","text":"Click"}}}`)
	if !strings.Contains(RenderHTML(safe, nil), `href="https://example.org/x"`) {
		t.Error("https href was not preserved")
	}
}

func TestButtonRendersInert(t *testing.T) {
	node := mustDecode(t, `{"id":"b","kind":{"$type":"Button","label":{"$type":"Literal","text":"Go"},"onClick":{"$type":"Navigate","route":"/x"},"variant":"Primary"}}`)
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
	tree := `{"id":"m1","kind":{"$type":"Metric","emphasis":"Normal","format":{"$type":"None"},"label":{"$type":"Literal","text":"Users"},"source":{"$type":"State","defaultValue":0,"key":"users"},"tone":"Default","weight":"Standard"}}`
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
	tree := `{"id":"sw","kind":{"$type":"Switch","cases":[{"child":{"id":"a","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"case A"}}},"match":"a"}],"default":{"id":"d","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"default"}}},"stateKey":"view"}}`
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
	tree := `{"id":"root","kind":{"$type":"Box","children":[{"id":"decl","kind":{"$type":"FragmentDecl","body":{"id":"body-md","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"template body"}}},"name":"tpl"}},{"id":"use","kind":{"$type":"FragmentRef","name":"tpl"}}],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
	html := RenderHTML(mustDecode(t, tree), nil)
	if !strings.Contains(html, "template body") {
		t.Errorf("fragment ref did not resolve the declared body:\n%s", html)
	}
	if strings.Count(html, "template body") != 1 {
		t.Errorf("the decl itself must be zero-paint (body rendered once via the ref):\n%s", html)
	}
}
