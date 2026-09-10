package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// WIRE_FORMAT.md §5: a decoded Binding.Computed resolves to an ERROR naming its
// replacements, never to a value.
//
// The fixture that used to render the empty state — a Metric whose value is a
// decoded Computed — put an em-dash on the page and told nobody, so a reader
// could not tell it from a metric whose query had not answered yet. This seam
// had no error channel at all, which held the NEGATIVE half of the rule (never
// 0 / "" / false) and not the positive one: that the reader is told, and told
// the remedy.
//
// Go-red: against the pre-1667 seam RenderHTML has no error to return and both
// error assertions below fail.
func TestDecodedComputedResolvesToAnErrorNamingItsReplacements(t *testing.T) {
	const metric = `{"id":"m","kind":{"$type":"Metric","emphasis":"Normal","format":{"$type":"None"},` +
		`"label":{"$type":"Literal","text":"Revenue"},"value":{"$type":"Computed","fn":"<closure>"},` +
		`"tone":"Default","weight":"Standard"}}`

	node := mustDecode(t, metric)

	// The seam itself, which is what a headless caller asks.
	value, err := resolveBinding(wire.Obj{Tag: "Computed", Fields: map[string]wire.Value{}}, nil)
	if value != nil {
		t.Fatalf("a decoded Computed resolved to %v — the silent-default defect this closes", value)
	}
	if !errors.Is(err, ErrDecodedComputed) {
		t.Fatalf("expected ErrDecodedComputed, got %v", err)
	}
	if !strings.Contains(err.Error(), "Binding.Expr") {
		t.Fatalf("the message must name the case that replaced it: %q", err.Error())
	}

	// ... and it still errors when the host furnishes a bag of sources whose keys
	// a fall-through lookup might otherwise have matched, which is the negative
	// half restated: no value of any kind, ever.
	_, err = resolveBinding(
		wire.Obj{Tag: "Computed", Fields: map[string]wire.Value{"fn": wire.Str("<closure>")}},
		BindingSources{"fn": wire.Int(7), "key": wire.Int(0), "name": wire.Str(""), "nodeId": wire.Bool(false)},
	)
	if !errors.Is(err, ErrDecodedComputed) {
		t.Fatalf("expected ErrDecodedComputed under a populated source bag, got %v", err)
	}

	// The exported entry point returns it, and STILL returns the document: a
	// render is a pure function of the tree and completes, so the caller gets both
	// what could be rendered and the reason the rest could not.
	html, err := RenderHTML(node, nil)
	if !errors.Is(err, ErrDecodedComputed) {
		t.Fatalf("RenderHTML must report the resolution error, got %v", err)
	}
	if !strings.Contains(html, `class="fuaran-kind-metric`) {
		t.Fatalf("the rest of the document must still render: %s", html)
	}

	// The negative half, over the rendered page: the value slot renders the
	// slot's empty state and never a fabricated answer.
	valueSlot := metricValueSlot(t, html)
	for _, forbidden := range []string{"0", "false", "true"} {
		if valueSlot == forbidden {
			t.Fatalf("the value slot rendered %q — a wrong answer indistinguishable from a right one", forbidden)
		}
	}

	// The islands surface reports it on the same terms — the two emission paths
	// must not differ, or a host would learn about this by marking a region.
	if _, err := RenderWithIslands(node, nil, nil); !errors.Is(err, ErrDecodedComputed) {
		t.Fatalf("RenderWithIslands must report the resolution error, got %v", err)
	}
}

// The go-red half of the test above: a change that made every binding error
// would satisfy it and break the host. Three shapes that must keep answering —
// a value, absence, and an UNEVALUABLE pipeline, which is the renderer unable to
// answer rather than the document asking something unanswerable.
func TestEveryOtherBindingStillResolvesOrSaysNotYet(t *testing.T) {
	value, err := resolveBinding(
		wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": wire.Int(41)}}, nil)
	if err != nil {
		t.Fatalf("a Static binding errored: %v", err)
	}
	if value != wire.Value(wire.Int(41)) {
		t.Fatalf("a Static binding must resolve, got %v", value)
	}

	value, err = resolveBinding(
		wire.Obj{Tag: "Query", Fields: map[string]wire.Value{"name": wire.Str("sales")}}, nil)
	if err != nil || value != nil {
		t.Fatalf("an unwritten Query is absence, not an error: value=%v err=%v", value, err)
	}

	// An UNEVALUABLE pipeline: this corpus fixture's Transform reads a `today`
	// param the host has not furnished, so the evaluator refuses with
	// UNBOUND_PARAM and the slot renders absence. That must NOT reach the caller —
	// making it reach is the over-reach this test pins against, and it reddened
	// three corpus legs when the first draft of the phase did exactly that.
	if _, err := RenderHTML(loadFixtureNode(t, "now-grain"), nil); err != nil {
		t.Fatalf("an unevaluable pipeline must render as absence and report nothing, got %v", err)
	}
}

// metricValueSlot extracts the text of the Metric value slot.
func metricValueSlot(t *testing.T, html string) string {
	t.Helper()
	const open = `<div class="fuaran-metric-value">`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("no metric value slot in %s", html)
	}
	rest := html[i+len(open):]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		t.Fatalf("the metric value slot does not close in %s", html)
	}
	return rest[:j]
}
