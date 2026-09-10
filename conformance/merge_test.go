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
	// The merge-TOTALITY family, a THIRD top-level key. Each entry is half of
	// a PAIR: a triad that must refuse (`merge-refusal`, carrying an
	// envelopeFile + envelopeHash) immediately followed by a corrected twin
	// that must auto-merge (`merge-3way`, carrying an expectedFile +
	// outcomeHash), cross-referenced by `twin` / `refusal`.
	//
	// The pair is the whole point, and it is why the key had to be separate
	// from both families above: a host passes a refusal suite by refusing
	// every structural merge, and passes an auto-merge suite by never growing
	// the arm at all, and only the pair pins the boundary between the two.
	//
	// It was invisible to this host until Phase 1653 — the manifest struct
	// simply did not name the key, so `encoding/json` dropped six fixtures and
	// both legs above stayed green while asserting nothing about the two
	// refusals the reference host raises.
	TotalityFixtures []struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		BaseFile     string `json:"baseFile"`
		AFile        string `json:"aFile"`
		BFile        string `json:"bFile"`
		EnvelopeFile string `json:"envelopeFile"`
		EnvelopeHash string `json:"envelopeHash"`
		ExpectedFile string `json:"expectedFile"`
		OutcomeHash  string `json:"outcomeHash"`
		Twin         string `json:"twin"`
		Refusal      string `json:"refusal"`
	} `json:"totalityFixtures"`
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

// TestMergeTotalityCorpus is the merge-totality leg — the pairs.
//
// Adopted by Phase 1653. Before it, this host did not read `totalityFixtures`
// at all, which is the quietest way a conformance leg can fail: the manifest
// grew a family, `encoding/json` discarded it because no field named it, and
// the suite reported the same green it had reported the day before. Nothing
// in either existing leg could have noticed — they iterate the keys they know.
//
// Each PAIR is checked as a pair, and both halves are required to be present:
// the refusal half exactly as `TestMergeRefusalCorpus` checks a refusal
// (refuses, envelope byte-identical, hash matches), the twin exactly as
// `TestMergeCorpus` checks a `merge-3way` (merges, tree byte-identical, hash
// matches). A host that refused both, or merged both, fails one half of every
// pair — which is the property the separate key exists to buy.
func TestMergeTotalityCorpus(t *testing.T) {
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
	// NOT a skip, for the reason the refusal leg gives: this host has adopted
	// the family, so an empty list means the corpus moved out from under it.
	if len(m.TotalityFixtures) == 0 {
		t.Fatal("merge corpus declares no totalityFixtures — the family this leg certifies is gone")
	}

	decode := func(rel string) wire.Node {
		node, err := wire.DecodeNode(readRel(t, root, rel))
		if err != nil {
			t.Fatalf("decode %s: %v", rel, err)
		}
		return node
	}

	// Index by id so each entry can assert its counterpart is really here. A
	// pair with one half missing is a corpus this host must not certify
	// against silently: the remaining half is exactly the suite a partial host
	// already passes.
	byID := map[string]bool{}
	for _, fx := range m.TotalityFixtures {
		byID[fx.ID] = true
	}

	refusals, twins := 0, 0
	for _, fx := range m.TotalityFixtures {
		t.Run(fx.ID, func(t *testing.T) {
			base, a, b := decode(fx.BaseFile), decode(fx.AFile), decode(fx.BFile)
			result := merge.Merge3Way(base, a, b)

			switch fx.Kind {
			case "merge-refusal":
				if fx.Twin == "" || !byID[fx.Twin] {
					t.Fatalf("refusal half names twin %q, which the corpus does not declare — "+
						"half a pair pins nothing this host does not already pass", fx.Twin)
				}
				if result.OK {
					t.Fatalf("totality refusal auto-merged — this host silently OMITS the merge " +
						"behaviour this pair exists to pin, and would look conformant without it")
				}
				envelope := merge.EncodeEnvelope(result.Conflicts)
				if want := readRel(t, root, fx.EnvelopeFile); envelope != want {
					t.Errorf("envelope not byte-identical: %s", firstDiff(envelope, want))
				}
				if got := sha256Hex(envelope); got != fx.EnvelopeHash {
					t.Errorf("envelopeHash = %s, want %s", got, fx.EnvelopeHash)
				}
			case "merge-3way":
				if fx.Refusal == "" || !byID[fx.Refusal] {
					t.Fatalf("twin names refusal %q, which the corpus does not declare", fx.Refusal)
				}
				if !result.OK {
					t.Fatalf("totality twin REFUSED — a host cannot pass this family by refusing "+
						"everything, which is what the twin is for: %+v", result.Conflicts)
				}
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
			default:
				t.Fatalf("unknown totality fixture kind %q — refused rather than skipped, because "+
					"an unrecognised kind that this leg passed over would be a fixture asserting nothing", fx.Kind)
			}
		})
		switch fx.Kind {
		case "merge-refusal":
			refusals++
		case "merge-3way":
			twins++
		}
	}

	// The family is only meaningful if BOTH halves ran. A corpus of refusals
	// alone, or twins alone, is one of the two suites the pair was invented to
	// replace.
	if refusals == 0 || twins == 0 {
		t.Fatalf("the totality family ran %d refusals and %d twins — a family with only one half "+
			"is exactly the shape a partial host passes", refusals, twins)
	}
	t.Logf("merge-totality EXECUTED: %d refusal(s) + %d twin(s)", refusals, twins)
}
