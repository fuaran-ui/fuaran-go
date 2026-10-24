// Package dataframe is the Core twin behind the internal/core boundary: the
// Compute-layer model, the Transform evaluator, list-parameter substitution and
// the decode half of the canonical codec. The public github.com/fuaran-ui/fuaran-go/dataframe
// package forwards to it; see that package for the full documentation.
package dataframe

// ── Column scalar types (the closed, Arrow-compatible set) — wire tags ───────

const (
	TypeInt       = "int"
	TypeFloat     = "float"
	TypeBool      = "bool"
	TypeString    = "string"
	TypeDate      = "date"
	TypeTimestamp = "timestamp"
	// TypeDecimal is the exact decimal (fuaran-core Phase 276): a cell carries its
	// canonical decimal TEXT (see decimal.go), never a host decimal or float.
	TypeDecimal = "decimal"
	// The three sub-second timestamp units (Phase 2206, fuaran-core#422).
	TypeTimestampMs = "timestamp_ms"
	TypeTimestampUs = "timestamp_us"
	TypeTimestampNs = "timestamp_ns"
)

var columnTypes = map[string]bool{
	TypeInt: true, TypeFloat: true, TypeBool: true, TypeString: true, TypeDate: true, TypeTimestamp: true,
	TypeDecimal:     true,
	TypeTimestampMs: true, TypeTimestampUs: true, TypeTimestampNs: true,
}

// TimestampUnitDigits is the timestamp family (WIRE_FORMAT §3.6.25): each tag's
// unit as the fraction digits it holds. Every unit carries one canonical
// instant text per instant, so a timestamp cell's Kind is TypeTimestamp
// whichever unit its column declares; a coarser unit widens into a finer one.
var TimestampUnitDigits = map[string]int{
	TypeTimestamp: 0, TypeTimestampMs: 3, TypeTimestampUs: 6, TypeTimestampNs: 9,
}

// IsTimestampType reports whether a column type tag is a timestamp of any unit.
func IsTimestampType(ty string) bool {
	_, ok := TimestampUnitDigits[ty]
	return ok
}

// Cell is one realised scalar, or the first-class null marker. Kind is a column
// type tag or "null"; Value is the native payload (int64 / float64 / bool /
// string — a decimal's canonical text — or nil for null). The explicit Kind keeps Date distinct from Str and
// bool distinct from int under the structural equality grouping/distinct rely on.
type Cell struct {
	Kind  string
	Value any
}

// Null is the type-agnostic null cell.
var Null = Cell{Kind: "null", Value: nil}

func CellInt(v int64) Cell        { return Cell{Kind: TypeInt, Value: v} }
func CellFloat(v float64) Cell    { return Cell{Kind: TypeFloat, Value: v} }
func CellBool(v bool) Cell        { return Cell{Kind: TypeBool, Value: v} }
func CellStr(v string) Cell       { return Cell{Kind: TypeString, Value: v} }
func CellDate(v string) Cell      { return Cell{Kind: TypeDate, Value: v} }
func CellTimestamp(v string) Cell { return Cell{Kind: TypeTimestamp, Value: v} }

// CellDecimal is a decimal cell holding the CANONICAL form of text, or ok=false
// where text is not decimal text — the way to build one, so a cell built here
// is canonical by construction.
func CellDecimal(text string) (Cell, bool) {
	c, ok := DecimalCanonical(text)
	if !ok {
		return Cell{}, false
	}
	return Cell{Kind: TypeDecimal, Value: c}, true
}

func isNull(c Cell) bool { return c.Kind == "null" }

// CellIsNull reports whether c is the null cell. It and DefaultFor are exported
// for the encode half of the codec, which lowers into the host's own wire model
// and so lives outside this boundary; the public dataframe package does not
// forward them.
func CellIsNull(c Cell) bool { return isNull(c) }

// DefaultFor is the type-default placeholder a null cell encodes as.
func DefaultFor(ty string) Cell { return defaultFor(ty) }

// typeOf is the column type a present cell carries ("" for the null).
func typeOf(c Cell) string {
	if c.Kind == "null" {
		return ""
	}
	return c.Kind
}

// defaultFor is the type-default placeholder a null cell encodes as (the
// validity mask, not this placeholder, carries the nullity).
func defaultFor(ty string) Cell {
	switch ty {
	case TypeInt:
		return CellInt(0)
	case TypeFloat:
		return CellFloat(0)
	case TypeBool:
		return CellBool(false)
	case TypeDecimal:
		return Cell{Kind: TypeDecimal, Value: "0"}
	default: // string / date / timestamp
		return Cell{Kind: ty, Value: ""}
	}
}

// ── Column / Table / DataSource ──────────────────────────────────────────────

// SchemaEntry is one (name, column-type) pair; column order follows the schema.
type SchemaEntry struct {
	Name string
	Type string
}

// Schema is an ordered column list.
type Schema []SchemaEntry

// Column is a named, typed column of cells.
type Column struct {
	Name  string
	Type  string
	Cells []Cell
}

// Table is a columnar table.
type Table struct {
	Schema  Schema
	Columns []Column
}

// DataSource is Embedded (rows travel on the wire) or Ref (host-resolved name).
type DataSource interface{ isDataSource() }

// Embedded carries the table rows.
type Embedded struct{ Table Table }

// Ref names a source the host resolves; the wire carries the name, never rows.
type Ref struct{ Name string }

func (Embedded) isDataSource() {}
func (Ref) isDataSource()      {}

// ── ColExpr — a scalar expression over a row's columns + literals ────────────

// ColExpr is a scalar expression (closed).
type ColExpr interface{ isColExpr() }

type Col struct{ Name string }
type Lit struct{ Cell Cell }
type Binary struct {
	Op    string
	Left  ColExpr
	Right ColExpr
}
type Not struct{ Expr ColExpr }
type Coalesce struct{ Exprs []ColExpr }
type WhenThen struct {
	When ColExpr
	Then ColExpr
}
type Case struct {
	Cases    []WhenThen
	ElseExpr ColExpr
}
type Cast struct {
	Type string
	Expr ColExpr
}
type ApplyFn struct {
	Fn   string
	Args []ColExpr
}
type Param struct{ Name string }

// InList is SQL three-valued membership over a literal-expression list:
// subject null => null; any equal item => true; no match having seen a null
// item => null; else false. Wire: {"$type":"in","expr":…,"items":[…]}.
type InList struct {
	Subject ColExpr
	Items   []ColExpr
}

// InParam is the LIST-valued named-parameter membership test (the multi-select
// binding). Wire: the same "in" tag with "param" in place of "items" (exactly
// one of the two). One that reaches evaluation unbound is a strict
// UnboundParam, matching the scalar Param posture.
type InParam struct {
	Subject ColExpr
	Name    string
}

// IsNull is the honest presence test — total: always Bool, never null.
type IsNull struct{ Expr ColExpr }

// Rounding is a rounding policy (Phase 277): a SCALE — decimal places, a literal
// or a named param, as the wire's integer slot carries it — and a MODE, one of the
// seven Round* spellings. ScaleParam non-empty is the param form
// ({"$param":"<name>"}); Scale is then unused. A scale outside 0..MaxRoundingScale
// is refused by name at evaluation.
type Rounding struct {
	Scale      int
	ScaleParam string
	Mode       string
}

// Quotient is the quotient of two EXACT numbers (decimal or int), correctly
// rounded to Rounding — computed by exact long division, the remainder deciding
// the last digit under the mode, never a double rounding. A float operand is
// refused by name; a zero divisor answers null; a null operand propagates.
// Wire: {"$type":"quotient","dividend":…,"divisor":…,"rounding":{"mode":…,"scale":…}}.
type Quotient struct {
	Dividend ColExpr
	Divisor  ColExpr
	Rounding Rounding
}

// Rounded is an exact number (decimal or int) brought to Rounding — the one
// general place a decimal loses digits. A float operand is refused by name.
// Wire: {"$type":"rounded","expr":…,"rounding":{"mode":…,"scale":…}}.
type Rounded struct {
	Expr     ColExpr
	Rounding Rounding
}

func (Col) isColExpr()      {}
func (Lit) isColExpr()      {}
func (Binary) isColExpr()   {}
func (Not) isColExpr()      {}
func (Coalesce) isColExpr() {}
func (Case) isColExpr()     {}
func (Cast) isColExpr()     {}
func (ApplyFn) isColExpr()  {}
func (Param) isColExpr()    {}
func (InList) isColExpr()   {}
func (InParam) isColExpr()  {}
func (IsNull) isColExpr()   {}
func (Quotient) isColExpr() {}
func (Rounded) isColExpr()  {}

// Closed vocabularies (wire tags) — additive only. The string predicates
// (contains / startsWith / endsWith) and the string/date scalar fns (concat /
// trim / replace / dateDiffDays) joined the algebra with the string-predicate
// wave; `cumulSum` is the canonical window tag (the legacy `cumSum` spelling
// decodes as an alias and normalises on re-encode).
var (
	binOps = set(
		"add", "sub", "mul", "div", "mod", "eq", "ne", "lt", "le", "gt", "ge", "and", "or",
		"contains", "startsWith", "endsWith",
	)
	scalarFns = set(
		"abs", "round", "floor", "ceil", "length", "lower", "upper", "substr", "datePart",
		"concat", "trim", "replace", "dateDiffDays",
		"pow", "exp", "ln", "log10",
	)
	aggFns    = set("sum", "mean", "min", "max", "count", "median", "stddev", "first", "last", "countDistinct")
	joinKinds = set("inner", "left", "right", "outer")
	windowFns = set("rowNumber", "rank", "lag", "lead", "cumulSum", "rollingMean")
	sortDirs  = set("asc", "desc")
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// ── Aggregates / window / pivot specs ────────────────────────────────────────

type Agg struct {
	Name string
	Fn   string
	Of   string
}

type OrderKey struct {
	Col string
	Dir string
}

type WindowSpec struct {
	PartitionBy []string
	OrderBy     []OrderKey
	Fn          string
	Of          string
	As          string
}

type PivotSpec struct {
	Index  []string
	On     string
	Values string
	Agg    string
}

// ── Transform — the v1 verb set ──────────────────────────────────────────────

// Transform is one pipeline step (closed).
type Transform interface{ isTransform() }

type Pair struct{ A, B string }

type Filter struct{ Pred ColExpr }
type Project struct{ Cols []Pair } // (source, output)
type Derive struct {
	Name string
	Expr ColExpr
}
type GroupBy struct {
	Keys []string
	Aggs []Agg
}
type Join struct {
	Source DataSource
	On     []Pair // (leftCol, rightCol)
	How    string
}
type Window struct{ Spec WindowSpec }
type Pivot struct{ Spec PivotSpec }
type Unpivot struct {
	IDVars    []string
	ValueVars []string
}
type Sort struct{ By []OrderKey }
type Distinct struct{}
type Limit struct {
	N      int
	Offset int
}
type Union struct{ Source DataSource }

// Intersect keeps the left rows whose FULL ROW also appears in Source, in the
// left's order and multiplicity (SQL INTERSECT ALL). Row identity is the
// grouping key, so a null matches a null and an int never matches a float.
type Intersect struct{ Source DataSource }

// Except keeps the left rows whose full row does NOT appear in Source (SQL
// EXCEPT ALL) — Intersect's complement.
type Except struct{ Source DataSource }

func (Filter) isTransform()    {}
func (Project) isTransform()   {}
func (Derive) isTransform()    {}
func (GroupBy) isTransform()   {}
func (Join) isTransform()      {}
func (Window) isTransform()    {}
func (Pivot) isTransform()     {}
func (Unpivot) isTransform()   {}
func (Sort) isTransform()      {}
func (Distinct) isTransform()  {}
func (Limit) isTransform()     {}
func (Union) isTransform()     {}
func (Intersect) isTransform() {}
func (Except) isTransform()    {}

// ── Error envelopes ──────────────────────────────────────────────────────────

// Codec codes.
const (
	NotJSON        = "NOT_JSON"
	MissingField   = "MISSING_FIELD"
	MalformedShape = "MALFORMED_SHAPE"
	UnknownType    = "UNKNOWN_TYPE"
	TypeMismatch   = "TYPE_MISMATCH"
	LengthMismatch = "LENGTH_MISMATCH"
	// LimitExceeded — the document is well-formed but breaches a §21 wire
	// limit (depth, string length, array/object width). Distinct from NotJSON
	// because §21.2 rule 2 forbids reporting a well-formed-but-too-large
	// document as malformed: it sends an author to repair the wrong thing.
	LimitExceeded = "LIMIT_EXCEEDED"
)

// ColumnError is a wire-shape violation.
type ColumnError struct {
	Code   string
	Detail string
}

func (e *ColumnError) Error() string { return e.Code + ": " + e.Detail }

// Evaluator codes.
const (
	UnknownColumn    = "UNKNOWN_COLUMN"
	TypeError        = "TYPE_ERROR"
	AggError         = "AGG_ERROR"
	JoinError        = "JOIN_ERROR"
	ArityError       = "ARITY_ERROR"
	UnresolvedSource = "UNRESOLVED_SOURCE"
	UnboundParam     = "UNBOUND_PARAM"
)

// EvalError is a pipeline-semantic failure.
type EvalError struct {
	Code   string
	Detail string
}

func (e *EvalError) Error() string { return e.Code + ": " + e.Detail }
