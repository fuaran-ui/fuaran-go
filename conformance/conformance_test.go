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

	"github.com/fuaran-ui/fuaran-go/wire"
)

// findCorpus walks up from the working directory looking for the shared
// wire-format-fixtures corpus (a sibling of the fuaran-go repo). Returns ""
// when absent, so the repo stays standalone-testable — the corpus legs skip
// rather than fail when the repo is checked out alone.
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

// TestDiscriminatorExhaustiveness is the corpus-driven exhaustiveness guard —
// the mitigation for Go's lack of compile-time totality over the closed
// "$type" vocabularies: every kind discriminator appearing anywhere in a
// round-trip node fixture, and every op discriminator (top-level or inside a
// Batch), must be a case the decoder's tables recognise. When the corpus
// grows a new case under the forward-coupling rule, this leg goes red naming
// the missing case even before the round-trip leg does.
func TestDiscriminatorExhaustiveness(t *testing.T) {
	corpus, m := loadCorpus(t)

	knownKinds := make(map[string]bool)
	for _, k := range wire.KnownNodeKinds() {
		knownKinds[k] = true
	}
	knownOps := make(map[string]bool)
	for _, k := range wire.KnownOpKinds() {
		knownOps[k] = true
	}

	seenKinds := make(map[string]bool)
	seenOps := make(map[string]bool)
	for _, fx := range m.Fixtures {
		switch fx.Kind {
		case "node-round-trip":
			var raw any
			if err := json.Unmarshal([]byte(readFixture(t, corpus, fx.InputFile)), &raw); err != nil {
				t.Fatalf("%s: %v", fx.ID, err)
			}
			collectNodeKinds(raw, seenKinds)
		case "op-round-trip":
			var raw any
			if err := json.Unmarshal([]byte(readFixture(t, corpus, fx.InputFile)), &raw); err != nil {
				t.Fatalf("%s: %v", fx.ID, err)
			}
			collectOpKinds(raw, seenOps, seenKinds)
		default:
			// lenient-accept / envelope / elicitation families land with their
			// own roadmap tiers.
		}
	}

	for kind := range seenKinds {
		if !knownKinds[kind] {
			t.Errorf("corpus carries node kind %q the decoder does not recognise — add its case (forward-coupling rule)", kind)
		}
	}
	for op := range seenOps {
		if !knownOps[op] {
			t.Errorf("corpus carries op kind %q the decoder does not recognise — add its case (forward-coupling rule)", op)
		}
	}
	if len(seenKinds) == 0 || len(seenOps) == 0 {
		t.Fatal("exhaustiveness guard collected no discriminators")
	}
	t.Logf("exhaustiveness guard: %d node kinds, %d op kinds exercised by the corpus", len(seenKinds), len(seenOps))
}

// collectNodeKinds treats raw as a node envelope and walks its genuine
// node-bearing positions only (children / child / fallback / default / body /
// switch cases / fragment-arg + mount-input slot trees / state surfaces). A
// blanket id+kind heuristic would over-match — a Form FIELD also carries an
// {id, kind} envelope whose kind.$type is a FormFieldKind, not a NodeKind.
func collectNodeKinds(raw any, kinds map[string]bool) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return
	}
	kind, ok := obj["kind"].(map[string]any)
	if !ok {
		return
	}
	if tag, ok := kind["$type"].(string); ok {
		kinds[tag] = true
	}
	if state, ok := obj["state"].(map[string]any); ok {
		collectNodeKinds(state["onLoading"], kinds)
		collectNodeKinds(state["onEmpty"], kinds)
	}
	if children, ok := kind["children"].([]any); ok {
		for _, c := range children {
			collectNodeKinds(c, kinds)
		}
	}
	collectNodeKinds(kind["child"], kinds)
	collectNodeKinds(kind["fallback"], kinds)
	collectNodeKinds(kind["default"], kinds)
	collectNodeKinds(kind["body"], kinds)
	if cases, ok := kind["cases"].([]any); ok {
		for _, c := range cases {
			if caseObj, ok := c.(map[string]any); ok {
				collectNodeKinds(caseObj["child"], kinds)
			}
		}
	}
	for _, slotMapKey := range []string{"args", "inputs"} {
		if slots, ok := kind[slotMapKey].(map[string]any); ok {
			for _, v := range slots {
				if arg, ok := v.(map[string]any); ok {
					collectNodeKinds(arg["tree"], kinds)
				}
			}
		}
	}
}

// collectOpKinds records an op fixture's top-level discriminator, recursing
// into Batch members and sweeping the node-bearing op fields for kinds.
func collectOpKinds(raw any, ops map[string]bool, kinds map[string]bool) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return
	}
	if tag, ok := obj["$type"].(string); ok {
		ops[tag] = true
	}
	if members, ok := obj["ops"].([]any); ok {
		for _, m := range members {
			collectOpKinds(m, ops, kinds)
		}
	}
	collectNodeKinds(obj["child"], kinds)               // InsertChild
	collectNodeKinds(obj["node"], kinds)                // ReplaceRoot
	if state, ok := obj["state"].(map[string]any); ok { // UpdateState
		collectNodeKinds(state["onLoading"], kinds)
		collectNodeKinds(state["onEmpty"], kinds)
	}
}
