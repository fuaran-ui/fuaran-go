// The corpus families this host's suite runs, declared and held to the corpus
// tree (WIRE_FORMAT §27.7, Phase 1938), and the host capability manifest that
// publishes the declaration.
//
// hostCorpusFamilies replaced a printer that logged the uncovered root fixture
// kinds and asserted nothing: it could not see a self-enumerated family, and it
// logged style-observer as a lag while styleobserver_corpus_test.go certified
// it. The declaration is a host STATEMENT — no introspection can see which
// families a test suite runs — so the gate is what keeps it true: a family the
// corpus has and the table does not name, or one the table names and the
// corpus no longer has, fails.
//
// The manifest makes no token claim yet (§27.1): each of the six token
// families is declared not covered, with its reason. Regenerate it with
//
//	FUARAN_MANIFEST_REGENERATE=1 go test ./conformance -run TestHostCapabilityManifestIsCurrent
package conformance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	fuarango "github.com/fuaran-ui/fuaran-go"
)

const (
	noCardCodec    = "this host has no contract-card codec (§25) for the card fixtures to run against yet"
	teleportUnrun  = "the teleport package is unit-tested by hand; this suite does not yet run the corpus teleport/ fixtures"
	lawUnrun       = "this host's suite does not yet run this law family's vectors"
	noTokenDeriver = "this host has no generator deriving construct tokens from its type model yet, so it makes no claim in this family (§27.1)"
)

func familyPending(why string) map[string]string { return map[string]string{"pending": why} }
func familyNotApplicable(why string) map[string]string {
	return map[string]string{"notApplicable": why}
}

// hostCorpusFamilies is every corpus family and this host's answer (§27.7).
var hostCorpusFamilies = map[string]any{
	"node-round-trip":           "covered",
	"op-round-trip":             "covered",
	"reject":                    "covered",
	"lenient-accept":            "covered",
	"envelope-round-trip":       "covered",
	"envelope-reject":           "covered",
	"elicitation-round-trip":    "covered",
	"elicitation-reject":        "covered",
	"elicitation-answer-accept": "covered",
	"elicitation-answer-reject": "covered",
	"style-observer":            "covered",
	"contract-card-round-trip":  familyPending(noCardCodec),
	"contract-card-reject":      familyPending(noCardCodec),
	"teleport-decode":           familyPending(teleportUnrun),
	"teleport-reject":           familyPending(teleportUnrun),
	"apply/duplicateIdsApply":   "covered",
	"apply/limitsApply":         "covered",
	"apply/styleNormaliseApply": "covered",
	"apply/skeletonApply":       familyPending("the apply engine's suite does not yet run the skeleton-apply vectors"),
	"dag":                       "covered",
	"merge-conformance":         "covered",
	"sanitization":              "covered",
	"laws/dataframeParity":      "covered",
	"laws/capabilityLaws": familyPending("the capability twin mirrors an older Core than the vectors are stamped for, " +
		"so capability_laws_test.go reports the gap and asserts nothing until the twin is raised"),
	"laws/decimal":   familyPending(lawUnrun),
	"laws/fieldLaws": familyPending(lawUnrun),
	"laws/transformLaws": familyPending("the evaluator certifies against the authored dataframeParity cases; " +
		"the drawn transform sample is not run yet"),
	"decode-policy":    familyNotApplicable("this host's decoder offers no §23 kind admission policy"),
	"devtools-relay":   familyNotApplicable("this host ships no devtools relay endpoint"),
	"lowers-to":        familyNotApplicable("this host carries no bounded program core for actions to lower onto (§30)"),
	"repair":           familyPending("this host has no §28 repair function yet"),
	"stored-emissions": familyPending("this host's suite does not yet run the stored-emission fixtures"),
}

// corpusFamilies derives the corpus family set from the corpus tree (§27.7):
// each root manifest fixture kind, and each top-level directory carrying its own
// manifest.json — one family named by the directory, or one family
// <directory>/<id> per entry of that manifest's families array of objects.
func corpusFamilies(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("reading the root manifest: %v", err)
	}
	var m corpusManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parsing the root manifest: %v", err)
	}
	found := map[string]bool{}
	for _, fx := range m.Fixtures {
		found[fx.Kind] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("listing the corpus: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var own struct {
			Families json.RawMessage `json:"families"`
		}
		if err := json.Unmarshal(raw, &own); err != nil {
			t.Fatalf("parsing %s/manifest.json: %v", e.Name(), err)
		}
		var members []map[string]any
		if json.Unmarshal(own.Families, &members) == nil && len(members) > 0 {
			for _, member := range members {
				id, _ := member["id"].(string)
				if id == "" {
					t.Fatalf("%s/manifest.json: a families entry carries no id", e.Name())
				}
				found[e.Name()+"/"+id] = true
			}
		} else {
			found[e.Name()] = true
		}
	}
	return found
}

func TestManifestNamesEveryCorpusFamilyAndNoOther(t *testing.T) {
	corpus, _ := loadCorpus(t)
	families := corpusFamilies(t, corpus)
	var missing, stale []string
	for f := range families {
		if _, ok := hostCorpusFamilies[f]; !ok {
			missing = append(missing, f)
		}
	}
	for f := range hostCorpusFamilies {
		if !families[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("corpus families this host's manifest does not name: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("families the manifest names that the corpus no longer has: %v", stale)
	}
}

// TestFamilyDerivationSeesSelfEnumeratedFamilies is the falsifier: a family that
// exists only as a directory with its own manifest — the shape the printer could
// not see — joins the derived set, and so does each families entry.
func TestFamilyDerivationSeesSelfEnumeratedFamilies(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", `{"fixtures":[{"kind":"node-round-trip"}]}`)
	write("newfamily/manifest.json", `{"cases":[]}`)
	write("split/manifest.json", `{"families":[{"id":"a"},{"id":"b"}]}`)
	write("goldens/goldens.json", `{}`)
	got := corpusFamilies(t, dir)
	want := map[string]bool{"node-round-trip": true, "newfamily": true, "split/a": true, "split/b": true}
	if len(got) != len(want) {
		t.Fatalf("derived %v, want %v", got, want)
	}
	for f := range want {
		if !got[f] {
			t.Fatalf("derived %v, want %v", got, want)
		}
	}
}

type tokenFamily struct {
	Covered bool   `json:"covered"`
	Reason  string `json:"reason"`
}

// hostCapabilityManifest renders the published artefact: the members in the
// §27.3 order, maps in sorted key order, two-space indent, trailing newline.
func hostCapabilityManifest(t *testing.T) []byte {
	t.Helper()
	doc := struct {
		Manifest        string                 `json:"$manifest"`
		Host            string                 `json:"host"`
		HostVersion     string                 `json:"hostVersion"`
		CorpusAuthority *string                `json:"corpusAuthority"`
		Generator       string                 `json:"generator"`
		Families        map[string]tokenFamily `json:"families"`
		CorpusFamilies  map[string]any         `json:"corpusFamilies"`
		Tokens          []string               `json:"tokens"`
	}{
		Manifest:    "fuaran.host-capability/1",
		Host:        "fuaran-go",
		HostVersion: fuarango.Version,
		Generator:   "github.com/fuaran-ui/fuaran-go/conformance (FUARAN_MANIFEST_REGENERATE=1 go test ./conformance -run TestHostCapabilityManifestIsCurrent)",
		Families: map[string]tokenFamily{
			"kinds":        {Reason: noTokenDeriver},
			"kindFields":   {Reason: noTokenDeriver},
			"unionCases":   {Reason: noTokenDeriver},
			"caseFields":   {Reason: noTokenDeriver},
			"recordFields": {Reason: noTokenDeriver},
			"hostedCases":  {Reason: noTokenDeriver},
		},
		CorpusFamilies: hostCorpusFamilies,
		Tokens:         []string{},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatalf("rendering the manifest: %v", err)
	}
	return buf.Bytes()
}

func TestHostCapabilityManifestIsCurrent(t *testing.T) {
	path := filepath.Join("..", "host-capability-manifest.json")
	want := hostCapabilityManifest(t)
	if os.Getenv("FUARAN_MANIFEST_REGENERATE") != "" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the published manifest: %v — regenerate it (see families_test.go)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("host-capability-manifest.json is not what this host's declaration renders — regenerate it " +
			"with FUARAN_MANIFEST_REGENERATE=1 go test ./conformance -run TestHostCapabilityManifestIsCurrent")
	}
}
