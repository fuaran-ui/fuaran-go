// Package conformance certifies the fuaran-go host against the shared
// wire-format-fixtures corpus: every node-round-trip / op-round-trip fixture
// must re-encode byte-identically, every reject fixture must fail decode with
// the canonical code + a $-rooted path prefix, and every lenient-accept
// fixture must normalise to its verbose canonical form (WIRE_FORMAT §16). The
// envelope and elicitation families land with their own roadmap tiers and are
// not run here yet.
package conformance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/wire"
)

type manifestFixture struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	Decoder           string `json:"decoder"`
	InputFile         string `json:"inputFile"`
	ExpectedFile      string `json:"expectedFile"`
	ExpectedErrorCode string `json:"expectedErrorCode"`
	ExpectedPath      string `json:"expectedPath"`
	Description       string `json:"description"`
}

type corpusManifest struct {
	Version  int               `json:"version"`
	Fixtures []manifestFixture `json:"fixtures"`
}

// loadCorpus locates the corpus and parses its manifest, skipping the calling
// test on a standalone checkout.
func loadCorpus(t *testing.T) (string, corpusManifest) {
	t.Helper()
	corpus := corpusroot.ForTest(t)
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
	return corpus, m
}

// readFixture reads a fixture payload, trimming at most one trailing newline
// (the cross-host runners' canon; the canonical form itself carries no
// trailing whitespace).
func readFixture(t *testing.T, corpus, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(corpus, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", rel, err)
	}
	s := string(raw)
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s
}

// decodeReencode drives the fixture's named decoder entry point and re-encodes.
func decodeReencode(decoder, input string) (string, error) {
	switch decoder {
	case "op":
		op, err := wire.DecodeOp(input)
		if err != nil {
			return "", err
		}
		return wire.EncodeOp(op)
	default: // "node"
		node, err := wire.DecodeNode(input)
		if err != nil {
			return "", err
		}
		return wire.EncodeNode(node)
	}
}

// firstDiff renders the first differing byte position with context.
func firstDiff(got, want string) string {
	n := min(len(got), len(want))
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			lo := max(0, i-24)
			hiG := min(len(got), i+24)
			hiW := min(len(want), i+24)
			return "first diff at byte " + strconv.Itoa(i) +
				": got …" + got[lo:hiG] + "… want …" + want[lo:hiW] + "…"
		}
	}
	return "length differs: got " + strconv.Itoa(len(got)) + ", want " + strconv.Itoa(len(want))
}

// TestCorpusManifestLoads is the smoke leg: the harness can locate and parse
// the shared corpus manifest.
func TestCorpusManifestLoads(t *testing.T) {
	_, m := loadCorpus(t)
	t.Logf("corpus located: %d fixtures declared", len(m.Fixtures))
}

// TestRoundTrip runs every node-round-trip / op-round-trip fixture:
// encode(decode(input)) must equal the expected file byte-for-byte — the
// canonical form is the reference host's output, so a pass proves this host
// is byte-identical to it for that fixture.
func TestRoundTrip(t *testing.T) {
	corpus, m := loadCorpus(t)
	ran := 0
	for _, fx := range m.Fixtures {
		if fx.Kind != "node-round-trip" && fx.Kind != "op-round-trip" {
			continue
		}
		ran++
		t.Run(fx.ID, func(t *testing.T) {
			input := readFixture(t, corpus, fx.InputFile)
			got, err := decodeReencode(fx.Decoder, input)
			if err != nil {
				t.Fatalf("expected decode to succeed, got %v", err)
			}
			want := readFixture(t, corpus, fx.ExpectedFile)
			if got != want {
				t.Errorf("not byte-identical: %s", firstDiff(got, want))
			}
		})
	}
	if ran == 0 {
		t.Fatal("manifest declares no round-trip fixtures")
	}
}

// TestLenientAccept runs every lenient-accept fixture (WIRE_FORMAT §16,
// normative): the SHORTHAND input MUST decode and MUST re-encode to the
// verbose canonical expected file — the same decode → encode → byte-compare
// leg as a round-trip fixture, with inputFile ≠ expectedFile. A host that
// rejects a shorthand, or decodes it to different bytes, is non-conformant.
func TestLenientAccept(t *testing.T) {
	corpus, m := loadCorpus(t)
	ran := 0
	for _, fx := range m.Fixtures {
		if fx.Kind != "lenient-accept" {
			continue
		}
		ran++
		t.Run(fx.ID, func(t *testing.T) {
			input := readFixture(t, corpus, fx.InputFile)
			got, err := decodeReencode(fx.Decoder, input)
			if err != nil {
				t.Fatalf("expected decode to succeed, got %v", err)
			}
			want := readFixture(t, corpus, fx.ExpectedFile)
			if got != want {
				t.Errorf("did not normalise to the canonical form: %s", firstDiff(got, want))
			}
		})
	}
	if ran == 0 {
		t.Fatal("manifest declares no lenient-accept fixtures")
	}
}

// TestReject runs every reject fixture: decode must fail with the manifest's
// expectedErrorCode and a path starting with expectedPath.
func TestReject(t *testing.T) {
	corpus, m := loadCorpus(t)
	ran := 0
	for _, fx := range m.Fixtures {
		if fx.Kind != "reject" {
			continue
		}
		ran++
		t.Run(fx.ID, func(t *testing.T) {
			input := readFixture(t, corpus, fx.InputFile)
			got, err := decodeReencode(fx.Decoder, input)
			if err == nil {
				t.Fatalf("expected decode to fail, but it succeeded: %s", got)
			}
			var derr *wire.DecodeError
			if !errors.As(err, &derr) {
				t.Fatalf("expected a *wire.DecodeError, got %T: %v", err, err)
			}
			if string(derr.Code) != fx.ExpectedErrorCode {
				t.Errorf("code = %s, want %s", derr.Code, fx.ExpectedErrorCode)
			}
			wantPath := fx.ExpectedPath
			if wantPath == "" {
				wantPath = "$"
			}
			if !strings.HasPrefix(derr.Path, wantPath) {
				t.Errorf("path = %q, want prefix %q", derr.Path, wantPath)
			}
		})
	}
	if ran == 0 {
		t.Fatal("manifest declares no reject fixtures")
	}
}

// TestCorpusFamiliesBeyondTheFloorAreNamed prints, by name and count, every
// corpus family this host does not run.
//
// A declared lag and a silent omission are indistinguishable from a green
// suite, and only one of them is honest. The contract-card family is a
// deliberate lag here, as it is on the Python host — but a reader has no way to
// tell that from a suite which simply never mentions it. The Rust host has
// printed this list since Phase 553; this is the same declaration.
//
// It always passes: the output IS the declaration. The one assertion guards the
// list going stale in the other direction — a family named as covered that the
// corpus no longer holds, which would mean this list, and not the corpus, is
// what changed.
func TestCorpusFamiliesBeyondTheFloorAreNamed(t *testing.T) {
	_, m := loadCorpus(t)
	covered := map[string]bool{
		"node-round-trip":           true,
		"op-round-trip":             true,
		"reject":                    true,
		"lenient-accept":            true,
		"envelope-round-trip":       true,
		"envelope-reject":           true,
		"elicitation-round-trip":    true,
		"elicitation-reject":        true,
		"elicitation-answer-accept": true,
		"elicitation-answer-reject": true,
	}
	present := map[string]int{}
	for _, fx := range m.Fixtures {
		present[fx.Kind]++
	}
	kinds := make([]string, 0, len(present))
	for kind := range present {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		if !covered[kind] {
			t.Logf("skipped family (declared lag, not covered by this host): %s x %d", kind, present[kind])
		}
	}
	for kind := range covered {
		if present[kind] == 0 {
			t.Errorf("named as covered but absent from the corpus: %s", kind)
		}
	}
}
