package dataframe

import "testing"

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
