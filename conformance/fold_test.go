package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/merge"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The merge-fold leg (WIRE_FORMAT §34.6, Phase 2643): every `foldFixtures`
// entry of merge-conformance/manifest.json, read under its own key by name.
//
//   - merge-fold-derive: ReconcileFold derives, the tree is byte-identical to
//     expectedFile, sha256 == outcomeHash, and the swapped reconcile derives the
//     same bytes.
//   - merge-fold-refusal: the policy refuses, the envelope is byte-identical to
//     envelopeFile, sha256 == envelopeHash, and the swapped reconcile refuses
//     with every entry's left and right swapped and nothing else changed.
//   - merge-fold-gated: the fold derives and the sample validator (the
//     test-side port in merge_test.go) finds defects neither head carries; the
//     verdict is byte-identical to verdictFile, sha256 == verdictHash, in both
//     branch orders.
//
// It lives beside the tree-merge legs because the gated fixture runs under the
// same sample validator, introduced-defect diff and verdict codec they share.

type foldFixture struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	BaseFile     string `json:"baseFile"`
	AOpsFile     string `json:"aOpsFile"`
	BOpsFile     string `json:"bOpsFile"`
	ExpectedFile string `json:"expectedFile"`
	OutcomeHash  string `json:"outcomeHash"`
	EnvelopeFile string `json:"envelopeFile"`
	EnvelopeHash string `json:"envelopeHash"`
	VerdictFile  string `json:"verdictFile"`
	VerdictHash  string `json:"verdictHash"`
}

func TestMergeFoldCorpus(t *testing.T) {
	corpus := corpusroot.ForTest(t)
	root := filepath.Join(corpus, "merge-conformance")
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("merge corpus not found: %v", err)
	}
	var m struct {
		FoldFixtures []foldFixture `json:"foldFixtures"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing merge manifest: %v", err)
	}
	// NOT a skip: this host certifies the family, so an empty list means the
	// corpus moved out from under it.
	if len(m.FoldFixtures) == 0 {
		t.Fatal("merge corpus declares no foldFixtures — the family this leg certifies is gone")
	}

	decodeTree := func(t *testing.T, rel string) wire.Node {
		node, err := wire.DecodeNode(readRel(t, root, rel))
		if err != nil {
			t.Fatalf("decode %s: %v", rel, err)
		}
		return node
	}
	decodeOps := func(t *testing.T, rel string) []wire.Obj {
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(readRel(t, root, rel)), &items); err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		out := make([]wire.Obj, len(items))
		for i, item := range items {
			op, err := wire.DecodeOp(string(item))
			if err != nil {
				t.Fatalf("decode %s[%d]: %v", rel, i, err)
			}
			out[i] = op
		}
		return out
	}

	kinds := map[string]int{}
	for _, fx := range m.FoldFixtures {
		kinds[fx.Kind]++
		t.Run(fx.ID, func(t *testing.T) {
			base := decodeTree(t, fx.BaseFile)
			a, b := decodeOps(t, fx.AOpsFile), decodeOps(t, fx.BOpsFile)
			switch fx.Kind {
			case "merge-fold-derive":
				for _, order := range []struct {
					name   string
					d1, d2 []wire.Obj
				}{{"a,b", a, b}, {"b,a", b, a}} {
					r, fault := merge.ReconcileFold(base, order.d1, order.d2)
					if fault != nil {
						t.Fatalf("%s: refused (%s): %+v", order.name, fault.Kind, fault.Refusals)
					}
					got, err := wire.EncodeNode(r.Tree)
					if err != nil {
						t.Fatalf("%s: encode derived tree: %v", order.name, err)
					}
					if want := readRel(t, root, fx.ExpectedFile); got != want {
						t.Errorf("%s: derived tree not byte-identical: %s", order.name, firstDiff(got, want))
					}
					if h := sha256Hex(got); h != fx.OutcomeHash {
						t.Errorf("%s: outcomeHash = %s, want %s", order.name, h, fx.OutcomeHash)
					}
				}

			case "merge-fold-refusal":
				want := readRel(t, root, fx.EnvelopeFile)
				_, fault := merge.ReconcileFold(base, a, b)
				if fault == nil || fault.Kind != merge.FaultRefused {
					t.Fatalf("expected a refusal, got %+v", fault)
				}
				envelope, err := merge.EncodeFoldEnvelope(fault.Refusals)
				if err != nil {
					t.Fatalf("encode envelope: %v", err)
				}
				if envelope != want {
					t.Errorf("envelope not byte-identical: %s", firstDiff(envelope, want))
				}
				if h := sha256Hex(envelope); h != fx.EnvelopeHash {
					t.Errorf("envelopeHash = %s, want %s", h, fx.EnvelopeHash)
				}

				// Swap symmetry: the swapped reconcile's refusals, mirrored back
				// (left <-> right) and re-sorted, are the committed envelope.
				_, swapped := merge.ReconcileFold(base, b, a)
				if swapped == nil || swapped.Kind != merge.FaultRefused {
					t.Fatalf("the swapped reconcile did not refuse: %+v", swapped)
				}
				mirrored := make([]merge.FoldRefusal, len(swapped.Refusals))
				for i, r := range swapped.Refusals {
					c := r.Conflict
					c.Left, c.Right = c.Right, c.Left
					mirrored[i] = merge.FoldRefusal{Conflict: c, Class: r.Class}
				}
				back, err := merge.EncodeFoldEnvelope(mirrored)
				if err != nil {
					t.Fatalf("encode mirrored envelope: %v", err)
				}
				if back != want {
					t.Errorf("swapped envelope is not the mirror: %s", firstDiff(back, want))
				}

			case "merge-fold-gated":
				want := readRel(t, root, fx.VerdictFile)
				for _, order := range []struct {
					name   string
					d1, d2 []wire.Obj
				}{{"a,b", a, b}, {"b,a", b, a}} {
					_, defects, fault := merge.ReconcileFoldGated(introducedDefects, base, order.d1, order.d2)
					if fault == nil || fault.Kind != merge.FaultIntroducedDefects {
						t.Fatalf("%s: expected the gate to refuse, got %+v", order.name, fault)
					}
					verdict := encodeVerdict(defects)
					if verdict != want {
						t.Errorf("%s: verdict not byte-identical: %s", order.name, firstDiff(verdict, want))
					}
					if h := sha256Hex(verdict); h != fx.VerdictHash {
						t.Errorf("%s: verdictHash = %s, want %s", order.name, h, fx.VerdictHash)
					}
				}
				// Ungated, the same deltas derive: the refusal is the gate's.
				if _, fault := merge.ReconcileFold(base, a, b); fault != nil {
					t.Errorf("ungated fold refused (%s); the gated fixture must derive", fault.Kind)
				}

			default:
				t.Fatalf("unknown foldFixtures kind %q", fx.Kind)
			}
		})
	}
	for _, k := range []string{"merge-fold-derive", "merge-fold-refusal", "merge-fold-gated"} {
		if kinds[k] == 0 {
			t.Errorf("foldFixtures holds no %s fixture", k)
		}
	}
}
