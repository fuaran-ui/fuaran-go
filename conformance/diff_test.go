package conformance

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/diff"
	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The diff-conformance leg: the structural tree diff → op-script producer
// (diff.Diff) is the inverse of the apply engine, and its defining property is
// the round-trip law
//
//	Apply(Diff(a, b), a) == b   (canonical-JSON equality)
//
// This exercises the law over the shared wire-format-fixtures corpus trees —
// every node-round-trip fixture is a real, spec-conformant tree the sibling
// hosts also carry. The in-repo generated property test (diff.TestRoundTripProperty)
// covers deep synthetic recursion; this leg pins the law against the canonical
// corpus. (No shared cross-host diff-golden family exists in the corpus yet;
// authoring one that rs/py also certify against is tracked as follow-up.)

// corpusNodeTrees decodes every node-round-trip fixture the manifest declares.
func corpusNodeTrees(t *testing.T) (string, []struct {
	name string
	tree wire.Node
}) {
	t.Helper()
	corpus, m := loadCorpus(t)
	var trees []struct {
		name string
		tree wire.Node
	}
	for _, f := range m.Fixtures {
		if f.Kind != "node-round-trip" {
			continue
		}
		node, err := wire.DecodeNode(readFixture(t, corpus, f.InputFile))
		if err != nil {
			t.Fatalf("decoding corpus node %s: %v", f.InputFile, err)
		}
		trees = append(trees, struct {
			name string
			tree wire.Node
		}{f.InputFile, node})
	}
	if len(trees) == 0 {
		t.Fatal("no node-round-trip fixtures in the corpus manifest")
	}
	return corpus, trees
}

func encNode(t *testing.T, n wire.Node) string {
	t.Helper()
	s, err := wire.EncodeNode(n)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	return s
}

func applyScript(t *testing.T, before wire.Node, script []wire.Obj) wire.Node {
	t.Helper()
	tree := before
	for i, op := range script {
		next, err := ops.Apply(op, tree)
		if err != nil {
			t.Fatalf("op #%d (%s) failed to apply: %v", i, op.Tag, err)
		}
		tree = next
	}
	return tree
}

func assertRoundTrip(t *testing.T, a, b wire.Node) {
	t.Helper()
	got := applyScript(t, a, diff.Diff(a, b))
	if encNode(t, got) != encNode(t, b) {
		t.Errorf("round-trip failed:\n got  %s\n want %s", encNode(t, got), encNode(t, b))
	}
}

// TestDiffCorpusSelfIsEmpty — every corpus tree diffs to a no-op against
// itself (the identity leg of the round-trip law).
func TestDiffCorpusSelfIsEmpty(t *testing.T) {
	_, trees := corpusNodeTrees(t)
	for _, tc := range trees {
		t.Run(tc.name, func(t *testing.T) {
			if script := diff.Diff(tc.tree, tc.tree); len(script) != 0 {
				t.Errorf("Diff(%s, %s) = %d op(s), want 0", tc.name, tc.name, len(script))
			}
		})
	}
}

// TestDiffCorpusPairsRoundTrip — Apply(Diff(a,b), a) == b for corpus tree pairs.
// Each tree is diffed against the next (rotation), exercising both root-id
// divergence (→ ReplaceRoot) and, where root ids coincide, the localised path.
func TestDiffCorpusPairsRoundTrip(t *testing.T) {
	_, trees := corpusNodeTrees(t)
	for i := range trees {
		a := trees[i]
		b := trees[(i+1)%len(trees)]
		t.Run(a.name+"→"+b.name, func(t *testing.T) {
			assertRoundTrip(t, a.tree, b.tree)
		})
	}
}

// TestDiffCorpusMutationRoundTrip — for each layout corpus tree with children,
// build a genuinely related same-root `after` by applying a real op (drop the
// first child) and assert Diff round-trips back. This drives the same-root
// diff path against spec-conformant trees rather than synthetic ones.
func TestDiffCorpusMutationRoundTrip(t *testing.T) {
	_, trees := corpusNodeTrees(t)
	covered := 0
	for _, tc := range trees {
		firstChild, ok := firstLayoutChildID(tc.tree)
		if !ok {
			continue
		}
		after, err := ops.Apply(wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{
			"target": wire.Str(firstChild),
		}}, tc.tree)
		if err != nil {
			continue // not a droppable child in this tree — skip
		}
		covered++
		t.Run(tc.name, func(t *testing.T) {
			assertRoundTrip(t, tc.tree, after)
		})
	}
	if covered == 0 {
		t.Fatal("no layout corpus trees with a droppable child — mutation leg vacuous")
	}
	t.Logf("mutation round-trip covered %d corpus trees", covered)
}

// firstLayoutChildID returns the id of the first structural child of the root,
// when the root is a layout kind with at least one child.
func firstLayoutChildID(n wire.Node) (string, bool) {
	layoutKinds := map[string]bool{
		"Box": true, "SplitPanel": true, "Tabs": true,
		"Stepper": true, "SummaryList": true, "Disclosure": true,
	}
	if !layoutKinds[n.Kind.Tag] {
		return "", false
	}
	arr, ok := n.Kind.Fields["children"].(wire.Arr)
	if !ok || len(arr) == 0 {
		return "", false
	}
	child, ok := arr[0].(wire.Node)
	if !ok {
		return "", false
	}
	return child.ID, true
}
