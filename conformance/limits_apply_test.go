package conformance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Certifies the apply engine against the shared apply/limits-apply.json family
// (Phase 2141): an op that can grow the tree is checked on its result, and one
// that takes the tree past a wire limit is refused with LimitExceeded. The same
// runner certifies apply/duplicate-ids-apply.json (Phase 2172) and
// apply/style-normalise-apply.json (Phase 2313). Each vector's tree and op are
// decoded by this host's own decoder and applied; an accepting vector that pins
// `expected.tree` also requires the applied tree to re-encode to those bytes.

type limitsVector struct {
	ID    string `json:"id"`
	Input struct {
		Tree string `json:"tree"`
		Op   string `json:"op"`
	} `json:"input"`
	Expected struct {
		Verdict string `json:"verdict"`
		Code    string `json:"code"`
		Tree    string `json:"tree"`
	} `json:"expected"`
}

func TestLimitsApplyCorpus(t *testing.T) { certifyApplyFamily(t, "limitsApply") }

// TestDuplicateIdsApplyCorpus certifies the apply/duplicate-ids-apply.json
// family (Phase 2172): an op that leaves an id it installed held twice is
// refused with DuplicateNodeId.
func TestDuplicateIdsApplyCorpus(t *testing.T) { certifyApplyFamily(t, "duplicateIdsApply") }

// TestStyleNormaliseApplyCorpus certifies the apply/style-normalise-apply.json
// family (Phase 2313): a style result equal to the default record is stored as
// the absent slot, so the applied tree encodes with no `style` member.
func TestStyleNormaliseApplyCorpus(t *testing.T) { certifyApplyFamily(t, "styleNormaliseApply") }

// certifyApplyFamily decodes and applies every vector of one apply/ family.
func certifyApplyFamily(t *testing.T, familyID string) {
	root := corpusroot.ForTest(t)

	var manifest struct {
		Families []struct {
			ID      string `json:"id"`
			File    string `json:"file"`
			Vectors int    `json:"vectors"`
		} `json:"families"`
	}
	readJSON(t, filepath.Join(root, "apply", "manifest.json"), &manifest)

	file, declared := "", -1
	for _, f := range manifest.Families {
		if f.ID == familyID {
			file, declared = f.File, f.Vectors
		}
	}
	if file == "" {
		t.Fatalf("apply/manifest.json declares no %s family", familyID)
	}

	var family struct {
		Vectors []limitsVector `json:"vectors"`
	}
	readJSON(t, filepath.Join(root, "apply", file), &family)
	if len(family.Vectors) != declared {
		t.Fatalf("%s holds %d vectors, the manifest declares %d", familyID, len(family.Vectors), declared)
	}

	for _, v := range family.Vectors {
		t.Run(v.ID, func(t *testing.T) {
			tree, err := wire.DecodeNode(v.Input.Tree)
			if err != nil {
				t.Fatalf("the tree did not decode: %v", err)
			}
			op, err := wire.DecodeOp(v.Input.Op)
			if err != nil {
				t.Fatalf("the op did not decode: %v", err)
			}
			applied, applyErr := ops.Apply(op, tree)
			switch v.Expected.Verdict {
			case "accept":
				if applyErr != nil {
					t.Fatalf("expected accept, refused: %v", applyErr)
				}
				if v.Expected.Tree != "" {
					got, err := wire.EncodeNode(applied)
					if err != nil {
						t.Fatalf("the applied tree did not encode: %v", err)
					}
					if got != v.Expected.Tree {
						t.Fatalf("applied tree bytes:\n got %s\nwant %s", got, v.Expected.Tree)
					}
				}
			case "reject":
				var ae *ops.ApplyError
				if !errors.As(applyErr, &ae) {
					t.Fatalf("expected a %s refusal, got %v", v.Expected.Code, applyErr)
				}
				if string(ae.Code) != v.Expected.Code {
					t.Fatalf("refusal code = %q, want %q", ae.Code, v.Expected.Code)
				}
			default:
				t.Fatalf("unknown verdict %q", v.Expected.Verdict)
			}
		})
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}
