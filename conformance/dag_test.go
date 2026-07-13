package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dag"
)

// The DAG-record conformance leg: decode each dag/ fixture, re-encode, and
// assert byte-equal to the expected file — the branching op-stream substrate
// (What-If / Counterfactual / Git-for-Interfaces branch model).

func TestDagCorpus(t *testing.T) {
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	dagRoot := filepath.Join(corpus, "dag")
	raw, err := os.ReadFile(filepath.Join(dagRoot, "manifest.json"))
	if err != nil {
		t.Skipf("dag corpus not found: %v", err)
	}
	var m struct {
		Fixtures []struct {
			ID           string `json:"id"`
			InputFile    string `json:"inputFile"`
			ExpectedFile string `json:"expectedFile"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing dag manifest: %v", err)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("dag corpus declares no fixtures")
	}
	for _, fx := range m.Fixtures {
		t.Run(fx.ID, func(t *testing.T) {
			input := readRel(t, dagRoot, fx.InputFile)
			record, err := dag.DecodeDagRecord(input)
			if err != nil {
				t.Fatalf("DecodeDagRecord: %v", err)
			}
			got, err := dag.EncodeDagRecord(record)
			if err != nil {
				t.Fatalf("EncodeDagRecord: %v", err)
			}
			want := readRel(t, dagRoot, fx.ExpectedFile)
			if got != want {
				t.Errorf("not byte-identical: %s", firstDiff(got, want))
			}
		})
	}
}

// readRel reads a fixture relative to a sub-corpus root, trimming a trailing newline.
func readRel(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	s := string(raw)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
