package renderer

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

const (
	islandChildA = `{"id":"md-a","kind":{"$type":"Markdown","text":"Static prose."}}`
	islandChildB = `{"id":"spark-b","kind":{"$type":"Sparkline","source":{"$type":"Static","value":[1,2,3]}}}`
)

func islandPage() string {
	return `{"id":"page","kind":{"$type":"Box","children":[` + islandChildA + `,` + islandChildB +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
}

func TestZeroIslandsIsByteIdenticalToPlainRender(t *testing.T) {
	node := mustDecode(t, islandPage())
	plain := renderHTML(t, node, BindingSources{})
	withIslands, err := RenderWithIslands(node, BindingSources{}, nil)
	if err != nil {
		t.Fatalf("RenderWithIslands: %v", err)
	}
	if withIslands != plain {
		t.Error("a zero-island page must be byte-identical to a plain render")
	}
}

func TestIslandsEmitBoundariesAndScopedPayloads(t *testing.T) {
	node := mustDecode(t, islandPage())
	plain := renderHTML(t, node, BindingSources{})
	withIslands, err := RenderWithIslands(node, BindingSources{}, map[string]string{"spark-b": "viz"})
	if err != nil {
		t.Fatalf("RenderWithIslands: %v", err)
	}

	// The boundary wrapper's children are exactly the island node's plain
	// static render — the mismatch-freedom contract the client hydrates
	// against.
	childPlain := renderHTML(t, mustDecode(t, islandChildB), BindingSources{})
	wrapped := `<div class="fuaran-island" data-fuaran-island="viz">` + childPlain + `</div>`
	if !strings.Contains(withIslands, wrapped) {
		t.Errorf("boundary wrapper (with the plain child render inside) missing:\n%s", withIslands)
	}

	// One scoped hydrate payload per island, carrying the subtree's canonical
	// wire JSON with < > & escaped for script embedding.
	payload, err := wire.EncodeNode(mustDecode(t, islandChildB))
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	script := `<script type="application/json" id="fuaran-hydrate-island-viz" data-fuaran-island-payload="viz">` +
		escapeForScript(payload) + `</script>`
	if !strings.Contains(withIslands, script) {
		t.Errorf("scoped hydrate script missing:\n%s", withIslands)
	}
	if !strings.HasSuffix(withIslands, script) {
		t.Error("hydrate scripts must follow the static HTML")
	}

	// The static remainder is byte-identical to the non-islands path: undo the
	// wrapper + drop the script and the plain render comes back exactly.
	static := strings.TrimSuffix(withIslands, script)
	unwrapped := strings.Replace(static, wrapped, childPlain, 1)
	if unwrapped != plain {
		t.Error("the static remainder diverged from the plain render")
	}
}

func TestIslandPayloadEscapesScriptBreakout(t *testing.T) {
	node := mustDecode(t, `{"id":"md","kind":{"$type":"Markdown","text":"</script><b>x</b>"}}`)
	html, err := RenderWithIslands(node, BindingSources{}, map[string]string{"md": "prose"})
	if err != nil {
		t.Fatalf("RenderWithIslands: %v", err)
	}
	payloadStart := strings.Index(html, `data-fuaran-island-payload="prose">`)
	if payloadStart < 0 {
		t.Fatalf("payload script missing:\n%s", html)
	}
	payload := html[payloadStart:]
	if strings.Contains(payload, "</script><b>") {
		t.Error("raw angle brackets inside the payload can break out of the script element")
	}
}

// TestIslandCollectionReachesKindHeldArms pins the reference's kind-held reach
// (fuaran#2297): an island under a Switch case, a Switch default or an
// ErrorBoundary fallback is collected, in document order — child before
// fallback, cases in order before the default.
func TestIslandCollectionReachesKindHeldArms(t *testing.T) {
	md := func(id string) string {
		return `{"id":"` + id + `","kind":{"$type":"Markdown","text":"x"}}`
	}
	page := `{"id":"page","kind":{"$type":"Box","children":[` +
		`{"id":"sw","kind":{"$type":"Switch","cases":[{"child":` + md("case-a") + `,"match":"a"},{"child":` + md("case-b") + `,"match":"b"}],"default":` + md("sw-default") + `,"stateKey":"view"}},` +
		`{"id":"eb","kind":{"$type":"ErrorBoundary","child":` + md("eb-child") + `,"fallback":` + md("eb-fallback") + `}}` +
		`],"layout":{"$type":"Flex","direction":"Vertical"},"role":"Group"}}`
	islands := map[string]string{"case-a": "i1", "case-b": "i2", "sw-default": "i3", "eb-child": "i4", "eb-fallback": "i5"}
	var got []string
	for _, e := range collectIslands(mustDecode(t, page), islands) {
		got = append(got, e.id)
	}
	want := []string{"i1", "i2", "i3", "i4", "i5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("collected islands %v, want %v", got, want)
	}
}
