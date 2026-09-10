// The render-TEXT conformance family — this host's leg (Phase 1663).
//
// Loads the corpus's render-text.json (the family the manifest's `renderText`
// pointer names) and, for every vector, decodes the named node fixture, finds
// the named node, resolves the named text slot under the vector's PINNED
// sources, and asserts the produced string equals expectedText byte for byte.
//
// THE ENUMERATION IS THE ARTEFACT'S, never a list beside these checkers. A
// vector added upstream arrives here as a claim this host must meet, and a
// vector naming a slot this host has no reader for is REPORTED BY NAME and
// FAILS — not checked is not passed, the posture WIRE_FORMAT.md §13 already
// takes for a render obligation. Without that, a host could pass by reading
// fewer vectors than the corpus declares.
//
// WHY THE SOURCES MUST BE PINNED, restated because it is the point. A
// Binding.Now slot rendered against the machine's own clock has no expected
// text at all, and a Format.Since rendered against it has one that changes every
// second. The instant and the locale are data in the vector, so the comparison
// is a function of the corpus and this host alone.
//
// In package `renderer` rather than in `conformance/` deliberately: the seam
// under test is renderText, the one every rendering surface in this package
// reaches, and asserting through RenderHTML would measure the surrounding markup
// as well as the text.

package renderer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// renderTextSlotReaders is the family's CLOSED slot vocabulary as THIS host
// reads it: the wire kind's tag paired with the field holding the TextSource. A
// vector naming a slot absent from this map fails the run with the slot named —
// the artefact's slotVocabulary is what a reader compares against, and being
// behind it is a fact this host must report rather than skip.
var renderTextSlotReaders = map[string][2]string{
	"Fact.value":    {"Fact", "value"},
	"Markdown.text": {"Markdown", "text"},
}

type renderTextPinnedSources struct {
	Now    string                 `json:"now"`
	Locale string                 `json:"locale"`
	Values map[string]interface{} `json:"values"`
}

type renderTextVector struct {
	ID           string                  `json:"id"`
	Fixture      string                  `json:"fixture"`
	NodeID       string                  `json:"nodeId"`
	Slot         string                  `json:"slot"`
	Sources      renderTextPinnedSources `json:"sources"`
	ExpectedText string                  `json:"expectedText"`
	Description  string                  `json:"description"`
}

type renderTextSlotEntry struct {
	Slot string `json:"slot"`
}

type renderTextExclusion struct {
	Slot    string `json:"slot"`
	Fixture string `json:"fixture"`
	NodeID  string `json:"nodeId"`
	Reason  string `json:"reason"`
}

type renderTextFamily struct {
	SlotVocabulary []renderTextSlotEntry `json:"slotVocabulary"`
	Excluded       []renderTextExclusion `json:"excluded"`
	Vectors        []renderTextVector    `json:"vectors"`
}

// loadRenderTextFamily reads the artefact, skipping on a standalone checkout.
func loadRenderTextFamily(t *testing.T) (renderTextFamily, string) {
	t.Helper()
	corpus := findFixtureCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "render-text.json"))
	if err != nil {
		t.Skipf("render-text.json not in the corpus (%v); skipping", err)
	}
	var family renderTextFamily
	if err := json.Unmarshal(raw, &family); err != nil {
		t.Fatalf("render-text.json did not parse: %v", err)
	}
	return family, corpus
}

// findNodeByID returns the node with id anywhere in a decoded tree.
//
// A generic structural walk over the decoded value graph: this host's tree is
// Obj / Arr all the way down, so it descends every container without naming one
// — which is what keeps the family extensible here with no edit.
func findNodeByID(value wire.Value, id string) (wire.Node, bool) {
	switch v := value.(type) {
	case wire.Node:
		if v.ID == id {
			return v, true
		}
		if found, ok := findNodeByID(v.Kind, id); ok {
			return found, true
		}
		for _, extra := range v.Extras {
			if found, ok := findNodeByID(extra, id); ok {
				return found, true
			}
		}
	case wire.Obj:
		for _, field := range v.Fields {
			if found, ok := findNodeByID(field, id); ok {
				return found, true
			}
		}
	case wire.Arr:
		for _, item := range v {
			if found, ok := findNodeByID(item, id); ok {
				return found, true
			}
		}
	}
	return wire.Node{}, false
}

// renderTextSlot resolves one vector against a corpus root, the way a
// conformant host does: from the artefact's own fields, never from a value
// authored beside these checkers.
func renderTextSlot(t *testing.T, corpus string, v renderTextVector) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(corpus, filepath.FromSlash(v.Fixture)))
	if err != nil {
		t.Fatalf("vector %s: reading fixture %s: %v", v.ID, v.Fixture, err)
	}
	node, err := wire.DecodeNode(string(raw))
	if err != nil {
		t.Fatalf("vector %s: fixture %s did not decode: %v", v.ID, v.Fixture, err)
	}
	target, ok := findNodeByID(node, v.NodeID)
	if !ok {
		t.Fatalf("vector %s: fixture %s carries no node with id %q", v.ID, v.Fixture, v.NodeID)
	}
	reader, ok := renderTextSlotReaders[v.Slot]
	if !ok {
		t.Fatalf(
			"vector %s names slot %q, which the corpus declares and this host has no reader for — the host is BEHIND the artefact and must add one (not checked is not passed)",
			v.ID, v.Slot)
	}
	if target.Kind.Tag != reader[0] {
		t.Fatalf("vector %s names slot %q on node %q, whose kind is %q", v.ID, v.Slot, v.NodeID, target.Kind.Tag)
	}
	sources := BindingSources{Values: map[string]wire.Value{}, Now: v.Sources.Now, Locale: v.Sources.Locale}
	text, err := renderText(target.Kind.Fields[reader[1]], sources)
	if err != nil {
		t.Fatalf("vector %s: resolution reported an error: %v", v.ID, err)
	}
	return text
}

func TestRenderTextFamily(t *testing.T) {
	family, corpus := loadRenderTextFamily(t)

	// Non-vacuity FIRST, derived from the artefact: the per-vector loop below
	// passes trivially over an empty list, and an artefact that lost the two arms
	// this phase exists for would still pass it.
	if len(family.Vectors) == 0 {
		t.Fatal("render-text.json declares no vectors")
	}
	var sawNow, sawSince, sawNoClock bool
	for _, v := range family.Vectors {
		sawNow = sawNow || strings.HasPrefix(v.ID, "now-")
		sawSince = sawSince || strings.HasPrefix(v.ID, "since-")
		sawNoClock = sawNoClock || (v.Sources.Now == "" && v.ExpectedText == "")
	}
	if !sawNow {
		t.Error("no Binding.Now vector — the family cannot show a host resolves the instant")
	}
	if !sawSince {
		t.Error("no Format.Since vector — the family cannot show a host renders a relative time")
	}
	if !sawNoClock {
		t.Error("no no-host-clock vector — the family does not pin what an unresolvable instant renders")
	}

	for _, v := range family.Vectors {
		t.Run(v.ID, func(t *testing.T) {
			if got := renderTextSlot(t, corpus, v); got != v.ExpectedText {
				t.Fatalf("expected %q, got %q — %s", v.ExpectedText, got, v.Description)
			}
		})
	}
}

// TestRenderTextComparisonCanGoRed is the go-red property, against the same
// comparison the gate uses. Without it, a reader cannot tell a host that renders
// every vector correctly from one whose renderer returns the corpus's own
// strings.
func TestRenderTextComparisonCanGoRed(t *testing.T) {
	family, corpus := loadRenderTextFamily(t)
	if len(family.Vectors) == 0 {
		t.Skip("no vectors to probe")
	}
	v := family.Vectors[0]
	if renderTextSlot(t, corpus, v) == v.ExpectedText+"!" {
		t.Fatal("the render-text comparison accepted a perturbed expectation — the certification is vacuous")
	}
}

// TestRenderTextHostReadsEveryDeclaredSlot measures this host against the
// artefact's slotVocabulary rather than against its vectors, because the
// vocabulary is what a future vector will draw from: a slot declared today and
// vectored tomorrow should redden this host today, while it is cheap.
func TestRenderTextHostReadsEveryDeclaredSlot(t *testing.T) {
	family, _ := loadRenderTextFamily(t)
	for _, entry := range family.SlotVocabulary {
		if _, ok := renderTextSlotReaders[entry.Slot]; !ok {
			t.Errorf("the corpus declares text slot %q and this host has no reader for it", entry.Slot)
		}
	}
}

// TestRenderTextExcludedSlotsAreNotRendered checks the exclusions as a claim
// about THIS host too. Every excluded slot's text comes out of a locale
// database; this host renders none of them, and a future change that started
// rendering one — inventing a locale answer rather than declaring absence —
// would pass every vector above while breaking the family's premise.
func TestRenderTextExcludedSlotsAreNotRendered(t *testing.T) {
	family, corpus := loadRenderTextFamily(t)
	for _, entry := range family.Excluded {
		raw, err := os.ReadFile(filepath.Join(corpus, filepath.FromSlash(entry.Fixture)))
		if err != nil {
			t.Fatalf("excluded slot %s: reading %s: %v", entry.Slot, entry.Fixture, err)
		}
		node, err := wire.DecodeNode(string(raw))
		if err != nil {
			t.Fatalf("excluded slot %s: %s did not decode: %v", entry.Slot, entry.Fixture, err)
		}
		target, ok := findNodeByID(node, entry.NodeID)
		if !ok {
			t.Fatalf("excluded slot %s names node %q, which is not in %s", entry.Slot, entry.NodeID, entry.Fixture)
		}
		text, err := renderText(target.Kind.Fields["text"], BindingSources{Now: "2026-08-02T06:59:24Z"})
		if err != nil {
			t.Fatalf("excluded slot %s: resolution reported an error: %v", entry.Slot, err)
		}
		if text != "" {
			t.Errorf("this host rendered the excluded slot %s as %q — %s", entry.Slot, text, entry.Reason)
		}
	}
}

// TestResolveLocaleTagPrecedence exercises the widened record's locale member.
//
// LocaleSource.Explicit pins its own tag whatever the host furnishes; Ambient
// reads the host's Locale, whose "" default means the runtime default. This is
// the member the locale-independent renderings deliberately do not consult, so
// it is exercised here or nowhere.
func TestResolveLocaleTagPrecedence(t *testing.T) {
	corpus := findFixtureCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(filepath.Join(corpus, "nodes", "format-since.json"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	node, err := wire.DecodeNode(string(raw))
	if err != nil {
		t.Fatalf("the fixture did not decode: %v", err)
	}
	bindingOf := func(id string) wire.Value {
		target, ok := findNodeByID(node, id)
		if !ok {
			t.Fatalf("no node %q in the fixture", id)
		}
		text, ok := target.Kind.Fields["text"].(wire.Obj)
		if !ok {
			t.Fatalf("node %q carries no bound text slot", id)
		}
		return text.Fields["binding"]
	}
	host := BindingSources{Locale: "fr-FR"}
	if tag, ok := ResolveLocaleTag(bindingOf("since-auto"), host); !ok || tag != "fr-FR" {
		t.Errorf("an Ambient locale must read the host's tag; got %q (ok=%v)", tag, ok)
	}
	if tag, ok := ResolveLocaleTag(bindingOf("since-declared-hour"), host); !ok || tag != "en-GB" {
		t.Errorf("an Explicit locale must pin its own tag; got %q (ok=%v)", tag, ok)
	}
	if tag, ok := ResolveLocaleTag(bindingOf("since-auto"), BindingSources{}); !ok || tag != "" {
		t.Errorf("an Ambient locale over a host furnishing none is the runtime default; got %q (ok=%v)", tag, ok)
	}
}
