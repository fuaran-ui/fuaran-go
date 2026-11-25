package dataframe

import "testing"

// The row key must be injective: a cell's key may itself contain the
// separator and another cell's tag, so two different multi-column rows must
// never share one key. The witness is built from cellKey's own tag for a
// string cell, so it tracks whatever spelling the host uses.

// collidingRows returns two different two-column string rows whose cell keys,
// joined with a bare separator, would read identically.
func collidingRows() (a1, b1, a2, b2 string) {
	tag := cellKey(CellStr(""))
	a1, b1 = "x\x01"+tag+"y", "z"
	a2, b2 = "x", "y\x01"+tag+"z"
	return
}

func twoColTable(a, b []string, extra ...Column) Table {
	ac := make([]Cell, len(a))
	bc := make([]Cell, len(b))
	for i := range a {
		ac[i] = CellStr(a[i])
		bc[i] = CellStr(b[i])
	}
	schema := Schema{{Name: "a", Type: TypeString}, {Name: "b", Type: TypeString}}
	cols := []Column{{Name: "a", Type: TypeString, Cells: ac}, {Name: "b", Type: TypeString, Cells: bc}}
	for _, c := range extra {
		schema = append(schema, SchemaEntry{Name: c.Name, Type: c.Type})
		cols = append(cols, c)
	}
	return Table{Schema: schema, Columns: cols}
}

func TestRowKeyIsInjective(t *testing.T) {
	a1, b1, a2, b2 := collidingRows()
	k1 := rowKey([]Cell{CellStr(a1), CellStr(b1)})
	k2 := rowKey([]Cell{CellStr(a2), CellStr(b2)})
	if k1 == k2 {
		t.Fatalf("two different rows share one key %q", k1)
	}
	// Equality of cells is unchanged: equal rows still share a key.
	if rowKey([]Cell{CellStr(a1), CellStr(b1)}) != k1 {
		t.Fatalf("one row gave two keys")
	}
}

func TestDistinctKeepsRowsWhoseCellsSpellTheSeparator(t *testing.T) {
	a1, b1, a2, b2 := collidingRows()
	out, err := EvalPipeline([]Transform{Distinct{}}, twoColTable([]string{a1, a2}, []string{b1, b2}))
	if err != nil {
		t.Fatalf("distinct: %v", err)
	}
	if n := rowCount(out); n != 2 {
		t.Fatalf("distinct kept %d rows of two different rows, want 2", n)
	}
}

func TestGroupByKeepsGroupsWhoseCellsSpellTheSeparator(t *testing.T) {
	a1, b1, a2, b2 := collidingRows()
	pipeline := []Transform{GroupBy{Keys: []string{"a", "b"}, Aggs: []Agg{{Name: "n", Fn: "count", Of: "a"}}}}
	out, err := EvalPipeline(pipeline, twoColTable([]string{a1, a2}, []string{b1, b2}))
	if err != nil {
		t.Fatalf("groupBy: %v", err)
	}
	if n := rowCount(out); n != 2 {
		t.Fatalf("groupBy made %d groups of two different keys, want 2", n)
	}
}

func TestIntersectDoesNotMatchADifferentRow(t *testing.T) {
	a1, b1, a2, b2 := collidingRows()
	right := twoColTable([]string{a2}, []string{b2})
	pipeline := []Transform{Intersect{Source: Embedded{Table: right}}}
	out, err := EvalPipeline(pipeline, twoColTable([]string{a1}, []string{b1}))
	if err != nil {
		t.Fatalf("intersect: %v", err)
	}
	if n := rowCount(out); n != 0 {
		t.Fatalf("intersect kept %d rows that the other side does not hold, want 0", n)
	}
}

func TestWindowTwoColumnPartitionsStayApart(t *testing.T) {
	a1, b1, a2, b2 := collidingRows()
	spec := WindowSpec{PartitionBy: []string{"a", "b"}, Fn: "rowNumber", As: "rn"}
	out, err := EvalPipeline([]Transform{Window{Spec: spec}}, twoColTable([]string{a1, a2}, []string{b1, b2}))
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	rn := out.Columns[len(out.Columns)-1].Cells
	for i, c := range rn {
		if !cellEq(c, CellInt(1)) {
			t.Fatalf("row %d numbered %v; each row is its own partition, want 1", i, c.Value)
		}
	}
}

func TestPivotTwoColumnIndexKeysStayApart(t *testing.T) {
	a1, b1, a2, b2 := collidingRows()
	on := Column{Name: "o", Type: TypeString, Cells: []Cell{CellStr("p"), CellStr("p")}}
	v := Column{Name: "v", Type: TypeInt, Cells: []Cell{CellInt(1), CellInt(2)}}
	spec := PivotSpec{Index: []string{"a", "b"}, On: "o", Values: "v", Agg: "sum"}
	out, err := EvalPipeline([]Transform{Pivot{Spec: spec}}, twoColTable([]string{a1, a2}, []string{b1, b2}, on, v))
	if err != nil {
		t.Fatalf("pivot: %v", err)
	}
	if n := rowCount(out); n != 2 {
		t.Fatalf("pivot made %d index rows of two different keys, want 2", n)
	}
}
