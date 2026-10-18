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
	"strconv"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/diff"
	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/ops"
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

// TestStructuralKindsApplyAndDiff — Phase 2297's acceptance for the kinds that
// used to decode structurally (absorbing Phase 1926). Their nested nodes now
// decode as wire.Node and the apply engine, the diff and the island walk all
// read one layout set from the generated tables, so:
//   - InsertChild into a decoded SplitPanel keeps the children it had;
//   - InsertChild into a Modal (and a ScrollArea) succeeds;
//   - Diff is non-empty for trees that differ only under an ErrorBoundary, a
//     SplitPanel or a Disclosure, and Apply(Diff(a, b), a) == b.
func TestStructuralKindsApplyAndDiff(t *testing.T) {
	decode := func(doc string) wire.Node {
		t.Helper()
		n, err := wire.DecodeNode(doc)
		if err != nil {
			t.Fatalf("DecodeNode(%s): %v", doc, err)
		}
		return n
	}
	leaf := func(id, text string) string {
		return `{"id":"` + id + `","kind":{"$type":"Markdown","text":"` + text + `"}}`
	}
	insert := func(parent string) wire.Obj {
		return wire.Obj{Tag: "InsertChild", Fields: map[string]wire.Value{
			"parentId": wire.Str(parent),
			"child":    decode(leaf("new", "inserted")),
		}}
	}

	for _, c := range []struct{ name, tree, parent, want string }{
		{"split panel keeps its children",
			`{"id":"p","kind":{"$type":"SplitPanel","children":[` + leaf("a", "A") + `],"weight":0.5}}`, "p",
			`{"id":"p","kind":{"$type":"SplitPanel","children":[` + leaf("a", "A") + `,` + leaf("new", "inserted") + `],"weight":0.5}}`},
		{"modal",
			`{"id":"m","kind":{"$type":"Modal","children":[],"open":{"$type":"Static","value":true}}}`, "m",
			`{"id":"m","kind":{"$type":"Modal","children":[` + leaf("new", "inserted") + `],"open":{"$type":"Static","value":true}}}`},
		{"scroll area",
			`{"id":"s","kind":{"$type":"ScrollArea","children":[],"orientation":"Vertical"}}`, "s",
			`{"id":"s","kind":{"$type":"ScrollArea","children":[` + leaf("new", "inserted") + `],"orientation":"Vertical"}}`},
	} {
		got, err := ops.Apply(insert(c.parent), decode(c.tree))
		if err != nil {
			t.Errorf("%s: InsertChild refused: %v", c.name, err)
			continue
		}
		if enc := encNode(t, got); enc != c.want {
			t.Errorf("%s: InsertChild gave\n %s\nwant\n %s", c.name, enc, c.want)
		}
	}

	for _, c := range []struct{ name, a, b string }{
		{"under an ErrorBoundary",
			`{"id":"e","kind":{"$type":"ErrorBoundary","child":` + leaf("c", "before") + `,"fallback":` + leaf("f", "F") + `}}`,
			`{"id":"e","kind":{"$type":"ErrorBoundary","child":` + leaf("c", "after") + `,"fallback":` + leaf("f", "F") + `}}`},
		{"under a SplitPanel",
			`{"id":"p","kind":{"$type":"SplitPanel","children":[` + leaf("a", "before") + `],"weight":0.5}}`,
			`{"id":"p","kind":{"$type":"SplitPanel","children":[` + leaf("a", "after") + `],"weight":0.5}}`},
		{"under a Disclosure",
			`{"id":"d","kind":{"$type":"Disclosure","children":[` + leaf("a", "before") + `],"defaultOpen":true,"heading":"H","open":{"$type":"Static","value":true}}}`,
			`{"id":"d","kind":{"$type":"Disclosure","children":[` + leaf("a", "after") + `],"defaultOpen":true,"heading":"H","open":{"$type":"Static","value":true}}}`},
	} {
		a, b := decode(c.a), decode(c.b)
		if len(diff.Diff(a, b)) == 0 {
			t.Errorf("%s: the diff of two different trees is empty", c.name)
			continue
		}
		assertRoundTrip(t, a, b)
	}
}
