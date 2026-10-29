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
// The manifest's token claim (§27.3, Phase 2332) is projected from this
// host's generated type model and read back against the corpus; see "The token
// claim" below. Regenerate the manifest with
//
//	FUARAN_MANIFEST_REGENERATE=1 go test ./conformance -run TestHostCapabilityManifestIsCurrent
package conformance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	fuarango "github.com/fuaran-ui/fuaran-go"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

const (
	noCardCodec       = "this host has no contract-card codec (§25) for the card fixtures to run against yet"
	teleportUnrun     = "the teleport package is unit-tested by hand; this suite does not yet run the corpus teleport/ fixtures"
	lawUnrun          = "this host's suite does not yet run this law family's vectors"
	noDesignSeamCodec = "this host has no codec for the design-provider seam's records for the design/ fixtures to run against yet"
	hostedOpaque      = "this host's generated type model carries each hosted slot opaquely, so it holds no enumeration of the discriminators inside one (§27.5) and this host makes no claim in this family"
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
	"design-seam-round-trip":    familyPending(noDesignSeamCodec),
	"design-seam-reject":        familyPending(noDesignSeamCodec),
	"apply/duplicateIdsApply":   "covered",
	"apply/limitsApply":         "covered",
	"apply/styleNormaliseApply": "covered",
	"apply/skeletonApply":       familyPending("the apply engine's suite does not yet run the skeleton-apply vectors"),
	"binding-propagation":       familyPending("this host has no binding walk deriving which bindings a write makes stale for the vectors to run against yet"),
	"chain":                     "covered", // conformance/chain_test.go
	"dag":                       "covered",
	"formatting":                familyPending("the host formatter is not yet certified against the golden; carried by fuaran#2328"),
	"merge-conformance":         "covered",
	"sanitization":              "covered",
	"row-encode":                "covered",
	"laws/dataframeParity":      "covered",
	"laws/capabilityLaws":       "covered",
	"laws/decimal":              "covered",
	"laws/fieldLaws":            "covered",
	"laws/propagationLaws":      familyPending(lawUnrun),
	"laws/transformLaws":        "covered",
	"diff/treeDiff":             "covered",
	"grid-edit":                 "covered", // conformance/grid_edit_test.go
	"grid-window-writer":        familyNotApplicable("this host renders a grid as static HTML and has no grid viewport, so nothing it runs measures a viewport or writes a window descriptor"),
	"decode-policy":             familyNotApplicable("this host's decoder offers no §23 kind admission policy"),
	"devtools-relay":            familyNotApplicable("this host ships no devtools relay endpoint"),
	"lowers-to":                 familyNotApplicable("this host carries no bounded program core for actions to lower onto (§30)"),
	"repair":                    familyPending("this host has no §28 repair function yet"),
	"stored-emissions":          familyPending("this host's suite does not yet run the stored-emission fixtures"),
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
	Reason  string `json:"reason,omitempty"`
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
		Manifest:       "fuaran.host-capability/1",
		Host:           "fuaran-go",
		HostVersion:    fuarango.Version,
		Generator:      "github.com/fuaran-ui/fuaran-go/conformance (FUARAN_MANIFEST_REGENERATE=1 go test ./conformance -run TestHostCapabilityManifestIsCurrent)",
		Families:       tokenFamilies(),
		CorpusFamilies: hostCorpusFamilies,
		Tokens:         constructTokens(hostTypeModel),
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

// ── The token claim (§27.1–27.4, Phase 2332) ────────────────────────────────
//
// The claim is a projection of this host's generated type model — the rows
// package wire/idl generates from idl.json — and is never written by hand: a
// construct the generated layer gains is claimed on the next regeneration, and
// one it loses is dropped. A row the IDL classes hostOnly never reaches the
// wire, so it is no token. hostedCases stays not covered: the generated layer
// types each hosted slot opaquely, so it holds no enumeration of the
// discriminators inside one (§27.5) for a projection to read.

// typeModel is the slice of the generated layer the projection reads.
type typeModel struct {
	kinds   []*idl.Kind
	unions  []*idl.Union
	records []*idl.Record
}

// hostTypeModel is this host's generated type model.
var hostTypeModel = typeModel{kinds: idl.Kinds, unions: idl.Unions, records: idl.Records}

// derivedFamilies are the token families constructTokens projects; the sixth,
// hostedCases, is declared not covered.
var derivedFamilies = []string{"caseFields", "kindFields", "kinds", "recordFields", "unionCases"}

// constructTokens is every construct token (§27.2) the model can author, sorted:
// every kind, every union case, and every wire member of a kind, a union case
// or a record. A pure function of the model, so the falsifiers can hand it a
// model with one construct removed.
func constructTokens(m typeModel) []string {
	found := map[string]bool{}
	members := func(owner string, fields []idl.Field) {
		for _, f := range fields {
			if f.Optionality != idl.HostOnly {
				found[owner+"."+f.Name] = true
			}
		}
	}
	for _, k := range m.kinds {
		found["Kind."+k.Tag] = true
		members("Kind."+k.Tag, k.Fields)
	}
	for _, u := range m.unions {
		for _, c := range u.Cases {
			found[u.Name+"."+c.Tag] = true
			members(u.Name+"."+c.Tag, c.Fields)
		}
	}
	for _, r := range m.records {
		members(r.Name, r.Fields)
	}
	tokens := make([]string, 0, len(found))
	for tok := range found {
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	return tokens
}

// familyOf is the §27.3 token family a token belongs to, read from the §27.2
// grammar alone.
func familyOf(token string) string {
	segments := strings.Split(token, ".")
	switch {
	case segments[0] == "Kind" && len(segments) == 2:
		return "kinds"
	case segments[0] == "Kind":
		return "kindFields"
	case len(segments) == 3:
		return "caseFields"
	case len(segments[1]) > 0 && segments[1][0] >= 'A' && segments[1][0] <= 'Z':
		return "unionCases"
	}
	return "recordFields"
}

// tokenFamilies is the manifest's families member: the derived five covered,
// hostedCases not.
func tokenFamilies() map[string]tokenFamily {
	families := map[string]tokenFamily{"hostedCases": {Reason: hostedOpaque}}
	for _, f := range derivedFamilies {
		families[f] = tokenFamily{Covered: true}
	}
	return families
}

func without[T any](xs []*T, drop func(*T) bool) []*T {
	var out []*T
	for _, x := range xs {
		if !drop(x) {
			out = append(out, x)
		}
	}
	return out
}

func removed(before, after []string) []string {
	keep := map[string]bool{}
	for _, t := range after {
		keep[t] = true
	}
	var gone []string
	for _, t := range before {
		if !keep[t] {
			gone = append(gone, t)
		}
	}
	return gone
}

func TestTokensLieInTheCoveredFamilies(t *testing.T) {
	families := tokenFamilies()
	for _, tok := range constructTokens(hostTypeModel) {
		if !families[familyOf(tok)].Covered {
			t.Errorf("%s lies in %s, which this host does not cover", tok, familyOf(tok))
		}
	}
}

func TestRemovingAKindRemovesExactlyItsTokens(t *testing.T) {
	m := hostTypeModel
	m.kinds = without(m.kinds, func(k *idl.Kind) bool { return k.Tag == "Badge" })
	got := removed(constructTokens(hostTypeModel), constructTokens(m))
	want := []string{"Kind.Badge", "Kind.Badge.label", "Kind.Badge.variant"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("removing Badge removed %v, want %v", got, want)
	}
}

func TestRemovingAUnionCaseRemovesExactlyItsTokens(t *testing.T) {
	m := hostTypeModel
	m.unions = nil
	for _, u := range hostTypeModel.unions {
		if u.Name == "Binding" {
			trimmed := *u
			trimmed.Cases = nil
			for _, c := range u.Cases {
				if c.Tag != "Static" {
					trimmed.Cases = append(trimmed.Cases, c)
				}
			}
			u = &trimmed
		}
		m.unions = append(m.unions, u)
	}
	got := removed(constructTokens(hostTypeModel), constructTokens(m))
	want := []string{"Binding.Static", "Binding.Static.value"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("removing Binding.Static removed %v, want %v", got, want)
	}
}

func TestAHostOnlyRowIsNoToken(t *testing.T) {
	claimed := map[string]bool{}
	for _, tok := range constructTokens(hostTypeModel) {
		claimed[tok] = true
	}
	if !claimed["Action.Dispatch"] || claimed["Action.Dispatch.msg"] {
		t.Fatalf("want Action.Dispatch claimed and its hostOnly msg not; got %v and %v",
			claimed["Action.Dispatch"], claimed["Action.Dispatch.msg"])
	}
}

// ── Reading the claim back against the corpus ───────────────────────────────
//
// Every fixture of the round-trip families is walked against the corpus's OWN
// idl.json — never this host's rows, so the claim is checked against the
// oracle rather than against itself — for the construct tokens it exercises
// (§27.4). A token the corpus exercises and this host does not claim fails by
// name; so does a claimed token carried by a fixture this host does not
// round-trip.

// exercisingFamilies are the families whose canonical documents are wire trees
// a host decodes and re-encodes.
var exercisingFamilies = map[string]bool{"node-round-trip": true, "op-round-trip": true, "lenient-accept": true}

type idlType struct {
	Type   string          `json:"$type"`
	Name   string          `json:"name"`
	Args   []idlType       `json:"args"`
	Of     *idlType        `json:"of"`
	Items  []idlType       `json:"items"`
	Values *idlType        `json:"values"`
	Wire   json.RawMessage `json:"wire"`
}

type idlField struct {
	Name string  `json:"name"`
	Type idlType `json:"type"`
}

type corpusIDL struct {
	Kinds []struct {
		Tag    string     `json:"tag"`
		Fields []idlField `json:"fields"`
	} `json:"kinds"`
	Unions []struct {
		Name  string `json:"name"`
		Cases []struct {
			Tag    string     `json:"tag"`
			Fields []idlField `json:"fields"`
		} `json:"cases"`
	} `json:"unions"`
	Records []struct {
		Name   string     `json:"name"`
		Fields []idlField `json:"fields"`
	} `json:"records"`
	Ops []struct {
		Tag    string     `json:"tag"`
		Fields []idlField `json:"fields"`
	} `json:"ops"`
	NodeFields []idlField `json:"nodeFields"`
	Harden     struct {
		TransparentUnions []struct {
			Union string `json:"union"`
			Case  string `json:"case"`
		} `json:"transparentUnions"`
	} `json:"harden"`
}

// vocabularyWalker walks one wire value against the corpus IDL, adding each
// construct token it exercises.
type vocabularyWalker struct {
	kinds, ops, records map[string][]idlField
	unions              map[string]map[string][]idlField
	transparent         map[string]string
	nodeFields          []idlField
}

func newVocabularyWalker(doc corpusIDL) *vocabularyWalker {
	w := &vocabularyWalker{
		kinds: map[string][]idlField{}, ops: map[string][]idlField{}, records: map[string][]idlField{},
		unions: map[string]map[string][]idlField{}, transparent: map[string]string{}, nodeFields: doc.NodeFields,
	}
	for _, k := range doc.Kinds {
		w.kinds[k.Tag] = k.Fields
	}
	for _, o := range doc.Ops {
		w.ops[o.Tag] = o.Fields
	}
	for _, r := range doc.Records {
		w.records[r.Name] = r.Fields
	}
	for _, u := range doc.Unions {
		cases := map[string][]idlField{}
		for _, c := range u.Cases {
			cases[c.Tag] = c.Fields
		}
		w.unions[u.Name] = cases
	}
	for _, tu := range doc.Harden.TransparentUnions {
		w.transparent[tu.Union] = tu.Case
	}
	return w
}

func discriminator(v any) (map[string]any, string, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, "", false
	}
	tag, ok := obj["$type"].(string)
	return obj, tag, ok
}

func (w *vocabularyWalker) members(v any, fields []idlField, owner string, arg *idlType, out map[string]bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	for _, f := range fields {
		value, present := obj[f.Name]
		if !present {
			continue
		}
		if owner != "" {
			out[owner+"."+f.Name] = true
		}
		ty := f.Type
		if ty.Type == "var" && arg != nil {
			ty = *arg
		}
		w.walk(value, ty, out)
	}
}

func (w *vocabularyWalker) walk(v any, t idlType, out map[string]bool) {
	switch t.Type {
	case "node":
		if obj, ok := v.(map[string]any); ok {
			w.walk(obj["kind"], idlType{Type: "kind"}, out)
			w.members(obj, w.nodeFields, "", nil, out)
		}
	case "kind":
		if obj, tag, ok := discriminator(v); ok {
			out["Kind."+tag] = true
			w.members(obj, w.kinds[tag], "Kind."+tag, nil, out)
		}
	case "op":
		if obj, tag, ok := discriminator(v); ok {
			w.members(obj, w.ops[tag], "", nil, out)
		}
	case "union":
		if obj, tag, ok := discriminator(v); ok {
			out[t.Name+"."+tag] = true
			var arg *idlType
			if len(t.Args) > 0 {
				arg = &t.Args[0]
			}
			w.members(obj, w.unions[t.Name][tag], t.Name+"."+tag, arg, out)
		} else if c, ok := w.transparent[t.Name]; ok {
			out[t.Name+"."+c] = true
		}
	case "record":
		w.members(v, w.records[t.Name], t.Name, nil, out)
	case "hosted":
		// A hosted slot whose wire type the IDL declares is walked through it;
		// one carried as bare JSON is opaque here, as it is to the IDL (§27.5).
		var wire idlType
		if json.Unmarshal(t.Wire, &wire) == nil && wire.Type != "" {
			w.walk(v, wire, out)
		}
	case "list":
		if items, ok := v.([]any); ok {
			for i, item := range items {
				switch {
				case t.Of != nil:
					w.walk(item, *t.Of, out)
				case i < len(t.Items):
					w.walk(item, t.Items[i], out)
				}
			}
		}
	case "map":
		if obj, ok := v.(map[string]any); ok && t.Values != nil {
			for _, item := range obj {
				w.walk(item, *t.Values, out)
			}
		}
	}
}

// exercisedTokens is each token the corpus exercises, with the round-trip
// fixtures that carry it.
func exercisedTokens(t *testing.T, corpus string, m corpusManifest) map[string][]manifestFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(corpus, "idl.json"))
	if err != nil {
		t.Fatalf("reading the corpus idl.json: %v", err)
	}
	var doc corpusIDL
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing the corpus idl.json: %v", err)
	}
	w := newVocabularyWalker(doc)
	byToken := map[string][]manifestFixture{}
	for _, fx := range m.Fixtures {
		if !exercisingFamilies[fx.Kind] {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(readFixture(t, corpus, fx.ExpectedFile)), &value); err != nil {
			t.Fatalf("parsing %s: %v", fx.ExpectedFile, err)
		}
		found := map[string]bool{}
		w.walk(value, idlType{Type: fx.Decoder}, found)
		for tok := range found {
			byToken[tok] = append(byToken[tok], fx)
		}
	}
	return byToken
}

// claimFindings is every way the claim and the corpus disagree, one finding per
// token, in token order.
func claimFindings(claim []string, byToken map[string][]manifestFixture, roundTrips func(manifestFixture) bool) []string {
	claimed := map[string]bool{}
	for _, tok := range claim {
		claimed[tok] = true
	}
	verdicts := map[string]bool{}
	green := func(fx manifestFixture) bool {
		v, known := verdicts[fx.ID]
		if !known {
			v = roundTrips(fx)
			verdicts[fx.ID] = v
		}
		return v
	}
	tokens := make([]string, 0, len(byToken))
	for tok := range byToken {
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	var findings []string
	for _, tok := range tokens {
		fixtures := byToken[tok]
		if !claimed[tok] {
			findings = append(findings, tok+": the corpus exercises it ("+fixtures[0].ID+") and this host does not claim it")
			continue
		}
		var red []string
		for _, fx := range fixtures {
			if !green(fx) {
				red = append(red, fx.ID)
			}
		}
		if len(red) > 0 {
			findings = append(findings, tok+": claimed, and this host does not round-trip "+strings.Join(red, ", "))
		}
	}
	return findings
}

// roundTripsCanonical reports whether this host decodes a fixture's canonical
// document and re-encodes it byte-identically.
func roundTripsCanonical(t *testing.T, corpus string) func(manifestFixture) bool {
	return func(fx manifestFixture) bool {
		want := readFixture(t, corpus, fx.ExpectedFile)
		got, err := decodeReencode(fx.Decoder, want)
		return err == nil && got == want
	}
}

func publishedTokens(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "host-capability-manifest.json"))
	if err != nil {
		t.Fatalf("reading the published manifest: %v", err)
	}
	var doc struct {
		Tokens []string `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing the published manifest: %v", err)
	}
	return doc.Tokens
}

func TestTheClaimReadsBackAgainstTheCorpus(t *testing.T) {
	corpus, m := loadCorpus(t)
	byToken := exercisedTokens(t, corpus, m)
	reached := map[string]bool{}
	for tok := range byToken {
		reached[familyOf(tok)] = true
	}
	for _, f := range derivedFamilies {
		if !reached[f] {
			t.Errorf("the corpus walk reached no %s token, so the read-back checks nothing there", f)
		}
	}
	for _, finding := range claimFindings(publishedTokens(t), byToken, roundTripsCanonical(t, corpus)) {
		t.Error(finding)
	}
}

func TestAnOmittedTokenTheCorpusExercisesFailsByName(t *testing.T) {
	corpus, m := loadCorpus(t)
	claim := withoutToken(constructTokens(hostTypeModel), "Kind.Badge")
	findings := claimFindings(claim, exercisedTokens(t, corpus, m), roundTripsCanonical(t, corpus))
	if len(findings) != 1 || !strings.HasPrefix(findings[0], "Kind.Badge: the corpus exercises it (") {
		t.Fatalf("want one finding naming Kind.Badge, got %v", findings)
	}
}

func TestAClaimedTokenThisHostDoesNotRoundTripFailsByName(t *testing.T) {
	corpus, m := loadCorpus(t)
	byToken := exercisedTokens(t, corpus, m)
	if len(byToken["Kind.Badge"]) == 0 {
		t.Fatal("no fixture exercises Kind.Badge, so this falsifier tests nothing")
	}
	victim := byToken["Kind.Badge"][0].ID
	findings := claimFindings(constructTokens(hostTypeModel), byToken, func(fx manifestFixture) bool { return fx.ID != victim })
	named := false
	for _, f := range findings {
		if !strings.Contains(f, victim) {
			t.Errorf("finding does not name the failing fixture %s: %s", victim, f)
		}
		named = named || strings.HasPrefix(f, "Kind.Badge: claimed")
	}
	if !named {
		t.Fatalf("no finding names Kind.Badge: %v", findings)
	}
}

func withoutToken(tokens []string, drop string) []string {
	var out []string
	for _, t := range tokens {
		if t != drop {
			out = append(out, t)
		}
	}
	return out
}
