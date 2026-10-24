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
//
// TOLERANCE (Phase 2400). A vector carrying `expected.tolerance` names the
// columns the reference marks host-rounded, each with a bound in ulps. Those
// columns are compared cell by cell — one type, one length, null in the same
// rows, two present nonzero finite floats of one sign within the bound (the
// distance being the difference of their IEEE-754 binary64 bit patterns), every
// other cell (a zero of either sign, a non-finite float, a non-float) exactly —
// and the rest of the table byte for byte: this host's answer with every named
// column replaced by the recorded one must encode to `expected.table` exactly.
// The bound is the vector's own, never a constant here. The worst distance seen
// per named column is logged, so a host drifting toward the bound is visible.
package conformance

import (
	"encoding/json"
	"math"
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
		Verdict   string         `json:"verdict"`
		Table     string         `json:"table"`
		Tolerance map[string]int `json:"tolerance"`
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
	if len(v.Expected.Tolerance) == 0 {
		if got != v.Expected.Table {
			t.Fatalf("evaluator mismatch\n got: %s\nwant: %s", got, v.Expected.Table)
		}
		return
	}
	compareWithinTolerance(t, v, result)
}

// ulpDistance is the distance between two doubles of one sign: the difference of
// their binary64 bit patterns read as ordered integers.
func ulpDistance(a, b float64) uint64 {
	ia, ib := math.Float64bits(a), math.Float64bits(b)
	if ia > ib {
		return ia - ib
	}
	return ib - ia
}

// compareWithinTolerance applies the family's tolerance rule (see the header).
func compareWithinTolerance(t *testing.T, v transformLawVector, result dataframe.Table) {
	t.Helper()
	src, cerr := dataframe.DecodeSource(v.Expected.Table)
	if cerr != nil {
		t.Fatalf("decode expected.table: %v", cerr)
	}
	want := src.(dataframe.Embedded).Table
	column := func(tb dataframe.Table, name string) (dataframe.Column, int) {
		for i, c := range tb.Columns {
			if c.Name == name {
				return c, i
			}
		}
		return dataframe.Column{}, -1
	}
	names := make([]string, 0, len(v.Expected.Tolerance))
	for name := range v.Expected.Tolerance {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		bound := uint64(v.Expected.Tolerance[name])
		wc, wi := column(want, name)
		gc, gi := column(result, name)
		if wi < 0 || gi < 0 {
			t.Fatalf("tolerance column %q is absent (expected %v, answered %v)", name, wi >= 0, gi >= 0)
		}
		if wc.Type != gc.Type || len(wc.Cells) != len(gc.Cells) {
			t.Fatalf("tolerance column %q: type %s/%s, length %d/%d", name, gc.Type, wc.Type, len(gc.Cells), len(wc.Cells))
		}
		worst := uint64(0)
		for r := range wc.Cells {
			w, g := wc.Cells[r], gc.Cells[r]
			if (w.Kind == "null") != (g.Kind == "null") {
				t.Fatalf("tolerance column %q row %d: null in one table only", name, r)
			}
			if w.Kind == "null" {
				continue
			}
			wf, wok := w.Value.(float64)
			gf, gok := g.Value.(float64)
			bothFloats := wok && gok && w.Kind == dataframe.TypeFloat && g.Kind == dataframe.TypeFloat
			if bothFloats && wf != 0 && gf != 0 && !math.IsInf(wf, 0) && !math.IsInf(gf, 0) &&
				!math.IsNaN(wf) && !math.IsNaN(gf) && math.Signbit(wf) == math.Signbit(gf) {
				d := ulpDistance(wf, gf)
				if d > worst {
					worst = d
				}
				if d > bound {
					t.Fatalf("tolerance column %q row %d: %v is %d ulp from the recorded %v; the bound is %d",
						name, r, gf, d, wf, bound)
				}
				continue
			}
			same := w.Kind == g.Kind
			switch {
			case same && bothFloats:
				same = math.Float64bits(wf) == math.Float64bits(gf) || (math.IsNaN(wf) && math.IsNaN(gf))
			case same:
				same = w.Value == g.Value
			}
			if !same {
				t.Fatalf("tolerance column %q row %d: %v differs from the recorded %v (compared exactly)", name, r, g.Value, w.Value)
			}
		}
		t.Logf("transformLaws tolerance: %s column %s worst %d ulp (bound %d)", v.ID, name, worst, bound)
		result.Columns[gi] = wc
	}
	got, err := dataframe.EncodeSource(dataframe.Embedded{Table: result})
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	if got != v.Expected.Table {
		t.Fatalf("evaluator mismatch outside the tolerance columns\n got: %s\nwant: %s", got, v.Expected.Table)
	}
}
