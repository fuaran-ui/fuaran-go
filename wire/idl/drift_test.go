package idl_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
	"github.com/fuaran-ui/fuaran-go/wire/idl/internal/idlgen"
)

// corpusIDL reads the corpus's idl.json through the module's one locator, or
// skips on a standalone checkout.
func corpusIDL(t *testing.T) []byte {
	t.Helper()
	root, err := corpusroot.Find()
	if err != nil {
		t.Fatal(err)
	}
	if root == "" {
		t.Skip("no conformance corpus beside this checkout (set " + corpusroot.EnvVar + ")")
	}
	src, err := os.ReadFile(filepath.Join(root, "idl.json"))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestRecordedHashMatchesCorpus — the generated package names the idl.json it
// was produced from. Red the moment the corpus moves and this host has not been
// regenerated: the cheap, specific half of the drift check.
func TestRecordedHashMatchesCorpus(t *testing.T) {
	src := corpusIDL(t)
	if got := idlgen.Hash(src); got != idl.SourceSHA256 {
		t.Fatalf("idl.json has moved since this host was generated (recorded %s, corpus %s) — run go generate ./wire/idl", idl.SourceSHA256, got)
	}
}

// TestGeneratedMatchesCorpus — regenerates from the corpus and compares every
// generated file byte-for-byte with the committed one. It catches what the
// hash cannot: a generated file edited by hand, or a generator change that was
// not re-run.
func TestGeneratedMatchesCorpus(t *testing.T) {
	src := corpusIDL(t)
	outs, err := idlgen.Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	module, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outs {
		committed, err := os.ReadFile(filepath.Join(module, filepath.FromSlash(o.Path)))
		if err != nil {
			t.Fatalf("%s: %v", o.Path, err)
		}
		if !bytes.Equal(committed, o.Content) {
			t.Errorf("%s is not what idl.json projects — run go generate ./wire/idl", o.Path)
		}
	}
}

// TestDriftCheckGoesRed proves both checks can fail: an idl.json differing by
// one added enum case yields a different hash AND a different projection.
func TestDriftCheckGoesRed(t *testing.T) {
	src := corpusIDL(t)
	moved := bytes.Replace(src, []byte(`"Neutral",`), []byte(`"Neutral", "ProbeCase",`), 1)
	if bytes.Equal(moved, src) {
		t.Fatal("the probe edit did not apply — the falsifier is vacuous")
	}
	if idlgen.Hash(moved) == idl.SourceSHA256 {
		t.Error("a moved idl.json hashes to the recorded value")
	}
	base, err := idlgen.Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := idlgen.Generate(moved)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(base[0].Content, probe[0].Content) {
		t.Error("a moved idl.json projects to the same generated package")
	}
}
