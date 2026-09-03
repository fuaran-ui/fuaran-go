// Executable render-obligation conformance (WIRE_FORMAT.md §13) — this host's
// adoption. The sibling of the reference host's suite, in Go.
//
// Codec conformance is byte-parity and strong. Render obligations were prose:
// §3.6.5 and §3.6.6 state, in sentences, that an accessible name is always
// emitted, that `autoplay` never appears without `muted`, that an audio
// transport has no autoplay pathway at all, that a refused source emits no
// affordance. A host can pass every fixture in the corpus and silently fail
// every one of those — none is a missing discriminator arm, so no codec test
// and, in a language without sum types, certainly no compiler reaches them.
//
// So the manifest carries them now, and this suite asserts FROM the manifest
// rather than from a hand list beside it. Three consequences, which are the
// whole point:
//
//   - The ENUMERATION is the corpus artefact's. A newly declared obligation on
//     a kind this host renders arrives here as a claim with no checker and
//     turns the suite RED — not as a paragraph a future reader may re-read.
//
//   - NOT CHECKED IS NOT PASSED. Every claim this host does not assert is
//     printed by name with the section that states it, and fails the gate
//     unless it carries a declared exemption. Silence is never an answer.
//
//   - The go-red property is PROVEN. statusOf is exercised against a claim no
//     checker covers and must report it unchecked — the shape a new obligation
//     takes on the day it lands.
//
// Every checker asserts in EMITTED HTML through RenderHTML. A checker that
// inspected the decoded tree would be re-stating the type system; the
// obligations are claims about output.
//
// The assertion bodies are the ones media_test.go has carried since the media
// wave; they moved here and are now reached BY CLAIM, and that file's tests
// delegate to them rather than holding a second copy.

package renderer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── The artefact ────────────────────────────────────────────────────────────

// renderObligation is one checkable claim a kind owes, as the manifest declares
// it (WIRE_FORMAT.md §13).
type renderObligation struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Section   string `json:"section"`
}

// obligationVocabularyEntry is one member of the CLOSED vocabulary the artefact
// enumerates at the top level. Closed is what lets a host report a claim id it
// has not implemented instead of silently accepting one it cannot name.
type obligationVocabularyEntry struct {
	ID      string `json:"id"`
	Meaning string `json:"meaning"`
}

// renderFidelityKind is the subset of a render-fidelity row this suite reads.
// The tier fields are deliberately not modelled: nothing here consumes them,
// and a partial decode of a generated artefact is honest where a partial mirror
// of it would be a second declaration to keep in step.
type renderFidelityKind struct {
	Kind        string             `json:"kind"`
	Obligations []renderObligation `json:"obligations"`
}

type renderFidelityManifest struct {
	ObligationVocabulary []obligationVocabularyEntry `json:"obligationVocabulary"`
	Kinds                []renderFidelityKind        `json:"kinds"`
}

// renderFidelityEnvVar overrides the artefact path. It exists so the go-red
// property can be PROVEN against a perturbed scratch copy — inject a claim no
// checker covers, watch the gate fail by name — without writing to the shared
// corpus, which is an oracle this host reads and never edits.
const renderFidelityEnvVar = "FUARAN_RENDER_FIDELITY"

// findRenderFidelityArtifact resolves the generated manifest: the environment
// override if set, else a walk up from the working directory to the shared
// corpus beside the repo. Returns "" when absent, so the repo stays
// standalone-testable (the posture every other corpus-reading test here takes).
func findRenderFidelityArtifact() string {
	if override := os.Getenv(renderFidelityEnvVar); override != "" {
		return override
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "wire-format-fixtures", "render-fidelity.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadRenderFidelityManifest reads the artefact, skipping on a standalone
// checkout. The skip says what was NOT certified: "nothing to check" must never
// read as "everything checked".
func loadRenderFidelityManifest(t *testing.T) renderFidelityManifest {
	t.Helper()
	path := findRenderFidelityArtifact()
	if path == "" {
		t.Skipf("render-fidelity.json not found alongside the repo and %s is unset; "+
			"NO render obligation was certified in this run (standalone checkout)", renderFidelityEnvVar)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var manifest renderFidelityManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return manifest
}

// ─── The reporting surface (the shared shape, in Go) ─────────────────────────
//
// Declared here so this host answers the same question in the same words as its
// siblings rather than inventing a way to say "we did not check that".

type obligationStatus string

const (
	obligationAsserted    obligationStatus = "asserted"
	obligationUnchecked   obligationStatus = "unchecked"
	obligationNotRendered obligationStatus = "notRendered"
)

// obligationOutcome is a host's answer for one declared obligation.
//
// unchecked is the case the whole mechanism exists for: a host that renders a
// kind and has no checker for one of its claims must say so, WITH a reason —
// not checked is not passed. notRendered is distinct: nothing is owed, rather
// than owed and unpaid.
type obligationOutcome struct {
	Status obligationStatus
	Reason string
}

// obligationReport is one line of a host's obligation report.
type obligationReport struct {
	Kind      string
	ClaimID   string
	Statement string
	Section   string
	Outcome   obligationOutcome
}

type kindObligation struct {
	Kind       string
	Obligation renderObligation
}

// allObligations pairs every declared obligation with the kind that owes it, in
// table order.
func allObligations(m renderFidelityManifest) []kindObligation {
	var all []kindObligation
	for _, row := range m.Kinds {
		for _, o := range row.Obligations {
			all = append(all, kindObligation{Kind: row.Kind, Obligation: o})
		}
	}
	return all
}

// reportObligations projects the manifest through this host's own answer, one
// line per declared obligation. The ENUMERATION is the manifest's, never the
// host's — so a newly declared obligation appears in the report the moment it
// lands rather than when someone remembers it.
func reportObligations(m renderFidelityManifest, statusOf func(kind, claimID string) obligationOutcome) []obligationReport {
	all := allObligations(m)
	report := make([]obligationReport, 0, len(all))
	for _, ko := range all {
		report = append(report, obligationReport{
			Kind:      ko.Kind,
			ClaimID:   ko.Obligation.ID,
			Statement: ko.Obligation.Statement,
			Section:   ko.Obligation.Section,
			Outcome:   statusOf(ko.Kind, ko.Obligation.ID),
		})
	}
	return report
}

// unassertedObligations is the set a host must SURFACE: everything it did not
// assert. Empty is the only silent result — anything else is printed, so an
// unchecked obligation is visible in the run rather than inferable from its
// absence.
func unassertedObligations(report []obligationReport) []obligationReport {
	var unmet []obligationReport
	for _, line := range report {
		if line.Outcome.Status != obligationAsserted {
			unmet = append(unmet, line)
		}
	}
	return unmet
}

// describeObligationReport is the one-line rendering of a report line, so the
// same sentence appears in every host's output.
func describeObligationReport(line obligationReport) string {
	var outcome string
	switch line.Outcome.Status {
	case obligationAsserted:
		outcome = "asserted"
	case obligationUnchecked:
		outcome = fmt.Sprintf("UNCHECKED (%s)", line.Outcome.Reason)
	default:
		outcome = fmt.Sprintf("not rendered (%s)", line.Outcome.Reason)
	}
	return fmt.Sprintf("%s/%s [%s]: %s", line.Kind, line.ClaimID, line.Section, outcome)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// refusedURL is a destination safe by the scheme floor and entirely undeclared,
// so the ambient default-deny egress policy refuses it. This is the input the
// three "refused" obligations are about.
const refusedURL = "https://collector.example/asset.jpg"

func renderJSON(t *testing.T, canonicalJSON string) string {
	t.Helper()
	return RenderHTML(mustDecode(t, canonicalJSON), nil)
}

func mustEmit(t *testing.T, html, needle, why string) {
	t.Helper()
	if !strings.Contains(html, needle) {
		t.Errorf("%s\nwanted: %s\ngot:\n%s", why, needle, html)
	}
}

func mustNotEmit(t *testing.T, html, needle, why string) {
	t.Helper()
	if strings.Contains(html, needle) {
		t.Errorf("%s\nmust not contain: %s\ngot:\n%s", why, needle, html)
	}
}

// ─── The checkers ────────────────────────────────────────────────────────────
//
// One per (kind, claim). Each pins BOTH directions where the obligation has
// two: an emission test alone cannot tell a renderer that honours a conditional
// from one that emits unconditionally.

// Media/accessible-name-always (§3.6.6). Both variants, because the label is
// mandatory for the KIND and not for one arm of it — a renderer emitting it
// only on <video> passes a video-only test.
func checkMediaAccessibleNameAlways(t *testing.T) {
	video := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp4"}}}`)
	mustEmit(t, video, `aria-label="Studio walkthrough"`, "a video emits the resolved label as aria-label")

	audio := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio"},"label":"Curator commentary","src":{"$type":"Static","value":"/commentary.mp3"}}}`)
	mustEmit(t, audio, `aria-label="Curator commentary"`, "an audio emits the resolved label as aria-label")
}

// Media/autoplay-muted-pairing (§3.6.6). The pairing is not a default a caller
// overrides; it is what the declaration MEANS, which is why the wire carries no
// separate muted slot to get out of step with it.
func checkMediaAutoplayMutedPairing(t *testing.T) {
	on := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video","autoplay":true},"label":"Ambient loop","src":{"$type":"Static","value":"/ambient.mp4"}}}`)
	mustEmit(t, on, ` autoplay=""`, "a declared autoplay is emitted")
	mustEmit(t, on, ` muted=""`, "…and never without muted — an unmuted autoplay is blocked and means nothing")

	// The pairing runs one way, and this is the half a one-sided assertion
	// misses: `muted` unasked silences a video the reader started themselves.
	off := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp4"}}}`)
	mustNotEmit(t, off, ` autoplay=""`, "autoplay is not declared, so it must not be emitted")
	mustNotEmit(t, off, ` muted=""`, "muted rides autoplay; unasked it is a behaviour change, not a default")
}

// Media/no-autoplay-pathway (§3.6.6). Audio has NO autoplay pathway: in the
// type, on the wire, or in the emission. The case carries no slot to read, so a
// document that names one decodes to an audio surface that does not autoplay.
func checkMediaNoAutoplayPathway(t *testing.T) {
	html := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio","autoplay":true},"label":"Curator commentary","src":{"$type":"Static","value":"/commentary.mp3"}}}`)
	mustNotEmit(t, html, "autoplay", "an <audio> must never carry an autoplay attribute")
	mustNotEmit(t, html, "muted", "an <audio> has no autoplay, so it has nothing to mute")
}

// Media/refused-source-dropped (§3.6.6). A <video> with no poster shows its
// first frame; a poster at the refusal URL is a broken image over the player.
func checkMediaRefusedSourceDropped(t *testing.T) {
	refused := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video","poster":{"$type":"Static","value":"`+refusedURL+`"}},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp4"}}}`)
	mustNotEmit(t, refused, "collector.example", "a refused poster's destination is never emitted")
	mustNotEmit(t, refused, "poster=", "a refused poster is DROPPED, not emitted at the refusal URL")
	mustEmit(t, refused, `src="/walkthrough.mp4"`, "dropping the poster must not disturb the source")

	// The allow twin. Without it a renderer that dropped EVERY poster would
	// pass the refusal assertion and this obligation would guard nothing.
	allowed := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video","poster":{"$type":"Static","value":"/walkthrough-poster.jpg"}},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp4"}}}`)
	mustEmit(t, allowed, `poster="/walkthrough-poster.jpg"`, "a local poster still renders")
}

// Media/authored-child-order (§3.6.6). The tracks are emitted in the order the
// array carries them, never re-sorted — the OPPOSITE of §3.6.4's srcSet rule,
// because a reader picks a track from a menu the user agent builds in DOCUMENT
// order, so ordering it would be rewriting someone else's menu.
//
// The fixture is authored in an order NO sort produces (gd, then two en), which
// is what makes this separately testable from the srcSet rule: a renderer that
// sorted by srclang, by label, or by kind would fail here and pass every other
// media assertion.
func checkMediaAuthoredChildOrder(t *testing.T) {
	html := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Harbour restoration","src":{"$type":"Static","value":"/restoration-2.mp4"},"tracks":[{"kind":"Subtitles","label":"Gàidhlig","src":{"$type":"Static","value":"/restoration-2.gd.vtt"},"srcLang":"gd"},{"default":true,"kind":"Captions","label":"English captions","src":{"$type":"Static","value":"/restoration-2.en.vtt"},"srcLang":"en"}]}}`)

	// Asserted as ONE contiguous string, not as two independent Contains: two
	// membership checks pass under either ordering, which is precisely the
	// defect this obligation is about.
	mustEmit(t, html,
		`<track kind="subtitles" src="/restoration-2.gd.vtt" srclang="gd" label="Gàidhlig" /><track kind="captions" src="/restoration-2.en.vtt" srclang="en" label="English captions" default="" />`,
		"the tracks were not emitted in AUTHORED order")
}

// Media/single-default-per-kind (§3.6.6). Two default captions tracks are legal
// BYTES — the decoder does not refuse them — so the host resolves the election,
// and every host resolves it the same way: FIRST election of a kind wins.
//
// Three legs, and the second and third are what make it an obligation rather
// than a spelling. The later track is still EMITTED (only its claim on the menu
// is dropped), and the election is PER KIND, so a subtitles default coexists
// with a captions one — a renderer that kept a single global "seen a default"
// flag passes the first leg and fails the third.
func checkMediaSingleDefaultPerKind(t *testing.T) {
	html := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Harbour restoration","src":{"$type":"Static","value":"/r.mp4"},"tracks":[{"default":true,"kind":"Captions","label":"English captions","src":{"$type":"Static","value":"/r.en.vtt"},"srcLang":"en"},{"default":true,"kind":"Captions","label":"English captions (verbose)","src":{"$type":"Static","value":"/r.en-verbose.vtt"},"srcLang":"en"},{"default":true,"kind":"Subtitles","label":"Gàidhlig","src":{"$type":"Static","value":"/r.gd.vtt"},"srcLang":"gd"}]}}`)

	mustEmit(t, html,
		`<track kind="captions" src="/r.en.vtt" srclang="en" label="English captions" default="" />`,
		"the FIRST election of a kind is honoured")
	// Still emitted, without the attribute — the track keeps its place in the
	// menu and loses only its claim on it.
	mustEmit(t, html,
		`<track kind="captions" src="/r.en-verbose.vtt" srclang="en" label="English captions (verbose)" />`,
		"the second election of the SAME kind is emitted WITHOUT default, not dropped")
	mustEmit(t, html,
		`<track kind="subtitles" src="/r.gd.vtt" srclang="gd" label="Gàidhlig" default="" />`,
		"the election is PER KIND — a subtitles default coexists with a captions one")

	// A refused track is DROPPED (obligation 4), and dropping it must not spend
	// the kind's election: without this leg a renderer that claimed the slot
	// before checking egress would leave the surviving track undefaulted.
	refused := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Harbour","src":{"$type":"Static","value":"/r.mp4"},"tracks":[{"default":true,"kind":"Captions","label":"Remote","src":{"$type":"Static","value":"`+refusedURL+`"},"srcLang":"en"},{"default":true,"kind":"Captions","label":"Local","src":{"$type":"Static","value":"/r.en.vtt"},"srcLang":"en"}]}}`)
	mustNotEmit(t, refused, "collector.example", "a refused track's destination is never emitted")
	mustNotEmit(t, refused, EgressRefusalURL, "a refused track is DROPPED, never emitted in neutered form")
	mustEmit(t, refused,
		`<track kind="captions" src="/r.en.vtt" srclang="en" label="Local" default="" />`,
		"a dropped track forfeits its election — the surviving track takes it")
}

// Media/transcript-disclosure-named (§3.6.6). The transcript renders as a
// disclosure BESIDE the transport, never inside it: <video> and <audio> admit
// only source-ish children, so a transcript placed there would be fallback
// content a browser never shows.
//
// The disclosure carries the MEDIA's resolved label as its own accessible name,
// so a reader meeting it out of context is told which recording it transcribes.
func checkMediaTranscriptDisclosureNamed(t *testing.T) {
	html := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio"},"label":"Curator's commentary","src":{"$type":"Static","value":"/commentary.mp3"},"transcript":"The harbour was rebuilt twice."}}`)

	// The element ORDER is pinned, not merely both elements' presence: a
	// transcript emitted as a CHILD of the <audio> would satisfy two Contains
	// checks and be invisible in every browser.
	mustEmit(t, html, `</audio><details class="fuaran-media-transcript"`,
		"the disclosure is a SIBLING of the transport, after it — never a child")
	mustEmit(t, html, `<details class="fuaran-media-transcript" aria-label="Curator&#x27;s commentary">`,
		"the disclosure carries the MEDIA's resolved label as its accessible name")
	mustEmit(t, html, `<summary class="fuaran-media-transcript-summary">Transcript</summary>`,
		"the summary is renderer chrome")
	mustEmit(t, html, `<div class="fuaran-media-transcript-body">The harbour was rebuilt twice.</div>`,
		"the transcript body is the document's own text")
	mustEmit(t, html, `<div class="fuaran-media-group">`,
		"a present transcript is the one case the emission gains a wrapper")

	// The absent twin. Without it a renderer that wrapped EVERY media element
	// would pass every assertion above and this obligation would guard nothing.
	bare := renderJSON(t, `{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio"},"label":"Curator's commentary","src":{"$type":"Static","value":"/commentary.mp3"}}}`)
	mustNotEmit(t, bare, "fuaran-media-group", "absent, the emission is the bare element it would otherwise be")
	mustNotEmit(t, bare, "fuaran-media-transcript", "…and no disclosure at all")
}

// Embed/accessible-name-always (§3.6.8). The title is mandatory on the wire and
// a browsing context has no decorative case, so the attribute is emitted always
// rather than where an author supplied one.
func checkEmbedAccessibleNameAlways(t *testing.T) {
	html := renderJSON(t, `{"id":"e","kind":{"$type":"Embed","src":{"$type":"Static","value":"https://player.example/embed/harbour"},"title":"Harbour restoration, part two"}}`)
	mustEmit(t, html, `title="Harbour restoration, part two"`, "the resolved title is emitted as the frame's title")

	// The name survives the case where the SOURCE was refused: a frame with no
	// src is still a frame a reader tabs into, and it is worse, not better, to
	// leave it unnamed.
	refused := renderJSON(t, `{"id":"e","kind":{"$type":"Embed","src":{"$type":"Static","value":"http://player.example/embed/harbour"},"title":"Harbour restoration, part two"}}`)
	mustEmit(t, refused, `title="Harbour restoration, part two"`, "a refused source does not cost the frame its name")
}

// Embed/sandbox-always-exactly-declared (§3.6.8).
//
// Four legs. The EMPTY case is the one the obligation exists for — omitting the
// attribute on a permissionless embed produces the same markup as an unsandboxed
// frame — and the fullscreen case is the one the corpus fixture was chosen to
// catch: a host that mapped the whole enum onto sandbox tokens passes every
// other fixture and fails here.
func checkEmbedSandboxAlwaysExactlyDeclared(t *testing.T) {
	empty := renderJSON(t, `{"id":"e","kind":{"$type":"Embed","src":{"$type":"Static","value":"https://player.example/e"},"title":"T"}}`)
	mustEmit(t, empty, ` sandbox=""`, "sandbox is emitted on EVERY embed, and EMPTY is the maximally-restrictive value")
	mustNotEmit(t, empty, ` allow=`, "an empty `allow` is not the same statement as an absent one")

	// Authored in REVERSE vocabulary order, so the assertion pins the render's
	// own ordering rather than the document's: a host echoing authored order
	// emits the same three tokens and fails here.
	ordered := renderJSON(t, `{"id":"e","kind":{"$type":"Embed","permissions":["AllowForms","AllowSameOrigin","AllowScripts","AllowForms"],"src":{"$type":"Static","value":"https://player.example/e"},"title":"T"}}`)
	mustEmit(t, ordered, ` sandbox="allow-scripts allow-same-origin allow-forms"`,
		"the tokens are emitted in VOCABULARY DECLARATION order, de-duplicated")

	// AllowFullscreen is NOT a sandbox token — it rides `allow`.
	fullscreen := renderJSON(t, `{"id":"e","kind":{"$type":"Embed","permissions":["AllowFullscreen"],"src":{"$type":"Static","value":"https://player.example/e"},"title":"T"}}`)
	mustEmit(t, fullscreen, ` sandbox=""`, "fullscreen grants NO sandbox relaxation, so the attribute stays empty")
	mustEmit(t, fullscreen, ` allow="fullscreen"`, "…it is a permissions-policy directive riding `allow`")
	mustNotEmit(t, fullscreen, "allow-fullscreen", "and never a sandbox token of that name")

	// The unconditional pair, stated here because there is deliberately no slot
	// for either and nothing else would notice their loss.
	mustEmit(t, empty, ` loading="lazy"`, "loading=lazy is unconditional")
	mustEmit(t, empty, ` referrerpolicy="strict-origin-when-cross-origin"`,
		"the referrer policy is conservative and deliberately not no-referrer")
}

// Embed/refused-embed-source-omitted (§19.1). The ONE place a refusal does not
// take §19 rule 6's substitute route: an <iframe> pointed at a refusal URL
// RENDERS that page, where one with no src is a well-defined empty browsing
// context that fetches nothing.
//
// Three refusal classes, because §19.1's floor is narrower than §19's in two
// ways a media-shaped check would miss — `http` and, more sharply, a SCHEMELESS
// reference, which names a same-origin document and is exactly the shape where a
// frame granted both AllowSameOrigin and AllowScripts can reach its own frame
// ELEMENT and remove the sandbox attribute from it.
func checkEmbedRefusedSourceOmitted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"http", "http://player.example/embed/harbour"},
		{"schemeless", "/local/embed.html"},
		{"undeclared https origin", refusedURL},
	} {
		html := renderJSON(t, `{"id":"e","kind":{"$type":"Embed","src":{"$type":"Static","value":"`+tc.src+`"},"title":"T"}}`)
		mustNotEmit(t, html, ` src=`, tc.name+": a refused source OMITS the attribute entirely")
		mustNotEmit(t, html, EgressRefusalURL, tc.name+": …and never substitutes the refusal URL")
		mustNotEmit(t, html, tc.src, tc.name+": …and never emits the original value")
		mustEmit(t, html, EgressRefusalAttribute, tc.name+": the refusal is still RECORDED as a data attribute")
	}

	// The allow twin — without it a renderer that omitted EVERY src would pass
	// every assertion above and this obligation would guard a worse bug than the
	// one it exists for. "Nothing was declared" and "this was refused" must also
	// stay different facts, so an ALLOWED source carries no refusal marker.
	allowed := RenderHTMLWithEgress(
		mustDecode(t, `{"id":"e","kind":{"$type":"Embed","src":{"$type":"Static","value":"https://player.example/embed/harbour"},"title":"T"}}`),
		nil, PermissiveEgress())
	mustEmit(t, allowed, ` src="https://player.example/embed/harbour"`, "an allowed https source still renders")
	mustNotEmit(t, allowed, EgressRefusalAttribute, "…and carries no refusal marker")
}

// Tree/accessible-name-always (§3.6.12 obligation 5). A treeitem OWNS its child
// group, so a name COMPUTED from contents reads the whole branch out as the
// row's own name. The name is therefore STATED, and it is the row's own visible
// label.
//
// Asserted on a PARENT and on a NESTED row, and the parent's own group is
// asserted to exist: a renderer that named only leaves — or that emitted no
// role="group" at all, so no row ever owned a subtree — would pass a
// leaf-only check while leaving exactly the rows the obligation is about unnamed.
func checkTreeAccessibleNameAlways(t *testing.T) {
	html := renderJSON(t, `{"id":"t","kind":{"$type":"Tree","items":[{"children":[{"id":"cocoa","label":"Cocoa"}],"id":"goods","label":"Goods"}]}}`)

	mustEmit(t, html, `aria-label="Goods"`, "a PARENT row states its own visible label as its accessible name")
	mustEmit(t, html, `aria-label="Cocoa"`, "…and so does a NESTED row")
	mustEmit(t, html, `role="group"`, "the parent genuinely owns a child group — the twin without which naming only leaves would pass")
	mustEmit(t, html, `<span class="fuaran-tree-label">Goods</span>`,
		"the stated name is byte-identical to the visible label, so `label in name` holds")
}

// Image/alt-always-emitted (§3.6.2). The decorative case is the one that
// matters: an omitted `alt` and an empty one are different claims to assistive
// technology — omitted means "nobody said", empty means "this is decorative,
// skip it".
func checkImageAltAlwaysEmitted(t *testing.T) {
	named := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats moored at first light","src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`)
	mustEmit(t, named, `alt="Fishing boats moored at first light"`, "the alt text is emitted")

	decorative := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"","src":{"$type":"Static","value":"/rule.png"},"variant":"Default"}}`)
	mustEmit(t, decorative, `alt=""`, "a decorative image emits an EMPTY alt, never no alt at all")
}

// Image/anchor-affordance-on-expandable (§3.6.5). The ELEMENT is pinned, not
// only the class: the whole no-JS claim is that this is an <a href>, and a
// <span class="fuaran-image-expand"> carrying the data attribute would pass a
// class-only assertion while giving a scriptless reader nothing.
func checkImageAnchorAffordanceOnExpandable(t *testing.T) {
	html := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","expandable":true,"src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`)
	mustEmit(t, html,
		`<a class="fuaran-image-expand" href="/harbour.jpg" data-fuaran-expandable=""><img `,
		"expandable did not emit a real, navigable anchor to the asset the image already names")
	mustNotEmit(t, html, "onclick", "nothing crosses the dispatch gate — the anchor is the whole no-JS story")

	notExpandable := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`)
	mustNotEmit(t, notExpandable, "fuaran-image-expand", "an undeclared expansion emits no anchor")
}

// Image/refused-src-no-affordance (§3.6.5). An affordance that cannot be
// honoured is worse than none.
func checkImageRefusedSrcNoAffordance(t *testing.T) {
	html := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","expandable":true,"src":{"$type":"Static","value":"`+refusedURL+`"},"variant":"Default"}}`)
	mustNotEmit(t, html, "fuaran-image-expand", "a src the egress floor refused must emit NO expand anchor")
	mustNotEmit(t, html, "data-fuaran-expandable", "…and no marker for an enhancement tier to find either")

	// The image itself still renders, at the refusal URL. Without this leg a
	// renderer that dropped the whole node would pass the assertion above, and
	// this obligation would be satisfied by a worse bug than the one it guards.
	mustEmit(t, html, `src="`+EgressRefusalURL+`"`, "the img is still emitted, with the marked refusal URL as its src")
	mustNotEmit(t, html, `href="`+refusedURL, "and the refused destination never becomes a navigable href")
}

// Image/figure-caption-outside-link (§3.6.3, §3.6.5). Asserting the two opening
// tags IN ORDER is what catches the inversion (anchor outside figure), which
// would carry every one of the same classes.
func checkImageFigureCaptionOutsideLink(t *testing.T) {
	html := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","caption":"The harbour at dawn","expandable":true,"src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`)
	mustEmit(t, html,
		`<figure class="fuaran-image-figure"><a class="fuaran-image-expand" href="/harbour.jpg" data-fuaran-expandable="">`,
		"the figure wraps the anchor, not the other way round")
	mustEmit(t, html,
		`</a><figcaption class="fuaran-image-figure-caption">The harbour at dawn</figcaption></figure>`,
		"the figcaption is the anchor's SIBLING — the caption is prose a reader quotes, not a second click surface")
}

// Image/srcset-ascending-by-width (§3.6.4). Authored DESCENDING, so the
// assertion pins the renderer's SORT and not merely its spelling: a renderer
// emitting authored order would produce a srcset containing all the same URLs
// and fail here.
func checkImageSrcSetAscendingByWidth(t *testing.T) {
	html := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-1600.jpg"},"width":1600},{"src":{"$type":"Static","value":"/harbour-800.jpg"},"width":800},{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400}],"variant":"Default"}}`)
	mustEmit(t, html,
		`srcset="/harbour-400.jpg 400w, /harbour-800.jpg 800w, /harbour-1600.jpg 1600w"`,
		"candidates were not ordered ascending by width")

	// The second half of the same obligation: a refused candidate is DROPPED,
	// so the primary src remains the fallback rather than the list carrying a
	// destination the floor refused.
	withRefused := renderJSON(t, `{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400},{"src":{"$type":"Static","value":"`+refusedURL+`"},"width":1600}],"variant":"Default"}}`)
	mustNotEmit(t, withRefused, "collector.example", "a refused candidate's destination is never emitted")
	mustNotEmit(t, withRefused, EgressRefusalURL+" 1600w", "a failing candidate is DROPPED, never emitted in neutered form")
	mustEmit(t, withRefused, `/harbour-400.jpg 400w`, "…while the candidates that pass the floor still are")
}

// Custom/unregistered-custom-labelled (§25.4).
//
// The claim is CONDITIONAL on a contract card being AVAILABLE. This host ships
// no card reader, so no card is ever available for any identity, and the
// identity-only placeholder is the conformant answer for the uncarded path —
// which is the only path this host has. What is asserted below is exactly that
// path: the placeholder carries the component identity, emits no prop VALUE,
// and invents no description it does not have.
//
// The carded branches (shown-and-unverified, hash-verified, hash-contradicted)
// are out of scope here because this host holds no card reader to reach them.
// This host does NOT thereby claim §25 adoption — that is a separate bar with
// its own §11.0 table, and nothing in this file speaks to it.
func checkCustomUnregisteredLabelled(t *testing.T) {
	html := renderJSON(t, `{"id":"cust","kind":{"$type":"Custom","componentId":"sparkline","moduleId":"analytics","props":{"series":"{\"points\":[1,2,3]}"}}}`)

	mustEmit(t, html, `[fuaran:custom analytics.sparkline]`, "the identity-only placeholder names the component")
	mustEmit(t, html, `data-fuaran-custom-module="analytics"`, "the module identity is machine-readable")
	mustEmit(t, html, `data-fuaran-custom-component="sparkline"`, "…and so is the component identity")

	mustNotEmit(t, html, "data-fuaran-custom-card", "a host with no card reader claims nothing about a card")
	// Never a prop VALUE: this host was not asked to interpret the node's props,
	// and a placeholder that echoed them would be guessing at a shape it cannot
	// describe.
	mustNotEmit(t, html, "points", "no prop value reaches the placeholder")
	mustNotEmit(t, html, "trend line", "and it invents no description it does not have")
}

// ─── The registry ────────────────────────────────────────────────────────────

type obligationChecker struct {
	Key   string
	Check func(*testing.T)
}

// checkers: which (kind, claim) pairs this host asserts, and how. Keyed by the
// claim's WIRE token, because the enumeration it is matched against comes from
// the artefact. A slice rather than a map so the subtests run in a stable
// order.
var checkers = []obligationChecker{
	{"Media/accessible-name-always", checkMediaAccessibleNameAlways},
	{"Media/autoplay-muted-pairing", checkMediaAutoplayMutedPairing},
	{"Media/no-autoplay-pathway", checkMediaNoAutoplayPathway},
	{"Media/refused-source-dropped", checkMediaRefusedSourceDropped},
	{"Media/authored-child-order", checkMediaAuthoredChildOrder},
	{"Media/single-default-per-kind", checkMediaSingleDefaultPerKind},
	{"Media/transcript-disclosure-named", checkMediaTranscriptDisclosureNamed},
	{"Embed/accessible-name-always", checkEmbedAccessibleNameAlways},
	{"Embed/sandbox-always-exactly-declared", checkEmbedSandboxAlwaysExactlyDeclared},
	{"Embed/refused-embed-source-omitted", checkEmbedRefusedSourceOmitted},
	{"Tree/accessible-name-always", checkTreeAccessibleNameAlways},
	{"Image/alt-always-emitted", checkImageAltAlwaysEmitted},
	{"Image/anchor-affordance-on-expandable", checkImageAnchorAffordanceOnExpandable},
	{"Image/refused-src-no-affordance", checkImageRefusedSrcNoAffordance},
	{"Image/figure-caption-outside-link", checkImageFigureCaptionOutsideLink},
	{"Image/srcset-ascending-by-width", checkImageSrcSetAscendingByWidth},
	{"Custom/unregistered-custom-labelled", checkCustomUnregisteredLabelled},
}

func hasChecker(key string) bool {
	for _, c := range checkers {
		if c.Key == key {
			return true
		}
	}
	return false
}

// declaredExemptions: obligations this host declares it does NOT check, each
// with a reason.
//
// EMPTY is the correct state for this host: it renders every kind the manifest
// declares an obligation for, so every declared obligation is one it owes. The
// map exists because the alternative — an unchecked obligation silently absent
// from the registry — is precisely the failure the manifest replaces. A host
// that genuinely cannot check a claim (no player, no network loader, a
// decode-only surface) records it here and its report says so out loud.
var declaredExemptions = map[string]string{}

// statusOf is this host's answer for one declared obligation.
//
// It never returns notRendered today: this host renders Media, Image and Custom,
// which are the three kinds carrying obligations. The arm exists because the
// report shape is shared across the hosts — and because a future obligation on a
// kind this host does NOT render would otherwise be reported as unchecked and go
// red, which is the correct forcing function: someone must then say which of the
// two it is.
func statusOf(kind, claimID string) obligationOutcome {
	key := kind + "/" + claimID
	if hasChecker(key) {
		return obligationOutcome{Status: obligationAsserted}
	}
	if reason, ok := declaredExemptions[key]; ok {
		return obligationOutcome{Status: obligationUnchecked, Reason: reason}
	}
	return obligationOutcome{
		Status: obligationUnchecked,
		Reason: "no checker registered in render_obligations_test.go and no declared exemption — " +
			"add one, or declare why this host cannot check it",
	}
}

// ─── The gate ────────────────────────────────────────────────────────────────

func TestRenderObligationsAreAllAsserted(t *testing.T) {
	manifest := loadRenderFidelityManifest(t)
	report := reportObligations(manifest, statusOf)

	if len(report) == 0 {
		t.Fatal("the manifest declares no obligations at all — either the artefact is stale or this " +
			"suite is reading the wrong file, and either way it is asserting nothing")
	}

	// NOT CHECKED IS NOT PASSED. Everything this host did not assert is printed
	// by name and section before the gate decides, so an exempted claim is
	// visible in the run rather than inferable from its absence.
	unmet := unassertedObligations(report)
	for _, line := range unmet {
		t.Logf("  render obligation not asserted: %s", describeObligationReport(line))
	}

	var undeclared []string
	for _, line := range unmet {
		if _, declared := declaredExemptions[line.Kind+"/"+line.ClaimID]; !declared {
			undeclared = append(undeclared, fmt.Sprintf("%s/%s [%s]", line.Kind, line.ClaimID, line.Section))
		}
	}
	if len(undeclared) > 0 {
		t.Errorf("a render obligation this host owes has no checker: assert it, or add a declared "+
			"exemption saying why this host cannot:\n  %s", strings.Join(undeclared, "\n  "))
	}
}

// ─── The go-red proof ────────────────────────────────────────────────────────

func TestRenderObligationWithNoCheckerIsReportedUnchecked(t *testing.T) {
	// The shape a NEWLY-DECLARED obligation takes on the day it lands: a
	// kind/claim pair the registry does not cover. Without this probe the gate
	// above could be green because the classification never reports anything,
	// which is the completeness check that cannot fail.
	outcome := statusOf("Markdown", "accessible-name-always")
	if outcome.Status != obligationUnchecked {
		t.Fatalf("an unregistered (kind, claim) must be reported UNCHECKED, got %q", outcome.Status)
	}
	if !strings.Contains(outcome.Reason, "no checker registered") {
		t.Errorf("the reason must be in words a reader can act on, got %q", outcome.Reason)
	}

	// …and the gate's own filter must classify it as unasserted, which is what
	// turns the suite red.
	probe := obligationReport{Kind: "Markdown", ClaimID: "accessible-name-always", Section: "probe", Outcome: outcome}
	if got := len(unassertedObligations([]obligationReport{probe})); got != 1 {
		t.Errorf("the probe must survive the unasserted filter, got %d lines", got)
	}
	if want := "Markdown/accessible-name-always [probe]: UNCHECKED"; !strings.Contains(describeObligationReport(probe), want) {
		t.Errorf("the report line must name the claim and its outcome, got %q", describeObligationReport(probe))
	}
}

// ─── The vocabulary seam ─────────────────────────────────────────────────────

func TestRenderObligationClaimIdsResolveAgainstTheClosedVocabulary(t *testing.T) {
	// A row naming a claim the vocabulary omits is unresolvable: a host keying
	// its registry off the vocabulary could never report it, and a host must
	// never accept a claim it cannot name.
	manifest := loadRenderFidelityManifest(t)

	vocabulary := make(map[string]bool, len(manifest.ObligationVocabulary))
	for _, v := range manifest.ObligationVocabulary {
		vocabulary[v.ID] = true
	}
	if len(vocabulary) == 0 {
		t.Fatal("the artefact carries no obligation vocabulary")
	}

	for _, ko := range allObligations(manifest) {
		key := ko.Kind + "/" + ko.Obligation.ID
		if !vocabulary[ko.Obligation.ID] {
			t.Errorf("%s: the closed vocabulary does not carry this claim id", key)
		}
		// An obligation with no section is an assertion about a host's habits
		// rather than about the specification, and is not admissible.
		if !strings.Contains(ko.Obligation.Section, "WIRE_FORMAT.md") {
			t.Errorf("%s: no spec section (got %q)", key, ko.Obligation.Section)
		}
		if ko.Obligation.Statement == "" {
			t.Errorf("%s: no normative statement", key)
		}
	}
}

// ─── The registry is not itself a second source of truth ─────────────────────

func TestRenderObligationCheckersDeclareNoOrphans(t *testing.T) {
	// A checker for a claim no row declares is a stale assertion: it passes
	// forever and guards a contract that has moved, which is exactly the drift
	// the generated artefact exists to remove.
	manifest := loadRenderFidelityManifest(t)

	declared := make(map[string]bool)
	for _, ko := range allObligations(manifest) {
		declared[ko.Kind+"/"+ko.Obligation.ID] = true
	}
	for _, c := range checkers {
		if !declared[c.Key] {
			t.Errorf("checker %q asserts an obligation no manifest row declares — either the row was "+
				"removed or the checker was never declared", c.Key)
		}
	}
}

// ─── The checkers themselves ─────────────────────────────────────────────────
//
// Run by name, so a failing obligation names the claim it broke rather than
// surfacing as one opaque red test.

func TestRenderObligationCheckers(t *testing.T) {
	for _, c := range checkers {
		t.Run(c.Key, c.Check)
	}
}
