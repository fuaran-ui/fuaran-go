package wire

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// Binding.Transform's columnar source + pipeline decode (the Compute-layer
// wire surface, WIRE_FORMAT.md §3.3). The compute sub-trees share the node
// codec's canonical `$type` discipline but have their own closed vocabularies
// (transform steps, the ColExpr algebra, the embedded columnar frame), so this
// file normalises them structurally: every accepted input decodes to exactly
// the canonical wire shape (aliases renamed, flat spellings nested, lenient
// frame shorthands expanded, epoch timestamps rendered as ISO instants), so
// the shared generic encoder re-emits the canonical bytes. A compute-shape
// violation surfaces as WRONG_TYPE at the enclosing `source` / `pipeline`
// path, mirroring the reference host's core-error mapping.

// computeFail carries a compute-shape failure to the enclosing slot's path.
type computeFail struct{ msg string }

func cfail(format string, args ...any) {
	panic(decodeFailure{&DecodeError{Code: CodeWrongType, Path: "", Message: fmt.Sprintf(format, args...)}})
}

// atComputePath runs fn, stamping any compute failure whose path is unset with
// the enclosing slot path.
func atComputePath(path string, fn func() Value) Value {
	defer func() {
		if r := recover(); r != nil {
			if f, ok := r.(decodeFailure); ok && f.err.Path == "" {
				f.err.Path = path
				panic(f)
			}
			panic(r)
		}
	}()
	return fn()
}

// ── Closed compute vocabularies (wire tags) ─────────────────────────────────

var (
	computeColumnTypes = map[string]bool{
		"int": true, "float": true, "bool": true, "string": true, "date": true, "timestamp": true,
	}
	computeBinOps = map[string]bool{
		"add": true, "sub": true, "mul": true, "div": true, "mod": true,
		"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
		"and": true, "or": true, "contains": true, "startsWith": true, "endsWith": true,
	}
	computeScalarFns = map[string]bool{
		"abs": true, "round": true, "floor": true, "ceil": true, "length": true,
		"lower": true, "upper": true, "substr": true, "datePart": true,
		"concat": true, "trim": true, "replace": true, "dateDiffDays": true,
	}
	computeAggFns = map[string]bool{
		"sum": true, "mean": true, "min": true, "max": true, "count": true,
		"median": true, "stddev": true, "first": true, "last": true,
	}
	computeJoinKinds = map[string]bool{"inner": true, "left": true, "right": true, "outer": true}
	computeWindowFns = map[string]bool{
		"rowNumber": true, "rank": true, "lag": true, "lead": true, "cumulSum": true, "rollingMean": true,
	}
	computeCellTags = map[string]bool{
		"Null": true, "Int": true, "Float": true, "Bool": true, "Str": true, "Date": true, "Timestamp": true,
	}
)

// ── Small helpers over parsed JSON ──────────────────────────────────────────

func cObj(raw any, ctx string) map[string]any {
	m, ok := raw.(map[string]any)
	if !ok {
		cfail("%s: expected object", ctx)
	}
	return m
}

func cArr(raw any, ctx string) []any {
	a, ok := raw.([]any)
	if !ok {
		cfail("%s: expected array", ctx)
	}
	return a
}

func cStr(raw any, ctx string) string {
	s, ok := raw.(string)
	if !ok {
		cfail("%s: expected string", ctx)
	}
	return s
}

func cField(m map[string]any, key, ctx string) any {
	v, ok := m[key]
	if !ok {
		cfail("%s: missing field: %s", ctx, key)
	}
	return v
}

// cFieldAliased accepts exactly one of the canonical field or its alias; both
// present is ambiguous (didactic), neither reports the canonical name.
func cFieldAliased(m map[string]any, canonical, alias, ctx string) any {
	cv, cok := m[canonical]
	av, aok := m[alias]
	switch {
	case cok && aok:
		cfail("%s: give \"%s\" (canonical) or \"%s\" (alias), not both", ctx, canonical, alias)
	case cok:
		return cv
	case aok:
		return av
	}
	cfail("%s: missing field: %s", ctx, canonical)
	return nil
}

func cKind(raw any, ctx string) (string, map[string]any) {
	m := cObj(raw, ctx)
	v, ok := m["$type"]
	if !ok {
		cfail("%s: missing field: $type", ctx)
	}
	s, ok := v.(string)
	if !ok {
		cfail("%s: $type must be a string", ctx)
	}
	return s, m
}

func cStrList(raw any, ctx string) Value {
	arr := cArr(raw, ctx)
	out := make(Arr, len(arr))
	for i, x := range arr {
		out[i] = Str(cStr(x, ctx))
	}
	return out
}

func cNumber(raw any, ctx string) Value {
	n, ok := raw.(json.Number)
	if !ok {
		cfail("%s: expected number", ctx)
	}
	return numberValue(n)
}

func cInt(raw any, ctx string) Value {
	n, ok := raw.(json.Number)
	if !ok || !isIntegerLiteral(string(n)) {
		cfail("%s: expected int", ctx)
	}
	return cNumber(raw, ctx)
}

func tagged(tag string, fields map[string]Value) Obj { return Obj{Tag: tag, Fields: fields} }

// ── Embedded columnar frame ─────────────────────────────────────────────────

// isoOfEpochSeconds renders an epoch-seconds instant as the canonical ISO-8601
// UTC timestamp string — pure integer civil-from-days math, clock-free.
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
// magnitude: >= 1e11 means milliseconds, else seconds.
func epochToIso(i int64) Str {
	secs := i
	if i >= 100_000_000_000 || i <= -100_000_000_000 {
		secs = i / 1000
	}
	return Str(isoOfEpochSeconds(secs))
}

// frameColumnParts reads one column's (values, validity) with the lenient
// shorthands: a BARE array, or a wrapped object with `values` but no
// `validity` mask, are the all-present statement (the wire has no JSON null).
func frameColumnParts(name string, colEl any) ([]any, []any) {
	if arr, ok := colEl.([]any); ok {
		validity := make([]any, len(arr))
		for i := range validity {
			validity[i] = true
		}
		return arr, validity
	}
	m := cObj(colEl, name)
	values := cArr(cField(m, "values", name), name+".values")
	rawValidity, ok := m["validity"]
	if !ok {
		validity := make([]any, len(values))
		for i := range validity {
			validity[i] = true
		}
		return values, validity
	}
	return values, cArr(rawValidity, name+".validity")
}

// inferFrameColumnType infers one column's type from its present cells —
// pinned rules: all-int => int, any fractional => float, all-bool => bool,
// all-string => string; never date/timestamp; empty or mixed is a didactic
// reject naming the explicit-schema remedy.
func inferFrameColumnType(name string, values []any) string {
	if len(values) == 0 {
		cfail("%s: cannot infer a column type from an empty / all-null column — declare it in an explicit \"schema\" array", name)
	}
	seen := map[string]bool{}
	for _, v := range values {
		switch t := v.(type) {
		case json.Number:
			if isIntegerLiteral(string(t)) {
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
		return "int"
	case (len(seen) == 1 && seen["float"]) || (len(seen) == 2 && seen["int"] && seen["float"]):
		return "float"
	case len(seen) == 1 && seen["bool"]:
		return "bool"
	case len(seen) == 1 && seen["string"]:
		return "string"
	}
	cfail("%s: cannot infer a single column type from mixed cell kinds — declare it in an explicit \"schema\" array", name)
	return ""
}

// frameCell normalises one present cell against its declared type. A float
// column widens an int token; a timestamp column accepts an epoch number.
func frameCell(name, ty string, v any) Value {
	switch ty {
	case "int":
		if n, ok := v.(json.Number); ok && isIntegerLiteral(string(n)) {
			return numberValue(n)
		}
	case "float":
		if n, ok := v.(json.Number); ok {
			return numberValue(n)
		}
	case "bool":
		if b, ok := v.(bool); ok {
			return Bool(b)
		}
	case "string", "date":
		if s, ok := v.(string); ok {
			return Str(s)
		}
	case "timestamp":
		if s, ok := v.(string); ok {
			return Str(s)
		}
		if n, ok := v.(json.Number); ok {
			if isIntegerLiteral(string(n)) {
				if i, err := n.Int64(); err == nil {
					return epochToIso(i)
				}
			}
			if f, err := n.Float64(); err == nil && f == math.Floor(f) && math.Abs(f) < 9e15 {
				return epochToIso(int64(f))
			}
		}
	}
	cfail("column '%s': expected %s value", name, ty)
	return nil
}

// frameCellDefault is the type-default placeholder an absent (validity-false)
// cell re-encodes as — the validity mask, not the placeholder, carries nullity.
func frameCellDefault(ty string) Value {
	switch ty {
	case "int":
		return Int(0)
	case "float":
		return Float(0)
	case "bool":
		return Bool(false)
	}
	return Str("")
}

type schemaEntry struct {
	name string
	ty   string
}

func decodeFrameSchema(raw any) []schemaEntry {
	arr := cArr(raw, "schema")
	out := make([]schemaEntry, 0, len(arr))
	for _, e := range arr {
		m := cObj(e, "schema")
		name := cStr(cField(m, "name", "schema"), "schema.name")
		ty := cStr(cField(m, "type", "schema"), "schema.type")
		if !computeColumnTypes[ty] {
			cfail("unknown column type '%s'", ty)
		}
		out = append(out, schemaEntry{name: name, ty: ty})
	}
	return out
}

// decodeFrameSource normalises a Transform DataSource — an embedded columnar
// frame (schema optional: inferred, Ordinal key order) or a `ref` (schema
// required). Emits the canonical explicit-schema `{columns, schema}` shape.
func decodeFrameSource(raw any) Value {
	m := cObj(raw, "source")
	var schema []schemaEntry
	haveSchema := false
	if sv, ok := m["schema"]; ok {
		schema = decodeFrameSchema(sv)
		haveSchema = true
	}
	if refV, ok := m["ref"]; ok {
		if !haveSchema {
			cfail("a ref source requires an explicit \"schema\" array — there are no cells to infer column types from")
		}
		schemaArr := make(Arr, len(schema))
		for i, e := range schema {
			schemaArr[i] = Obj{Fields: map[string]Value{"name": Str(e.name), "type": Str(e.ty)}}
		}
		return Obj{Fields: map[string]Value{"ref": Str(cStr(refV, "ref")), "schema": schemaArr}}
	}
	columnsRaw := cObj(cField(m, "columns", "source"), "columns")
	if !haveSchema {
		names := make([]string, 0, len(columnsRaw))
		for k := range columnsRaw {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			values, _ := frameColumnParts(name, columnsRaw[name])
			schema = append(schema, schemaEntry{name: name, ty: inferFrameColumnType(name, values)})
		}
	}
	columns := make(map[string]Value, len(schema))
	schemaArr := make(Arr, len(schema))
	for i, e := range schema {
		schemaArr[i] = Obj{Fields: map[string]Value{"name": Str(e.name), "type": Str(e.ty)}}
		colEl, ok := columnsRaw[e.name]
		if !ok {
			cfail("missing field: columns.%s", e.name)
		}
		values, validity := frameColumnParts(e.name, colEl)
		if len(values) != len(validity) {
			cfail("column '%s': values/validity length mismatch (%d vs %d)", e.name, len(values), len(validity))
		}
		outValues := make(Arr, len(values))
		outValidity := make(Arr, len(values))
		for i2 := range values {
			present, ok := validity[i2].(bool)
			if !ok {
				cfail("%s.validity: expected bool", e.name)
			}
			outValidity[i2] = Bool(present)
			if present {
				outValues[i2] = frameCell(e.name, e.ty, values[i2])
			} else {
				outValues[i2] = frameCellDefault(e.ty)
			}
		}
		columns[e.name] = Obj{Fields: map[string]Value{"values": outValues, "validity": outValidity}}
	}
	return Obj{Fields: map[string]Value{"columns": Obj{Fields: columns}, "schema": schemaArr}}
}

// ── ColExpr ─────────────────────────────────────────────────────────────────

func decodeCellLiteral(raw any) Value {
	tag, m := cKind(raw, "lit")
	if !computeCellTags[tag] {
		cfail("lit: unknown cell tag '%s'", tag)
	}
	if tag == "Null" {
		return tagged("Null", map[string]Value{})
	}
	v, ok := m["value"]
	if !ok {
		cfail("lit: missing field: value")
	}
	switch tag {
	case "Int":
		return tagged("Int", map[string]Value{"value": cInt(v, "lit.value")})
	case "Float":
		return tagged("Float", map[string]Value{"value": cNumber(v, "lit.value")})
	case "Bool":
		b, ok := v.(bool)
		if !ok {
			cfail("lit: expected bool value")
		}
		return tagged("Bool", map[string]Value{"value": Bool(b)})
	default: // Str / Date / Timestamp
		return tagged(tag, map[string]Value{"value": Str(cStr(v, "lit.value"))})
	}
}

// decodeComputeExpr normalises a ColExpr: canonical tags pass through typed,
// the lenient spellings (call/fn for apply, flat scalar-fn tags, flat
// comparison/logical/string-predicate tags, variadic and/or) nest into the
// canonical forms.
func decodeComputeExpr(raw any) Value {
	k, m := cKind(raw, "expr")
	flatBin := func(op string) Value {
		left := decodeComputeExpr(cFieldAliased(m, "left", "expr", op))
		right := decodeComputeExpr(cFieldAliased(m, "right", "other", op))
		return tagged("binary", map[string]Value{"left": left, "op": Str(op), "right": right})
	}
	switch k {
	case "col", "param":
		return tagged(k, map[string]Value{"name": Str(cStr(cField(m, "name", k), k+".name"))})
	case "lit":
		return tagged("lit", map[string]Value{"cell": decodeCellLiteral(cField(m, "cell", "lit"))})
	case "binary":
		op := cStr(cField(m, "op", "binary"), "binary.op")
		if !computeBinOps[op] {
			cfail("unknown binary op '%s'", op)
		}
		left := decodeComputeExpr(cField(m, "left", "binary"))
		right := decodeComputeExpr(cField(m, "right", "binary"))
		return tagged("binary", map[string]Value{"left": left, "op": Str(op), "right": right})
	case "not", "isNull":
		return tagged(k, map[string]Value{"expr": decodeComputeExpr(cField(m, "expr", k))})
	case "coalesce":
		arr := cArr(cField(m, "exprs", "coalesce"), "coalesce.exprs")
		out := make(Arr, len(arr))
		for i, x := range arr {
			out[i] = decodeComputeExpr(x)
		}
		return tagged("coalesce", map[string]Value{"exprs": out})
	case "case":
		arr := cArr(cField(m, "cases", "case"), "case.cases")
		cases := make(Arr, len(arr))
		for i, c := range arr {
			cm := cObj(c, "case.cases")
			cases[i] = Obj{Fields: map[string]Value{
				"when": decodeComputeExpr(cField(cm, "when", "case")),
				"then": decodeComputeExpr(cField(cm, "then", "case")),
			}}
		}
		return tagged("case", map[string]Value{
			"cases": cases,
			"else":  decodeComputeExpr(cField(m, "else", "case")),
		})
	case "cast":
		ty := cStr(cField(m, "type", "cast"), "cast.type")
		if !computeColumnTypes[ty] {
			cfail("unknown column type '%s'", ty)
		}
		return tagged("cast", map[string]Value{
			"expr": decodeComputeExpr(cField(m, "expr", "cast")),
			"type": Str(ty),
		})
	// `call` and `fn` are lenient spellings of `apply` (same fn/args fields).
	case "apply", "call", "fn":
		fn := cStr(cField(m, "fn", k), k+".fn")
		if !computeScalarFns[fn] {
			cfail("unknown scalar fn '%s'", fn)
		}
		arr := cArr(cField(m, "args", k), k+".args")
		args := make(Arr, len(arr))
		for i, a := range arr {
			args[i] = decodeComputeExpr(a)
		}
		return tagged("apply", map[string]Value{"args": args, "fn": Str(fn)})
	case "in":
		subject := decodeComputeExpr(cField(m, "expr", "in"))
		itemsV, hasItems := m["items"]
		paramV, hasParam := m["param"]
		switch {
		case hasItems && hasParam:
			cfail("in: give exactly ONE of \"items\" (a literal list) or \"param\" (a multi-select list param), not both")
		case hasItems:
			arr := cArr(itemsV, "in.items")
			items := make(Arr, len(arr))
			for i, x := range arr {
				items[i] = decodeComputeExpr(x)
			}
			return tagged("in", map[string]Value{"expr": subject, "items": items})
		case hasParam:
			return tagged("in", map[string]Value{"expr": subject, "param": Str(cStr(paramV, "in.param"))})
		}
		cfail("in: missing field: items")
	// Expression-level string-predicate spellings normalise to "binary".
	case "contains", "startsWith", "endsWith":
		return flatBin(k)
	// Flat logical spellings: variadic exprs left-fold; left/right pairs nest.
	case "and", "or":
		if exprsV, ok := m["exprs"]; ok {
			arr := cArr(exprsV, k+".exprs")
			if len(arr) == 0 {
				cfail("%s.exprs: expected a non-empty array", k)
			}
			acc := decodeComputeExpr(arr[0])
			for _, x := range arr[1:] {
				acc = tagged("binary", map[string]Value{
					"left":  acc,
					"op":    Str(k),
					"right": decodeComputeExpr(x),
				})
			}
			return acc
		}
		return flatBin(k)
	// Flat comparison spellings normalise to "binary".
	case "eq", "ne", "lt", "le", "gt", "ge":
		return flatBin(k)
	}
	// Flat scalar-fn spellings: {"$type":"lower","expr":X} / {"$type":"concat",
	// "args":[…]} denote the canonical "apply".
	if computeScalarFns[k] {
		if argsV, ok := m["args"]; ok {
			arr := cArr(argsV, k+".args")
			args := make(Arr, len(arr))
			for i, a := range arr {
				args[i] = decodeComputeExpr(a)
			}
			return tagged("apply", map[string]Value{"args": args, "fn": Str(k)})
		}
		return tagged("apply", map[string]Value{
			"args": Arr{decodeComputeExpr(cField(m, "expr", k))},
			"fn":   Str(k),
		})
	}
	cfail("unknown ColExpr '%s'", k)
	return nil
}

// ── Transform steps ─────────────────────────────────────────────────────────

func decodeOrderKey(raw any) Value {
	m := cObj(raw, "order")
	col := cStr(cFieldAliased(m, "col", "column", "order"), "order.col")
	dirV, hasDir := m["dir"]
	descV, hasDesc := m["descending"]
	directionV, hasDirection := m["direction"]
	count := 0
	for _, h := range []bool{hasDir, hasDesc, hasDirection} {
		if h {
			count++
		}
	}
	if count > 1 {
		cfail("give ONE of \"dir\" (canonical: asc|desc), \"descending\" (alias boolean), or \"direction\" (alias: asc|desc)")
	}
	dir := "asc"
	switch {
	case hasDir, hasDirection:
		v := dirV
		if hasDirection {
			v = directionV
		}
		if s, ok := v.(string); ok && s == "desc" {
			dir = "desc"
		}
	case hasDesc:
		b, ok := descV.(bool)
		if !ok {
			cfail("\"descending\" must be a JSON boolean")
		}
		if b {
			dir = "desc"
		}
	}
	return Obj{Fields: map[string]Value{"col": Str(col), "dir": Str(dir)}}
}

func decodeOrderKeys(raw any, ctx string) Value {
	arr := cArr(raw, ctx)
	out := make(Arr, len(arr))
	for i, x := range arr {
		out[i] = decodeOrderKey(x)
	}
	return out
}

func decodeAggEntry(raw any) Value {
	m := cObj(raw, "agg")
	name := cStr(cFieldAliased(m, "name", "as", "agg"), "agg.name")
	fn := cStr(cFieldAliased(m, "fn", "op", "agg"), "agg.fn")
	if fn == "avg" {
		fn = "mean"
	}
	if !computeAggFns[fn] {
		cfail("unknown agg fn '%s'", fn)
	}
	of := cStr(cFieldAliased(m, "of", "column", "agg"), "agg.of")
	return Obj{Fields: map[string]Value{"fn": Str(fn), "name": Str(name), "of": Str(of)}}
}

func decodePairList(raw any, ctx string) Value {
	arr := cArr(raw, ctx)
	out := make(Arr, len(arr))
	for i, x := range arr {
		m := cObj(x, ctx)
		out[i] = Obj{Fields: map[string]Value{
			"a": Str(cStr(cField(m, "a", ctx), ctx+".a")),
			"b": Str(cStr(cField(m, "b", ctx), ctx+".b")),
		}}
	}
	return out
}

func decodeComputeStep(raw any) Value {
	m, isObj := raw.(map[string]any)
	if isObj {
		if _, ok := m["$type"]; !ok {
			cfail("a pipeline step is a $type-discriminated op (filter | project | derive | groupBy | join | window | pivot | unpivot | sort | distinct | limit | union) — this step object has no \"$type\"")
		}
	}
	k, m := cKind(raw, "step")
	switch k {
	case "filter":
		predV, hasPred := m["pred"]
		predicateV, hasPredicate := m["predicate"]
		switch {
		case hasPred && hasPredicate:
			cfail("give \"pred\" (canonical) or \"predicate\" (alias), not both")
		case hasPred, hasPredicate:
			v := predV
			if hasPredicate {
				v = predicateV
			}
			return tagged("filter", map[string]Value{"pred": decodeComputeExpr(v)})
		}
		// The flat filter-step prior: {column, op, param|value} coerces to the
		// canonical nested predicate.
		colV, hasCol := m["column"]
		opV, hasOp := m["op"]
		if !hasCol || !hasOp {
			cfail("a filter step carries \"pred\" (a $type-discriminated expression: binary/col/param/lit/apply) — or the flat short form {\"column\":…,\"op\":…,\"param\":…|\"value\":…}")
		}
		col := cStr(colV, "filter.column")
		op := cStr(opV, "filter.op")
		if !computeBinOps[op] {
			cfail("unknown binary op '%s'", op)
		}
		left := tagged("col", map[string]Value{"name": Str(col)})
		paramV, hasParam := m["param"]
		valueV, hasValue := m["value"]
		switch {
		case hasParam && hasValue:
			cfail("flat filter step: give exactly ONE of \"param\" (a pipeline param name) or \"value\" (a scalar literal), not both")
		case hasParam:
			right := tagged("param", map[string]Value{"name": Str(cStr(paramV, "filter.param"))})
			return tagged("filter", map[string]Value{
				"pred": tagged("binary", map[string]Value{"left": left, "op": Str(op), "right": right}),
			})
		case hasValue:
			var cell Value
			switch t := valueV.(type) {
			case string:
				cell = tagged("Str", map[string]Value{"value": Str(t)})
			case bool:
				cell = tagged("Bool", map[string]Value{"value": Bool(t)})
			case json.Number:
				if isIntegerLiteral(string(t)) {
					cell = tagged("Int", map[string]Value{"value": numberValue(t)})
				} else {
					cell = tagged("Float", map[string]Value{"value": numberValue(t)})
				}
			default:
				cfail("flat filter step: \"value\" must be a scalar (string/int/float/bool)")
			}
			right := tagged("lit", map[string]Value{"cell": cell})
			return tagged("filter", map[string]Value{
				"pred": tagged("binary", map[string]Value{"left": left, "op": Str(op), "right": right}),
			})
		}
		cfail("flat filter step: {column, op} needs \"param\" (a pipeline param name) or \"value\" (a scalar literal) as the right-hand side")
	case "project":
		return tagged("project", map[string]Value{"cols": decodePairList(cField(m, "cols", "project"), "project.cols")})
	case "derive":
		return tagged("derive", map[string]Value{
			"expr": decodeComputeExpr(cField(m, "expr", "derive")),
			"name": Str(cStr(cField(m, "name", "derive"), "derive.name")),
		})
	case "groupBy":
		keys := cStrList(cFieldAliased(m, "keys", "by", "groupBy"), "groupBy.keys")
		aggsArr := cArr(cFieldAliased(m, "aggs", "aggregations", "groupBy"), "groupBy.aggs")
		aggs := make(Arr, len(aggsArr))
		for i, a := range aggsArr {
			aggs[i] = decodeAggEntry(a)
		}
		return tagged("groupBy", map[string]Value{"aggs": aggs, "keys": keys})
	case "join":
		src := decodeFrameSource(cField(m, "source", "join"))
		on := decodePairList(cField(m, "on", "join"), "join.on")
		how := cStr(cField(m, "how", "join"), "join.how")
		if !computeJoinKinds[how] {
			cfail("unknown join kind '%s'", how)
		}
		return tagged("join", map[string]Value{"how": Str(how), "on": on, "source": src})
	case "window":
		pb := cStrList(cField(m, "partitionBy", "window"), "window.partitionBy")
		ob := decodeOrderKeys(cField(m, "orderBy", "window"), "window.orderBy")
		fn := cStr(cField(m, "fn", "window"), "window.fn")
		if fn == "cumSum" {
			// Legacy alias — the pre-rename wire tag; normalises on re-encode.
			fn = "cumulSum"
		}
		if !computeWindowFns[fn] {
			cfail("unknown window fn '%s'", fn)
		}
		return tagged("window", map[string]Value{
			"as":          Str(cStr(cField(m, "as", "window"), "window.as")),
			"fn":          Str(fn),
			"of":          Str(cStr(cField(m, "of", "window"), "window.of")),
			"orderBy":     ob,
			"partitionBy": pb,
		})
	case "pivot":
		agg := cStr(cField(m, "agg", "pivot"), "pivot.agg")
		if agg == "avg" {
			agg = "mean"
		}
		if !computeAggFns[agg] {
			cfail("unknown agg fn '%s'", agg)
		}
		return tagged("pivot", map[string]Value{
			"agg":    Str(agg),
			"index":  cStrList(cField(m, "index", "pivot"), "pivot.index"),
			"on":     Str(cStr(cField(m, "on", "pivot"), "pivot.on")),
			"values": Str(cStr(cField(m, "values", "pivot"), "pivot.values")),
		})
	case "unpivot":
		return tagged("unpivot", map[string]Value{
			"idVars":    cStrList(cField(m, "idVars", "unpivot"), "unpivot.idVars"),
			"valueVars": cStrList(cField(m, "valueVars", "unpivot"), "unpivot.valueVars"),
		})
	case "sort":
		return tagged("sort", map[string]Value{"by": decodeOrderKeys(cFieldAliased(m, "by", "keys", "sort"), "sort.by")})
	case "distinct":
		return tagged("distinct", map[string]Value{})
	case "limit":
		n := cInt(cFieldAliased(m, "n", "count", "limit"), "limit.n")
		var offset Value = Int(0)
		if offV, ok := m["offset"]; ok {
			offset = cInt(offV, "limit.offset")
		}
		return tagged("limit", map[string]Value{"n": n, "offset": offset})
	case "union":
		return tagged("union", map[string]Value{"source": decodeFrameSource(cField(m, "source", "union"))})
	}
	cfail("unknown Transform '%s'", k)
	return nil
}

func decodeComputePipeline(raw any) Value {
	arr, ok := raw.([]any)
	if !ok {
		cfail("pipeline: expected a JSON array of transform steps")
	}
	out := make(Arr, len(arr))
	for i, step := range arr {
		out[i] = decodeComputeStep(step)
	}
	return out
}
