package dataframe

// Grid edits as columnar ops (WIRE_FORMAT §3.2, "Edits as column ops —
// GridEdit"). The algebra — apply, inverse, the row delta under a declared
// rowKeyField, the edit history and the decode half of the codec — is the Core
// twin in internal/core/dataframe, forwarded here unchanged. The encode half
// lowers into this host's structural wire model and the text decode runs the
// bounded parse, so both live here, as the DataSource codec's halves do.

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/dataframe"
	"github.com/fuaran-ui/fuaran-go/wire"
)

type (
	ColumnOp        = core.ColumnOp
	SetCell         = core.SetCell
	SetColumn       = core.SetColumn
	InsertColumn    = core.InsertColumn
	RemoveColumn    = core.RemoveColumn
	RowCell         = core.RowCell
	AppendRows      = core.AppendRows
	ApplyTransform  = core.ApplyTransform
	GridEdit        = core.GridEdit
	EditRefusal     = core.EditRefusal
	RowChange       = core.RowChange
	TableDelta      = core.TableDelta
	GridEditHistory = core.GridEditHistory
)

// The refusal tokens the corpus names.
const (
	RefusalNoSuchColumn          = core.RefusalNoSuchColumn
	RefusalDuplicateColumn       = core.RefusalDuplicateColumn
	RefusalRowOutOfRange         = core.RefusalRowOutOfRange
	RefusalCellTypeMismatch      = core.RefusalCellTypeMismatch
	RefusalColumnLengthMismatch  = core.RefusalColumnLengthMismatch
	RefusalRowShapeUnknownColumn = core.RefusalRowShapeUnknownColumn
	RefusalTransformRejected     = core.RefusalTransformRejected
	RefusalNotInvertible         = core.RefusalNotInvertible
	RefusalNothingToUndo         = core.RefusalNothingToUndo
	RefusalNothingToRedo         = core.RefusalNothingToRedo
)

// ApplyColumnOp applies a columnar op to a table — a typed refusal, never a panic.
func ApplyColumnOp(op ColumnOp, t Table) (Table, *EditRefusal) { return core.ApplyColumnOp(op, t) }

// InvertColumnOp is the op that undoes op applied to the pre-state t.
func InvertColumnOp(op ColumnOp, t Table) (ColumnOp, *EditRefusal) { return core.InvertColumnOp(op, t) }

// GridEditDelta is the row delta op induces on before under identity by keyField.
func GridEditDelta(keyField string, before Table, op ColumnOp) TableDelta {
	return core.GridEditDelta(keyField, before, op)
}

// CellToken is the canonical token a row key is spelled in.
func CellToken(c Cell) string { return core.CellToken(c) }

// NewGridEditHistory is an edit history over table keeping at most capacity undoable edits.
func NewGridEditHistory(table Table, capacity int) GridEditHistory {
	return core.NewGridEditHistory(table, capacity)
}

// DecodeColumnOpParsed decodes a columnar op from an already-parsed JSON document.
func DecodeColumnOpParsed(el any) (ColumnOp, *ColumnError) { return core.DecodeColumnOpParsed(el) }

// DecodeGridEditParsed decodes a GridEdit from an already-parsed JSON document.
func DecodeGridEditParsed(el any) (GridEdit, *ColumnError) { return core.DecodeGridEditParsed(el) }

// DecodeGridEdit decodes a GridEdit wire string (strict: see DecodeGridEditParsed).
func DecodeGridEdit(text string) (GridEdit, *ColumnError) {
	raw, e := parseJSON(text)
	if e != nil {
		return GridEdit{}, e
	}
	return core.DecodeGridEditParsed(raw)
}

// opCellWire is a cell in the columnar op codec's form: {"$type":"Int","v":21}.
func opCellWire(c Cell) wire.Value {
	var v wire.Value
	tag := ""
	switch c.Kind {
	case TypeInt:
		tag, v = "Int", wire.Int(c.Value.(int64))
	case TypeFloat:
		tag, v = "Float", wire.Float(c.Value.(float64))
	case TypeBool:
		tag, v = "Bool", wire.Bool(c.Value.(bool))
	case TypeString:
		tag, v = "Str", wire.Str(c.Value.(string))
	case TypeDate:
		tag, v = "Date", wire.Str(c.Value.(string))
	case TypeTimestamp:
		tag, v = "Timestamp", wire.Str(c.Value.(string))
	case TypeDecimal:
		text := c.Value.(string)
		if canonical, ok := core.DecimalCanonical(text); ok {
			text = canonical
		}
		tag, v = "Decimal", wire.Str(text)
	default:
		return typed("Null", map[string]wire.Value{})
	}
	return typed(tag, map[string]wire.Value{"v": v})
}

func opColumnWire(c Column) wire.Value {
	cells := make(wire.Arr, len(c.Cells))
	for i, x := range c.Cells {
		cells[i] = opCellWire(x)
	}
	return wire.Obj{Fields: map[string]wire.Value{"name": wire.Str(c.Name), "type": wire.Str(c.Type), "cells": cells}}
}

// ColumnOpValue lowers a columnar op to the structural wire model.
func ColumnOpValue(op ColumnOp) wire.Value {
	switch op := op.(type) {
	case SetCell:
		return typed("setCell", map[string]wire.Value{"col": wire.Str(op.Col), "row": wire.Int(op.Row), "value": opCellWire(op.Value)})
	case SetColumn:
		return typed("setColumn", map[string]wire.Value{"column": opColumnWire(op.Column)})
	case InsertColumn:
		return typed("insertColumn", map[string]wire.Value{"index": wire.Int(op.Index), "column": opColumnWire(op.Column)})
	case RemoveColumn:
		return typed("removeColumn", map[string]wire.Value{"name": wire.Str(op.Name)})
	case AppendRows:
		rows := make(wire.Arr, len(op.Rows))
		for i, row := range op.Rows {
			cells := make(wire.Arr, len(row))
			for j, rc := range row {
				cells[j] = wire.Obj{Fields: map[string]wire.Value{"col": wire.Str(rc.Col), "value": opCellWire(rc.Value)}}
			}
			rows[i] = cells
		}
		return typed("appendRows", map[string]wire.Value{"rows": rows})
	case ApplyTransform:
		steps := make(wire.Arr, len(op.Pipeline))
		for i, s := range op.Pipeline {
			steps[i] = EncodeTransformValue(s)
		}
		return typed("applyTransform", map[string]wire.Value{"pipeline": steps})
	}
	return wire.Null{}
}

// EncodeColumnOp is the canonical wire string for a columnar op.
func EncodeColumnOp(op ColumnOp) (string, error) { return wire.EncodeValue(ColumnOpValue(op)) }

// EncodeGridEdit is the canonical wire string for a grid edit; an undeclared
// rowKeyField is omitted (absence means no identity is declared).
func EncodeGridEdit(edit GridEdit) (string, error) {
	fields := map[string]wire.Value{"key": wire.Str(edit.Key), "op": ColumnOpValue(edit.Op)}
	if edit.RowKeyField != nil {
		fields["rowKeyField"] = wire.Str(*edit.RowKeyField)
	}
	return wire.EncodeValue(typed("GridEdit", fields))
}

// EncodeTableDelta is the canonical wire string for a row delta.
func EncodeTableDelta(d TableDelta) (string, error) {
	if d.FullRefresh {
		return wire.EncodeValue(typed("fullRefresh", map[string]wire.Value{}))
	}
	rows := make(wire.Arr, len(d.Rows))
	for i, r := range d.Rows {
		rows[i] = typed(r.Change, map[string]wire.Value{"key": wire.Str(r.Key)})
	}
	return wire.EncodeValue(typed("rowSet", map[string]wire.Value{"scheme": wire.Str(d.Scheme), "rows": rows, "columns": wire.Arr{}}))
}
