package dataframe

import (
	"sort"
	"strings"
)

func toLower(s string) string { return strings.ToLower(s) }
func toUpper(s string) string { return strings.ToUpper(s) }

func evalFilter(f Frame, pred ColExpr) (Frame, *EvalError) {
	var kept []row
	for _, r := range f.Rows {
		c, e := evalExpr(f.Cols, r, pred)
		if e != nil {
			return Frame{}, e
		}
		if c.Kind == TypeBool && c.Value.(bool) {
			kept = append(kept, r)
		}
	}
	return Frame{Cols: f.Cols, Rows: kept}, nil
}

func evalProject(f Frame, pairs []Pair) (Frame, *EvalError) {
	type resolved struct {
		out string
		ty  string
		idx int
	}
	res := make([]resolved, 0, len(pairs))
	for _, p := range pairs {
		i := colIndex(f.Cols, p.A)
		if i < 0 {
			return Frame{}, unknownColumn(p.A, f.Cols)
		}
		res = append(res, resolved{out: p.B, ty: f.Cols[i].Type, idx: i})
	}
	cols := make(Schema, len(res))
	for i, r := range res {
		cols[i] = SchemaEntry{Name: r.out, Type: r.ty}
	}
	rows := make([]row, len(f.Rows))
	for ri, r := range f.Rows {
		nr := make(row, len(res))
		for ci, rc := range res {
			nr[ci] = r[rc.idx]
		}
		rows[ri] = nr
	}
	return Frame{Cols: cols, Rows: rows}, nil
}

func evalDerive(f Frame, name string, expr ColExpr) (Frame, *EvalError) {
	newCells := make([]Cell, len(f.Rows))
	for ri, r := range f.Rows {
		c, e := evalExpr(f.Cols, r, expr)
		if e != nil {
			return Frame{}, e
		}
		newCells[ri] = c
	}
	ty := inferType(newCells)
	i := colIndex(f.Cols, name)
	if i >= 0 {
		cols := append(Schema(nil), f.Cols...)
		cols[i] = SchemaEntry{Name: cols[i].Name, Type: ty}
		rows := make([]row, len(f.Rows))
		for ri, r := range f.Rows {
			nr := append(row(nil), r...)
			nr[i] = newCells[ri]
			rows[ri] = nr
		}
		return Frame{Cols: cols, Rows: rows}, nil
	}
	cols := append(append(Schema(nil), f.Cols...), SchemaEntry{Name: name, Type: ty})
	rows := make([]row, len(f.Rows))
	for ri, r := range f.Rows {
		rows[ri] = append(append(row(nil), r...), newCells[ri])
	}
	return Frame{Cols: cols, Rows: rows}, nil
}

func evalGroupBy(f Frame, keys []string, aggs []Agg) (Frame, *EvalError) {
	idxs := make([]int, len(keys))
	for j, k := range keys {
		i := colIndex(f.Cols, k)
		if i < 0 {
			return Frame{}, unknownColumn(k, f.Cols)
		}
		idxs[j] = i
	}
	var order []string
	keyCells := map[string][]Cell{}
	groups := map[string][]row{}
	for _, r := range f.Rows {
		kc := make([]Cell, len(idxs))
		for j, i := range idxs {
			kc[j] = r[i]
		}
		k := rowKey(kc)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
			keyCells[k] = kc
		}
		groups[k] = append(groups[k], r)
	}
	type resolved struct {
		a   Agg
		ty  string
		idx int
	}
	res := make([]resolved, 0, len(aggs))
	for _, a := range aggs {
		ty := colType(f.Cols, a.Of)
		if ty == "" {
			return Frame{}, unknownColumn(a.Of, f.Cols)
		}
		res = append(res, resolved{a: a, ty: ty, idx: colIndex(f.Cols, a.Of)})
	}
	cols := make(Schema, 0, len(keys)+len(res))
	for _, k := range keys {
		t := colType(f.Cols, k)
		if t == "" {
			t = TypeString
		}
		cols = append(cols, SchemaEntry{Name: k, Type: t})
	}
	for _, rc := range res {
		cols = append(cols, SchemaEntry{Name: rc.a.Name, Type: aggType(rc.a.Fn, rc.ty)})
	}
	rows := make([]row, 0, len(order))
	for _, k := range order {
		grp := groups[k]
		r := append(row(nil), keyCells[k]...)
		for _, rc := range res {
			cellsOf := make([]Cell, len(grp))
			for gi, gr := range grp {
				cellsOf[gi] = gr[rc.idx]
			}
			r = append(r, aggCells(rc.a.Fn, rc.ty, cellsOf))
		}
		rows = append(rows, r)
	}
	return Frame{Cols: cols, Rows: rows}, nil
}

func evalSort(f Frame, by []OrderKey) Frame {
	rows := append([]row(nil), f.Rows...)
	sort.SliceStable(rows, func(i, j int) bool {
		return rowKeyCompare(f.Cols, by, rows[i], rows[j]) < 0
	})
	return Frame{Cols: f.Cols, Rows: rows}
}

func evalDistinct(f Frame) Frame {
	seen := map[string]bool{}
	var out []row
	for _, r := range f.Rows {
		k := rowKey(r)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return Frame{Cols: f.Cols, Rows: out}
}

func evalLimit(f Frame, n, offset int) Frame {
	start := clampInt(offset, 0, len(f.Rows))
	take := n
	if take < 0 {
		take = 0
	}
	end := clampInt(start+take, start, len(f.Rows))
	return Frame{Cols: f.Cols, Rows: f.Rows[start:end]}
}

func evalJoin(f, right Frame, on []Pair, how string) (Frame, *EvalError) {
	li := make([]int, 0, len(on))
	ri := make([]int, 0, len(on))
	for _, p := range on {
		il := colIndex(f.Cols, p.A)
		if il < 0 {
			return Frame{}, unknownColumn(p.A, f.Cols)
		}
		ir := colIndex(right.Cols, p.B)
		if ir < 0 {
			return Frame{}, unknownColumn(p.B, right.Cols)
		}
		li = append(li, il)
		ri = append(ri, ir)
	}
	keyMatch := func(lr, rr row) bool {
		for k := range li {
			if !cellEq(lr[li[k]], rr[ri[k]]) {
				return false
			}
		}
		return true
	}
	leftNames := map[string]bool{}
	for _, e := range f.Cols {
		leftNames[e.Name] = true
	}
	rightCols := make(Schema, len(right.Cols))
	for i, e := range right.Cols {
		name := e.Name
		if leftNames[name] {
			name += "_right"
		}
		rightCols[i] = SchemaEntry{Name: name, Type: e.Type}
	}
	outCols := append(append(Schema(nil), f.Cols...), rightCols...)
	leftNulls := make(row, len(f.Cols))
	rightNulls := make(row, len(right.Cols))
	for i := range leftNulls {
		leftNulls[i] = Null
	}
	for i := range rightNulls {
		rightNulls[i] = Null
	}
	var out []row
	for _, lr := range f.Rows {
		var matches []row
		for _, rr := range right.Rows {
			if keyMatch(lr, rr) {
				matches = append(matches, rr)
			}
		}
		if len(matches) == 0 {
			if how == "left" || how == "outer" {
				out = append(out, append(append(row(nil), lr...), rightNulls...))
			}
		} else {
			for _, rr := range matches {
				out = append(out, append(append(row(nil), lr...), rr...))
			}
		}
	}
	if how == "right" || how == "outer" {
		for _, rr := range right.Rows {
			matched := false
			for _, lr := range f.Rows {
				if keyMatch(lr, rr) {
					matched = true
					break
				}
			}
			if !matched {
				out = append(out, append(append(row(nil), leftNulls...), rr...))
			}
		}
	}
	return Frame{Cols: outCols, Rows: out}, nil
}

func evalUnion(f, other Frame) (Frame, *EvalError) {
	if !strSliceEq(available(f.Cols), available(other.Cols)) {
		return Frame{}, evalErr(JoinError, "union requires matching column names")
	}
	return Frame{Cols: f.Cols, Rows: append(append([]row(nil), f.Rows...), other.Rows...)}, nil
}

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func evalWindow(f Frame, spec WindowSpec) (Frame, *EvalError) {
	ofIdx := colIndex(f.Cols, spec.Of)
	if ofIdx < 0 && spec.Fn != "rowNumber" && spec.Fn != "rank" {
		return Frame{}, unknownColumn(spec.Of, f.Cols)
	}
	var partIdx []int
	for _, p := range spec.PartitionBy {
		if i := colIndex(f.Cols, p); i >= 0 {
			partIdx = append(partIdx, i)
		}
	}
	partKey := func(r row) string {
		kc := make([]Cell, len(partIdx))
		for j, i := range partIdx {
			kc[j] = r[i]
		}
		return rowKey(kc)
	}
	type indexed struct {
		orig int
		r    row
	}
	var order []string
	partitions := map[string][]indexed{}
	for i, r := range f.Rows {
		k := partKey(r)
		if _, ok := partitions[k]; !ok {
			order = append(order, k)
		}
		partitions[k] = append(partitions[k], indexed{orig: i, r: r})
	}
	valueAt := func(r row) Cell {
		if ofIdx >= 0 {
			return r[ofIdx]
		}
		return Null
	}
	computed := make([]Cell, len(f.Rows))
	for _, k := range order {
		members := append([]indexed(nil), partitions[k]...)
		sort.SliceStable(members, func(i, j int) bool {
			return rowKeyCompare(f.Cols, spec.OrderBy, members[i].r, members[j].r) < 0
		})
		vals := make([]Cell, len(members))
		for i, m := range members {
			vals[i] = valueAt(m.r)
		}
		n := len(members)
		outs := make([]Cell, n)
		switch spec.Fn {
		case "rowNumber":
			for i := range outs {
				outs[i] = CellInt(int64(i + 1))
			}
		case "rank":
			acc := int64(0)
			for i := range members {
				if i == 0 {
					acc++
				} else if rowKeyCompare(f.Cols, spec.OrderBy, members[i-1].r, members[i].r) != 0 {
					acc++
				}
				outs[i] = CellInt(acc)
			}
		case "lag":
			outs[0] = Null
			for i := 1; i < n; i++ {
				outs[i] = vals[i-1]
			}
		case "lead":
			for i := 0; i < n-1; i++ {
				outs[i] = vals[i+1]
			}
			if n > 0 {
				outs[n-1] = Null
			}
		case "cumSum":
			total := 0.0
			for i, v := range vals {
				if x, ok := asNum(v); ok {
					total += x
				}
				outs[i] = CellFloat(total)
			}
		default: // rollingMean
			for i := 0; i < n; i++ {
				lo := i - 2
				if lo < 0 {
					lo = 0
				}
				var nums []float64
				for _, v := range vals[lo : i+1] {
					if x, ok := asNum(v); ok {
						nums = append(nums, x)
					}
				}
				if len(nums) == 0 {
					outs[i] = Null
				} else {
					var s float64
					for _, x := range nums {
						s += x
					}
					outs[i] = CellFloat(s / float64(len(nums)))
				}
			}
		}
		for i, m := range members {
			computed[m.orig] = outs[i]
		}
	}
	var ty string
	switch spec.Fn {
	case "rowNumber", "rank":
		ty = TypeInt
	case "cumSum", "rollingMean":
		ty = TypeFloat
	default:
		ty = colType(f.Cols, spec.Of)
		if ty == "" {
			ty = TypeString
		}
	}
	cols := append(append(Schema(nil), f.Cols...), SchemaEntry{Name: spec.As, Type: ty})
	rows := make([]row, len(f.Rows))
	for ri, r := range f.Rows {
		rows[ri] = append(append(row(nil), r...), computed[ri])
	}
	return Frame{Cols: cols, Rows: rows}, nil
}

func evalPivot(f Frame, spec PivotSpec) (Frame, *EvalError) {
	idxIdx := make([]int, 0, len(spec.Index))
	for _, n := range spec.Index {
		i := colIndex(f.Cols, n)
		if i < 0 {
			return Frame{}, unknownColumn(n, f.Cols)
		}
		idxIdx = append(idxIdx, i)
	}
	onIdx := colIndex(f.Cols, spec.On)
	if onIdx < 0 {
		return Frame{}, unknownColumn(spec.On, f.Cols)
	}
	valIdx := colIndex(f.Cols, spec.Values)
	if valIdx < 0 {
		return Frame{}, unknownColumn(spec.Values, f.Cols)
	}
	valType := f.Cols[valIdx].Type
	indexKeyCells := func(r row) []Cell {
		kc := make([]Cell, len(idxIdx))
		for j, i := range idxIdx {
			kc[j] = r[i]
		}
		return kc
	}
	// on-values: first-seen (structural dedup), then sorted by canonical string.
	var onValues []Cell
	seenOn := map[string]bool{}
	for _, r := range f.Rows {
		c := r[onIdx]
		if !isNull(c) {
			k := cellKey(c)
			if !seenOn[k] {
				seenOn[k] = true
				onValues = append(onValues, c)
			}
		}
	}
	sort.SliceStable(onValues, func(i, j int) bool { return cellString(onValues[i]) < cellString(onValues[j]) })
	// index keys: first-appearance.
	var order [][]Cell
	seenKeys := map[string]bool{}
	for _, r := range f.Rows {
		kc := indexKeyCells(r)
		k := rowKey(kc)
		if !seenKeys[k] {
			seenKeys[k] = true
			order = append(order, kc)
		}
	}
	cols := make(Schema, 0, len(spec.Index)+len(onValues))
	for _, n := range spec.Index {
		t := colType(f.Cols, n)
		if t == "" {
			t = TypeString
		}
		cols = append(cols, SchemaEntry{Name: n, Type: t})
	}
	for _, ov := range onValues {
		cols = append(cols, SchemaEntry{Name: cellString(ov), Type: aggType(spec.Agg, valType)})
	}
	rows := make([]row, 0, len(order))
	for _, kc := range order {
		kk := rowKey(kc)
		r := append(row(nil), kc...)
		for _, ov := range onValues {
			var matching []Cell
			for _, fr := range f.Rows {
				if rowKey(indexKeyCells(fr)) == kk && cellEq(fr[onIdx], ov) {
					matching = append(matching, fr[valIdx])
				}
			}
			r = append(r, aggCells(spec.Agg, valType, matching))
		}
		rows = append(rows, r)
	}
	return Frame{Cols: cols, Rows: rows}, nil
}

func evalUnpivot(f Frame, idVars, valueVars []string) (Frame, *EvalError) {
	idIdx := make([]int, 0, len(idVars))
	for _, n := range idVars {
		i := colIndex(f.Cols, n)
		if i < 0 {
			return Frame{}, unknownColumn(n, f.Cols)
		}
		idIdx = append(idIdx, i)
	}
	valIdx := make([]int, 0, len(valueVars))
	for _, n := range valueVars {
		i := colIndex(f.Cols, n)
		if i < 0 {
			return Frame{}, unknownColumn(n, f.Cols)
		}
		valIdx = append(valIdx, i)
	}
	cols := make(Schema, 0, len(idVars)+2)
	for _, n := range idVars {
		t := colType(f.Cols, n)
		if t == "" {
			t = TypeString
		}
		cols = append(cols, SchemaEntry{Name: n, Type: t})
	}
	valType := TypeString
	for _, n := range valueVars {
		if t := colType(f.Cols, n); t != "" {
			valType = t
			break
		}
	}
	cols = append(cols, SchemaEntry{Name: "variable", Type: TypeString}, SchemaEntry{Name: "value", Type: valType})
	var rows []row
	for _, r := range f.Rows {
		idCells := make([]Cell, len(idIdx))
		for j, i := range idIdx {
			idCells[j] = r[i]
		}
		for k, vi := range valIdx {
			nr := append(append(row(nil), idCells...), CellStr(valueVars[k]), r[vi])
			rows = append(rows, nr)
		}
	}
	return Frame{Cols: cols, Rows: rows}, nil
}

// ── pipeline driver ──────────────────────────────────────────────────────────

// Resolver provides a Table for a named Ref source.
type Resolver func(name string) (Table, *EvalError)

// NoResolve refuses every Ref (embedded-only evaluation).
func NoResolve(name string) (Table, *EvalError) {
	return Table{}, evalErr(UnresolvedSource, "unresolved source ref: "+name)
}

func evalSource(resolve Resolver, src DataSource) (Table, *EvalError) {
	switch s := src.(type) {
	case Embedded:
		return s.Table, nil
	case Ref:
		return resolve(s.Name)
	}
	return Table{}, evalErr(UnresolvedSource, "unknown source")
}

func evalStep(resolve Resolver, f Frame, t Transform) (Frame, *EvalError) {
	switch v := t.(type) {
	case Filter:
		return evalFilter(f, v.Pred)
	case Project:
		return evalProject(f, v.Cols)
	case Derive:
		return evalDerive(f, v.Name, v.Expr)
	case GroupBy:
		return evalGroupBy(f, v.Keys, v.Aggs)
	case Sort:
		return evalSort(f, v.By), nil
	case Distinct:
		return evalDistinct(f), nil
	case Limit:
		return evalLimit(f, v.N, v.Offset), nil
	case Window:
		return evalWindow(f, v.Spec)
	case Pivot:
		return evalPivot(f, v.Spec)
	case Unpivot:
		return evalUnpivot(f, v.IDVars, v.ValueVars)
	case Join:
		t, e := evalSource(resolve, v.Source)
		if e != nil {
			return Frame{}, e
		}
		return evalJoin(f, toFrame(t), v.On, v.How)
	case Union:
		t, e := evalSource(resolve, v.Source)
		if e != nil {
			return Frame{}, e
		}
		return evalUnion(f, toFrame(t))
	}
	return Frame{}, evalErr(TypeError, "unknown Transform")
}

// EvalPipelineWith folds the pipeline over the input table; resolve provides
// any Ref source.
func EvalPipelineWith(resolve Resolver, pipeline []Transform, input Table) (Table, *EvalError) {
	f := toFrame(input)
	for _, step := range pipeline {
		next, e := evalStep(resolve, f, step)
		if e != nil {
			return Table{}, e
		}
		f = next
	}
	return ofFrame(f), nil
}

// EvalPipeline is the reference evaluator over embedded sources only (a Ref is
// UNRESOLVED_SOURCE).
func EvalPipeline(pipeline []Transform, input Table) (Table, *EvalError) {
	return EvalPipelineWith(NoResolve, pipeline, input)
}
