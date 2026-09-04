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

// mergeManifest models the two fixture families the corpus declares. The
// refusal family lives under its OWN key rather than beside the auto-merge
// triads in `fixtures`, because the leg below iterates every `fixtures` entry
// and asserts the merge SUCCEEDS before it looks at `kind` — a refusal triad
// added there would turn a conformant host red for modelling the corpus
// correctly.
type mergeManifest struct {
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
	RefusalFixtures []struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		BaseFile     string `json:"baseFile"`
		AFile        string `json:"aFile"`
		BFile        string `json:"bFile"`
		EnvelopeFile string `json:"envelopeFile"`
		EnvelopeHash string `json:"envelopeHash"`
	} `json:"refusalFixtures"`
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
	var m mergeManifest
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

// TestMergeRefusalCorpus is the refusal leg: each triad REFUSES, and the
// canonically-encoded two-sided envelope is byte-equal to the committed bytes
// with sha256(envelope) == envelopeHash.
//
// The swap is asserted here rather than committed twice — two fixture files that
// were transpositions of each other would pin the same fact in a form a host
// could satisfy by emitting both from one side.
func TestMergeRefusalCorpus(t *testing.T) {
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	root := filepath.Join(corpus, "merge-conformance")
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Skipf("merge corpus not found: %v", err)
	}
	var m mergeManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing merge manifest: %v", err)
	}
	// NOT a skip: this host has adopted the family, so an empty list means the
	// corpus moved out from under it, and "nothing to check" must never read as
	// "everything checked".
	if len(m.RefusalFixtures) == 0 {
		t.Fatal("merge corpus declares no refusalFixtures — the family this leg certifies is gone")
	}

	decode := func(rel string) wire.Node {
		node, err := wire.DecodeNode(readRel(t, root, rel))
		if err != nil {
			t.Fatalf("decode %s: %v", rel, err)
		}
		return node
	}

	for _, fx := range m.RefusalFixtures {
		t.Run(fx.ID, func(t *testing.T) {
			base, a, b := decode(fx.BaseFile), decode(fx.AFile), decode(fx.BFile)

			forward := merge.Merge3Way(base, a, b)
			if forward.OK {
				t.Fatalf("refusal fixture auto-merged — the triad no longer refuses")
			}
			envelope := merge.EncodeEnvelope(forward.Conflicts)
			if want := readRel(t, root, fx.EnvelopeFile); envelope != want {
				t.Errorf("envelope not byte-identical: %s", firstDiff(envelope, want))
			}
			if got := sha256Hex(envelope); got != fx.EnvelopeHash {
				t.Errorf("envelopeHash = %s, want %s", got, fx.EnvelopeHash)
			}

			// Swapping the caller's branches TRANSPOSES A and B and changes
			// nothing else. Without this the sides could be populated from
			// argument position and still match the committed bytes.
			swapped := merge.Merge3Way(base, b, a)
			if swapped.OK {
				t.Fatalf("the swapped merge auto-merged — a refusal must not depend on branch order")
			}
			fwd, rev := merge.SortCanonical(forward.Conflicts), merge.SortCanonical(swapped.Conflicts)
			if len(fwd) != len(rev) {
				t.Fatalf("swapped refusal has %d cells, forward has %d", len(rev), len(fwd))
			}
			for i := range fwd {
				f, r := fwd[i], rev[i]
				if f.NodeID != r.NodeID || f.Facet != r.Facet || f.ConflictClass != r.ConflictClass {
					t.Errorf("cell %d: forward %s/%s/%s != swapped %s/%s/%s",
						i, f.NodeID, f.Facet, f.ConflictClass, r.NodeID, r.Facet, r.ConflictClass)
					continue
				}
				if !sameSide(f.A, r.B) {
					t.Errorf("%s/%s: forward A != swapped B", f.NodeID, f.Facet)
				}
				if !sameSide(f.B, r.A) {
					t.Errorf("%s/%s: forward B != swapped A", f.NodeID, f.Facet)
				}
			}
		})
	}
}

func sameSide(x, y *merge.Side) bool {
	if x == nil || y == nil {
		return x == y
	}
	if x.Value != y.Value {
		return false
	}
	if x.Tag == nil || y.Tag == nil {
		return x.Tag == nil && y.Tag == nil
	}
	return *x.Tag == *y.Tag
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
