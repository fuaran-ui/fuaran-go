package dataframe

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
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
	case InList:
		items := make(wire.Arr, len(x.Items))
		for i, it := range x.Items {
			items[i] = EncodeExprValue(it)
		}
		return typed("in", map[string]wire.Value{"expr": EncodeExprValue(x.Subject), "items": items})
	case InParam:
		return typed("in", map[string]wire.Value{"expr": EncodeExprValue(x.Subject), "param": wire.Str(x.Name)})
	case IsNull:
		return typed("isNull", map[string]wire.Value{"expr": EncodeExprValue(x.Expr)})
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

func tryF(obj any, key string) (any, bool) {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := m[key]
	return v, ok
}

// fieldAliased accepts exactly one of the canonical field or its observed
// alias (the SQL/pandas prior); both present is ambiguous (didactic), neither
// reports the canonical name.
func fieldAliased(obj any, canonical, alias string) (any, *ColumnError) {
	cv, cok := tryF(obj, canonical)
	av, aok := tryF(obj, alias)
	switch {
	case cok && aok:
		return nil, cerr(MalformedShape, "give \""+canonical+"\" (canonical) or \""+alias+"\" (alias), not both")
	case cok:
		return cv, nil
	case aok:
		return av, nil
	}
	return nil, cerr(MissingField, canonical)
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

// isoOfEpochSeconds renders an epoch-seconds instant as the canonical ISO-8601
// UTC timestamp string. Pure integer arithmetic (civil-from-days), so it is
// clock-free and deterministic; negative epochs (pre-1970) are handled.
func isoOfEpochSeconds(secs int64) string {
	days := secs / 86400
	if secs%86400 < 0 {
		days--
	}
	sod := secs - days*86400
	z := days + 719468
	era := z
	if z < 0 {
		era = z - 146096
	}
	era /= 146097
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day := doy - (153*mp+2)/5 + 1
	month := mp + 3
	if mp >= 10 {
		month = mp - 9
	}
	year := yoe + era*400
	if month <= 2 {
		year++
	}
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02dZ", year, month, day, sod/3600, sod%3600/60, sod%60)
}

// epochToIso maps an epoch number to the canonical ISO instant — unit by
// magnitude: >= 1e11 means milliseconds, else seconds (epoch-seconds stay
// below 1e11 until year 5138).
func epochToIso(i int64) Cell {
	secs := i
	if i >= 100_000_000_000 || i <= -100_000_000_000 {
		secs = i / 1000
	}
	return CellTimestamp(isoOfEpochSeconds(secs))
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
		// Lenient-ingest: a timestamp column accepts an epoch number (models
		// emit epoch instants against their own correct "timestamp" schema).
		if n, ok := v.(json.Number); ok {
			if isIntToken(n) {
				i, _ := n.Int64()
				return epochToIso(i), nil
			}
			if f, err := n.Float64(); err == nil && f == math.Floor(f) && math.Abs(f) < 9e15 {
				return epochToIso(int64(f)), nil
			}
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

// columnParts reads one column's (values, validity) with the lenient-ingest
// shorthands: a BARE array is the "just the data" form (all-present validity —
// the wire has no JSON null, so a bare array can only mean every cell
// present); a wrapped object carrying values but NO validity mask is the same
// all-present statement. Absent cells still require the full wrapped form,
// which stays canonical.
func columnParts(name string, colEl any) ([]any, []any, *ColumnError) {
	if arr, ok := colEl.([]any); ok {
		validity := make([]any, len(arr))
		for i := range validity {
			validity[i] = true
		}
		return arr, validity, nil
	}
	valuesV, e := field(colEl, "values")
	if e != nil {
		return nil, nil, e
	}
	values, ok := valuesV.([]any)
	if !ok {
		return nil, nil, cerr(MalformedShape, name+".values: expected array")
	}
	validityV, hasValidity := tryF(colEl, "validity")
	if !hasValidity {
		validity := make([]any, len(values))
		for i := range validity {
			validity[i] = true
		}
		return values, validity, nil
	}
	validity, ok := validityV.([]any)
	if !ok {
		return nil, nil, cerr(MalformedShape, name+".validity: expected array")
	}
	return values, validity, nil
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
	values, validity, e := columnParts(name, colEl)
	if e != nil {
		return Column{}, e
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

// inferColumnType infers one column's type from its present cells. PINNED
// deterministic rules: all-int numerics => int, any fractional => float,
// all-bool => bool, all-string => string — never date/timestamp (temporal
// types require a declared schema). An empty column, or mixed kinds, is a
// didactic reject naming the explicit-schema remedy.
func inferColumnType(name string, values []any) (string, *ColumnError) {
	if len(values) == 0 {
		return "", cerr(MalformedShape,
			name+": cannot infer a column type from an empty / all-null column — declare it in an explicit \"schema\" array")
	}
	seen := map[string]bool{}
	for _, v := range values {
		switch t := v.(type) {
		case json.Number:
			if isIntToken(t) {
				seen["int"] = true
			} else {
				seen["float"] = true
			}
		case bool:
			seen["bool"] = true
		case string:
			seen["string"] = true
		default:
			seen["other"] = true
		}
	}
	switch {
	case len(seen) == 1 && seen["int"]:
		return TypeInt, nil
	case (len(seen) == 1 && seen["float"]) || (len(seen) == 2 && seen["int"] && seen["float"]):
		return TypeFloat, nil
	case len(seen) == 1 && seen["bool"]:
		return TypeBool, nil
	case len(seen) == 1 && seen["string"]:
		return TypeString, nil
	}
	return "", cerr(MalformedShape,
		name+": cannot infer a single column type from mixed cell kinds — declare it in an explicit \"schema\" array")
}

func decodeSourceJSON(el any) (DataSource, *ColumnError) {
	var schema Schema
	var haveSchema bool
	if schemaV, ok := tryF(el, "schema"); ok {
		s, e := decodeSchema(schemaV)
		if e != nil {
			return nil, e
		}
		schema = s
		haveSchema = true
	}
	if refV, ok := tryF(el, "ref"); ok {
		if !haveSchema {
			return nil, cerr(MalformedShape,
				"a ref source requires an explicit \"schema\" array — there are no cells to infer column types from")
		}
		ref, ok := refV.(string)
		if !ok {
			return nil, cerr(MalformedShape, "ref: expected string")
		}
		return Ref{Name: ref}, nil
	}
	columnsV, e := field(el, "columns")
	if e != nil {
		return nil, e
	}
	if !haveSchema {
		// Infer from the columns object, Ordinal key order.
		m, ok := columnsV.(map[string]any)
		if !ok {
			return nil, cerr(MalformedShape, "columns: expected object")
		}
		names := make([]string, 0, len(m))
		for k := range m {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			values, _, ce := columnParts(name, m[name])
			if ce != nil {
				return nil, ce
			}
			ty, ce := inferColumnType(name, values)
			if ce != nil {
				return nil, ce
			}
			schema = append(schema, SchemaEntry{Name: name, Type: ty})
		}
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
	// "call" and "fn" are lenient-ingest spellings of "apply" (same fn/args
	// fields); canonical stays "apply" — they normalise on re-encode.
	case "apply", "call", "fn":
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
	case "in":
		// Exactly one of `items` (literal list) / `param` (a bound
		// multi-select list param).
		exprV, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		subject, ce := DecodeExpr(exprV)
		if ce != nil {
			return nil, ce
		}
		itemsV, hasItems := tryF(el, "items")
		paramV, hasParam := tryF(el, "param")
		switch {
		case hasItems && hasParam:
			return nil, cerr(MalformedShape,
				"in: give exactly ONE of \"items\" (a literal list) or \"param\" (a multi-select list param), not both")
		case hasItems:
			items, ce := decodeExprList(itemsV)
			if ce != nil {
				return nil, ce
			}
			return InList{Subject: subject, Items: items}, nil
		case hasParam:
			p, ok := paramV.(string)
			if !ok {
				return nil, cerr(MalformedShape, "in.param: expected string")
			}
			return InParam{Subject: subject, Name: p}, nil
		}
		return nil, cerr(MissingField, "items")
	case "isNull":
		v, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		inner, ce := DecodeExpr(v)
		if ce != nil {
			return nil, ce
		}
		return IsNull{Expr: inner}, nil
	// Expression-level string-predicate spellings:
	// {"$type":"contains","expr":X,"other":Y} (also left/right) denotes
	// exactly Binary(contains, X, Y); same for startsWith/endsWith. Canonical
	// stays the "binary" form — these normalise on re-encode.
	case "contains", "startsWith", "endsWith":
		return flatBinary(el, k)
	// Flat logical spellings: {"$type":"or","exprs":[e1,e2,…]} (variadic —
	// left-folds into the nested canonical form) or {"$type":"and","left":X,
	// "right":Y}. Canonical stays "binary".
	case "and", "or":
		if exprsV, ok := tryF(el, "exprs"); ok {
			xs, ce := decodeExprList(exprsV)
			if ce != nil {
				return nil, ce
			}
			switch len(xs) {
			case 0:
				return nil, cerr(MalformedShape, k+".exprs: expected a non-empty array")
			case 1:
				return xs[0], nil
			}
			acc := xs[0]
			for _, x := range xs[1:] {
				acc = Binary{Op: k, Left: acc, Right: x}
			}
			return acc, nil
		}
		return flatBinary(el, k)
	// Flat comparison spellings: {"$type":"eq","left":X,"right":Y} denotes
	// exactly Binary(eq, X, Y); same for ne/lt/le/gt/ge.
	case "eq", "ne", "lt", "le", "gt", "ge":
		return flatBinary(el, k)
	}
	_ = m
	// Flat scalar-fn spellings: {"$type":"lower","expr":X} /
	// {"$type":"concat","args":[…]} denote ApplyFn(fn, args). The scalar-fn
	// name vocabulary is disjoint from the expr tags, so the mapping is
	// one-to-one; canonical stays "apply".
	if scalarFns[k] {
		if argsV, ok := tryF(el, "args"); ok {
			xs, ce := decodeExprList(argsV)
			if ce != nil {
				return nil, ce
			}
			return ApplyFn{Fn: k, Args: xs}, nil
		}
		v, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		inner, ce := DecodeExpr(v)
		if ce != nil {
			return nil, ce
		}
		return ApplyFn{Fn: k, Args: []ColExpr{inner}}, nil
	}
	return nil, cerr(UnknownType, "unknown ColExpr '"+k+"'")
}

// flatBinary decodes the flat two-operand spelling ({left|expr, right|other})
// into the canonical nested Binary.
func flatBinary(el any, op string) (ColExpr, *ColumnError) {
	leftV, e := fieldAliased(el, "left", "expr")
	if e != nil {
		return nil, e
	}
	left, ce := DecodeExpr(leftV)
	if ce != nil {
		return nil, ce
	}
	rightV, e := fieldAliased(el, "right", "other")
	if e != nil {
		return nil, e
	}
	right, ce := DecodeExpr(rightV)
	if ce != nil {
		return nil, ce
	}
	return Binary{Op: op, Left: left, Right: right}, nil
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
	// Sort-key aliases: `column` for `col`; ONE of `dir` (canonical),
	// `descending` (alias boolean), or `direction` (alias) — a directionless
	// entry is the SQL default (asc).
	colV, e := fieldAliased(el, "col", "column")
	if e != nil {
		return OrderKey{}, e
	}
	col, ok := colV.(string)
	if !ok {
		return OrderKey{}, cerr(MalformedShape, "order.col: expected string")
	}
	dirV, hasDir := tryF(el, "dir")
	descV, hasDesc := tryF(el, "descending")
	directionV, hasDirection := tryF(el, "direction")
	count := 0
	for _, h := range []bool{hasDir, hasDesc, hasDirection} {
		if h {
			count++
		}
	}
	if count > 1 {
		return OrderKey{}, cerr(MalformedShape,
			"give ONE of \"dir\" (canonical: asc|desc), \"descending\" (alias boolean), or \"direction\" (alias: asc|desc)")
	}
	switch {
	case hasDir, hasDirection:
		v := dirV
		if hasDirection {
			v = directionV
		}
		dir, _ := v.(string)
		if !sortDirs[dir] {
			dir = "asc"
		}
		return OrderKey{Col: col, Dir: dir}, nil
	case hasDesc:
		b, ok := descV.(bool)
		if !ok {
			return OrderKey{}, cerr(MalformedShape, "\"descending\" must be a JSON boolean")
		}
		if b {
			return OrderKey{Col: col, Dir: "desc"}, nil
		}
		return OrderKey{Col: col, Dir: "asc"}, nil
	}
	return OrderKey{Col: col, Dir: "asc"}, nil
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
	// Aggregate-entry aliases: `as` for `name`, `op` for `fn`, `column` for
	// `of`; `avg` is the SQL-prior alias for `mean` (canonical encode stays
	// "mean").
	nameV, e := fieldAliased(el, "name", "as")
	if e != nil {
		return Agg{}, e
	}
	fnV, e := fieldAliased(el, "fn", "op")
	if e != nil {
		return Agg{}, e
	}
	ofV, e := fieldAliased(el, "of", "column")
	if e != nil {
		return Agg{}, e
	}
	fn, _ := fnV.(string)
	if fn == "avg" {
		fn = "mean"
	}
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
		// `predicate` aliases `pred`; the FLAT filter-step prior
		// {"$type":"filter","column":C,"op":O,"param":P|"value":V} coerces to
		// the canonical nested predicate — exactly one canonical value, so the
		// coercion is admitted. `pred` present takes the canonical path.
		predV, hasPred := tryF(el, "pred")
		predicateV, hasPredicate := tryF(el, "predicate")
		switch {
		case hasPred && hasPredicate:
			return nil, cerr(MalformedShape, "give \"pred\" (canonical) or \"predicate\" (alias), not both")
		case hasPred, hasPredicate:
			v := predV
			if hasPredicate {
				v = predicateV
			}
			pred, ce := DecodeExpr(v)
			if ce != nil {
				return nil, ce
			}
			return Filter{Pred: pred}, nil
		}
		colV, hasCol := tryF(el, "column")
		opV, hasOp := tryF(el, "op")
		if !hasCol || !hasOp {
			return nil, cerr(MalformedShape,
				"a filter step carries \"pred\" (a $type-discriminated expression) — or the flat short form {\"column\":…,\"op\":…,\"param\":…|\"value\":…}")
		}
		col, ok := colV.(string)
		if !ok {
			return nil, cerr(MalformedShape, "flat filter step: \"column\" must be a string")
		}
		op, _ := opV.(string)
		if !binOps[op] {
			return nil, cerr(UnknownType, "unknown binary op '"+op+"'")
		}
		paramV, hasParam := tryF(el, "param")
		valueV, hasValue := tryF(el, "value")
		switch {
		case hasParam && hasValue:
			return nil, cerr(MalformedShape,
				"flat filter step: give exactly ONE of \"param\" (a pipeline param name) or \"value\" (a scalar literal), not both")
		case hasParam:
			p, ok := paramV.(string)
			if !ok {
				return nil, cerr(MalformedShape, "flat filter step: \"param\" must be a string")
			}
			return Filter{Pred: Binary{Op: op, Left: Col{Name: col}, Right: Param{Name: p}}}, nil
		case hasValue:
			var cell Cell
			switch t := valueV.(type) {
			case string:
				cell = CellStr(t)
			case bool:
				cell = CellBool(t)
			case json.Number:
				if isIntToken(t) {
					i, _ := t.Int64()
					cell = CellInt(i)
				} else {
					f, _ := t.Float64()
					cell = CellFloat(f)
				}
			default:
				return nil, cerr(MalformedShape, "flat filter step: \"value\" must be a scalar (string/int/float/bool)")
			}
			return Filter{Pred: Binary{Op: op, Left: Col{Name: col}, Right: Lit{Cell: cell}}}, nil
		}
		return nil, cerr(MalformedShape,
			"flat filter step: {column, op} needs \"param\" (a pipeline param name) or \"value\" (a scalar literal) as the right-hand side")
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
		// `by` (pandas prior) aliases `keys`; `aggregations` aliases `aggs`.
		keysV, e := fieldAliased(el, "keys", "by")
		if e != nil {
			return nil, e
		}
		keys, ce := strList(keysV, "groupBy.keys")
		if ce != nil {
			return nil, ce
		}
		aggsV, e := fieldAliased(el, "aggs", "aggregations")
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
		if fn == "cumSum" {
			// Legacy alias — the pre-rename wire tag; normalises on re-encode.
			fn = "cumulSum"
		}
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
		if agg == "avg" {
			agg = "mean"
		}
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
		// `keys` (SQL ORDER-BY-list prior) aliases `by`.
		v, e := fieldAliased(el, "by", "keys")
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
		// `count` aliases `n`; an absent `offset` is unambiguously 0.
		nV, e := fieldAliased(el, "n", "count")
		if e != nil {
			return nil, e
		}
		n, ok := intToken(nV)
		if !ok {
			return nil, cerr(MalformedShape, "limit: expected ints")
		}
		off := int64(0)
		if offV, hasOff := tryF(el, "offset"); hasOff {
			o, ok := intToken(offV)
			if !ok {
				return nil, cerr(MalformedShape, "limit: expected ints")
			}
			off = o
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
