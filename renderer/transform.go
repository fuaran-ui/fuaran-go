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

// resolveScalarNumber resolves a numeric-slot binding (Metric / LabelValueRow
// value) to a wire numeric value, or nil when unresolved / ambiguous / empty. A
// `Transform` yields its 1×1 result cell, admitted only when numeric (a text /
// bool / date cell in a numeric slot renders absence, never a wrong number);
// every other binding resolves via resolveBinding unchanged, so non-Transform
// slots keep their established behaviour.
func resolveScalarNumber(binding wire.Value, sources BindingSources) wire.Value {
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
