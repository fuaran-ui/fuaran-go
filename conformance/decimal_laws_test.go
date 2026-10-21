// decimal_laws_test.go — Phase 2001: this host runs the exact decimal's law
// vectors (the corpus law family laws/decimal, fuaran-core Phase 276).
//
// Every vector of every case runs: the canonical form, the exact order and sum,
// the nearest-double reading, the decimal column's codec in both directions and
// its aggregates. A `reject` vector requires this host to refuse. Its class is
// compared only for the codec cases, where this host carries the same refusal
// classes (TYPE_MISMATCH, MALFORMED_SHAPE); the other cases' classes
// (notDecimal, pastFloatRange, AggregateOverflow, CellOutsideType) have no
// counterpart here, so refusal alone is asserted.
//
// The family is read through laws/manifest.json and held to its row; the set of
// case kinds is held to the seven this leg runs, so a new kind fails loudly.
package conformance

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/dataframe"
)

const decimalLawsFamily = "decimal"

type decimalLawCell struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type decimalLawVector struct {
	ID    string `json:"id"`
	Case  string `json:"case"`
	Input struct {
		Text     string           `json:"text"`
		A        string           `json:"a"`
		B        string           `json:"b"`
		Document string           `json:"document"`
		Cells    []decimalLawCell `json:"cells"`
		Fn       string           `json:"fn"`
	} `json:"input"`
	Expected struct {
		Verdict   string `json:"verdict"`
		Error     string `json:"error"`
		Canonical string `json:"canonical"`
		Order     *int   `json:"order"`
		Sum       string `json:"sum"`
		Float     string `json:"float"`
		Token     string `json:"token"`
	} `json:"expected"`
}

// codecErrorCodes maps the file's codec refusal classes to this host's codes.
var codecErrorCodes = map[string]string{
	"TypeMismatch":   dataframe.TypeMismatch,
	"MalformedShape": dataframe.MalformedShape,
}

var decimalLawCases = map[string]func(*testing.T, decimalLawVector){
	"canonical":   runDecimalCanonical,
	"compare":     runDecimalCompare,
	"add":         runDecimalAdd,
	"toFloat":     runDecimalToFloat,
	"codecDecode": runDecimalCodecDecode,
	"codecEncode": runDecimalCodecEncode,
	"aggregate":   runDecimalAggregate,
}

func TestDecimalLaws(t *testing.T) {
	row, raw := loadLawFamily(t, decimalLawsFamily)
	var doc struct {
		Family     string             `json:"family"`
		KitVersion string             `json:"kitVersion"`
		Vectors    []decimalLawVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing laws/%s: %v", row.File, err)
	}
	if doc.Family != row.ID {
		t.Fatalf("laws/%s declares family %q; the manifest row is %q", row.File, doc.Family, row.ID)
	}
	if doc.KitVersion != row.KitVersion {
		t.Fatalf("laws/%s is stamped kitVersion %q; the manifest row says %q", row.File, doc.KitVersion, row.KitVersion)
	}
	if len(doc.Vectors) != row.Vectors {
		t.Fatalf("laws/%s carries %d vectors; the manifest row says %d", row.File, len(doc.Vectors), row.Vectors)
	}
	seen := map[string]int{}
	for _, v := range doc.Vectors {
		run, ok := decimalLawCases[v.Case]
		if !ok {
			t.Fatalf("vector %s is case %q, which this leg does not know", v.ID, v.Case)
		}
		seen[v.Case]++
		v := v
		t.Run(v.ID, func(t *testing.T) {
			switch v.Expected.Verdict {
			case "accept", "reject":
			default:
				t.Fatalf("unknown verdict %q", v.Expected.Verdict)
			}
			run(t, v)
		})
	}
	for c := range decimalLawCases {
		if seen[c] == 0 {
			t.Fatalf("laws/%s carries no %q vectors; the leg's case set is stale", row.File, c)
		}
	}
	t.Logf("decimal (kitVersion %s): ran %d vectors across %d cases", doc.KitVersion, len(doc.Vectors), len(seen))
}

func expectRefusal(t *testing.T, v decimalLawVector, refused bool) bool {
	t.Helper()
	if v.Expected.Verdict == "reject" {
		if !refused {
			t.Fatalf("the reference refuses (%s); this host accepted", v.Expected.Error)
		}
		return true
	}
	if refused {
		t.Fatalf("the reference accepts; this host refused")
	}
	return false
}

func runDecimalCanonical(t *testing.T, v decimalLawVector) {
	got, ok := dataframe.DecimalCanonical(v.Input.Text)
	if expectRefusal(t, v, !ok) {
		return
	}
	if got != v.Expected.Canonical {
		t.Fatalf("canonical(%q) = %q, want %q", v.Input.Text, got, v.Expected.Canonical)
	}
}

func runDecimalCompare(t *testing.T, v decimalLawVector) {
	got, ok := dataframe.DecimalCompare(v.Input.A, v.Input.B)
	if expectRefusal(t, v, !ok) {
		return
	}
	if v.Expected.Order == nil || got != *v.Expected.Order {
		t.Fatalf("compare(%q, %q) = %d, want %v", v.Input.A, v.Input.B, got, v.Expected.Order)
	}
}

func runDecimalAdd(t *testing.T, v decimalLawVector) {
	got, ok := dataframe.DecimalAdd(v.Input.A, v.Input.B)
	if expectRefusal(t, v, !ok) {
		return
	}
	if got != v.Expected.Sum {
		t.Fatalf("add(%q, %q) = %q, want %q", v.Input.A, v.Input.B, got, v.Expected.Sum)
	}
}

func runDecimalToFloat(t *testing.T, v decimalLawVector) {
	got, ok := dataframe.DecimalToFloat(v.Input.Text)
	if expectRefusal(t, v, !ok) {
		return
	}
	if s := canonical.FormatFiniteDouble(got); s != v.Expected.Float {
		t.Fatalf("toFloat(%q) = %s, want %s", v.Input.Text, s, v.Expected.Float)
	}
}

// expectCodecRefusal asserts a codec refusal, comparing its class where the
// file's class has a counterpart here.
func expectCodecRefusal(t *testing.T, v decimalLawVector, err error) bool {
	t.Helper()
	if !expectRefusal(t, v, err != nil) {
		return false
	}
	want, mapped := codecErrorCodes[v.Expected.Error]
	if !mapped {
		t.Fatalf("codec refusal class %q has no counterpart in this host's codes", v.Expected.Error)
	}
	ce, ok := err.(*dataframe.ColumnError)
	if !ok || ce.Code != want {
		t.Fatalf("refused with %v, want class %s", err, want)
	}
	return true
}

func runDecimalCodecDecode(t *testing.T, v decimalLawVector) {
	src, cerr := dataframe.DecodeSource(v.Input.Document)
	var err error
	if cerr != nil {
		err = cerr
	}
	if expectCodecRefusal(t, v, err) {
		return
	}
	got, encErr := dataframe.EncodeSource(src)
	if encErr != nil {
		t.Fatalf("re-encode: %v", encErr)
	}
	if got != v.Expected.Canonical {
		t.Fatalf("decode-then-encode\n got: %s\nwant: %s", got, v.Expected.Canonical)
	}
}

// decimalColumn builds column c of type decimal from the vector's cells AS
// THEY STAND — a decimal cell keeps its text, canonical or not, because the
// guarded encode and the aggregate's admission are what is under test.
func decimalColumn(t *testing.T, cells []decimalLawCell) dataframe.Table {
	t.Helper()
	out := make([]dataframe.Cell, len(cells))
	for i, c := range cells {
		switch c.Kind {
		case "null":
			out[i] = dataframe.Null
		case "decimal":
			out[i] = dataframe.Cell{Kind: dataframe.TypeDecimal, Value: c.Text}
		case "int":
			n, err := strconv.ParseInt(c.Text, 10, 64)
			if err != nil {
				t.Fatalf("int cell %q: %v", c.Text, err)
			}
			out[i] = dataframe.CellInt(n)
		default:
			t.Fatalf("unknown cell kind %q", c.Kind)
		}
	}
	return dataframe.Table{
		Schema:  dataframe.Schema{{Name: "c", Type: dataframe.TypeDecimal}},
		Columns: []dataframe.Column{{Name: "c", Type: dataframe.TypeDecimal, Cells: out}},
	}
}

func runDecimalCodecEncode(t *testing.T, v decimalLawVector) {
	got, err := dataframe.EncodeSource(dataframe.Embedded{Table: decimalColumn(t, v.Input.Cells)})
	if expectCodecRefusal(t, v, err) {
		return
	}
	if got != v.Expected.Canonical {
		t.Fatalf("encode\n got: %s\nwant: %s", got, v.Expected.Canonical)
	}
}

// cellToken is the result cell's identity token as the file spells it.
func cellToken(t *testing.T, c dataframe.Cell) string {
	t.Helper()
	switch c.Kind {
	case dataframe.TypeDecimal:
		return "m:" + c.Value.(string)
	case dataframe.TypeInt:
		return "i:" + strconv.FormatInt(c.Value.(int64), 10)
	case dataframe.TypeFloat:
		return "f:" + canonical.FormatFiniteDouble(c.Value.(float64))
	case "null":
		return "n:"
	}
	t.Fatalf("no token for a %s cell", c.Kind)
	return ""
}

// runDecimalAggregate folds the aggregate through the evaluator — a groupBy with
// no keys over the column, whose one row carries the aggregate's answer — so the
// semantics under test are the ones a pipeline gets.
func runDecimalAggregate(t *testing.T, v decimalLawVector) {
	pipeline := []dataframe.Transform{dataframe.GroupBy{Aggs: []dataframe.Agg{{Name: "r", Fn: v.Input.Fn, Of: "c"}}}}
	result, evalErr := dataframe.EvalPipeline(pipeline, decimalColumn(t, v.Input.Cells))
	if expectRefusal(t, v, evalErr != nil) {
		return
	}
	if len(result.Columns) != 1 || len(result.Columns[0].Cells) != 1 {
		t.Fatalf("the groupBy answered %d columns, not one row of one aggregate", len(result.Columns))
	}
	if got := cellToken(t, result.Columns[0].Cells[0]); got != v.Expected.Token {
		t.Fatalf("%s = %s, want %s", v.Input.Fn, got, v.Expected.Token)
	}
}
