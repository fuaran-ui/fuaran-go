package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// WIRE_FORMAT.md §3.1 — the declared text direction.
//
// Before Phase 1653 this host emitted NOTHING for `style.direction`: the slot
// decoded, round-tripped and survived every conformance family, and changed no
// markup at all — so an RTL document rendered left-to-right on a host reporting
// full codec conformance for it. The corpus's own render-fidelity roster does
// not declare the obligation, which is why no gate said so; declaring it there
// moves five hosts in one change-set and is not this host's to make alone, so
// the obligation is pinned HERE, against the two corpus fixtures that carry a
// declared direction and against the Rust host's emission for the same trees.
//
// Three §3.1 clauses, and the two emissions carry all three between them:
//
//   - EMIT the direction — `dir="ltr"` / `dir="rtl"` on the node's own wrapper.
//   - ISOLATE the run — the `fuaran-dir-*` class, which the reference
//     stylesheet gives `unicode-bidi: isolate`, so a declared run cannot
//     reorder the text around it.
//   - A DECLARATION WINS — putting `dir` on the node itself is what overrides
//     an inherited or heuristic direction, and the nested fixture below is the
//     case that proves it: an `ltr` reference inside an `rtl` block.
//
// `auto` is deliberately absent from all three: it is the inherited direction,
// and this host has not adopted the reference tier's `dir="auto"` heuristic
// over bound display leaves.
func findCorpusDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "wire-format-fixtures")
		if _, err := os.Stat(filepath.Join(candidate, "manifest.json")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("wire-format-fixtures corpus not found; skipping (standalone checkout)")
		}
		dir = parent
	}
}

func renderCorpusNode(t *testing.T, id string) string {
	t.Helper()
	corpus := findCorpusDir(t)
	raw, err := os.ReadFile(filepath.Join(corpus, "nodes", id+".json"))
	if err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	node, err := wire.DecodeNode(strings.TrimRight(string(raw), "\r\n"))
	if err != nil {
		t.Fatalf("decoding %s: %v", id, err)
	}
	return renderHTML(t, node, BindingSources{})
}

func TestDeclaredDirectionEmitsDirAndIsolationClass(t *testing.T) {
	html := renderCorpusNode(t, "style-direction-ltr-1")
	if !strings.Contains(html, ` fuaran-dir-ltr" dir="ltr">`) {
		t.Errorf("a declared ltr direction must emit both the isolation class (last in the class "+
			"list) and dir=\"ltr\" on the node's own wrapper:\n%s", html)
	}
}

func TestADeclaredDirectionWinsInsideAnOppositeOne(t *testing.T) {
	html := renderCorpusNode(t, "style-direction-isolated-1")
	// The rtl container...
	if !strings.Contains(html, `fuaran-kind-stack`) || !strings.Contains(html, ` fuaran-dir-rtl" dir="rtl">`) {
		t.Errorf("the rtl container lost its direction:\n%s", html)
	}
	// ...and the ltr reference INSIDE it, which is the clause that matters:
	// a declaration wins over the inherited direction, so the nested node
	// carries its own dir rather than inheriting the block's.
	if !strings.Contains(html, ` fuaran-dir-ltr" dir="ltr">`) {
		t.Errorf("the nested ltr reference did not override the inherited rtl direction — "+
			"a declaration must win (§3.1):\n%s", html)
	}
	// The undeclared sibling emits neither, because `auto` is the inherited
	// direction and this host declares no heuristic. Verifying the probe: if
	// this host started emitting dir unconditionally, the two tests above would
	// still pass.
	if strings.Count(html, `dir="`) != 2 {
		t.Errorf("expected exactly two dir attributes (the rtl block and the ltr reference); "+
			"an undeclared node must emit none:\n%s", html)
	}
}
