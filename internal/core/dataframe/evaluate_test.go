package dataframe

import (
	"math"
	"testing"
)

// A tiny two-column table: k=["a","b"], x=[1,2].
func sampleTable() Table {
	return Table{
		Schema:  Schema{{Name: "k", Type: TypeString}, {Name: "x", Type: TypeInt}},
		Columns: []Column{{Name: "k", Type: TypeString, Cells: []Cell{CellStr("a"), CellStr("b")}}, {Name: "x", Type: TypeInt, Cells: []Cell{CellInt(1), CellInt(2)}}},
	}
}

// EvalPipeline refuses a Ref source with UNRESOLVED_SOURCE (embedded-only).
func TestEvalPipelineRefUnresolved(t *testing.T) {
	pipeline := []Transform{Union{Source: Ref{Name: "other"}}}
	_, err := EvalPipeline(pipeline, sampleTable())
	if err == nil || err.Code != UnresolvedSource {
		t.Fatalf("expected UNRESOLVED_SOURCE, got %v", err)
	}
}

// EvalPipelineWith threads a resolver so a Ref join succeeds.
func TestEvalPipelineWithResolver(t *testing.T) {
	right := Table{
		Schema:  Schema{{Name: "k", Type: TypeString}, {Name: "y", Type: TypeInt}},
		Columns: []Column{{Name: "k", Type: TypeString, Cells: []Cell{CellStr("a")}}, {Name: "y", Type: TypeInt, Cells: []Cell{CellInt(9)}}},
	}
	resolve := func(name string) (Table, *EvalError) { return right, nil }
	pipeline := []Transform{Join{Source: Ref{Name: "r"}, On: []Pair{{A: "k", B: "k"}}, How: "inner"}}
	out, err := EvalPipelineWith(resolve, pipeline, sampleTable())
	if err != nil {
		t.Fatalf("join with resolver: %v", err)
	}
	if len(out.Columns) == 0 || len(out.Columns[0].Cells) != 1 {
		t.Fatalf("expected one inner-join row, got %d cols", len(out.Columns))
	}
}

// Totality: an unknown column surfaces a typed error, never a panic.
func TestEvalUnknownColumnTotality(t *testing.T) {
	pipeline := []Transform{Filter{Pred: Binary{Op: "gt", Left: Col{Name: "nope"}, Right: Lit{Cell: CellInt(0)}}}}
	_, err := EvalPipeline(pipeline, sampleTable())
	if err == nil || err.Code != UnknownColumn {
		t.Fatalf("expected UNKNOWN_COLUMN, got %v", err)
	}
}

// Null propagates through arithmetic (Kleene): null + 1 == null.
func TestArithNullPropagates(t *testing.T) {
	got, err := arith("add", Null, CellInt(1))
	if err != nil || !isNull(got) {
		t.Fatalf("null + 1 should be null, got %v (err %v)", got, err)
	}
}

// int + int stays int; any float widens.
func TestArithIntStaysInt(t *testing.T) {
	sum, _ := arith("add", CellInt(2), CellInt(3))
	if sum.Kind != TypeInt || sum.Value.(int64) != 5 {
		t.Fatalf("2+3 should be int 5, got %v", sum)
	}
	wide, _ := arith("add", CellInt(2), CellFloat(0.5))
	if wide.Kind != TypeFloat || wide.Value.(float64) != 2.5 {
		t.Fatalf("2+0.5 should widen to float 2.5, got %v", wide)
	}
}

// Division by zero is null, not an error.
func TestDivByZeroIsNull(t *testing.T) {
	got, err := arith("div", CellInt(1), CellInt(0))
	if err != nil || !isNull(got) {
		t.Fatalf("1/0 should be null, got %v (err %v)", got, err)
	}
}

// Phase 2206: timestamp cells order by the instant, not by their canonical text. A whole second
// is written without a fraction, so "…:00Z" and "…:00.5Z" would order backwards byte-for-byte.
func TestCompareCellsTimestampOrdersByInstant(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2025-07-08T18:40:00Z", "2025-07-08T18:40:00.5Z", -1},
		{"2025-07-08T18:40:00.5Z", "2025-07-08T18:40:00Z", 1},
		{"2025-07-08T18:40:00.12Z", "2025-07-08T18:40:00.123456789Z", -1},
		{"2025-07-08T18:40:00.9Z", "2025-07-08T18:40:01Z", -1},
		{"2025-07-08T18:40:00.5Z", "2025-07-08T18:40:00.5Z", 0},
	}
	for _, c := range cases {
		got, ok := compareCells(CellTimestamp(c.a), CellTimestamp(c.b))
		if !ok || got != c.want {
			t.Fatalf("compareCells(%s, %s) = %d, %v; want %d", c.a, c.b, got, ok, c.want)
		}
	}
}

// Phase 2400: the rows of the power functions' special-value table the wire
// cannot carry (a NaN or an infinity), the pinned product tree past the cube,
// and the int32 band an int power stays inside.
func TestPowerFunctionsOffTheWire(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	isNaN := func(c Cell) bool { f, ok := c.Value.(float64); return ok && math.IsNaN(f) }
	for _, c := range []struct {
		name string
		got  Cell
		want func(Cell) bool
	}{
		{"pow(1, NaN) is NaN", powFloat(1, nan), isNaN},
		{"pow(NaN, 0) is NaN", powFloat(nan, 0), isNaN},
		{"pow(-1, Inf) is 1", powFloat(-1, inf), func(c Cell) bool { return c == CellFloat(1) }},
		{"pow(-1, -Inf) is 1", powFloat(-1, -inf), func(c Cell) bool { return c == CellFloat(1) }},
		{"exp(NaN) is NaN", CellFloat(expHost(nan)), isNaN},
		{"exp above the edge is Inf", CellFloat(expHost(709.8)), func(c Cell) bool { return c == CellFloat(inf) }},
		{"ln(Inf) is Inf", logHost(math.Log, inf), func(c Cell) bool { return c == CellFloat(inf) }},
		{"log10(NaN) is NaN", logHost(math.Log10, nan), isNaN},
		{"a NaN base under an int exponent is NaN", mustApply(t, "pow", CellFloat(nan), CellInt(0)), isNaN},
		{"1e-300 to the -2 is null", mustApply(t, "pow", CellFloat(1e-300), CellInt(-2)), func(c Cell) bool { return isNull(c) }},
	} {
		if !c.want(c.got) {
			t.Errorf("%s: got %v", c.name, c.got)
		}
	}
	x := 1.1
	if powGo(x, 4) != (x*x)*(x*x) || powGo(x, 5) != x*((x*x)*(x*x)) {
		t.Errorf("the product tree past the cube is not the squaring tree")
	}
	if _, err := applyScalar("pow", []Cell{CellInt(46341), CellInt(2)}); err == nil {
		t.Errorf("46341^2 leaves the int32 band and must be refused")
	}
	if c, err := applyScalar("pow", []Cell{CellInt(-2), CellInt(31)}); err != nil || c != CellInt(-2147483648) {
		t.Errorf("(-2)^31 is the band's floor, got %v %v", c, err)
	}
}

func mustApply(t *testing.T, fn string, args ...Cell) Cell {
	t.Helper()
	c, err := applyScalar(fn, args)
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	return c
}

// ── Phase 2208: units propagate and are checked ─────────────────────────────

func mustUnit(t *testing.T, text string) *Unit {
	t.Helper()
	u, r := ParseUnit(text)
	if r != nil {
		t.Fatalf("ParseUnit(%q): %v", text, r)
	}
	return &u
}

func strp(s string) *string { return &s }

// unitTable: d metres (labelled), t seconds, dk kilometres, n unitless and
// labelled, described, with an extension key.
func unitTable(t *testing.T, withUnits bool) Table {
	u := func(text string) *Unit {
		if !withUnits {
			return nil
		}
		return mustUnit(t, text)
	}
	return Table{
		Schema: Schema{
			{Name: "g", Type: TypeString},
			{Name: "d", Type: TypeInt, Unit: u("m"), Label: strp("Distance")},
			{Name: "t", Type: TypeFloat, Unit: u("s")},
			{Name: "dk", Type: TypeFloat, Unit: u("km")},
			{Name: "n", Type: TypeInt, Label: strp("Count"), Description: strp("How many"), Ext: map[string]string{"acme.k": "v"}},
		},
		Columns: []Column{
			{Name: "g", Type: TypeString, Cells: []Cell{CellStr("a"), CellStr("b")}},
			{Name: "d", Type: TypeInt, Cells: []Cell{CellInt(100), CellInt(200)}},
			{Name: "t", Type: TypeFloat, Cells: []Cell{CellFloat(4), CellFloat(8)}},
			{Name: "dk", Type: TypeFloat, Cells: []Cell{CellFloat(0.5), CellFloat(1)}},
			{Name: "n", Type: TypeInt, Cells: []Cell{CellInt(1), CellInt(2)}},
		},
	}
}

func outEntry(t *testing.T, pipeline []Transform, name string) SchemaEntry {
	t.Helper()
	out, err := EvalPipeline(pipeline, unitTable(t, true))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	for _, e := range out.Schema {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no output column %q", name)
	return SchemaEntry{}
}

func entryUnit(e SchemaEntry) string {
	if e.Unit == nil {
		return ""
	}
	return e.Unit.String()
}

func TestUnitAlgebraConversionFactor(t *testing.T) {
	num, den, ok := ConversionFactor(*mustUnit(t, "km/h"), *mustUnit(t, "m/s"))
	if !ok || num.String() != "5" || den.String() != "18" {
		t.Fatalf("km/h -> m/s = %v/%v (%v), want 5/18", num, den, ok)
	}
	if _, _, ok := ConversionFactor(*mustUnit(t, "m"), *mustUnit(t, "s")); ok {
		t.Fatal("m -> s converted")
	}
	if !CompatibleUnits(*mustUnit(t, "N"), *mustUnit(t, "kg.m/s2")) || CompatibleUnits(*mustUnit(t, "[GBP]"), *mustUnit(t, "[USD]")) {
		t.Fatal("dimension comparison is wrong")
	}
	if p, ok := PowUnit(*mustUnit(t, "m/s"), 2); !ok || p.String() != "m2/s2" {
		t.Fatalf("(m/s)^2 = %s", p.String())
	}
}

// KEPT: a projection and a join keep the whole entry.
func TestUnitEntryKept(t *testing.T) {
	e := outEntry(t, []Transform{Project{Cols: []Pair{{A: "n", B: "count"}}}}, "count")
	if e.Label == nil || *e.Label != "Count" || e.Description == nil || e.Ext["acme.k"] != "v" {
		t.Fatalf("projection dropped metadata: %+v", e)
	}
	j := outEntry(t, []Transform{Join{Source: Embedded{Table: unitTable(t, true)}, On: []Pair{{A: "g", B: "g"}}, How: "inner"}}, "d_right")
	if entryUnit(j) != "m" || j.Label == nil || *j.Label != "Distance" {
		t.Fatalf("join's right side dropped metadata: %+v", j)
	}
}

// DROPPED and UNIT-ONLY: a derive, an aggregate and a window build their own
// entry — the derived unit, no label.
func TestUnitEntryRebuilt(t *testing.T) {
	d := outEntry(t, []Transform{Derive{Name: "v", Expr: Col{Name: "d"}}}, "v")
	if entryUnit(d) != "m" || d.Label != nil {
		t.Fatalf("derive: %+v", d)
	}
	n := outEntry(t, []Transform{Derive{Name: "n2", Expr: Col{Name: "n"}}}, "n2")
	if n.HasMetadata() {
		t.Fatalf("derive over a labelled unitless column states metadata: %+v", n)
	}
	c := outEntry(t, []Transform{GroupBy{Keys: []string{"g"}, Aggs: []Agg{{Name: "c", Fn: "count", Of: "d"}, {Name: "s", Fn: "sum", Of: "d"}}}}, "c")
	if c.HasMetadata() {
		t.Fatalf("count states metadata: %+v", c)
	}
	s := outEntry(t, []Transform{GroupBy{Keys: []string{"g"}, Aggs: []Agg{{Name: "s", Fn: "sum", Of: "d"}}}}, "s")
	if entryUnit(s) != "m" || s.Label != nil {
		t.Fatalf("sum: %+v", s)
	}
	w := outEntry(t, []Transform{Window{Spec: WindowSpec{OrderBy: []OrderKey{{Col: "n", Dir: "asc"}}, Fn: "lag", Of: "d", As: "p"}}}, "p")
	if entryUnit(w) != "m" || w.Label != nil {
		t.Fatalf("window: %+v", w)
	}
}

// Go-red: each refusal is the UNIT check's — a UNIT_MISMATCH — and the same
// pipeline over the same data answers once the units are withdrawn.
func TestUnitMismatchIsTheUnitCheck(t *testing.T) {
	cases := map[string]Transform{
		"m + s":  Derive{Name: "x", Expr: Binary{Op: "add", Left: Col{Name: "d"}, Right: Col{Name: "t"}}},
		"km + m": Derive{Name: "x", Expr: Binary{Op: "add", Left: Col{Name: "dk"}, Right: Col{Name: "d"}}},
		"m > s":  Filter{Pred: Binary{Op: "gt", Left: Col{Name: "d"}, Right: Col{Name: "t"}}},
		"m to s": Derive{Name: "x", Expr: Convert{Expr: Col{Name: "d"}, Unit: *mustUnit(t, "s")}},
	}
	for what, step := range cases {
		if _, err := EvalPipeline([]Transform{step}, unitTable(t, true)); err == nil || err.Code != UnitMismatch {
			t.Errorf("%s: want UNIT_MISMATCH, got %v", what, err)
		}
		if _, err := EvalPipeline([]Transform{step}, unitTable(t, false)); err != nil {
			t.Errorf("%s without units: refused %v", what, err)
		}
	}
}
