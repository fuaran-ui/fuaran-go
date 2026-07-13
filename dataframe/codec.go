package dataframe

import (
	"encoding/json"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Byte-exact canonical codec for the Compute-layer wire surface. Encode lowers
// the columnar/algebra trees into the generic structural wire model and hands
// them to the shared canonical encoder (wire.EncodeValue), so the Ordinal
// key-sort + cross-host float layout + escape rules match the node codec by
// construction. Decode parses with the standard library (json.Number-preserving)
// and walks the parsed JSON against the schema, surfacing the six-code
// *ColumnError envelope on any wire-shape violation.

// ── Encode ──────────────────────────────────────────────────────────────────

func typed(tag string, fields map[string]wire.Value) wire.Obj {
	return wire.Obj{Tag: tag, Fields: fields}
}

// cellValueWire is the present-value JSON payload for a cell in a column of
// type ty (a null cell becomes the type-default; an int in a float column widens).
func cellValueWire(ty string, c Cell) wire.Value {
	present := c
	if isNull(c) {
		present = defaultFor(ty)
	}
	if present.Kind == TypeInt && ty == TypeFloat {
		return wire.Float(float64(present.Value.(int64)))
	}
	return scalarWire(present)
}

func scalarWire(c Cell) wire.Value {
	switch c.Kind {
	case TypeInt:
		return wire.Int(c.Value.(int64))
	case TypeFloat:
		return wire.Float(c.Value.(float64))
	case TypeBool:
		return wire.Bool(c.Value.(bool))
	default: // string / date / timestamp
		return wire.Str(c.Value.(string))
	}
}

func columnWire(col Column) wire.Obj {
	values := make(wire.Arr, len(col.Cells))
	validity := make(wire.Arr, len(col.Cells))
	for i, c := range col.Cells {
		values[i] = cellValueWire(col.Type, c)
		validity[i] = wire.Bool(!isNull(c))
	}
	return wire.Obj{Fields: map[string]wire.Value{"values": values, "validity": validity}}
}

func schemaWire(schema Schema) wire.Arr {
	arr := make(wire.Arr, len(schema))
	for i, e := range schema {
		arr[i] = wire.Obj{Fields: map[string]wire.Value{"name": wire.Str(e.Name), "type": wire.Str(e.Type)}}
	}
	return arr
}

// EncodeSourceValue lowers a DataSource to the structural wire model.
func EncodeSourceValue(src DataSource) wire.Value {
	switch s := src.(type) {
	case Embedded:
		t := s.Table
		columns := make(map[string]wire.Value)
		for _, e := range t.Schema {
			col := Column{Name: e.Name, Type: TypeString}
			for _, c := range t.Columns {
				if c.Name == e.Name {
					col = c
					break
				}
			}
			columns[e.Name] = columnWire(col)
		}
		return wire.Obj{Fields: map[string]wire.Value{"schema": schemaWire(t.Schema), "columns": wire.Obj{Fields: columns}}}
	case Ref:
		return wire.Obj{Fields: map[string]wire.Value{"schema": wire.Arr{}, "ref": wire.Str(s.Name)}}
	}
	return wire.Null{}
}

// EncodeSource is the canonical wire string for a DataSource.
func EncodeSource(src DataSource) (string, error) {
	return wire.EncodeValue(EncodeSourceValue(src))
}

var litTag = map[string]string{
	TypeInt: "Int", TypeFloat: "Float", TypeBool: "Bool",
	TypeString: "Str", TypeDate: "Date", TypeTimestamp: "Timestamp",
}

func cellLiteralWire(c Cell) wire.Obj {
	if c.Kind == "null" {
		return typed("Null", map[string]wire.Value{})
	}
	return typed(litTag[c.Kind], map[string]wire.Value{"value": scalarWire(c)})
}

// EncodeExprValue lowers a ColExpr to the structural wire model.
func EncodeExprValue(e ColExpr) wire.Value {
	switch x := e.(type) {
	case Col:
		return typed("col", map[string]wire.Value{"name": wire.Str(x.Name)})
	case Lit:
		return typed("lit", map[string]wire.Value{"cell": cellLiteralWire(x.Cell)})
	case Binary:
		return typed("binary", map[string]wire.Value{"op": wire.Str(x.Op), "left": EncodeExprValue(x.Left), "right": EncodeExprValue(x.Right)})
	case Not:
		return typed("not", map[string]wire.Value{"expr": EncodeExprValue(x.Expr)})
	case Coalesce:
		exprs := make(wire.Arr, len(x.Exprs))
		for i, e := range x.Exprs {
			exprs[i] = EncodeExprValue(e)
		}
		return typed("coalesce", map[string]wire.Value{"exprs": exprs})
	case Case:
		cases := make(wire.Arr, len(x.Cases))
		for i, wt := range x.Cases {
			cases[i] = wire.Obj{Fields: map[string]wire.Value{"when": EncodeExprValue(wt.When), "then": EncodeExprValue(wt.Then)}}
		}
		return typed("case", map[string]wire.Value{"cases": cases, "else": EncodeExprValue(x.ElseExpr)})
	case Cast:
		return typed("cast", map[string]wire.Value{"type": wire.Str(x.Type), "expr": EncodeExprValue(x.Expr)})
	case ApplyFn:
		args := make(wire.Arr, len(x.Args))
		for i, a := range x.Args {
			args[i] = EncodeExprValue(a)
		}
		return typed("apply", map[string]wire.Value{"fn": wire.Str(x.Fn), "args": args})
	case Param:
		return typed("param", map[string]wire.Value{"name": wire.Str(x.Name)})
	}
	return wire.Null{}
}

func pairWire(p Pair) wire.Obj {
	return wire.Obj{Fields: map[string]wire.Value{"a": wire.Str(p.A), "b": wire.Str(p.B)}}
}
func orderWire(o OrderKey) wire.Obj {
	return wire.Obj{Fields: map[string]wire.Value{"col": wire.Str(o.Col), "dir": wire.Str(o.Dir)}}
}
func strArr(xs []string) wire.Arr {
	arr := make(wire.Arr, len(xs))
	for i, s := range xs {
		arr[i] = wire.Str(s)
	}
	return arr
}

// EncodeTransformValue lowers a Transform to the structural wire model.
func EncodeTransformValue(t Transform) wire.Value {
	switch v := t.(type) {
	case Filter:
		return typed("filter", map[string]wire.Value{"pred": EncodeExprValue(v.Pred)})
	case Project:
		cols := make(wire.Arr, len(v.Cols))
		for i, p := range v.Cols {
			cols[i] = pairWire(p)
		}
		return typed("project", map[string]wire.Value{"cols": cols})
	case Derive:
		return typed("derive", map[string]wire.Value{"name": wire.Str(v.Name), "expr": EncodeExprValue(v.Expr)})
	case GroupBy:
		aggs := make(wire.Arr, len(v.Aggs))
		for i, a := range v.Aggs {
			aggs[i] = wire.Obj{Fields: map[string]wire.Value{"name": wire.Str(a.Name), "fn": wire.Str(a.Fn), "of": wire.Str(a.Of)}}
		}
		return typed("groupBy", map[string]wire.Value{"keys": strArr(v.Keys), "aggs": aggs})
	case Join:
		on := make(wire.Arr, len(v.On))
		for i, p := range v.On {
			on[i] = pairWire(p)
		}
		return typed("join", map[string]wire.Value{"source": EncodeSourceValue(v.Source), "on": on, "how": wire.Str(v.How)})
	case Window:
		s := v.Spec
		ob := make(wire.Arr, len(s.OrderBy))
		for i, o := range s.OrderBy {
			ob[i] = orderWire(o)
		}
		return typed("window", map[string]wire.Value{
			"partitionBy": strArr(s.PartitionBy), "orderBy": ob, "fn": wire.Str(s.Fn), "of": wire.Str(s.Of), "as": wire.Str(s.As),
		})
	case Pivot:
		s := v.Spec
		return typed("pivot", map[string]wire.Value{"index": strArr(s.Index), "on": wire.Str(s.On), "values": wire.Str(s.Values), "agg": wire.Str(s.Agg)})
	case Unpivot:
		return typed("unpivot", map[string]wire.Value{"idVars": strArr(v.IDVars), "valueVars": strArr(v.ValueVars)})
	case Sort:
		by := make(wire.Arr, len(v.By))
		for i, o := range v.By {
			by[i] = orderWire(o)
		}
		return typed("sort", map[string]wire.Value{"by": by})
	case Distinct:
		return typed("distinct", map[string]wire.Value{})
	case Limit:
		return typed("limit", map[string]wire.Value{"n": wire.Int(int64(v.N)), "offset": wire.Int(int64(v.Offset))})
	case Union:
		return typed("union", map[string]wire.Value{"source": EncodeSourceValue(v.Source)})
	}
	return wire.Null{}
}

// EncodePipeline is the canonical wire string for an ordered pipeline.
func EncodePipeline(pipeline []Transform) (string, error) {
	arr := make(wire.Arr, len(pipeline))
	for i, t := range pipeline {
		arr[i] = EncodeTransformValue(t)
	}
	return wire.EncodeValue(arr)
}

// ── Decode ──────────────────────────────────────────────────────────────────

func cerr(code, detail string) *ColumnError { return &ColumnError{Code: code, Detail: detail} }

func parseJSON(text string) (any, *ColumnError) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, cerr(NotJSON, err.Error())
	}
	return raw, nil
}

func isIntToken(n json.Number) bool { return !strings.ContainsAny(string(n), ".eE") }

func field(obj any, key string) (any, *ColumnError) {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil, cerr(MalformedShape, "expected object")
	}
	v, ok := m[key]
	if !ok {
		return nil, cerr(MissingField, key)
	}
	return v, nil
}

func kindOf(obj any) (string, *ColumnError) {
	v, e := field(obj, "$type")
	if e != nil {
		return "", e
	}
	s, ok := v.(string)
	if !ok {
		return "", cerr(MalformedShape, "$type must be a string")
	}
	return s, nil
}

func decodeCellValue(col, ty string, v any) (Cell, *ColumnError) {
	switch ty {
	case TypeInt:
		if n, ok := v.(json.Number); ok && isIntToken(n) {
			i, _ := n.Int64()
			return CellInt(i), nil
		}
	case TypeFloat:
		if n, ok := v.(json.Number); ok {
			f, _ := n.Float64()
			return CellFloat(f), nil
		}
	case TypeBool:
		if b, ok := v.(bool); ok {
			return CellBool(b), nil
		}
	case TypeString:
		if s, ok := v.(string); ok {
			return CellStr(s), nil
		}
	case TypeDate:
		if s, ok := v.(string); ok {
			return CellDate(s), nil
		}
	case TypeTimestamp:
		if s, ok := v.(string); ok {
			return CellTimestamp(s), nil
		}
	}
	return Cell{}, cerr(TypeMismatch, "column '"+col+"': expected "+ty+" value")
}

func decodeSchema(el any) (Schema, *ColumnError) {
	arr, ok := el.([]any)
	if !ok {
		return nil, cerr(MalformedShape, "schema: expected array")
	}
	out := make(Schema, 0, len(arr))
	for _, entry := range arr {
		nameV, e := field(entry, "name")
		if e != nil {
			return nil, e
		}
		typeV, e := field(entry, "type")
		if e != nil {
			return nil, e
		}
		name, ok1 := nameV.(string)
		ty, ok2 := typeV.(string)
		if !ok1 {
			return nil, cerr(MalformedShape, "schema.name: expected string")
		}
		if !ok2 || !columnTypes[ty] {
			return nil, cerr(UnknownType, "unknown column type")
		}
		out = append(out, SchemaEntry{Name: name, Type: ty})
	}
	return out, nil
}

func decodeColumn(columnsObj any, name, ty string) (Column, *ColumnError) {
	m, ok := columnsObj.(map[string]any)
	if !ok {
		return Column{}, cerr(MissingField, "columns."+name)
	}
	colEl, ok := m[name]
	if !ok {
		return Column{}, cerr(MissingField, "columns."+name)
	}
	valuesV, e := field(colEl, "values")
	if e != nil {
		return Column{}, e
	}
	validityV, e := field(colEl, "validity")
	if e != nil {
		return Column{}, e
	}
	values, ok1 := valuesV.([]any)
	validity, ok2 := validityV.([]any)
	if !ok1 {
		return Column{}, cerr(MalformedShape, name+".values: expected array")
	}
	if !ok2 {
		return Column{}, cerr(MalformedShape, name+".validity: expected array")
	}
	if len(values) != len(validity) {
		return Column{}, cerr(LengthMismatch, "column '"+name+"': values/validity length mismatch")
	}
	cells := make([]Cell, len(values))
	for i := range values {
		present, ok := validity[i].(bool)
		if !ok {
			return Column{}, cerr(MalformedShape, name+".validity: expected bool")
		}
		if !present {
			cells[i] = Null
			continue
		}
		c, ce := decodeCellValue(name, ty, values[i])
		if ce != nil {
			return Column{}, ce
		}
		cells[i] = c
	}
	return Column{Name: name, Type: ty, Cells: cells}, nil
}

func decodeSourceJSON(el any) (DataSource, *ColumnError) {
	schemaV, e := field(el, "schema")
	if e != nil {
		return nil, e
	}
	schema, e := decodeSchema(schemaV)
	if e != nil {
		return nil, e
	}
	if m, ok := el.(map[string]any); ok {
		if refV, ok := m["ref"]; ok {
			ref, ok := refV.(string)
			if !ok {
				return nil, cerr(MalformedShape, "ref: expected string")
			}
			return Ref{Name: ref}, nil
		}
	}
	columnsV, e := field(el, "columns")
	if e != nil {
		return nil, e
	}
	cols := make([]Column, 0, len(schema))
	for _, se := range schema {
		col, ce := decodeColumn(columnsV, se.Name, se.Type)
		if ce != nil {
			return nil, ce
		}
		cols = append(cols, col)
	}
	return Embedded{Table: Table{Schema: schema, Columns: cols}}, nil
}

// DecodeSource decodes a canonical DataSource wire string.
func DecodeSource(text string) (DataSource, *ColumnError) {
	raw, e := parseJSON(text)
	if e != nil {
		return nil, e
	}
	return decodeSourceJSON(raw)
}

func decodeCellLiteral(el any) (Cell, *ColumnError) {
	tag, e := kindOf(el)
	if e != nil {
		return Cell{}, e
	}
	if tag == "Null" {
		return Null, nil
	}
	m := el.(map[string]any)
	v := m["value"]
	switch tag {
	case "Int":
		if n, ok := v.(json.Number); ok && isIntToken(n) {
			i, _ := n.Int64()
			return CellInt(i), nil
		}
	case "Float":
		if n, ok := v.(json.Number); ok {
			f, _ := n.Float64()
			return CellFloat(f), nil
		}
	case "Bool":
		if b, ok := v.(bool); ok {
			return CellBool(b), nil
		}
	case "Str":
		if s, ok := v.(string); ok {
			return CellStr(s), nil
		}
	case "Date":
		if s, ok := v.(string); ok {
			return CellDate(s), nil
		}
	case "Timestamp":
		if s, ok := v.(string); ok {
			return CellTimestamp(s), nil
		}
	}
	return Cell{}, cerr(TypeMismatch, "lit: bad value for "+tag)
}

// DecodeExpr decodes a ColExpr from a parsed value.
func DecodeExpr(el any) (ColExpr, *ColumnError) {
	k, e := kindOf(el)
	if e != nil {
		return nil, e
	}
	m := el.(map[string]any)
	switch k {
	case "col":
		v, e := field(el, "name")
		if e != nil {
			return nil, e
		}
		s, ok := v.(string)
		if !ok {
			return nil, cerr(MalformedShape, "col.name: expected string")
		}
		return Col{Name: s}, nil
	case "lit":
		v, e := field(el, "cell")
		if e != nil {
			return nil, e
		}
		c, ce := decodeCellLiteral(v)
		if ce != nil {
			return nil, ce
		}
		return Lit{Cell: c}, nil
	case "binary":
		opV, e := field(el, "op")
		if e != nil {
			return nil, e
		}
		op, _ := opV.(string)
		if !binOps[op] {
			return nil, cerr(UnknownType, "unknown binary op '"+op+"'")
		}
		leftV, e := field(el, "left")
		if e != nil {
			return nil, e
		}
		left, ce := DecodeExpr(leftV)
		if ce != nil {
			return nil, ce
		}
		rightV, e := field(el, "right")
		if e != nil {
			return nil, e
		}
		right, ce := DecodeExpr(rightV)
		if ce != nil {
			return nil, ce
		}
		return Binary{Op: op, Left: left, Right: right}, nil
	case "not":
		v, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		inner, ce := DecodeExpr(v)
		if ce != nil {
			return nil, ce
		}
		return Not{Expr: inner}, nil
	case "coalesce":
		v, e := field(el, "exprs")
		if e != nil {
			return nil, e
		}
		xs, ce := decodeExprList(v)
		if ce != nil {
			return nil, ce
		}
		return Coalesce{Exprs: xs}, nil
	case "case":
		casesV, e := field(el, "cases")
		if e != nil {
			return nil, e
		}
		arr, ok := casesV.([]any)
		if !ok {
			return nil, cerr(MalformedShape, "case.cases: expected array")
		}
		var pairs []WhenThen
		for _, c := range arr {
			whenV, e := field(c, "when")
			if e != nil {
				return nil, e
			}
			when, ce := DecodeExpr(whenV)
			if ce != nil {
				return nil, ce
			}
			thenV, e := field(c, "then")
			if e != nil {
				return nil, e
			}
			then, ce := DecodeExpr(thenV)
			if ce != nil {
				return nil, ce
			}
			pairs = append(pairs, WhenThen{When: when, Then: then})
		}
		elseV, e := field(el, "else")
		if e != nil {
			return nil, e
		}
		elseE, ce := DecodeExpr(elseV)
		if ce != nil {
			return nil, ce
		}
		return Case{Cases: pairs, ElseExpr: elseE}, nil
	case "cast":
		tyV, e := field(el, "type")
		if e != nil {
			return nil, e
		}
		ty, _ := tyV.(string)
		if !columnTypes[ty] {
			return nil, cerr(UnknownType, "unknown cast type '"+ty+"'")
		}
		exprV, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		inner, ce := DecodeExpr(exprV)
		if ce != nil {
			return nil, ce
		}
		return Cast{Type: ty, Expr: inner}, nil
	case "apply":
		fnV, e := field(el, "fn")
		if e != nil {
			return nil, e
		}
		fn, _ := fnV.(string)
		if !scalarFns[fn] {
			return nil, cerr(UnknownType, "unknown scalar fn '"+fn+"'")
		}
		argsV, e := field(el, "args")
		if e != nil {
			return nil, e
		}
		xs, ce := decodeExprList(argsV)
		if ce != nil {
			return nil, ce
		}
		return ApplyFn{Fn: fn, Args: xs}, nil
	case "param":
		v, e := field(el, "name")
		if e != nil {
			return nil, e
		}
		s, ok := v.(string)
		if !ok {
			return nil, cerr(MalformedShape, "param.name: expected string")
		}
		return Param{Name: s}, nil
	}
	_ = m
	return nil, cerr(UnknownType, "unknown ColExpr '"+k+"'")
}

func decodeExprList(el any) ([]ColExpr, *ColumnError) {
	arr, ok := el.([]any)
	if !ok {
		return nil, cerr(MalformedShape, "expected array of expressions")
	}
	out := make([]ColExpr, 0, len(arr))
	for _, x := range arr {
		e, ce := DecodeExpr(x)
		if ce != nil {
			return nil, ce
		}
		out = append(out, e)
	}
	return out, nil
}

func strList(el any, ctx string) ([]string, *ColumnError) {
	arr, ok := el.([]any)
	if !ok {
		return nil, cerr(MalformedShape, ctx+": expected array of strings")
	}
	out := make([]string, len(arr))
	for i, x := range arr {
		s, ok := x.(string)
		if !ok {
			return nil, cerr(MalformedShape, ctx+": expected array of strings")
		}
		out[i] = s
	}
	return out, nil
}

func pairOf(el any) (Pair, *ColumnError) {
	aV, e := field(el, "a")
	if e != nil {
		return Pair{}, e
	}
	bV, e := field(el, "b")
	if e != nil {
		return Pair{}, e
	}
	a, ok1 := aV.(string)
	b, ok2 := bV.(string)
	if !ok1 || !ok2 {
		return Pair{}, cerr(MalformedShape, "pair: expected strings")
	}
	return Pair{A: a, B: b}, nil
}

func orderOf(el any) (OrderKey, *ColumnError) {
	colV, e := field(el, "col")
	if e != nil {
		return OrderKey{}, e
	}
	dirV, e := field(el, "dir")
	if e != nil {
		return OrderKey{}, e
	}
	dir, _ := dirV.(string)
	if !sortDirs[dir] {
		dir = "asc"
	}
	col, ok := colV.(string)
	if !ok {
		return OrderKey{}, cerr(MalformedShape, "order.col: expected string")
	}
	return OrderKey{Col: col, Dir: dir}, nil
}

func pairList(el any, ctx string) ([]Pair, *ColumnError) {
	arr, ok := el.([]any)
	if !ok {
		return nil, cerr(MalformedShape, ctx+": expected array")
	}
	out := make([]Pair, 0, len(arr))
	for _, x := range arr {
		p, ce := pairOf(x)
		if ce != nil {
			return nil, ce
		}
		out = append(out, p)
	}
	return out, nil
}

func orderList(el any, ctx string) ([]OrderKey, *ColumnError) {
	arr, ok := el.([]any)
	if !ok {
		return nil, cerr(MalformedShape, ctx+": expected array")
	}
	out := make([]OrderKey, 0, len(arr))
	for _, x := range arr {
		o, ce := orderOf(x)
		if ce != nil {
			return nil, ce
		}
		out = append(out, o)
	}
	return out, nil
}

func aggOf(el any) (Agg, *ColumnError) {
	nameV, e := field(el, "name")
	if e != nil {
		return Agg{}, e
	}
	fnV, e := field(el, "fn")
	if e != nil {
		return Agg{}, e
	}
	ofV, e := field(el, "of")
	if e != nil {
		return Agg{}, e
	}
	fn, _ := fnV.(string)
	if !aggFns[fn] {
		return Agg{}, cerr(UnknownType, "unknown agg fn '"+fn+"'")
	}
	name, ok1 := nameV.(string)
	of, ok2 := ofV.(string)
	if !ok1 || !ok2 {
		return Agg{}, cerr(MalformedShape, "agg: expected strings")
	}
	return Agg{Name: name, Fn: fn, Of: of}, nil
}

// DecodeTransform decodes a Transform from a parsed value.
func DecodeTransform(el any) (Transform, *ColumnError) {
	k, e := kindOf(el)
	if e != nil {
		return nil, e
	}
	switch k {
	case "filter":
		v, e := field(el, "pred")
		if e != nil {
			return nil, e
		}
		pred, ce := DecodeExpr(v)
		if ce != nil {
			return nil, ce
		}
		return Filter{Pred: pred}, nil
	case "project":
		v, e := field(el, "cols")
		if e != nil {
			return nil, e
		}
		ps, ce := pairList(v, "project.cols")
		if ce != nil {
			return nil, ce
		}
		return Project{Cols: ps}, nil
	case "derive":
		nameV, e := field(el, "name")
		if e != nil {
			return nil, e
		}
		exprV, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		expr, ce := DecodeExpr(exprV)
		if ce != nil {
			return nil, ce
		}
		name, ok := nameV.(string)
		if !ok {
			return nil, cerr(MalformedShape, "derive.name: expected string")
		}
		return Derive{Name: name, Expr: expr}, nil
	case "groupBy":
		keysV, e := field(el, "keys")
		if e != nil {
			return nil, e
		}
		keys, ce := strList(keysV, "groupBy.keys")
		if ce != nil {
			return nil, ce
		}
		aggsV, e := field(el, "aggs")
		if e != nil {
			return nil, e
		}
		arr, ok := aggsV.([]any)
		if !ok {
			return nil, cerr(MalformedShape, "groupBy.aggs: expected array")
		}
		aggs := make([]Agg, 0, len(arr))
		for _, x := range arr {
			a, ce := aggOf(x)
			if ce != nil {
				return nil, ce
			}
			aggs = append(aggs, a)
		}
		return GroupBy{Keys: keys, Aggs: aggs}, nil
	case "join":
		srcV, e := field(el, "source")
		if e != nil {
			return nil, e
		}
		src, ce := decodeSourceJSON(srcV)
		if ce != nil {
			return nil, ce
		}
		onV, e := field(el, "on")
		if e != nil {
			return nil, e
		}
		on, ce := pairList(onV, "join.on")
		if ce != nil {
			return nil, ce
		}
		howV, e := field(el, "how")
		if e != nil {
			return nil, e
		}
		how, _ := howV.(string)
		if !joinKinds[how] {
			return nil, cerr(UnknownType, "unknown join kind '"+how+"'")
		}
		return Join{Source: src, On: on, How: how}, nil
	case "window":
		pbV, e := field(el, "partitionBy")
		if e != nil {
			return nil, e
		}
		pb, ce := strList(pbV, "window.partitionBy")
		if ce != nil {
			return nil, ce
		}
		obV, e := field(el, "orderBy")
		if e != nil {
			return nil, e
		}
		ob, ce := orderList(obV, "window.orderBy")
		if ce != nil {
			return nil, ce
		}
		fnV, e := field(el, "fn")
		if e != nil {
			return nil, e
		}
		fn, _ := fnV.(string)
		if !windowFns[fn] {
			return nil, cerr(UnknownType, "unknown window fn '"+fn+"'")
		}
		ofV, e := field(el, "of")
		if e != nil {
			return nil, e
		}
		asV, e := field(el, "as")
		if e != nil {
			return nil, e
		}
		return Window{Spec: WindowSpec{PartitionBy: pb, OrderBy: ob, Fn: fn, Of: str(ofV), As: str(asV)}}, nil
	case "pivot":
		indexV, e := field(el, "index")
		if e != nil {
			return nil, e
		}
		index, ce := strList(indexV, "pivot.index")
		if ce != nil {
			return nil, ce
		}
		onV, e := field(el, "on")
		if e != nil {
			return nil, e
		}
		valsV, e := field(el, "values")
		if e != nil {
			return nil, e
		}
		aggV, e := field(el, "agg")
		if e != nil {
			return nil, e
		}
		agg, _ := aggV.(string)
		if !aggFns[agg] {
			return nil, cerr(UnknownType, "unknown agg fn '"+agg+"'")
		}
		return Pivot{Spec: PivotSpec{Index: index, On: str(onV), Values: str(valsV), Agg: agg}}, nil
	case "unpivot":
		idV, e := field(el, "idVars")
		if e != nil {
			return nil, e
		}
		idv, ce := strList(idV, "unpivot.idVars")
		if ce != nil {
			return nil, ce
		}
		vvV, e := field(el, "valueVars")
		if e != nil {
			return nil, e
		}
		vv, ce := strList(vvV, "unpivot.valueVars")
		if ce != nil {
			return nil, ce
		}
		return Unpivot{IDVars: idv, ValueVars: vv}, nil
	case "sort":
		v, e := field(el, "by")
		if e != nil {
			return nil, e
		}
		by, ce := orderList(v, "sort.by")
		if ce != nil {
			return nil, ce
		}
		return Sort{By: by}, nil
	case "distinct":
		return Distinct{}, nil
	case "limit":
		nV, e := field(el, "n")
		if e != nil {
			return nil, e
		}
		offV, e := field(el, "offset")
		if e != nil {
			return nil, e
		}
		n, ok1 := intToken(nV)
		off, ok2 := intToken(offV)
		if !ok1 || !ok2 {
			return nil, cerr(MalformedShape, "limit: expected ints")
		}
		return Limit{N: int(n), Offset: int(off)}, nil
	case "union":
		v, e := field(el, "source")
		if e != nil {
			return nil, e
		}
		src, ce := decodeSourceJSON(v)
		if ce != nil {
			return nil, ce
		}
		return Union{Source: src}, nil
	}
	return nil, cerr(UnknownType, "unknown Transform '"+k+"'")
}

func str(v any) string { s, _ := v.(string); return s }

func intToken(v any) (int64, bool) {
	if n, ok := v.(json.Number); ok && isIntToken(n) {
		i, _ := n.Int64()
		return i, true
	}
	return 0, false
}

// DecodePipeline decodes a canonical pipeline wire string.
func DecodePipeline(text string) ([]Transform, *ColumnError) {
	raw, e := parseJSON(text)
	if e != nil {
		return nil, e
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, cerr(MalformedShape, "pipeline: expected a JSON array of transform steps")
	}
	out := make([]Transform, 0, len(arr))
	for _, step := range arr {
		t, ce := DecodeTransform(step)
		if ce != nil {
			return nil, ce
		}
		out = append(out, t)
	}
	return out, nil
}
