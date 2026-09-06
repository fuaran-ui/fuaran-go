package merge

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// nodeWithUnencodableState builds a node whose state extra holds a nil wire
// Value — the one shape the canonical encoder refuses ("cannot encode a nil
// Value"). It is the cheapest way to reach the failure path; what matters is
// that the path exists at all, not how a real tree gets there.
func nodeWithUnencodableState(id, label string) wire.Node {
	return wire.Node{
		ID: id,
		Kind: wire.Obj{Tag: "Markdown", Fields: map[string]wire.Value{
			"text": wire.Str(label),
		}},
		Extras: map[string]wire.Value{
			"state": wire.Obj{Fields: map[string]wire.Value{"onLoading": nil}},
		},
	}
}

func plainNode(id, label string) wire.Node {
	return wire.Node{
		ID: id,
		Kind: wire.Obj{Tag: "Markdown", Fields: map[string]wire.Value{
			"text": wire.Str(label),
		}},
	}
}

func TestUnencodableFacetsDoNotCompareEqual(t *testing.T) {
	// mustEncode swallowed the error and returned "", so every unencodable
	// facet compared equal to every other one. Two branches that could not be
	// encoded AT ALL therefore concluded they agreed, and the merge returned
	// one of them — the strongest possible verdict from the one comparison
	// that carried no information.
	base := plainNode("root", "base")
	a := nodeWithUnencodableState("root", "a")
	b := nodeWithUnencodableState("root", "b")

	result := Merge3Way(base, a, b)

	if result.OK {
		t.Fatal("two unencodable facets merged cleanly — they were treated as agreeing")
	}
	found := false
	for _, c := range result.Conflicts {
		if c.ConflictClass == classUnencodableFacet {
			found = true
			if c.Facet != "state" {
				t.Errorf("conflict facet = %q, want \"state\"", c.Facet)
			}
		}
	}
	if !found {
		t.Errorf("conflicts = %+v, want one classed %q", result.Conflicts, classUnencodableFacet)
	}
}

func TestUnencodableFacetBlocksEvenUnderPrimacy(t *testing.T) {
	// Primacy resolves a DISAGREEMENT. An encode failure is not a
	// disagreement — it is a failure to establish whether there is one — so a
	// pin must not silently pick a side.
	base := plainNode("root", "base")
	a := nodeWithUnencodableState("root", "a")
	b := plainNode("root", "b")

	result := Merge3WayWithAuthor(Primary(), Secondary(nil), base, a, b)

	if result.OK {
		t.Fatal("primacy resolved an unencodable facet; it must block")
	}
}

func TestOrdinaryMergeIsUnaffected(t *testing.T) {
	// The failure path must not cost the working one.
	base := plainNode("root", "base")
	a := plainNode("root", "changed")
	b := plainNode("root", "base")

	result := Merge3Way(base, a, b)
	if !result.OK {
		t.Fatalf("a clean one-sided edit refused: %+v", result.Conflicts)
	}
	text, _ := result.Tree.Kind.Fields["text"].(wire.Str)
	if string(text) != "changed" {
		t.Errorf("merged text = %q, want %q", text, "changed")
	}
}
