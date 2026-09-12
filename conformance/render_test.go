package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
				html = renderHTML(t, node, renderer.BindingSources{})
			} else {
				html = renderHTMLWithEgress(t, node, renderer.BindingSources{}, egressPolicyFor(t, fx.Policy))
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
			html := renderHTML(t, node, renderer.BindingSources{})
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
			widened := renderHTMLWithEgress(t, node, renderer.BindingSources{}, renderer.PermissiveEgress())
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

// referenceRendererProjectPrefix names the reference host's renderer projects.
// Every .fs file inside one is a place the reference may spell an emitted class.
const referenceRendererProjectPrefix = "Fuaran.UI.Renderer"

// referenceRendererSourceFloor is the list this extraction used to BE, kept as
// a floor rather than as the list. A glob that silently stops matching — a
// project renamed, an src/ layout change — otherwise reports an empty
// vocabulary as a clean run, which is the same "stale but valid-looking" shape
// the derivation replaces.
var referenceRendererSourceFloor = []string{
	"Fuaran.UI.Renderer.Server/Render.fs",
	"Fuaran.UI.Renderer/Render.fs",
	"Fuaran.UI.Renderer.Core/Theme.fs",
	"Fuaran.UI.Renderer.Core/DrawingSvg.fs",
	"Fuaran.UI.Renderer.Core/Css.fs",
	// Named by the 2026-09-02 diagnosis as two the literal list never had:
	// "fuaran-custom-%s-%s" is composed across these, which is the prefix the
	// Python host's guard asserts.
	"Fuaran.UI.Renderer/Runtime.fs",
	"Fuaran.UI.Renderer.Server/Registry.fs",
}

// fsSourcesUnder collects every .fs file under dir, recursively, skipping build
// output. Sorted, so the derived vocabulary does not depend on walk order.
func fsSourcesUnder(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == "obj" || info.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(info.Name(), ".fs") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// referenceRendererFiles are the canonical renderer sources the class
// vocabulary is extracted from (the parity oracle) — DERIVED, not listed.
//
// This was a hand-maintained five-file literal, and the same literal in three
// translations (this host, the Rust host, the Python host). The reference then
// factored its class spellings into helper modules the literal did not name,
// and all three hosts went red at once for one reason: not a host defect, and
// not the reference having dropped a spelling, but the oracle having stopped
// looking where the classes live. Css.fs's own header says it exists so an
// inline spelling can no longer drift — so the anti-drift refactor is what
// broke the drift detector.
//
// Appending the missing filenames would have fixed the instance and reset the
// clock. Deriving removes the class: a new helper module inside a renderer
// project is in the set the moment it exists.
//
// Where the derivation can still be wrong is that it scopes to the renderer
// PROJECTS; explainAbsentClass below searches the whole reference src/ tree for
// an offending class and names the file that spells it, so a failure says which
// of the two defects it is.
func referenceRendererFiles(t *testing.T, corpus string) []string {
	t.Helper()
	root := referenceHostRoot(t, corpus)
	src := filepath.Join(root, "src")

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("the reference host was located at %s but its src/ could not be read: %v", root, err)
	}
	var projects []string
	for _, e := range entries {
		// `*.Tests` projects are EXCLUDED (Phase 1677, aligning with the Python
		// host, which excluded them from the start). A test's expectation string
		// is not the reference's own spelling, so admitting one lets this oracle
		// be satisfied by an assertion about the very drift it is checking for.
		// It was not hypothetical: the reference's renderer test projects spell
		// `fuaran-image-aspect-` as a bare trailing-dash token, which admitted it
		// as a composition PREFIX here — so this host could have emitted an
		// invented `fuaran-image-aspect-cinemascope` and passed, while the
		// production renderer spells only four closed variants.
		if e.IsDir() && strings.HasPrefix(e.Name(), referenceRendererProjectPrefix) &&
			!strings.HasSuffix(e.Name(), ".Tests") {
			projects = append(projects, filepath.Join(src, e.Name()))
		}
	}
	sort.Strings(projects)

	var files []string
	for _, project := range projects {
		files = append(files, fsSourcesUnder(project)...)
	}

	present := make(map[string]bool, len(files))
	for _, f := range files {
		if rel, err := filepath.Rel(src, f); err == nil {
			present[filepath.ToSlash(rel)] = true
		}
	}
	var missing []string
	for _, f := range referenceRendererSourceFloor {
		if !present[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the derived renderer-source set is missing files the hand-maintained list named: %v. "+
			"Either the reference host's layout moved (update referenceRendererProjectPrefix and the floor "+
			"together, deliberately) or this glob matched the wrong tree — it scanned %d project(s) under %s. "+
			"A silently-empty derivation reports a clean run, which is the failure this floor exists to make loud.",
			missing, len(projects), src)
	}
	return files
}

// explainAbsentClass answers, for a class this host emits that the derived
// vocabulary lacks: does the reference spell it ANYWHERE under src/, and where?
//
// A non-empty answer means the derivation did not reach that file — fix the
// derivation. An empty one means the reference genuinely does not spell the
// class — fix this host, and do NOT relax the assertion, which is the branch
// the 2026-09-02 first reading took wrongly.
func explainAbsentClass(corpus string, root string, class string) string {
	src := filepath.Join(root, "src")
	for _, path := range fsSourcesUnder(src) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, tok := range classTokenRe.FindAllString(string(raw), -1) {
			if tok == class {
				rel, err := filepath.Rel(src, path)
				if err != nil {
					rel = path
				}
				return filepath.ToSlash(rel)
			}
		}
	}
	return ""
}

// describeOffender renders one offending class with the completeness verdict
// attached, so the failure says which of the two defects it is.
func describeOffender(corpus string, root string, class string) string {
	if file := explainAbsentClass(corpus, root, class); file != "" {
		return fmt.Sprintf("emitted class %q is absent from the derived reference vocabulary, but the reference DOES "+
			"spell it in src/%s — the derived renderer-source set does not reach that file, so fix the DERIVATION", class, file)
	}
	return fmt.Sprintf("emitted class %q is absent from the reference renderer vocabulary and the reference spells it "+
		"nowhere under src/ — fix THIS HOST's spelling (do not relax the assertion)", class)
}

var (
	fsBlockCommentRe = regexp.MustCompile(`(?s)\(\*.*?\*\)`)
	fsLineCommentRe  = regexp.MustCompile(`(?m)//.*$`)
)

// stripFSharpComments drops F# comments before class tokens are extracted
// (Phase 1677, aligning with the Python host).
//
// This is not tidiness. The reference's doc comments legitimately contain PROSE
// about the vocabulary — a markup example spelling `class="fuaran-icon
// fuaran-{kind}-icon"`, a sentence about "every `fuaran-`-shaped token" — and
// four such comments yielded composition prefixes (`fuaran-drawing-`,
// `fuaran-heading-`, `fuaran-math-`, `fuaran-modal-`) that the production
// renderer does not spell. A prefix admitted from prose widens what this host
// may emit without any reference code having said so, which is the same class
// of vacuity the bare-namespace guard below refuses one step further along.
func stripFSharpComments(text string) string {
	return fsLineCommentRe.ReplaceAllString(fsBlockCommentRe.ReplaceAllString(text, ""), "")
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
		for _, token := range classTokenRe.FindAllString(stripFSharpComments(string(raw)), -1) {
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
			html := renderHTML(t, node, renderer.BindingSources{})
			for cls := range emittedClasses(html) {
				checked++
				if !inVocab(cls) {
					t.Error(describeOffender(corpus, referenceHostRoot(t, corpus), cls))
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

// TestReferenceSourceSetIsDerivedRatherThanListed is the completeness property
// the 2026-09-02 diagnosis asked for: not "is every listed file present" (which
// passed while the list was two files short) but "does the derivation reach past
// the list at all, and does it reach the files the drift was hiding in".
func TestReferenceSourceSetIsDerivedRatherThanListed(t *testing.T) {
	corpus, _ := loadCorpus(t)
	root := referenceHostRoot(t, corpus)
	src := filepath.Join(root, "src")
	files := referenceRendererFiles(t, corpus)

	var relative []string
	for _, f := range files {
		if rel, err := filepath.Rel(src, f); err == nil {
			relative = append(relative, filepath.ToSlash(rel))
		}
	}

	// The floor is asserted inside the derivation; what this adds is that the
	// derivation is not merely REPRODUCING the floor. A glob narrowed until it
	// matched exactly the hand-listed files would satisfy the floor and have
	// re-created the defect.
	if len(relative) <= len(referenceRendererSourceFloor) {
		t.Fatalf("the derived set (%d) is no larger than the hand-maintained floor (%d) — the derivation is not "+
			"reaching past the list it replaced:\n  %s", len(relative), len(referenceRendererSourceFloor),
			strings.Join(relative, "\n  "))
	}

	present := make(map[string]bool, len(relative))
	for _, r := range relative {
		present[r] = true
	}
	// The two the 2026-09-02 diagnosis named as never having been in the list.
	for _, f := range []string{"Fuaran.UI.Renderer/Runtime.fs", "Fuaran.UI.Renderer.Server/Registry.fs"} {
		if !present[f] {
			t.Errorf("the derivation does not reach %s, the file that composes `fuaran-custom-`", f)
		}
	}
	t.Logf("reference renderer sources DERIVED: %d files under %s", len(relative), src)
}

// TestOffenderExplanationDistinguishesItsTwoBranches verifies the probe rather
// than the verdict: describeOffender is only useful if it can tell its two
// branches apart, and both are unreachable in a green run.
func TestOffenderExplanationDistinguishesItsTwoBranches(t *testing.T) {
	corpus, _ := loadCorpus(t)
	root := referenceHostRoot(t, corpus)

	// A class the reference spells OUTSIDE the renderer projects
	// (Fuaran.UI/Defaults.fs): the derivation-gap branch.
	gap := describeOffender(corpus, root, "fuaran-error-boundary-placeholder")
	if !strings.Contains(gap, "fix the DERIVATION") {
		t.Errorf("a class the reference spells outside the renderer projects must be reported as a derivation gap: %s", gap)
	}

	// A class nothing spells anywhere: the host-defect branch.
	defect := describeOffender(corpus, root, "fuaran-not-a-real-class-1653")
	if !strings.Contains(defect, "fix THIS HOST") {
		t.Errorf("a class the reference spells nowhere must be reported as a host defect: %s", defect)
	}
}

// TestReferenceVocabularyAdmitsNoDegeneratePrefix asks the question every other
// guard around the oracle cannot (Phase 1677, porting the Python host's
// test_reference_vocabulary_admits_no_degenerate_prefix): not "is the oracle
// big enough" but "can the oracle still say no".
//
// Every neighbouring guard measures the vocabulary's SIZE — ">50 exact classes",
// "fuaran-node is present", "the derived set is larger than the floor" — and a
// vocabulary can be large, correct in every named member, and still admit
// every possible input, because one over-broad prefix subsumes the lot. Two
// such prefixes have reached this oracle already: the bare namespace, from a
// doc-comment sentence (dropped at extraction, see referenceVocabulary), and
// `fuaran-image-aspect-`, from a TEST project's expectation string (closed by
// the *.Tests exclusion in referenceRendererFiles). Each was a different way
// in. Stripping comments and excluding tests remove the two known ways; this
// test is what catches the next one, because it checks the property those
// fixes exist to restore rather than the fixes themselves.
func TestReferenceVocabularyAdmitsNoDegeneratePrefix(t *testing.T) {
	corpus, _ := loadCorpus(t)
	exact, prefixes := referenceVocabulary(t, corpus)

	// A prefix that names nothing beyond the namespace admits every class this
	// host can emit, by construction. The extraction drops the bare token, so
	// this half is enforced twice — deliberately: a future change to the
	// extraction must not be able to reintroduce it silently.
	for _, p := range prefixes {
		if p == classPrefixNamespace || !strings.HasPrefix(p, classPrefixNamespace) || len(p) <= len(classPrefixNamespace) {
			t.Errorf("the extracted prefix set contains %q, which admits every class this host can emit — the parity "+
				"assertion is a tautology while it is there. It comes from prose (a doc-comment markup example, or a "+
				"sentence about the vocabulary) leaking into the extraction; check stripFSharpComments still covers "+
				"the comment form the reference used.", p)
		}
	}

	// The go-red proof, run in-process: a parity lock that has silently gone
	// vacuous looks exactly like one that is passing, so the falsifier is worth
	// an assertion of its own rather than a comment claiming the check works.
	// Two probes: an invented class no reference file could plausibly spell,
	// and the instance this phase measured — the production renderer spells
	// exactly four `fuaran-image-aspect-*` variants (Render.fs), so a fifth is
	// admissible only through a prefix the reference never wrote.
	for _, invented := range []string{
		"fuaran-a-class-the-reference-host-does-not-spell",
		"fuaran-image-aspect-cinemascope",
	} {
		if exact[invented] {
			t.Errorf("the oracle admits %q as an EXACT class, which no reference source spells", invented)
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(invented, p) {
				t.Errorf("the oracle admits the invented class %q through the prefix %q, so it admits anything "+
					"under that prefix — find where the reference spells that prefix and whether it is code or prose", invented, p)
			}
		}
	}
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

// ── Test helpers for the Phase 1667 error return ────────────────────────────
//
// renderer.RenderHTML / RenderHTMLWithEgress answer (string, error) since Phase
// 1667. These wrap them and FAIL the test on a resolution error rather than
// each site discarding it: no corpus fixture carries a decoded
// Binding.Computed, so a non-nil error here is a regression, not an expectation.

func renderHTML(t *testing.T, node wire.Node, sources renderer.BindingSources) string {
	t.Helper()
	html, err := renderer.RenderHTML(node, sources)
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	return html
}

func renderHTMLWithEgress(t *testing.T, node wire.Node, sources renderer.BindingSources, policy renderer.EgressPolicy) string {
	t.Helper()
	html, err := renderer.RenderHTMLWithEgress(node, sources, policy)
	if err != nil {
		t.Fatalf("RenderHTMLWithEgress: %v", err)
	}
	return html
}
