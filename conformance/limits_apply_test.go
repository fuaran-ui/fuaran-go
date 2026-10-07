package conformance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Certifies the apply engine against the shared apply/limits-apply.json family
// (Phase 2141): an op that can grow the tree is checked on its result, and one
// that takes the tree past a wire limit is refused with LimitExceeded. Each
// vector's tree and op are decoded by this host's own decoder and applied.

const limitsCorpusEnvVar = "FUARAN_WIRE_FIXTURES"

// limitsCorpusRoot honours FUARAN_WIRE_FIXTURES (a declared root holding no
// manifest fails rather than skipping), else walks up from the package.
func limitsCorpusRoot(t *testing.T) string {
	t.Helper()
	if declared := os.Getenv(limitsCorpusEnvVar); declared != "" {
		if _, err := os.Stat(filepath.Join(declared, "manifest.json")); err != nil {
			t.Fatalf("%s=%q does not name a conformance corpus (no manifest.json under it)", limitsCorpusEnvVar, declared)
		}
		return declared
	}
	return findCorpus()
}

type limitsVector struct {
	ID    string `json:"id"`
	Input struct {
		Tree string `json:"tree"`
		Op   string `json:"op"`
	} `json:"input"`
	Expected struct {
		Verdict string `json:"verdict"`
		Code    string `json:"code"`
	} `json:"expected"`
}

func TestLimitsApplyCorpus(t *testing.T) {
	root := limitsCorpusRoot(t)
	if root == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}

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
		if f.ID == "limitsApply" {
			file, declared = f.File, f.Vectors
		}
	}
	if file == "" {
		t.Fatal("apply/manifest.json declares no limitsApply family")
	}

	var family struct {
		Vectors []limitsVector `json:"vectors"`
	}
	readJSON(t, filepath.Join(root, "apply", file), &family)
	if len(family.Vectors) != declared {
		t.Fatalf("limitsApply holds %d vectors, the manifest declares %d", len(family.Vectors), declared)
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
			_, applyErr := ops.Apply(op, tree)
			switch v.Expected.Verdict {
			case "accept":
				if applyErr != nil {
					t.Fatalf("expected accept, refused: %v", applyErr)
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
