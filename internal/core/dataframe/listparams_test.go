package dataframe

import (
	"reflect"
	"testing"
)

// LIST-valued Transform params (WIRE_FORMAT.md, "LIST-valued `Binding.Transform`
// params (Phase 610)"; Fuaran.Core's `Transform.substituteListParams`) — the
// mechanism half, asserted at the algebra layer where it is decidable.
//
// Three rules, each asserted through the mechanism rather than through a
// downstream verdict, because a render that draws nothing looks the same whether
// the pipeline was correctly pruned, correctly refused, or silently broken:
//
//   - Resolution is by SUBSTITUTION, before evaluation: a bound `InParam`
//     becomes an `InList` of literals; one that reaches `EvalPipeline` still
//     spelled `InParam` is a strict UNBOUND_PARAM, never a silent pass.
//   - An EMPTY selection is UNBOUND, never `InList(x, [])` — the caller records
//     it in `unbound` and `BindParams` prunes the dependent filter step, so
//     deselecting everything leaves the frame unconstrained.
//   - A KIND MISMATCH in either direction substitutes nothing and reaches the
//     same strict refusal: the scalar env never binds an `InParam`, and the list
//     env never binds a scalar `Param`.

// deptIn is the fixture's predicate shape: dept IN :depts.
func deptIn() ColExpr { return InParam{Subject: Col{Name: "dept"}, Name: "depts"} }

// deptTable is a three-row frame matching nodes/multiselect-chip-list-param.json.
func deptTable() Table {
	return Table{
		Schema: Schema{{Name: "dept", Type: TypeString}},
		Columns: []Column{{
			Name:  "dept",
			Type:  TypeString,
			Cells: []Cell{CellStr("eng"), CellStr("sales"), CellStr("ops")},
		}},
	}
}

func TestSubstituteListParamsRewritesMembershipToLiteralList(t *testing.T) {
	pipeline := []Transform{Filter{Pred: deptIn()}}
	listEnv := map[string][]Cell{"depts": {CellStr("eng"), CellStr("ops")}}

	got := SubstituteListParams(pipeline, listEnv)

	want := []Transform{Filter{Pred: InList{
		Subject: Col{Name: "dept"},
		Items:   []ColExpr{Lit{Cell: CellStr("eng")}, Lit{Cell: CellStr("ops")}},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("substitution did not rewrite in/param to the literal in/items form:\n got %#v\nwant %#v", got, want)
	}
}

func TestSubstituteListParamsLeavesAnUnboundInParamIntact(t *testing.T) {
	pipeline := []Transform{Filter{Pred: deptIn()}}

	got := SubstituteListParams(pipeline, map[string][]Cell{"other": {CellStr("x")}})

	if !reflect.DeepEqual(got, pipeline) {
		t.Fatalf("an unbound list param must survive substitution so the prune (or the refusal) can see it: %#v", got)
	}
}

// The list env never binds a SCALAR param — one half of the kind-mismatch rule.
func TestSubstituteListParamsDoesNotBindAScalarParam(t *testing.T) {
	pipeline := []Transform{Filter{Pred: Binary{
		Op:    "eq",
		Left:  Col{Name: "dept"},
		Right: Param{Name: "depts"},
	}}}

	got := SubstituteListParams(pipeline, map[string][]Cell{"depts": {CellStr("eng")}})

	if !reflect.DeepEqual(got, pipeline) {
		t.Fatalf("a list bound to a name the pipeline reads as a SCALAR param must substitute nothing: %#v", got)
	}
}

func TestSubstituteListParamsWalksNestedExpressions(t *testing.T) {
	pipeline := []Transform{
		Filter{Pred: Not{Expr: deptIn()}},
		Derive{Name: "hit", Expr: Case{
			Cases:    []WhenThen{{When: deptIn(), Then: Lit{Cell: CellInt(1)}}},
			ElseExpr: Lit{Cell: CellInt(0)},
		}},
	}

	got := SubstituteListParams(pipeline, map[string][]Cell{"depts": {CellStr("eng")}})

	for _, step := range got {
		if names := stepInParamNames(step); len(names) != 0 {
			t.Fatalf("substitution did not reach a nested in/param (%v still bound): %#v", names, got)
		}
	}
}

// stepInParamNames names every InParam a step still carries after substitution.
func stepInParamNames(step Transform) []string {
	var expr ColExpr
	switch v := step.(type) {
	case Filter:
		expr = v.Pred
	case Derive:
		expr = v.Expr
	default:
		return nil
	}
	var out []string
	var walk func(ColExpr)
	walk = func(e ColExpr) {
		switch x := e.(type) {
		case InParam:
			out = append(out, x.Name)
			walk(x.Subject)
		case InList:
			walk(x.Subject)
			for _, it := range x.Items {
				walk(it)
			}
		case Binary:
			walk(x.Left)
			walk(x.Right)
		case Not:
			walk(x.Expr)
		case Case:
			for _, wt := range x.Cases {
				walk(wt.When)
				walk(wt.Then)
			}
			walk(x.ElseExpr)
		}
	}
	walk(expr)
	return out
}

// Substitution happens BEFORE the prune: a SUBSTITUTED step names no param at
// all and survives; an UNBOUND one still names its own and is pruned. One rule
// covers both param kinds precisely because of that ordering.
func TestSubstitutionPrecedesThePrune(t *testing.T) {
	pipeline := []Transform{Filter{Pred: deptIn()}}

	bound := BindParams(
		SubstituteListParams(pipeline, map[string][]Cell{"depts": {CellStr("eng")}}),
		map[string]Cell{}, map[string]bool{},
	)
	if len(bound) != 1 {
		t.Fatalf("a SUBSTITUTED filter step must survive the prune, got %d steps", len(bound))
	}

	pruned := BindParams(
		SubstituteListParams(pipeline, map[string][]Cell{}),
		map[string]Cell{}, map[string]bool{"depts": true},
	)
	if len(pruned) != 0 {
		t.Fatalf("an UNBOUND list param's filter step must be pruned, got %d steps", len(pruned))
	}
}

// The evaluated result, end to end: substitution then evaluation keeps exactly
// the selected rows.
func TestSubstitutedListParamScopesTheRows(t *testing.T) {
	pipeline := SubstituteListParams(
		[]Transform{Filter{Pred: deptIn()}},
		map[string][]Cell{"depts": {CellStr("eng"), CellStr("ops")}},
	)

	result, err := EvalPipeline(pipeline, deptTable())
	if err != nil {
		t.Fatalf("evaluating a substituted pipeline: %v", err)
	}
	got := make([]string, 0, 2)
	for _, c := range result.Columns[0].Cells {
		got = append(got, c.Value.(string))
	}
	if !reflect.DeepEqual(got, []string{"eng", "ops"}) {
		t.Fatalf("substituted membership scoped wrongly: %v", got)
	}
}

// An empty selection recorded as UNBOUND prunes to the UNFILTERED frame — the
// acceptance criterion. The wrong reading (substituting an empty membership set)
// would leave zero rows, and the second half of this test pins that the two
// readings are genuinely distinguishable rather than accidentally equal.
func TestEmptySelectionPrunesToTheUnfilteredFrame(t *testing.T) {
	pipeline := BindParams(
		SubstituteListParams([]Transform{Filter{Pred: deptIn()}}, map[string][]Cell{}),
		map[string]Cell{}, map[string]bool{"depts": true},
	)

	result, err := EvalPipeline(pipeline, deptTable())
	if err != nil {
		t.Fatalf("evaluating a pruned pipeline: %v", err)
	}
	if n := len(result.Columns[0].Cells); n != 3 {
		t.Fatalf("an empty selection must show the UNFILTERED 3 rows, got %d", n)
	}

	empty, eerr := EvalPipeline(
		[]Transform{Filter{Pred: InList{Subject: Col{Name: "dept"}}}},
		deptTable(),
	)
	if eerr != nil {
		t.Fatalf("evaluating an empty membership set: %v", eerr)
	}
	if len(empty.Columns) != 0 && len(empty.Columns[0].Cells) != 0 {
		t.Fatalf("guard: membership over an empty item list should match nothing, got %d rows",
			len(empty.Columns[0].Cells))
	}
}

// Substitution-before-evaluation, the strict half: an in/param that reaches the
// evaluator names an unbound param and refuses.
func TestUnsubstitutedInParamIsAStrictUnboundParam(t *testing.T) {
	_, err := EvalPipeline([]Transform{Filter{Pred: deptIn()}}, deptTable())
	if err == nil {
		t.Fatal("an unsubstituted in/param reaching the evaluator must refuse, not silently pass")
	}
	if err.Code != UnboundParam {
		t.Fatalf("expected %s, got %s: %s", UnboundParam, err.Code, err.Detail)
	}
}

// Kind mismatch, both directions, at the mechanism: neither env binds the other's
// spelling, so both reach the same strict refusal.
func TestKindMismatchReachesTheStrictRefusal(t *testing.T) {
	// A SCALAR bound to a name the pipeline reads as an in/param.
	scalarToList := BindParams(
		SubstituteListParams([]Transform{Filter{Pred: deptIn()}}, map[string][]Cell{}),
		map[string]Cell{"depts": CellStr("eng")}, map[string]bool{},
	)
	if _, err := EvalPipeline(scalarToList, deptTable()); err == nil || err.Code != UnboundParam {
		t.Fatalf("a scalar bound to an in/param must reach %s, got %v", UnboundParam, err)
	}

	// A LIST bound to a name the pipeline reads as a scalar param.
	scalarPred := []Transform{Filter{Pred: Binary{
		Op: "eq", Left: Col{Name: "dept"}, Right: Param{Name: "depts"},
	}}}
	listToScalar := BindParams(
		SubstituteListParams(scalarPred, map[string][]Cell{"depts": {CellStr("eng")}}),
		map[string]Cell{}, map[string]bool{},
	)
	if _, err := EvalPipeline(listToScalar, deptTable()); err == nil || err.Code != UnboundParam {
		t.Fatalf("a list bound to a scalar param must reach %s, got %v", UnboundParam, err)
	}
}
