package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/renderer"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The renderer certification legs: the deterministic markdown renderer is
// pinned byte-for-byte by the shared markdown corpus; the emitted fuaran-*
// class vocabulary is parity-locked to the reference renderer source; and the
// shipped reference CSS must be a byte-copy of the canonical artefact. The
// reference-source legs skip when the canonical sibling is not checked out
// alongside, mirroring the corpus skip.

type markdownFixture struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Source      string `json:"source"`
	HTML        string `json:"html"`
}

type markdownCorpus struct {
	Version  int               `json:"version"`
	Fixtures []markdownFixture `json:"fixtures"`
}

// TestMarkdownCorpus pins the GFM renderer to the shared corpus: a one-byte
// divergence from the reference render turns this leg red.
func TestMarkdownCorpus(t *testing.T) {
	corpus, _ := loadCorpus(t)
	raw, err := os.ReadFile(filepath.Join(corpus, "markdown", "corpus.json"))
	if err != nil {
		t.Skipf("markdown corpus not found: %v", err)
	}
	var m markdownCorpus
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing markdown corpus: %v", err)
	}
	if len(m.Fixtures) == 0 {
		t.Fatal("markdown corpus declares no fixtures")
	}
	for _, fx := range m.Fixtures {
		t.Run(fx.ID, func(t *testing.T) {
			got := renderer.MarkdownToHTML(fx.Source)
			if got != fx.HTML {
				t.Errorf("render diverged: %s", firstDiff(got, fx.HTML))
			}
		})
	}
}

// referenceRendererFiles are the canonical renderer sources the class
// vocabulary is extracted from (the parity oracle).
func referenceRendererFiles(corpus string) []string {
	estate := filepath.Dir(corpus)
	return []string{
		filepath.Join(estate, "fuaran", "src", "Fuaran.UI.Renderer.Server", "Render.fs"),
		filepath.Join(estate, "fuaran", "src", "Fuaran.UI.Renderer", "Render.fs"),
		filepath.Join(estate, "fuaran", "src", "Fuaran.UI.Renderer.Core", "Theme.fs"),
		// The canonical inline-SVG builder — the source of the fuaran-drawing*
		// class vocabulary (both F# renderers call into it).
		filepath.Join(estate, "fuaran", "src", "Fuaran.UI.Renderer.Core", "DrawingSvg.fs"),
	}
}

var classTokenRe = regexp.MustCompile(`fuaran-[a-zA-Z0-9-]*`)

// referenceVocabulary returns (exact, prefixes): a token ending in '-' is a
// composition prefix (fuaran-metric- styles fuaran-metric-brand); the rest
// are exact class literals.
func referenceVocabulary(t *testing.T, corpus string) (map[string]bool, []string) {
	t.Helper()
	exact := make(map[string]bool)
	var prefixes []string
	for _, path := range referenceRendererFiles(corpus) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("reference renderer source not found alongside the repo: %v", err)
		}
		for _, token := range classTokenRe.FindAllString(string(raw), -1) {
			if strings.HasSuffix(token, "-") {
				prefixes = append(prefixes, token)
			} else {
				exact[token] = true
			}
		}
	}
	return exact, prefixes
}

var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)

func emittedClasses(html string) map[string]bool {
	out := make(map[string]bool)
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, tok := range strings.Fields(m[1]) {
			if strings.HasPrefix(tok, "fuaran-") {
				out[tok] = true
			}
		}
	}
	return out
}

// TestClassVocabularyParity renders every node round-trip fixture and asserts
// every emitted fuaran-* class is in the reference renderer's vocabulary —
// the cross-host parity lock, the rendering analogue of the wire corpus.
func TestClassVocabularyParity(t *testing.T) {
	corpus, m := loadCorpus(t)
	exact, prefixes := referenceVocabulary(t, corpus)
	// Guard against an extraction regression silently emptying the oracle.
	if len(exact) <= 50 || !exact["fuaran-node"] {
		t.Fatalf("reference vocabulary extraction looks broken: %d exact classes", len(exact))
	}

	inVocab := func(cls string) bool {
		if exact[cls] {
			return true
		}
		for _, p := range prefixes {
			if strings.HasPrefix(cls, p) {
				return true
			}
		}
		return false
	}

	for _, fx := range m.Fixtures {
		if fx.Kind != "node-round-trip" {
			continue
		}
		t.Run(fx.ID, func(t *testing.T) {
			node, err := wire.DecodeNode(readFixture(t, corpus, fx.InputFile))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			html := renderer.RenderHTML(node, nil)
			for cls := range emittedClasses(html) {
				if !inVocab(cls) {
					t.Errorf("emitted class %q is not in the reference renderer vocabulary", cls)
				}
			}
		})
	}
}

// TestReferenceCSSByteParity asserts the shipped reference stylesheet is a
// byte-copy of the canonical artefact (skips on a standalone checkout).
func TestReferenceCSSByteParity(t *testing.T) {
	corpus, _ := loadCorpus(t)
	canonical := filepath.Join(filepath.Dir(corpus),
		"fuaran", "src", "Fuaran.UI.Renderer", "content", "fuaran-reference.css")
	raw, err := os.ReadFile(canonical)
	if err != nil {
		t.Skipf("canonical reference CSS not found alongside the repo: %v", err)
	}
	if renderer.ReferenceCSS() != string(raw) {
		t.Error("renderer/content/fuaran-reference.css has drifted from the canonical stylesheet — re-copy it byte-for-byte")
	}
}
