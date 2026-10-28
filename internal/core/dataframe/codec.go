package dataframe

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	canonical "github.com/Fuaran-Core/fuaran-core-go/json"
)

// The decode half of the canonical codec for the Compute-layer wire surface.
// It walks an already-parsed JSON document (json.Number-preserving) against the
// schema, surfacing the six-code *ColumnError envelope on any wire-shape
// violation. The encode half lowers into the host's structural wire model, which
// is the host's own value type, so it lives outside this boundary in the
// dataframe package, together with the bounded text parse.

// ── Decode ──────────────────────────────────────────────────────────────────

func cerr(code, detail string) *ColumnError { return &ColumnError{Code: code, Detail: detail} }

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

// The epoch seconds the canonical instant text spells: 0000-01-01T00:00:00Z
// and 9999-12-31T23:59:59Z.
const (
	minEpochSecond int64 = -62_167_219_200
	maxEpochSecond int64 = 253_402_300_799
)

func timestampTagOfDigits(d int) string {
	switch {
	case d == 0:
		return TypeTimestamp
	case d <= 3:
		return TypeTimestampMs
	case d <= 6:
		return TypeTimestampUs
	}
	return TypeTimestampNs
}

func daysInMonth(y, m int) int {
	switch m {
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// instantFractionDigits answers the fraction digits of a CANONICAL instant text
// (YYYY-MM-DDThh:mm:ssZ, or YYYY-MM-DDThh:mm:ss.FZ with one to nine digits and
// no trailing zero, naming a moment that exists), or -1 for any other text.
func instantFractionDigits(s string) int {
	if len(s) < 20 || s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':' {
		return -1
	}
	num := func(at, n int) int {
		acc := 0
		for k := at; k < at+n; k++ {
			if s[k] < '0' || s[k] > '9' {
				return -1
			}
			acc = acc*10 + int(s[k]-'0')
		}
		return acc
	}
	y, mo, d := num(0, 4), num(5, 2), num(8, 2)
	h, mi, se := num(11, 2), num(14, 2), num(17, 2)
	if y < 0 || mo < 1 || mo > 12 || d < 1 || d > daysInMonth(y, mo) ||
		h < 0 || h > 23 || mi < 0 || mi > 59 || se < 0 || se > 59 {
		return -1
	}
	if len(s) == 20 {
		if s[19] == 'Z' {
			return 0
		}
		return -1
	}
	if len(s) > 30 || s[19] != '.' || s[len(s)-1] != 'Z' || s[len(s)-2] == '0' {
		return -1
	}
	if num(20, len(s)-21) < 0 {
		return -1
	}
	return len(s) - 21
}

// ReadInstant reads one present value of a timestamp column of type ty (any
// unit; WIRE_FORMAT §3.6.25, fuaran-core#422). TEXT is read only in its
// canonical form, with a fraction no finer than the unit — a coarser instant
// widens in unchanged, since one instant has one text. An EPOCH NUMBER
// (whole-valued, |x| < 9e15) is read in a "timestamp" column by magnitude, as
// fuaran-core#94 pinned it (>= 1e11 means milliseconds, truncated to the
// second), and in a sub-second column in its own unit with no guess; either way
// it must name an instant in the years 0000-9999. It answers the canonical text,
// or a non-empty malformed reason, or mismatch for a value of another JSON kind.
func ReadInstant(col, ty string, v any) (text, malformed string, mismatch bool) {
	digits := TimestampUnitDigits[ty]
	if s, ok := v.(string); ok {
		n := instantFractionDigits(s)
		if n < 0 {
			return "", col + ": a " + ty + " value must be canonical ISO-8601 text, " +
				"YYYY-MM-DDThh:mm:ssZ or YYYY-MM-DDThh:mm:ss.FZ, naming a moment that exists", false
		}
		if n > digits {
			return "", col + ": a " + ty + " value's fraction is finer than the column's unit holds: " +
				timestampTagOfDigits(n) + " or finer is needed", false
		}
		return s, "", false
	}
	num, ok := v.(json.Number)
	if !ok {
		return "", "", true
	}
	f, err := num.Float64()
	if err != nil || f != math.Floor(f) || math.Abs(f) >= 9e15 {
		return "", "", true
	}
	i := int64(f)
	if isIntToken(num) {
		// An integer token is read exactly; within the 9e15 guard it agrees with f.
		if exact, convErr := num.Int64(); convErr == nil {
			i = exact
		}
	}
	second, fraction := i, int64(0)
	if digits == 0 {
		if i >= 100_000_000_000 || i <= -100_000_000_000 {
			second = i / 1000
		}
	} else {
		scale := int64(1)
		for k := 0; k < digits; k++ {
			scale *= 10
		}
		second = i / scale
		if i%scale < 0 {
			second--
		}
		fraction = i - second*scale
	}
	if second < minEpochSecond || second > maxEpochSecond {
		return "", col + ": the epoch " + strconv.FormatInt(i, 10) +
			" names an instant outside the years 0000-9999 the canonical timestamp spells", false
	}
	text = isoOfEpochSeconds(second)
	if fraction != 0 {
		frac := strings.TrimRight(fmt.Sprintf("%0*d", digits, fraction), "0")
		text = text[:19] + "." + frac + "Z"
	}
	return text, "", false
}

func decodeCellValue(col, ty string, v any) (Cell, *ColumnError) {
	switch ty {
	case TypeInt:
		if n, ok := v.(json.Number); ok && isIntToken(n) {
			// Int64 REFUSES rather than clamping. The error it returns is
			// out-of-range for an integer literal wider than int64 — a real
			// case, since the wire's integers are arbitrary-precision text and
			// the discarded error left the cell holding a silently wrong
			// number that then travelled as data.
			i, convErr := n.Int64()
			if convErr != nil {
				return Cell{}, cerr(TypeMismatch,
					col+": integer literal "+string(n)+" does not fit an int64 cell")
			}
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
	case TypeDecimal:
		return decodeDecimalValue(col, v)
	case TypeTimestamp, TypeTimestampMs, TypeTimestampUs, TypeTimestampNs:
		text, malformed, mismatch := ReadInstant(col, ty, v)
		if malformed != "" {
			return Cell{}, cerr(MalformedShape, malformed)
		}
		if !mismatch {
			return CellTimestamp(text), nil
		}
	}
	return Cell{}, cerr(TypeMismatch, "column '"+col+"': expected "+ty+" value")
}

// int53Max is the largest magnitude a whole-valued number token carries exactly.
const int53Max = 9007199254740992

// decodeDecimalValue reads one present value of a decimal column. A decimal is a
// JSON STRING of decimal text on the wire, canonicalised on read (12.50 reads as
// 12.5). A number token is read only where it is EXACT: a whole-valued token of
// magnitude at most 2^53, whose value is its digits. A fractional token, or a
// whole one past 2^53, has been through a float by the time it arrives, and a
// type whose purpose is exactness cannot vouch for it — refused.
func decodeDecimalValue(col string, v any) (Cell, *ColumnError) {
	switch t := v.(type) {
	case string:
		if c, ok := CellDecimal(t); ok {
			return c, nil
		}
		return Cell{}, cerr(MalformedShape, col+": a decimal value must be decimal text — an optional '-', digits, "+
			"and an optional '.' followed by digits, with no exponent, sign '+', separator or white space")
	case json.Number:
		if text, ok := exactWholeToken(t); ok {
			return Cell{Kind: TypeDecimal, Value: text}, nil
		}
	}
	return Cell{}, cerr(TypeMismatch, "column '"+col+"': expected decimal value")
}

// exactWholeToken is the integer digits of a whole-valued number token of
// magnitude at most 2^53, or ok=false.
func exactWholeToken(n json.Number) (string, bool) {
	if isIntToken(n) {
		i, err := n.Int64()
		if err != nil || i > int53Max || i < -int53Max {
			return "", false
		}
		return strconv.FormatInt(i, 10), true
	}
	f, err := n.Float64()
	if err != nil || f != math.Floor(f) || math.Abs(f) > int53Max {
		return "", false
	}
	return strconv.FormatInt(int64(f), 10), true
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
		se, e := decodeFieldMetadata(entry, SchemaEntry{Name: name, Type: ty})
		if e != nil {
			return nil, e
		}
		out = append(out, se)
	}
	return out, nil
}

// jsonKindName names a parsed JSON value's kind for a refusal.
func jsonKindName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "number"
}

// optionalText is an optional string member of a schema entry: absent is nil;
// present and not a string is refused naming the column and the member.
func optionalText(column, member string, entry any) (*string, *ColumnError) {
	v, ok := tryF(entry, member)
	if !ok {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, cerr(MalformedShape, column+": schema entry member '"+member+"' must be a string, got "+jsonKindName(v))
	}
	return &s, nil
}

// decodeFieldMetadata reads a schema entry's metadata members (WIRE_FORMAT
// §3.6.26; twin of the reference's ColumnCodec.decodeField): `unit` is parsed by
// the unit algebra and REFUSED, naming the column and the reason, where it does
// not parse (a parseable spelling is read to the unit and re-encodes canonical);
// `label` and `description` must be strings; `ext` must be an object whose every
// member is a string (an empty one states nothing). A member this codec does not
// know is read past.
func decodeFieldMetadata(entry any, se SchemaEntry) (SchemaEntry, *ColumnError) {
	unitText, e := optionalText(se.Name, "unit", entry)
	if e != nil {
		return se, e
	}
	if unitText != nil {
		u, r := ParseUnit(*unitText)
		if r != nil {
			return se, cerr(MalformedShape, se.Name+": unit '"+*unitText+"' is not a unit the kit parses: "+r.Error())
		}
		se.Unit = &u
	}
	if se.Label, e = optionalText(se.Name, "label", entry); e != nil {
		return se, e
	}
	if se.Description, e = optionalText(se.Name, "description", entry); e != nil {
		return se, e
	}
	extV, ok := tryF(entry, "ext")
	if !ok {
		return se, nil
	}
	members, ok := extV.(map[string]any)
	if !ok {
		return se, cerr(MalformedShape, se.Name+": schema entry member 'ext' must be an object, got "+jsonKindName(extV))
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	canonical.SortKeys(keys)
	for _, k := range keys {
		v, ok := members[k].(string)
		if !ok {
			return se, cerr(MalformedShape, se.Name+": schema entry extension member '"+k+"' must be a string, got "+jsonKindName(members[k]))
		}
		if se.Ext == nil {
			se.Ext = make(map[string]string, len(members))
		}
		se.Ext[k] = v
	}
	return se, nil
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

// DecodeSourceParsed decodes a DataSource from an already-parsed JSON value.
// Parsing the text under the host's wire limits happens before the boundary
// (the dataframe package's DecodeSource), so this decode needs no parser.
func DecodeSourceParsed(raw any) (DataSource, *ColumnError) {
	return decodeSourceJSON(raw)
}

// DecodeSchemaParsed decodes a schema (an array of schema entries, each with its
// metadata members) from an already-parsed JSON value.
func DecodeSchemaParsed(raw any) (Schema, *ColumnError) {
	return decodeSchema(raw)
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
	case "Decimal":
		// Decimal text, canonicalised on read; an exact integer token is read too,
		// and anything else — a fractional number token included — is refused.
		switch t := v.(type) {
		case string:
			if c, ok := CellDecimal(t); ok {
				return c, nil
			}
		case json.Number:
			if text, ok := exactWholeToken(t); ok && isIntToken(t) {
				return Cell{Kind: TypeDecimal, Value: text}, nil
			}
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
	case "quotient":
		dividendV, e := field(el, "dividend")
		if e != nil {
			return nil, e
		}
		dividend, ce := DecodeExpr(dividendV)
		if ce != nil {
			return nil, ce
		}
		divisorV, e := field(el, "divisor")
		if e != nil {
			return nil, e
		}
		divisor, ce := DecodeExpr(divisorV)
		if ce != nil {
			return nil, ce
		}
		r, ce := decodeRounding(el)
		if ce != nil {
			return nil, ce
		}
		return Quotient{Dividend: dividend, Divisor: divisor, Rounding: r}, nil
	case "rounded":
		v, e := field(el, "expr")
		if e != nil {
			return nil, e
		}
		inner, ce := DecodeExpr(v)
		if ce != nil {
			return nil, ce
		}
		r, ce := decodeRounding(el)
		if ce != nil {
			return nil, ce
		}
		return Rounded{Expr: inner, Rounding: r}, nil
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

// decodeRounding reads the "rounding" member: "mode" one of the seven
// spellings (anything else is UNKNOWN_TYPE naming them), "scale" an integer
// slot — an int token, or {"$param":"<name>"}.
func decodeRounding(el any) (Rounding, *ColumnError) {
	rv, e := field(el, "rounding")
	if e != nil {
		return Rounding{}, e
	}
	mv, e := field(rv, "mode")
	if e != nil {
		return Rounding{}, e
	}
	mode, ok := mv.(string)
	if !ok {
		return Rounding{}, cerr(MalformedShape, "rounding.mode: expected string")
	}
	if !roundingModes[mode] {
		return Rounding{}, cerr(UnknownType, "unknown rounding mode '"+mode+"' (expected one of "+
			strings.Join([]string{RoundHalfEven, RoundHalfUp, RoundHalfDown, RoundUp, RoundDown, RoundCeiling, RoundFloor}, ", ")+")")
	}
	sv, e := field(rv, "scale")
	if e != nil {
		return Rounding{}, e
	}
	if obj, isObj := sv.(map[string]any); isObj {
		pv, has := obj["$param"]
		name, isStr := pv.(string)
		if !has || !isStr {
			return Rounding{}, cerr(MalformedShape, "rounding scale: an object here is a parameter slot and must carry a string \"$param\"")
		}
		return Rounding{ScaleParam: name, Mode: mode}, nil
	}
	n, ok := intToken(sv)
	if !ok || n < math.MinInt32 || n > math.MaxInt32 {
		return Rounding{}, cerr(MalformedShape, "rounding scale: expected int")
	}
	return Rounding{Scale: int(n), Mode: mode}, nil
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
	// fuaran-core#92 admitted `column` as an alias of `col`; 0.28.0 SWAPPED which of the
	// two is canonical, so both still decode, `column` re-encodes, and giving both is
	// refused as ambiguous. Then ONE of `dir` (canonical), `descending` (alias boolean),
	// or `direction` (alias) — a directionless entry is the SQL default (asc).
	colV, e := fieldAliased(el, "column", "col")
	if e != nil {
		return OrderKey{}, e
	}
	col, ok := colV.(string)
	if !ok {
		return OrderKey{}, cerr(MalformedShape, "order.column: expected string")
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
		// 0.28.0 — `cols` is the pre-rename spelling, kept as a decode alias; giving both
		// is the same ambiguity refusal every other aliased member of this algebra makes.
		v, e := fieldAliased(el, "columns", "cols")
		if e != nil {
			return nil, e
		}
		ps, ce := pairList(v, "project.columns")
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
	case "intersect", "except":
		v, e := field(el, "source")
		if e != nil {
			return nil, e
		}
		src, ce := decodeSourceJSON(v)
		if ce != nil {
			return nil, ce
		}
		if k == "intersect" {
			return Intersect{Source: src}, nil
		}
		return Except{Source: src}, nil
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

// DecodePipelineParsed decodes an ordered pipeline from an already-parsed JSON
// value. Parsing the text under the host's wire limits happens before the
// boundary (the dataframe package's DecodePipeline).
func DecodePipelineParsed(raw any) ([]Transform, *ColumnError) {
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
