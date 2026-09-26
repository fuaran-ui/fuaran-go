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
//
// The implementation is the Core twin in internal/core/dataframe, kept behind an
// import boundary (see CONTRIBUTING.md, "The Core boundary"). This package forwards
// every exported identifier to it unchanged, and keeps the two pieces that
// belong to this host rather than to Core: the encode half of the codec, which
// lowers into this host's structural wire model, and the bounded text parse.
package dataframe

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/dataframe"
)

// Column scalar types (the closed, Arrow-compatible set) — wire tags.
const (
	TypeInt       = core.TypeInt
	TypeFloat     = core.TypeFloat
	TypeBool      = core.TypeBool
	TypeString    = core.TypeString
	TypeDate      = core.TypeDate
	TypeTimestamp = core.TypeTimestamp
)

// Error codes: the ColumnError decode codes and the EvalError evaluation codes.
const (
	AggError         = core.AggError
	ArityError       = core.ArityError
	JoinError        = core.JoinError
	LengthMismatch   = core.LengthMismatch
	LimitExceeded    = core.LimitExceeded
	MalformedShape   = core.MalformedShape
	MissingField     = core.MissingField
	NotJSON          = core.NotJSON
	TypeError        = core.TypeError
	TypeMismatch     = core.TypeMismatch
	UnboundParam     = core.UnboundParam
	UnknownColumn    = core.UnknownColumn
	UnknownType      = core.UnknownType
	UnresolvedSource = core.UnresolvedSource
)

// Null is the type-agnostic null cell.
var Null = core.Null

// The model, the ColExpr algebra, the Transform steps and the evaluator's types.
type (
	Agg         = core.Agg
	ApplyFn     = core.ApplyFn
	Binary      = core.Binary
	Case        = core.Case
	Cast        = core.Cast
	Cell        = core.Cell
	Coalesce    = core.Coalesce
	Col         = core.Col
	ColExpr     = core.ColExpr
	Column      = core.Column
	ColumnError = core.ColumnError
	DataSource  = core.DataSource
	Derive      = core.Derive
	Distinct    = core.Distinct
	Embedded    = core.Embedded
	EvalError   = core.EvalError
	Filter      = core.Filter
	Frame       = core.Frame
	GroupBy     = core.GroupBy
	InList      = core.InList
	InParam     = core.InParam
	IsNull      = core.IsNull
	Join        = core.Join
	Limit       = core.Limit
	Lit         = core.Lit
	Not         = core.Not
	OrderKey    = core.OrderKey
	Pair        = core.Pair
	Param       = core.Param
	Pivot       = core.Pivot
	PivotSpec   = core.PivotSpec
	Project     = core.Project
	Ref         = core.Ref
	Resolver    = core.Resolver
	Schema      = core.Schema
	SchemaEntry = core.SchemaEntry
	Sort        = core.Sort
	Table       = core.Table
	Transform   = core.Transform
	Union       = core.Union
	Unpivot     = core.Unpivot
	WhenThen    = core.WhenThen
	Window      = core.Window
	WindowSpec  = core.WindowSpec
)

// CellInt constructs an int cell.
func CellInt(v int64) Cell { return core.CellInt(v) }

// CellFloat constructs a float cell.
func CellFloat(v float64) Cell { return core.CellFloat(v) }

// CellBool constructs a bool cell.
func CellBool(v bool) Cell { return core.CellBool(v) }

// CellStr constructs a string cell.
func CellStr(v string) Cell { return core.CellStr(v) }

// CellDate constructs a date cell.
func CellDate(v string) Cell { return core.CellDate(v) }

// CellTimestamp constructs a timestamp cell.
func CellTimestamp(v string) Cell { return core.CellTimestamp(v) }

// BindParams substitutes the bound scalar parameters of a pipeline.
func BindParams(pipeline []Transform, env map[string]Cell, unbound map[string]bool) []Transform {
	return core.BindParams(pipeline, env, unbound)
}

// SubstituteListParams rewrites list-parameter membership tests to literal lists.
func SubstituteListParams(pipeline []Transform, listEnv map[string][]Cell) []Transform {
	return core.SubstituteListParams(pipeline, listEnv)
}

// NoResolve refuses every Ref (embedded-only evaluation).
func NoResolve(name string) (Table, *EvalError) { return core.NoResolve(name) }

// EvalPipelineWith folds the pipeline over the input table; resolve provides
// any Ref source.
func EvalPipelineWith(resolve Resolver, pipeline []Transform, input Table) (Table, *EvalError) {
	return core.EvalPipelineWith(resolve, pipeline, input)
}

// EvalPipeline is the reference evaluator over embedded sources only (a Ref is
// UNRESOLVED_SOURCE).
func EvalPipeline(pipeline []Transform, input Table) (Table, *EvalError) {
	return core.EvalPipeline(pipeline, input)
}

// DecodeExpr decodes a ColExpr from a parsed value.
func DecodeExpr(el any) (ColExpr, *ColumnError) { return core.DecodeExpr(el) }

// DecodeTransform decodes a Transform from a parsed value.
func DecodeTransform(el any) (Transform, *ColumnError) { return core.DecodeTransform(el) }
