package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// TestChartLoweringDrawingsRoundTrip: the chart-lowering goldens are Drawing
// trees this host does not produce (require-pre-lowered posture) but MUST
// round-trip byte-identically — they carry the Phase 642 DrawStyle.markId.
func TestChartLoweringDrawingsRoundTrip(t *testing.T) {
	corpus, _ := loadCorpus(t)
	dir := filepath.Join(corpus, "chart-lowering")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("chart-lowering goldens not found: %v", err)
	}
	ran := 0
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" || len(name) < 14 || name[len(name)-14:] != ".expected.json" {
			continue
		}
		ran++
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			node, err := wire.DecodeNode(string(raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := wire.EncodeNode(node)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if out != string(raw) {
				t.Errorf("not byte-identical: %s", firstDiff(out, string(raw)))
			}
		})
	}
	if ran == 0 {
		t.Fatal("no chart-lowering expected goldens found")
	}
}
