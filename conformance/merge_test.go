package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/merge"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The merge-conformance leg. merge-3way: encode(Merge3Way(base,a,b)) is
// byte-identical to the committed expectedFile and sha256(bytes)==outcomeHash
// (the SemanticStyle sub-field blend + the NodeId-byte tie-break).
// merge-validator-gated: a structurally-clean merge that INTRODUCES a
// domain-validity defect (present in the merged tree but in neither parent) is
// a semantic conflict; the deterministic artefact is the verdict (the
// introduced-defect set, canonically encoded). The sample domain validator +
// the introduced-defect diff + the verdict codec are ported TEST-SIDE, exactly
// as the sibling hosts port them — the invariant is a documented sample, not a
// host API.

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestMergeCorpus(t *testing.T) {
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	root := filepath.Join(corpus, "merge-conformance")
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Skipf("merge corpus not found: %v", err)
	}
	var m struct {
		Fixtures []struct {
			ID           string `json:"id"`
			Kind         string `json:"kind"`
			BaseFile     string `json:"baseFile"`
			AFile        string `json:"aFile"`
			BFile        string `json:"bFile"`
			ExpectedFile string `json:"expectedFile"`
			OutcomeHash  string `json:"outcomeHash"`
			VerdictFile  string `json:"verdictFile"`
			VerdictHash  string `json:"verdictHash"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing merge manifest: %v", err)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("merge corpus declares no fixtures")
	}

	decode := func(rel string) wire.Node {
		node, err := wire.DecodeNode(readRel(t, root, rel))
		if err != nil {
			t.Fatalf("decode %s: %v", rel, err)
		}
		return node
	}

	for _, fx := range m.Fixtures {
		t.Run(fx.ID, func(t *testing.T) {
			base, a, b := decode(fx.BaseFile), decode(fx.AFile), decode(fx.BFile)
			result := merge.Merge3Way(base, a, b)
			if !result.OK {
				t.Fatalf("unexpected conflicts: %+v", result.Conflicts)
			}
			if fx.Kind == "merge-3way" {
				merged, err := wire.EncodeNode(result.Tree)
				if err != nil {
					t.Fatalf("encode merged: %v", err)
				}
				if want := readRel(t, root, fx.ExpectedFile); merged != want {
					t.Errorf("merged tree not byte-identical: %s", firstDiff(merged, want))
				}
				if got := sha256Hex(merged); got != fx.OutcomeHash {
					t.Errorf("outcomeHash = %s, want %s", got, fx.OutcomeHash)
				}
				return
			}
			// merge-validator-gated
			introduced := introducedDefects(a, b, result.Tree)
			if len(introduced) == 0 {
				t.Fatal("expected an introduced defect")
			}
			verdict := encodeVerdict(introduced)
			if want := readRel(t, root, fx.VerdictFile); verdict != want {
				t.Errorf("verdict not byte-identical: %s", firstDiff(verdict, want))
			}
			if got := sha256Hex(verdict); got != fx.VerdictHash {
				t.Errorf("verdictHash = %s, want %s", got, fx.VerdictHash)
			}
		})
	}
}

// ── test-side sample validator + verdict codec (Phase-184 port) ─────────────

type mergeDefect struct {
	code, nodeID, facet, message string
}

func nodeChildren(n wire.Node) []wire.Node {
	arr, ok := n.Kind.Fields["children"].(wire.Arr)
	if !ok {
		return nil
	}
	var out []wire.Node
	for _, item := range arr {
		if c, ok := item.(wire.Node); ok {
			out = append(out, c)
		}
	}
	return out
}

func nodeTone(n wire.Node) string {
	if style, ok := n.Extras["style"].(wire.Obj); ok {
		if v, ok := style.Fields["tone"].(wire.Str); ok {
			return string(v)
		}
	}
	return "Default"
}

// gatedValidator is the sample domain rule: "at most one Brand-toned pane per
// dashboard" — inspects the root node only (mirrors the sibling walkers).
func gatedValidator(tree wire.Node) []mergeDefect {
	role, _ := tree.Kind.Fields["role"].(wire.Str)
	if tree.Kind.Tag != "Box" || role != "Dashboard" {
		return nil
	}
	var brand []wire.Node
	for _, c := range nodeChildren(tree) {
		if nodeTone(c) == "Brand" {
			brand = append(brand, c)
		}
	}
	if len(brand) <= 1 {
		return nil
	}
	var out []mergeDefect
	for _, c := range brand {
		out = append(out, mergeDefect{
			code: "TESTBRAND001", nodeID: c.ID, facet: "style.tone",
			message: "Pane '" + c.ID + "' shares Brand tone with a sibling — at most one Brand pane per dashboard.",
		})
	}
	return out
}

func defectIdentity(d mergeDefect) string { return d.code + " " + d.nodeID + " " + d.facet }
func defectOrderKey(d mergeDefect) string { return d.nodeID + " " + d.facet + " " + d.code }

func introducedDefects(parentA, parentB, merged wire.Node) []mergeDefect {
	parentKeys := make(map[string]bool)
	for _, d := range gatedValidator(parentA) {
		parentKeys[defectIdentity(d)] = true
	}
	for _, d := range gatedValidator(parentB) {
		parentKeys[defectIdentity(d)] = true
	}
	var introduced []mergeDefect
	for _, d := range gatedValidator(merged) {
		if !parentKeys[defectIdentity(d)] {
			introduced = append(introduced, d)
		}
	}
	sort.Slice(introduced, func(i, j int) bool {
		return defectOrderKey(introduced[i]) < defectOrderKey(introduced[j])
	})
	return introduced
}

func encodeVerdict(defects []mergeDefect) string {
	sort.Slice(defects, func(i, j int) bool { return defectOrderKey(defects[i]) < defectOrderKey(defects[j]) })
	out := "["
	for i, d := range defects {
		if i > 0 {
			out += ","
		}
		out += "{" + `"code":` + canonical.EscapeString(d.code) +
			`,"facet":` + canonical.EscapeString(d.facet) +
			`,"message":` + canonical.EscapeString(d.message) +
			`,"nodeId":` + canonical.EscapeString(d.nodeID) + "}"
	}
	return out + "]"
}
