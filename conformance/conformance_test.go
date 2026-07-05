// Package conformance certifies the fuaran-go host against the shared
// wire-format-fixtures corpus. Stage-0: a smoke leg that locates and parses the
// corpus manifest. The byte-for-byte round-trip and reject legs land with the
// codec (roadmap floor) and are skipped here.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// findCorpus walks up from the working directory looking for the shared
// wire-format-fixtures corpus (a sibling of the fuaran-go repo under Fuaran-UI).
// Returns "" when absent, so the repo stays standalone-testable — the corpus
// legs skip rather than fail when the repo is checked out alone.
func findCorpus() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		manifest := filepath.Join(dir, "wire-format-fixtures", "manifest.json")
		if _, err := os.Stat(manifest); err == nil {
			return filepath.Dir(manifest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

type corpusManifest struct {
	Version  int `json:"version"`
	Fixtures []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"fixtures"`
}

// TestCorpusManifestLoads is the stage-0 smoke leg: it proves the harness can
// locate and parse the shared corpus manifest. Round-trip + reject legs land with
// the codec floor.
func TestCorpusManifestLoads(t *testing.T) {
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "manifest.json"))
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}
	var m corpusManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing manifest: %v", err)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("corpus manifest declares no fixtures")
	}
	t.Logf("corpus located: %d fixtures declared (round-trip + reject legs pending the codec floor)", len(m.Fixtures))
}

// TestCodecRoundTripPending is a placeholder for the byte-for-byte round-trip and
// reject legs, which require the codec (roadmap floor).
func TestCodecRoundTripPending(t *testing.T) {
	t.Skip("node/op round-trip + reject legs pending the codec floor — see CLAUDE.md and the fuaran roadmap")
}
