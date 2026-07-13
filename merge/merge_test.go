package merge

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func decode(t *testing.T, s string) wire.Node {
	t.Helper()
	n, err := wire.DecodeNode(s)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return n
}

func encode(t *testing.T, n wire.Node) string {
	t.Helper()
	s, err := wire.EncodeNode(n)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return s
}

// A tone tree: a Box holding one Markdown child, with a chosen root tone.
func toneTree(tone string) string {
	style := ""
	if tone != "" {
		style = `,"style":{"emphasis":"Normal","tone":"` + tone + `","weight":"Standard"}`
	}
	return `{"id":"root","kind":{"$type":"Box","children":[` +
		`{"id":"c","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"x"}}}` +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}` + style + `}`
}

// The author-agnostic merge blocks a genuine tone conflict (both sides changed
// the root tone differently).
func TestAgnosticMergeBlocksTrueConflict(t *testing.T) {
	base := decode(t, toneTree(""))
	a := decode(t, toneTree("Brand"))
	b := decode(t, toneTree("Success"))
	result := Merge3Way(base, a, b)
	if result.OK {
		t.Fatal("expected a blocking conflict")
	}
	if len(result.Conflicts) == 0 || result.Conflicts[0].Facet != "style.tone" {
		t.Errorf("expected a style.tone conflict, got %+v", result.Conflicts)
	}
}

// Human primacy resolves the same conflict in the primary branch's favour and
// records it as resolved (not blocking).
func TestHumanPrimacyResolvesConflict(t *testing.T) {
	base := decode(t, toneTree(""))
	a := decode(t, toneTree("Brand"))   // the human
	b := decode(t, toneTree("Success")) // the agent
	result := Merge3WayWithAuthor(Primary(), Secondary(nil), base, a, b)
	if !result.OK {
		t.Fatalf("human primacy should not block: %+v", result.Conflicts)
	}
	if len(result.Resolved) == 0 || !result.Resolved[0].PrimacyHeld {
		t.Errorf("expected a primacy-held resolution, got %+v", result.Resolved)
	}
	// The primary (A = Brand) tone wins.
	if got := encode(t, result.Tree); got != encode(t, a) {
		t.Errorf("primary tone did not win:\n got %s\nwant %s", got, encode(t, a))
	}
}

// A no-op merge (a == b == base) returns base unchanged with no conflicts.
func TestNoOpMerge(t *testing.T) {
	base := decode(t, toneTree("Brand"))
	result := Merge3Way(base, decode(t, toneTree("Brand")), decode(t, toneTree("Brand")))
	if !result.OK {
		t.Fatalf("no-op merge conflicted: %+v", result.Conflicts)
	}
	if encode(t, result.Tree) != encode(t, base) {
		t.Error("no-op merge changed the tree")
	}
}
