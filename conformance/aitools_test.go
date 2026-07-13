package conformance

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/aitools"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The AI-tools introspection leg: introspect every node fixture tree and assert
// the static surface is well-formed over the whole corpus — the root snapshot
// matches, every walked node has a kind, and NodeState resolves the root. This
// certifies the introspection surface handles the full node vocabulary
// (there is no dedicated shared corpus; the node corpus is the input).
func TestAiToolsIntrospectionOverCorpus(t *testing.T) {
	corpus, m := loadCorpus(t)
	ran := 0
	for _, fx := range m.Fixtures {
		if fx.Kind != "node-round-trip" {
			continue
		}
		ran++
		t.Run(fx.ID, func(t *testing.T) {
			tree, err := wire.DecodeNode(readFixture(t, corpus, fx.InputFile))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			snap := aitools.InspectTree(tree)
			if snap.ID != tree.ID {
				t.Errorf("root snapshot id = %q, want %q", snap.ID, tree.ID)
			}
			if snap.Kind == "" || snap.Kind == "<untagged>" {
				t.Errorf("root snapshot has no kind")
			}
			// Every node the walk reaches carries a kind discriminator.
			for _, node := range aitools.WalkNodes(tree) {
				if aitools.KindName(node) == "<untagged>" {
					t.Errorf("node %q has no kind discriminator", node.ID)
				}
			}
			// NodeState resolves the root and agrees with the snapshot.
			if ns, ok := aitools.NodeState(tree, tree.ID); !ok || ns.Kind != snap.Kind {
				t.Errorf("NodeState(root) = %+v ok=%v, want kind %s", ns, ok, snap.Kind)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no node fixtures to introspect")
	}
}
