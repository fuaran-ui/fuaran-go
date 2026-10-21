// transform_laws_test.go — Phase 2001: this host runs the reference
// evaluator's own transform-law answers (the corpus law family
// laws/transformLaws).
//
// Each evalPipeline vector carries a canonical source table and pipeline and the
// reference's answer: the canonical encoding of the table it produced, or the
// bare verdict that it refused. This leg decodes both inputs with this host's
// codec, evaluates with this host's evaluator, and compares the result BYTE FOR
// BYTE; an `error` verdict requires this evaluator to refuse too, and the
// refusal's class is deliberately not compared (the file carries none).
//
// The family is read THROUGH laws/manifest.json, and the file is held to its
// manifest row (family id, kitVersion, seed, iterations, vector count) — no
// count is restated here, so a re-emitted draw is noticed rather than misread.
//
// NAMED EXCEPTIONS, each reported by name and counted, never silent:
//
//   - every evalPipeline vector whose id carries `-unit-`: carried by fuaran#2208
//     — this host does not yet carry field metadata (fuaran#2207) or the unit
//     algebra. The skip set is DERIVED from the ids, and every member must be an
//     evalPipeline vector, so a unit vector renamed out of the pattern runs (and
//     fails) rather than hiding;
//   - the case kinds `columnOp` and `delta`: this host carries no ColumnOp
//     surface and no table-delta surface. The set of case kinds in the file must
//     be exactly {evalPipeline, columnOp, delta}, so a new kind fails loudly.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dataframe"
)

// lawManifestRow is one laws/manifest.json family row, with the stamps a
// drawn family declares (absent members read as their zero value).
type lawManifestRow struct {
	ID         string `json:"id"`
	File       string `json:"file"`
	KitVersion string `json:"kitVersion"`
	Seed       *int64 `json:"seed"`
	Iterations *int   `json:"iterations"`
	Vectors    int    `json:"vectors"`
}

// loadLawFamily reads the manifest row for id and the raw bytes of its file.
func loadLawFamily(t *testing.T, id string) (lawManifestRow, []byte) {
	t.Helper()
	corpus, _ := loadCorpus(t)
	laws := filepath.Join(corpus, "laws")
	raw, err := os.ReadFile(filepath.Join(laws, "manifest.json"))
	if err != nil {
		t.Fatalf("reading laws/manifest.json: %v", err)
	}
	var manifest struct {
		Families []lawManifestRow `json:"families"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing laws/manifest.json: %v", err)
	}
	for _, row := range manifest.Families {
		if row.ID != id {
			continue
		}
		body, err := os.ReadFile(filepath.Join(laws, row.File))
		if err != nil {
			t.Fatalf("reading laws/%s: %v", row.File, err)
		}
		return row, body
	}
	t.Fatalf("laws/manifest.json declares no %s family", id)
	return lawManifestRow{}, nil
}

const transformLawsFamily = "transformLaws"

type transformLawVector struct {
	ID    string `json:"id"`
	Case  string `json:"case"`
	Input struct {
		Source   string `json:"source"`
		Pipeline string `json:"pipeline"`
	} `json:"input"`
	Expected struct {
		Verdict string `json:"verdict"`
		Table   string `json:"table"`
	} `json:"expected"`
}

// transformLawKindsNotRun names each case kind this host does not run, and why.
var transformLawKindsNotRun = map[string]string{
	"columnOp": "this host carries no ColumnOp surface",
	"delta":    "this host carries no table-delta surface",
}

const unitVectorReason = "carried by fuaran#2208 — this host does not yet carry field metadata (fuaran#2207) or the unit algebra"

func TestTransformLaws(t *testing.T) {
	row, raw := loadLawFamily(t, transformLawsFamily)
	var doc struct {
		Family     string               `json:"family"`
		KitVersion string               `json:"kitVersion"`
		Seed       *int64               `json:"seed"`
		Iterations *int                 `json:"iterations"`
		Vectors    []transformLawVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing laws/%s: %v", row.File, err)
	}

	// The file agrees with its manifest row.
	if doc.Family != row.ID {
		t.Fatalf("laws/%s declares family %q; the manifest row is %q", row.File, doc.Family, row.ID)
	}
	if doc.KitVersion != row.KitVersion {
		t.Fatalf("laws/%s is stamped kitVersion %q; the manifest row says %q", row.File, doc.KitVersion, row.KitVersion)
	}
	if doc.Seed == nil || row.Seed == nil || *doc.Seed != *row.Seed {
		t.Fatalf("laws/%s seed %v disagrees with the manifest row's %v", row.File, doc.Seed, row.Seed)
	}
	if len(doc.Vectors) != row.Vectors {
		t.Fatalf("laws/%s carries %d vectors; the manifest row says %d", row.File, len(doc.Vectors), row.Vectors)
	}

	// The case kinds are exactly the three this leg knows.
	kinds := map[string]int{}
	for _, v := range doc.Vectors {
		kinds[v.Case]++
	}
	want := map[string]bool{"evalPipeline": true}
	for k := range transformLawKindsNotRun {
		want[k] = true
	}
	for k := range kinds {
		if !want[k] {
			t.Fatalf("laws/%s carries case kind %q, which this leg does not know — decide whether this host runs it", row.File, k)
		}
	}
	for k := range want {
		if kinds[k] == 0 {
			t.Fatalf("laws/%s carries no %q vectors; the leg's kind set is stale", row.File, k)
		}
	}
	if doc.Iterations == nil || row.Iterations == nil || *doc.Iterations != *row.Iterations || *doc.Iterations != kinds["evalPipeline"] {
		t.Fatalf("laws/%s iterations %v / manifest %v / evalPipeline vectors %d disagree",
			row.File, doc.Iterations, row.Iterations, kinds["evalPipeline"])
	}

	// The unit skip set, derived from the ids.
	var unitSkipped []string
	for _, v := range doc.Vectors {
		if strings.Contains(v.ID, "-unit-") {
			if v.Case != "evalPipeline" {
				t.Fatalf("vector %s carries -unit- but is a %s vector; the unit skip covers evalPipeline vectors only", v.ID, v.Case)
			}
			unitSkipped = append(unitSkipped, v.ID)
		}
	}

	ran := 0
	for _, v := range doc.Vectors {
		if _, notRun := transformLawKindsNotRun[v.Case]; notRun || strings.Contains(v.ID, "-unit-") {
			continue // reported by name below
		}
		ran++
		v := v
		t.Run(v.ID, func(t *testing.T) { runTransformLawVector(t, v) })
	}

	for _, id := range unitSkipped {
		t.Logf("transformLaws: not run %s — %s", id, unitVectorReason)
	}
	notRunKinds := make([]string, 0, len(transformLawKindsNotRun))
	for k := range transformLawKindsNotRun {
		notRunKinds = append(notRunKinds, k)
	}
	sort.Strings(notRunKinds)
	for _, k := range notRunKinds {
		t.Logf("transformLaws: not run %d %s vector(s) — %s", kinds[k], k, transformLawKindsNotRun[k])
	}
	t.Logf("transformLaws (kitVersion %s): ran %d evalPipeline vectors; skipped %d unit vectors, %d columnOp, %d delta",
		doc.KitVersion, ran, len(unitSkipped), kinds["columnOp"], kinds["delta"])
	if ran+len(unitSkipped) != kinds["evalPipeline"] {
		t.Fatalf("ran %d + unit-skipped %d != %d evalPipeline vectors", ran, len(unitSkipped), kinds["evalPipeline"])
	}
}

func runTransformLawVector(t *testing.T, v transformLawVector) {
	src, cerr := dataframe.DecodeSource(v.Input.Source)
	if cerr != nil {
		t.Fatalf("decode source: %v", cerr)
	}
	pipeline, cerr := dataframe.DecodePipeline(v.Input.Pipeline)
	if cerr != nil {
		t.Fatalf("decode pipeline: %v", cerr)
	}
	embedded, ok := src.(dataframe.Embedded)
	if !ok {
		t.Fatalf("the vector's source is not embedded")
	}
	result, evalErr := dataframe.EvalPipeline(pipeline, embedded.Table)
	switch v.Expected.Verdict {
	case "error":
		if evalErr == nil {
			t.Fatalf("the reference refused the pipeline; this evaluator returned a result")
		}
		return
	case "ok":
	default:
		t.Fatalf("unknown verdict %q", v.Expected.Verdict)
	}
	if evalErr != nil {
		t.Fatalf("evaluate: %v", evalErr)
	}
	got, err := dataframe.EncodeSource(dataframe.Embedded{Table: result})
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	if got != v.Expected.Table {
		t.Fatalf("evaluator mismatch\n got: %s\nwant: %s", got, v.Expected.Table)
	}
}
