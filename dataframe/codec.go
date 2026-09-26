package dataframe

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/dataframe"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Byte-exact canonical codec for the Compute-layer wire surface. Encode lowers
// the columnar/algebra trees into the generic structural wire model and hands
// them to the shared canonical encoder (wire.EncodeValue), so the Ordinal
// key-sort + cross-host float layout + escape rules match the node codec by
// construction. Decode parses the text under the §21 limits here and hands the
// parsed JSON to the Core twin's decoder (internal/core/dataframe), which walks
// it against the schema, surfacing the six-code *ColumnError envelope on any
// wire-shape violation.

// ── Encode ──────────────────────────────────────────────────────────────────

func typed(tag string, fields map[string]wire.Value) wire.Obj {
	return wire.Obj{Tag: tag, Fields: fields}
}

// cellValueWire is the present-value JSON payload for a cell in a column of
// type ty (a null cell becomes the type-default; an int in a float column widens).
func cellValueWire(ty string, c Cell) wire.Value {
	present := c
	if core.CellIsNull(c) {
		present = core.DefaultFor(ty)
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
		validity[i] = wire.Bool(!core.CellIsNull(c))
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

// 0.28.0 — the member is `column`, not `col`: a member whose only honest name is "the
// column" is spelled out in full. `col` remains a decode alias and is never emitted.
func orderWire(o OrderKey) wire.Obj {
	return wire.Obj{Fields: map[string]wire.Value{"column": wire.Str(o.Col), "dir": wire.Str(o.Dir)}}
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
		// 0.28.0 — `columns`, not `cols`, for the reason `orderWire` above states.
		return typed("project", map[string]wire.Value{"columns": cols})
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

// parseJSON parses one Compute-layer document under the §21 limits.
//
// This is the HOST-FED Query path — a column pipeline arrives as text from
// wherever the host got it — and it used to call encoding/json directly, so no
// §21 limit reached it at all. A 10 001-deep payload was reported as NOT_JSON,
// which is both wrong (the document is well-formed) and misleading (rule 2
// forbids that classification precisely because it sends an author to repair
// the wrong thing). Strings and arrays were unbounded outright.
func parseJSON(text string) (any, *ColumnError) {
	raw, err := wire.ParseBounded(text)
	if err != nil {
		if de, ok := err.(*wire.DecodeError); ok && de.Code == wire.CodeLimitExceeded {
			return nil, &ColumnError{Code: LimitExceeded, Detail: de.Message}
		}
		return nil, &ColumnError{Code: NotJSON, Detail: err.Error()}
	}
	return raw, nil
}

// DecodeSource decodes a canonical DataSource wire string.
func DecodeSource(text string) (DataSource, *ColumnError) {
	raw, e := parseJSON(text)
	if e != nil {
		return nil, e
	}
	return core.DecodeSourceParsed(raw)
}

// DecodePipeline decodes a canonical pipeline wire string.
func DecodePipeline(text string) ([]Transform, *ColumnError) {
	raw, e := parseJSON(text)
	if e != nil {
		return nil, e
	}
	return core.DecodePipelineParsed(raw)
}
