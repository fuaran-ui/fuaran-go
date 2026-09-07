package renderer

import (
	"fmt"
	"sort"

	"github.com/fuaran-ui/fuaran-go/dataframe"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Render-time compute resolution (Phase 651) — deliberate posture widening: the
// go host's static-HTML and islands emission resolve `Bound` `Transform`
// bindings and `Selection.defaultValue` through the certified `dataframe`
// evaluator, so a Go binary emits COMPLETE static output — pages whose computed
// values are correct before any JS runs, and genuinely no-JS surfaces (email
// digests, ops reports) where hydration can never fill the gap. This is render
// wiring over the UNTOUCHED evaluator (the dataframe package's semantics are not
// changed); like the sibling hosts (F# 647 / py 648 / rs 649) it is wiring +
// locks, not new evaluator semantics.
//
// Two entry points share one evaluation: resolveSource (row context → the
// transformed rows) and the scalar-slot path (resolveScalarText /
// resolveScalarNumber, the Phase 632 1×1 law). Both the full static render and
// the islands skeleton walk the same renderNode, so the two emission paths carry
// the resolved values identically (correct-before-hydration; hydration may
// re-resolve, never first-fill).
//
// The line that does not move — library, not runtime. Everything here is a pure
// function over the per-render BindingSources: no package-level mutable state, no
// session type, no per-user state held between calls. render(tree, data) → bytes
// stays a pure function.

// transformBinding reports a decoded `Transform` pipeline binding.
func transformBinding(binding wire.Value) (wire.Obj, bool) {
	if obj, ok := binding.(wire.Obj); ok && obj.Tag == "Transform" {
		return obj, true
	}
	return wire.Obj{}, false
}

// exprBinding reports a Phase-1534 Binding.Expr, rewritten as the equivalent
// one-row Transform.
//
// The rewrite IS the implementation, on purpose: param resolution, list-param
// substitution and the evaluator are then literally the code the pipeline runs,
// so an expression cannot mean one thing inside a `derive` and another inside an
// `Expr`. A second evaluator here would be a second thing to specify, certify on
// five hosts, and keep in step.
//
// The frame carries one column of one row so `derive` has a row to produce; the
// expression never reads it (a `col` reference is refused at decode), and the
// trailing `project` drops it so the result is 1x1 by construction rather than
// by inspection.
func exprBinding(binding wire.Value) (wire.Obj, bool) {
	obj, ok := binding.(wire.Obj)
	if !ok || obj.Tag != "Expr" {
		return wire.Obj{}, false
	}
	unitFrame := wire.Obj{Fields: map[string]wire.Value{
		"columns": wire.Obj{Fields: map[string]wire.Value{"__unit": wire.Arr{wire.Bool(true)}}},
	}}
	pipeline := wire.Arr{
		wire.Obj{Tag: "derive", Fields: map[string]wire.Value{"expr": obj.Fields["expr"], "name": wire.Str("__value")}},
		wire.Obj{Tag: "project", Fields: map[string]wire.Value{
			"cols": wire.Arr{wire.Obj{Fields: map[string]wire.Value{"a": wire.Str("__value"), "b": wire.Str("__value")}}},
		}},
	}
	fields := map[string]wire.Value{"pipeline": pipeline, "source": unitFrame}
	if params, ok := obj.Fields["params"]; ok {
		fields["params"] = params
	}
	return wire.Obj{Tag: "Transform", Fields: fields}, true
}

// liveSourceBinding reports a Phase-818 preserved LIVE Transform source — a
// binding-shaped source (State / Selection / Query) the decoder kept verbatim
// so a runtime re-evaluates the pipeline with subscription semantics. This
// headless host's render-time analogue: the host-seeded store value when
// present, else the binding's carried default (the decode-time initial
// snapshot), so SSR output is byte-identical to the Phase-815 snapshot era.
func liveSourceBinding(source wire.Value) (wire.Obj, bool) {
	if obj, ok := source.(wire.Obj); ok && (obj.Tag == "State" || obj.Tag == "Selection" || obj.Tag == "Query") {
		return obj, true
	}
	return wire.Obj{}, false
}

// normaliseLiveRows mirrors the decode-time Phase-815 normalisation at the
// Value level: ROW-MAJOR data (an Arr of row Objs) transposes to the canonical
// columnar `{"columns": …}` shape — FIRST-row key set (sorted ordinal), absent
// cells (and non-object rows' cells) null. Anything else passes through
// untouched, so canonical columnar data reaches the frame codec unchanged.
func normaliseLiveRows(v wire.Value) wire.Value {
	rows, ok := v.(wire.Arr)
	if !ok || len(rows) == 0 {
		return v
	}
	first, ok := rows[0].(wire.Obj)
	if !ok {
		return v
	}
	keys := make([]string, 0, len(first.Fields))
	for k := range first.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	cols := make(map[string]wire.Value, len(keys))
	for _, k := range keys {
		cells := make(wire.Arr, len(rows))
		for i, row := range rows {
			cells[i] = wire.Value(wire.Null{})
			if rm, ok := row.(wire.Obj); ok {
				if cell, ok := rm.Fields[k]; ok {
					cells[i] = cell
				}
			}
		}
		cols[k] = cells
	}
	return wire.Obj{Fields: map[string]wire.Value{"columns": wire.Obj{Fields: cols}}}
}

// liveInputTable materialises a LIVE source's current data as the pipeline's
// input table: the host-resolved value when the store is seeded, else the
// binding's carried defaultValue (the initial snapshot), else the empty table
// (a Selection / Query with nothing yet — the pipeline evaluates over zero
// rows). A non-tabular value is an error, so the caller renders absence
// rather than a wrong value.
func liveInputTable(obj wire.Obj, sources BindingSources) (dataframe.Table, error) {
	v := resolveBinding(obj, sources)
	if v == nil {
		if dv, ok := obj.Fields["defaultValue"]; ok {
			v = dv
		}
	}
	if v == nil {
		return dataframe.Table{}, nil
	}
	dataJSON, err := wire.EncodeValue(normaliseLiveRows(v))
	if err != nil {
		return dataframe.Table{}, err
	}
	src, cerr := dataframe.DecodeSource(dataJSON)
	if cerr != nil {
		return dataframe.Table{}, cerr
	}
	if emb, ok := src.(dataframe.Embedded); ok {
		return emb.Table, nil
	}
	return dataframe.Table{}, fmt.Errorf("transform live source resolved to a non-embedded source")
}

// resolveSource resolves a data-bearing node's `source` slot to a row collection
// (a wire.Arr of column-keyed row objects) — the ROW context. A `Transform`
// evaluates through the certified evaluator; any other binding falls back to
// resolveBinding (e.g. a Static row list). An evaluation failure resolves to nil
// (the caller's empty / placeholder path) — never the scalar 1×1 law.
func resolveSource(source wire.Value, sources BindingSources) wire.Value {
	if t, ok := transformBinding(source); ok {
		table, err := evalTransformFrame(t, sources)
		if err != nil {
			return nil
		}
		return tableToRows(table)
	}
	return resolveBinding(source, sources)
}

// evalTransformFrame evaluates a `Transform` binding to a concrete result table
// through the certified `dataframe` evaluator. The nested `source` / `pipeline`
// wire subtrees are canonically re-encoded and handed to the dataframe codec (so
// the renderer never re-implements the algebra decode); each param's `from`
// binding resolves through resolveBinding — a `Selection.defaultValue` seeds the
// env (Phase 629) — and a filter over an unbound (unset) param is pruned. A
// non-scalar param value, an unresolved Ref source, a decode failure, or an
// evaluator error is returned as an error, so the caller renders absence rather
// than a wrong value.
func evalTransformFrame(t wire.Obj, sources BindingSources) (dataframe.Table, error) {
	srcVal, ok := t.Fields["source"]
	if !ok {
		return dataframe.Table{}, fmt.Errorf("transform binding has no source")
	}
	var input dataframe.Table
	if live, isLive := liveSourceBinding(srcVal); isLive {
		// Phase 818 — a preserved LIVE source: evaluate over the current data
		// (host-seeded store value, else the initial snapshot).
		var lerr error
		input, lerr = liveInputTable(live, sources)
		if lerr != nil {
			return dataframe.Table{}, lerr
		}
	} else {
		srcJSON, err := wire.EncodeValue(srcVal)
		if err != nil {
			return dataframe.Table{}, err
		}
		src, cerr := dataframe.DecodeSource(srcJSON)
		if cerr != nil {
			return dataframe.Table{}, cerr
		}
		switch s := src.(type) {
		case dataframe.Embedded:
			input = s.Table
		case dataframe.Ref:
			// A headless host resolves no named sources — the top-level source must
			// travel embedded (a Ref inside a join is likewise UNRESOLVED_SOURCE).
			return dataframe.Table{}, fmt.Errorf("transform source is an unresolved Ref %q", s.Name)
		}
	}

	var pipeline []dataframe.Transform
	if pipeVal, ok := t.Fields["pipeline"]; ok {
		pipeJSON, err := wire.EncodeValue(pipeVal)
		if err != nil {
			return dataframe.Table{}, err
		}
		decoded, cerr := dataframe.DecodePipeline(pipeJSON)
		if cerr != nil {
			return dataframe.Table{}, cerr
		}
		pipeline = decoded
	}

	env, listEnv, unbound, perr := resolveTransformParams(t.Fields["params"], sources)
	if perr != nil {
		return dataframe.Table{}, perr
	}
	// A bound LIST param resolves by SUBSTITUTION, and it happens BEFORE the
	// prune: a substituted `in`/`param` has become an `in`/`items` and so names
	// no param at all, while an unbound one survives to be caught by the prune
	// under its own name. That ordering is why one prune covers both param kinds.
	bound := dataframe.BindParams(
		dataframe.SubstituteListParams(pipeline, listEnv),
		env, unbound,
	)

	result, evErr := dataframe.EvalPipeline(bound, input)
	if evErr != nil {
		return dataframe.Table{}, evErr
	}
	return result, nil
}

// resolveTransformParams resolves each Transform param's `from` binding to a
// scalar cell (the env fed to scalar substitution), to a LIST of cells (the
// separate env fed to list substitution), or marks it unbound (no host value, no
// default → its filter step is pruned). A param resolving to neither a scalar
// nor a list of scalars is an error (the whole transform renders absence).
//
// A param's source resolving to an ARRAY is a LIST param (the multi-select chip's
// selection). Two rules govern it, and both are the wire format's rather than
// this host's:
//
//   - It never enters the SCALAR env. It resolves by substitution through
//     [dataframe.SubstituteListParams], the two envs staying disjoint so a kind
//     mismatch in either direction substitutes nothing and reaches the strict
//     unbound-param refusal instead of a silently wrong scoping.
//   - An EMPTY selection is UNBOUND, never a list of no items. Nothing selected
//     is the absence of a constraint, not a constraint no row satisfies, so it
//     takes the same lenient prune an unset scalar chip already gets and the
//     static render shows the UNFILTERED table. The distinction is visible in the
//     output — an empty membership set would render zero rows — which is why it
//     is decided here rather than left to the substitution.
func resolveTransformParams(
	paramsVal wire.Value,
	sources BindingSources,
) (map[string]dataframe.Cell, map[string][]dataframe.Cell, map[string]bool, error) {
	env := map[string]dataframe.Cell{}
	listEnv := map[string][]dataframe.Cell{}
	unbound := map[string]bool{}
	arr, ok := paramsVal.(wire.Arr)
	if !ok {
		return env, listEnv, unbound, nil
	}
	for _, item := range arr {
		p, ok := item.(wire.Obj)
		if !ok {
			continue
		}
		name, ok := p.Fields["name"].(wire.Str)
		if !ok {
			continue
		}
		resolved := resolveBinding(p.Fields["from"], sources)
		if resolved == nil {
			unbound[string(name)] = true
			continue
		}
		if cell, ok := valueToCell(resolved); ok {
			env[string(name)] = cell
			continue
		}
		cells, ok := valueToCells(resolved)
		if !ok {
			return nil, nil, nil, fmt.Errorf("transform param %q resolved to a non-scalar value", string(name))
		}
		if len(cells) == 0 {
			unbound[string(name)] = true // deselected to empty ⇒ no constraint
			continue
		}
		listEnv[string(name)] = cells
	}
	return env, listEnv, unbound, nil
}

// valueToCells coerces a resolved ARRAY of scalars to evaluator cells, preserving
// order. A non-array, or an array holding a structured item, has no membership
// reading (ok=false ⇒ a param error), so a half-readable selection is refused
// rather than silently truncated to the items that happened to coerce.
func valueToCells(v wire.Value) ([]dataframe.Cell, bool) {
	arr, ok := v.(wire.Arr)
	if !ok {
		return nil, false
	}
	cells := make([]dataframe.Cell, len(arr))
	for i, item := range arr {
		cell, ok := valueToCell(item)
		if !ok {
			return nil, false
		}
		cells[i] = cell
	}
	return cells, true
}

// valueToCell coerces a resolved scalar wire value to an evaluator Cell; a
// structured (Arr / Obj) value has no scalar form (ok=false ⇒ a param error).
func valueToCell(v wire.Value) (dataframe.Cell, bool) {
	switch t := v.(type) {
	case wire.Str:
		return dataframe.CellStr(string(t)), true
	case wire.Int:
		return dataframe.CellInt(int64(t)), true
	case wire.Float:
		return dataframe.CellFloat(float64(t)), true
	case wire.Bool:
		return dataframe.CellBool(bool(t)), true
	case wire.Null:
		return dataframe.Null, true
	}
	return dataframe.Cell{}, false
}

// tableToRows projects an evaluated table into wire-shaped row objects (one
// tag-less wire.Obj per row, column-keyed) — the shape a data-bearing node's
// source slot consumes.
func tableToRows(table dataframe.Table) wire.Value {
	n := 0
	if len(table.Columns) > 0 {
		n = len(table.Columns[0].Cells)
	}
	rows := make(wire.Arr, n)
	for i := 0; i < n; i++ {
		fields := make(map[string]wire.Value, len(table.Columns))
		for _, col := range table.Columns {
			c := dataframe.Null
			if i < len(col.Cells) {
				c = col.Cells[i]
			}
			fields[col.Name] = cellToWire(c)
		}
		rows[i] = wire.Obj{Fields: fields}
	}
	return rows
}

// cellToWire boxes an evaluator Cell to a scalar wire value (a null cell → the
// JSON null).
func cellToWire(c dataframe.Cell) wire.Value {
	switch c.Kind {
	case dataframe.TypeInt:
		return wire.Int(c.Value.(int64))
	case dataframe.TypeFloat:
		return wire.Float(c.Value.(float64))
	case dataframe.TypeBool:
		return wire.Bool(c.Value.(bool))
	case dataframe.TypeString, dataframe.TypeDate, dataframe.TypeTimestamp:
		return wire.Str(c.Value.(string))
	}
	return wire.Null{}
}

// scalarOutcome is the result of interpreting a scalar-slot Transform's table
// under the Phase 632 1×1 law.
type scalarOutcome int

const (
	scalarResolved scalarOutcome = iota // a single non-null cell (or the trailing-count completion)
	scalarEmpty                         // an unresolved / empty slot — renders absence
	scalarError                         // ambiguous (>1×1) or a failed pipeline — loud, never a silent first cell
)

// evalScalarTransform evaluates a `Transform` binding in a SCALAR slot to its
// single result cell under the Phase 632 1×1 law: exactly one row × one column
// resolves (a non-null cell); ambiguity (>1 row or >1 column) is a loud miss
// (scalarError — never a silent first cell); an empty result renders absence
// (scalarEmpty), EXCEPT a trailing global single-`count` groupBy over an empty
// frame, which resolves 0 (the count of nothing is 0).
func evalScalarTransform(t wire.Obj, sources BindingSources) (wire.Value, scalarOutcome) {
	table, err := evalTransformFrame(t, sources)
	if err != nil {
		return nil, scalarError
	}
	cols := len(table.Columns)
	rows := 0
	if cols > 0 {
		rows = len(table.Columns[0].Cells)
	}
	if rows == 1 && cols == 1 {
		cell := table.Columns[0].Cells[0]
		if cell.Kind == dataframe.Null.Kind {
			return nil, scalarEmpty
		}
		return cellToWire(cell), scalarResolved
	}
	if rows == 0 {
		if trailingGlobalCount(t) {
			return wire.Int(0), scalarResolved
		}
		return nil, scalarEmpty
	}
	return nil, scalarError
}

// trailingGlobalCount reports a pipeline ending in a global single-`count`
// groupBy (keys [], one count agg) — the terminal whose empty-frame result the
// host completes to 0.
func trailingGlobalCount(t wire.Obj) bool {
	pipe, ok := t.Fields["pipeline"].(wire.Arr)
	if !ok || len(pipe) == 0 {
		return false
	}
	last, ok := pipe[len(pipe)-1].(wire.Obj)
	if !ok || last.Tag != "groupBy" {
		return false
	}
	keys, ok := last.Fields["keys"].(wire.Arr)
	if !ok || len(keys) != 0 {
		return false
	}
	aggs, ok := last.Fields["aggs"].(wire.Arr)
	if !ok || len(aggs) != 1 {
		return false
	}
	agg, ok := aggs[0].(wire.Obj)
	if !ok {
		return false
	}
	return agg.Fields["fn"] == wire.Str("count")
}

// resolveScalarText resolves a text-slot binding to a plain string, or
// ("", false) when unresolved / ambiguous / empty (the caller renders ""). A
// `Transform` yields its 1×1 result cell as text (never the rows list); every
// other binding resolves via resolveBinding then stringifies — so a
// `Selection.defaultValue` in a text slot renders resolved (Phase 629).
func resolveScalarText(binding wire.Value, sources BindingSources) (string, bool) {
	if e, ok := exprBinding(binding); ok {
		// Phase 1534 — the scalar expression, through the same 1x1 law.
		cell, outcome := evalScalarTransform(e, sources)
		if outcome == scalarResolved {
			return displayString(cell), true
		}
		return "", false
	}
	if t, ok := transformBinding(binding); ok {
		cell, outcome := evalScalarTransform(t, sources)
		if outcome == scalarResolved {
			return displayString(cell), true
		}
		return "", false
	}
	if v := resolveBinding(binding, sources); v != nil {
		return displayString(v), true
	}
	return "", false
}

// resolveScalarBool resolves a boolean-slot binding to (value, true), or
// (false, false) when unresolved / ambiguous / non-boolean (Phase 1535).
//
// The third of the trio beside resolveScalarText / resolveScalarNumber, so a
// Transform or an Expr reaches a boolean slot through the same 1x1 seam a text
// or numeric one does.
//
// STRICT: only a genuine boolean resolves. 0, "" and "false" are all refused
// rather than read as false, because every language that has guessed at
// truthiness has guessed differently and five hosts agreeing on a rendering is
// the whole point of the corpus. The vocabulary already carries the total
// spellings (isNull, =, not), so refusing costs an author nothing but the
// explicit operator.
func resolveScalarBool(binding wire.Value, sources BindingSources) (bool, bool) {
	if e, ok := exprBinding(binding); ok {
		cell, outcome := evalScalarTransform(e, sources)
		if outcome != scalarResolved {
			return false, false
		}
		b, isBool := cell.(wire.Bool)
		return bool(b), isBool
	}
	if t, ok := transformBinding(binding); ok {
		cell, outcome := evalScalarTransform(t, sources)
		if outcome != scalarResolved {
			return false, false
		}
		b, isBool := cell.(wire.Bool)
		return bool(b), isBool
	}
	b, ok := resolveBinding(binding, sources).(wire.Bool)
	return bool(b), ok
}

// ── Conditional presence and predicate branching (Phase 1535) ───────────────
//
// Two decisions a renderer takes BEFORE it draws anything, stated once here so
// every rendering surface in this package takes them identically.

// isNodeVisible is THE rule for whether a node reaches the output at all
// (WIRE_FORMAT §3.1).
//
// A node is removed ONLY on a resolved false. An absent predicate, an
// unresolved one and an errored one all RENDER, and the asymmetry is the design
// rather than a leniency: a false is an author saying "not now", and every
// other outcome is the renderer failing to answer the question. Content that
// vanishes because a source was missing is the one failure a reader cannot see,
// cannot report and cannot work around.
func isNodeVisible(node wire.Node, sources BindingSources) bool {
	raw, declared := node.Extras["visible"]
	if !declared {
		return true
	}
	value, ok := resolveScalarBool(raw, sources)
	return !ok || value
}

// selectSwitchCase is first-match-wins over BOTH kinds of case: a literal
// `match` compared against the already-resolved selector, and a `when`
// predicate evaluated here (Phase 1535).
//
// selector is the switch's resolved `on` value; selectorResolved says whether
// it resolved at all, because "" is a legal match string and would otherwise be
// indistinguishable from absence. A predicate case ignores both — which is why
// a switch whose cases are all predicates needs no selector.
//
// A predicate case is taken ONLY on a resolved true; false, unresolved and
// errored all fall through to the next case and ultimately to `default`. That
// is the OPPOSITE default from isNodeVisible, and deliberately so: falling
// through here lands on a `default` branch the author wrote, so no content
// disappears — whereas a node with no verdict has no fallback.
func selectSwitchCase(cases wire.Value, selector string, selectorResolved bool, sources BindingSources) (wire.Node, bool) {
	arr, ok := cases.(wire.Arr)
	if !ok {
		return wire.Node{}, false
	}
	for _, item := range arr {
		caseObj, ok := item.(wire.Obj)
		if !ok {
			continue
		}
		if match, hasMatch := caseObj.Fields["match"].(wire.Str); hasMatch {
			if selectorResolved && string(match) == selector {
				if child, ok := asNode(caseObj.Fields["child"]); ok {
					return child, true
				}
			}
			continue
		}
		// A case carrying neither is unreachable from the wire (the decoder
		// refuses it) and reported pre-emit; it is skipped rather than asserted
		// away because a tree built in-process can still hold one.
		when, hasWhen := caseObj.Fields["when"]
		if !hasWhen {
			continue
		}
		if value, ok := resolveScalarBool(when, sources); ok && value {
			if child, ok := asNode(caseObj.Fields["child"]); ok {
				return child, true
			}
		}
	}
	return wire.Node{}, false
}

// resolveScalarNumber resolves a numeric-slot binding (Metric / LabelValueRow
// value) to a wire numeric value, or nil when unresolved / ambiguous / empty. A
// `Transform` yields its 1×1 result cell, admitted only when numeric (a text /
// bool / date cell in a numeric slot renders absence, never a wrong number);
// every other binding resolves via resolveBinding unchanged, so non-Transform
// slots keep their established behaviour.
func resolveScalarNumber(binding wire.Value, sources BindingSources) wire.Value {
	if e, ok := exprBinding(binding); ok {
		// Phase 1534 — the scalar expression. Admitted only when numeric, on the
		// same rule the Transform arm below follows: a text / bool / date cell in
		// a numeric slot renders absence, never a wrong number.
		cell, outcome := evalScalarTransform(e, sources)
		if outcome != scalarResolved {
			return nil
		}
		switch cell.(type) {
		case wire.Int, wire.Float:
			return cell
		}
		return nil
	}
	if t, ok := transformBinding(binding); ok {
		cell, outcome := evalScalarTransform(t, sources)
		if outcome != scalarResolved {
			return nil
		}
		switch cell.(type) {
		case wire.Int, wire.Float:
			return cell
		}
		return nil
	}
	return resolveBinding(binding, sources)
}
