// Package dataframe is the Go host of the Compute-layer columnar/dataframe
// surface — the typed null-aware Cell/Column/Table model, the DataSource codec,
// and the reference evaluator that runs a serialisable Binding.Transform
// pipeline (filter / project / derive / groupBy / join / window / pivot /
// unpivot / sort / distinct / limit / union over the scalar ColExpr algebra) to
// produce result rows. The pipeline is data on the wire; every host's evaluator
// runs it identically. This is the substrate for the Living Sheet and the data
// behind the Pandas dashboard.
//
// Host-local: no wire change. The pipeline round-trip is already corpus-
// certified (grid-transform.json); this adds the deep typed decode + the
// evaluation, certified value-for-value against the F# reference report
// (fuaran-py/tests/fixtures/dataframe_parity.json). The per-step shape is
// Fuaran.Core-owned; this mirrors fuaran-py, not the UI spec.
package dataframe

// ── Column scalar types (the closed, Arrow-compatible set) — wire tags ───────

const (
	TypeInt       = "int"
	TypeFloat     = "float"
	TypeBool      = "bool"
	TypeString    = "string"
	TypeDate      = "date"
	TypeTimestamp = "timestamp"
)

var columnTypes = map[string]bool{
	TypeInt: true, TypeFloat: true, TypeBool: true, TypeString: true, TypeDate: true, TypeTimestamp: true,
}

// Cell is one realised scalar, or the first-class null marker. Kind is a column
// type tag or "null"; Value is the native payload (int64 / float64 / bool /
// string, or nil for null). The explicit Kind keeps Date distinct from Str and
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

func isNull(c Cell) bool { return c.Kind == "null" }

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

func (Col) isColExpr()      {}
func (Lit) isColExpr()      {}
func (Binary) isColExpr()   {}
func (Not) isColExpr()      {}
func (Coalesce) isColExpr() {}
func (Case) isColExpr()     {}
func (Cast) isColExpr()     {}
func (ApplyFn) isColExpr()  {}
func (Param) isColExpr()    {}

// Closed vocabularies (wire tags) — additive only.
var (
	binOps    = set("add", "sub", "mul", "div", "mod", "eq", "ne", "lt", "le", "gt", "ge", "and", "or")
	scalarFns = set("abs", "round", "floor", "ceil", "length", "lower", "upper", "substr", "datePart")
	aggFns    = set("sum", "mean", "min", "max", "count", "median", "stddev", "first", "last")
	joinKinds = set("inner", "left", "right", "outer")
	windowFns = set("rowNumber", "rank", "lag", "lead", "cumSum", "rollingMean")
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

func (Filter) isTransform()   {}
func (Project) isTransform()  {}
func (Derive) isTransform()   {}
func (GroupBy) isTransform()  {}
func (Join) isTransform()     {}
func (Window) isTransform()   {}
func (Pivot) isTransform()    {}
func (Unpivot) isTransform()  {}
func (Sort) isTransform()     {}
func (Distinct) isTransform() {}
func (Limit) isTransform()    {}
func (Union) isTransform()    {}

// ── Error envelopes ──────────────────────────────────────────────────────────

// Codec codes.
const (
	NotJSON        = "NOT_JSON"
	MissingField   = "MISSING_FIELD"
	MalformedShape = "MALFORMED_SHAPE"
	UnknownType    = "UNKNOWN_TYPE"
	TypeMismatch   = "TYPE_MISMATCH"
	LengthMismatch = "LENGTH_MISMATCH"
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
