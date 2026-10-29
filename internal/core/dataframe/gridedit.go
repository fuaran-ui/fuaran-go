package dataframe

// Grid edits as columnar ops (WIRE_FORMAT §3.2, "Edits as column ops —
// GridEdit"; this host since Phase 2500). A twin of the reference's columnar op
// algebra (Fuaran.Compute.ColumnOps) and its server-driven edit log: the op's
// application to a table (a typed refusal, never a panic), its inverse read
// from the table it applies to, the row delta it induces under the grid's
// declared rowKeyField, an immutable edit history (apply, undo, redo), and the
// decode half of the GridEdit codec. The encode half lowers into this host's
// structural wire model and so lives in the public dataframe package.

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	canonical "github.com/Fuaran-Core/fuaran-core-go/json"
)

// ── the op, the edit, the refusals ───────────────────────────────────────────

// ColumnOp is a columnar edit op over a Table: SetCell, SetColumn,
// InsertColumn, RemoveColumn, AppendRows or ApplyTransform.
type ColumnOp interface{ isColumnOp() }

// SetCell sets the cell at Row of column Col (Null or a cell that widens into the column's type).
type SetCell struct {
	Col   string
	Row   int
	Value Cell
}

// SetColumn replaces an existing column by name (its length must equal the row count).
type SetColumn struct{ Column Column }

// InsertColumn inserts a NEW column at Index (clamped into range).
type InsertColumn struct {
	Index  int
	Column Column
}

// RemoveColumn removes the named column.
type RemoveColumn struct{ Name string }

// RowCell is one named cell of an appended row.
type RowCell struct {
	Col   string
	Value Cell
}

// AppendRows appends rows; a column a row does not name gets Null.
type AppendRows struct{ Rows [][]RowCell }

// ApplyTransform applies a whole pipeline; the result replaces the table.
type ApplyTransform struct{ Pipeline []Transform }

func (SetCell) isColumnOp()        {}
func (SetColumn) isColumnOp()      {}
func (InsertColumn) isColumnOp()   {}
func (RemoveColumn) isColumnOp()   {}
func (AppendRows) isColumnOp()     {}
func (ApplyTransform) isColumnOp() {}

// GridEdit is a grid edit: the State key it commits to, the op, and the grid's
// declared row identity (nil — the member absent — means none is declared).
type GridEdit struct {
	Key         string
	Op          ColumnOp
	RowKeyField *string
}

// The refusal tokens the corpus names.
const (
	RefusalNoSuchColumn          = "noSuchColumn"
	RefusalDuplicateColumn       = "duplicateColumn"
	RefusalRowOutOfRange         = "rowOutOfRange"
	RefusalCellTypeMismatch      = "cellTypeMismatch"
	RefusalColumnLengthMismatch  = "columnLengthMismatch"
	RefusalRowShapeUnknownColumn = "rowShapeUnknownColumn"
	RefusalTransformRejected     = "transformRejected"
	RefusalNotInvertible         = "notInvertible"
	RefusalNothingToUndo         = "nothingToUndo"
	RefusalNothingToRedo         = "nothingToRedo"
)

// EditRefusal is why an op, or a history gesture, was refused.
type EditRefusal struct {
	Token  string
	Detail string
}

func (r *EditRefusal) Error() string { return r.Token + ": " + r.Detail }

func refuse(token, detail string) *EditRefusal { return &EditRefusal{Token: token, Detail: detail} }

// ── table helpers ────────────────────────────────────────────────────────────

func rowCount(t Table) int {
	if len(t.Columns) == 0 {
		return 0
	}
	return len(t.Columns[0].Cells)
}

func columnNames(t Table) string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

func findColumn(t Table, name string) (Column, int, bool) {
	for i, c := range t.Columns {
		if c.Name == name {
			return c, i, true
		}
	}
	return Column{}, -1, false
}

// cellType is the column type a present cell carries; a timestamp's is the
// unit its text's fraction spells.
func cellType(c Cell) string {
	if c.Kind != TypeTimestamp {
		return c.Kind
	}
	text, _ := c.Value.(string)
	dot := strings.IndexByte(text, '.')
	d := 0
	if dot >= 0 {
		d = len(text) - dot - 2 // the digits between "." and the closing "Z"
	}
	switch {
	case d == 0:
		return TypeTimestamp
	case d <= 3:
		return TypeTimestampMs
	case d <= 6:
		return TypeTimestampUs
	default:
		return TypeTimestampNs
	}
}

// fits reports whether a cell is Null or of a type that widens into ty.
func fits(ty string, c Cell) bool {
	return isNull(c) || ColumnTypeWidens(cellType(c), ty)
}

func copyCells(cs []Cell) []Cell { return append([]Cell(nil), cs...) }

// withColumn replaces the column named col.Name; a retyped column gets a fresh schema entry.
func withColumn(t Table, col Column) Table {
	schema := make(Schema, len(t.Schema))
	for i, e := range t.Schema {
		if e.Name == col.Name && e.Type != col.Type {
			e = SchemaEntry{Name: col.Name, Type: col.Type}
		}
		schema[i] = e
	}
	columns := make([]Column, len(t.Columns))
	for i, c := range t.Columns {
		if c.Name == col.Name {
			c = col
		}
		columns[i] = c
	}
	return Table{Schema: schema, Columns: columns}
}

// ── apply / invert ───────────────────────────────────────────────────────────

// ApplyColumnOp applies a columnar op to a table — total: a typed refusal, never a panic.
func ApplyColumnOp(op ColumnOp, t Table) (Table, *EditRefusal) {
	switch op := op.(type) {
	case SetCell:
		col, _, ok := findColumn(t, op.Col)
		if !ok {
			return Table{}, refuse(RefusalNoSuchColumn, "no such column '"+op.Col+"'; available: "+columnNames(t))
		}
		rc := rowCount(t)
		if op.Row < 0 || op.Row >= rc {
			return Table{}, refuse(RefusalRowOutOfRange, "row "+strconv.Itoa(op.Row)+" out of range (row count "+strconv.Itoa(rc)+")")
		}
		if !fits(col.Type, op.Value) {
			return Table{}, refuse(RefusalCellTypeMismatch, "column '"+op.Col+"': expected "+col.Type+" cell")
		}
		cells := copyCells(col.Cells)
		cells[op.Row] = op.Value
		return withColumn(t, Column{Name: col.Name, Type: col.Type, Cells: cells}), nil
	case SetColumn:
		if _, _, ok := findColumn(t, op.Column.Name); !ok {
			return Table{}, refuse(RefusalNoSuchColumn, "no such column '"+op.Column.Name+"'; available: "+columnNames(t))
		}
		if rc, n := rowCount(t), len(op.Column.Cells); n != rc {
			return Table{}, refuse(RefusalColumnLengthMismatch, "column '"+op.Column.Name+"': length "+strconv.Itoa(n)+" != row count "+strconv.Itoa(rc))
		}
		return withColumn(t, op.Column), nil
	case InsertColumn:
		if _, _, ok := findColumn(t, op.Column.Name); ok {
			return Table{}, refuse(RefusalDuplicateColumn, "duplicate column: "+op.Column.Name)
		}
		if rc, n := rowCount(t), len(op.Column.Cells); len(t.Columns) > 0 && n != rc {
			return Table{}, refuse(RefusalColumnLengthMismatch, "column '"+op.Column.Name+"': length "+strconv.Itoa(n)+" != row count "+strconv.Itoa(rc))
		}
		i := max(0, min(op.Index, len(t.Columns)))
		schema := append(append(append(Schema{}, t.Schema[:i]...), SchemaEntry{Name: op.Column.Name, Type: op.Column.Type}), t.Schema[i:]...)
		columns := append(append(append([]Column{}, t.Columns[:i]...), op.Column), t.Columns[i:]...)
		return Table{Schema: schema, Columns: columns}, nil
	case RemoveColumn:
		if _, _, ok := findColumn(t, op.Name); !ok {
			return Table{}, refuse(RefusalNoSuchColumn, "no such column '"+op.Name+"'; available: "+columnNames(t))
		}
		var schema Schema
		for _, e := range t.Schema {
			if e.Name != op.Name {
				schema = append(schema, e)
			}
		}
		var columns []Column
		for _, c := range t.Columns {
			if c.Name != op.Name {
				columns = append(columns, c)
			}
		}
		return Table{Schema: schema, Columns: columns}, nil
	case AppendRows:
		for _, row := range op.Rows {
			for _, rc := range row {
				col, _, ok := findColumn(t, rc.Col)
				if !ok {
					return Table{}, refuse(RefusalRowShapeUnknownColumn, "row references unknown column '"+rc.Col+"'; available: "+columnNames(t))
				}
				if !fits(col.Type, rc.Value) {
					return Table{}, refuse(RefusalCellTypeMismatch, "column '"+rc.Col+"': expected "+col.Type+" cell")
				}
			}
		}
		columns := make([]Column, len(t.Columns))
		for i, col := range t.Columns {
			cells := copyCells(col.Cells)
			for _, row := range op.Rows {
				v := Null
				for _, rc := range row {
					if rc.Col == col.Name {
						v = rc.Value
						break
					}
				}
				cells = append(cells, v)
			}
			columns[i] = Column{Name: col.Name, Type: col.Type, Cells: cells}
		}
		return Table{Schema: t.Schema, Columns: columns}, nil
	case ApplyTransform:
		out, err := EvalPipeline(op.Pipeline, t)
		if err != nil {
			return Table{}, refuse(RefusalTransformRejected, err.Detail)
		}
		return out, nil
	}
	return Table{}, refuse(RefusalTransformRejected, "unknown columnar op")
}

// InvertColumnOp is the op that undoes op applied to the PRE-state t. An op the
// table would refuse has no inverse (its refusal is returned); AppendRows and
// ApplyTransform have none at any table.
func InvertColumnOp(op ColumnOp, t Table) (ColumnOp, *EditRefusal) {
	switch op.(type) {
	case AppendRows:
		return nil, refuse(RefusalNotInvertible, "appendRows is not invertible")
	case ApplyTransform:
		return nil, refuse(RefusalNotInvertible, "applyTransform is not invertible")
	}
	if _, err := ApplyColumnOp(op, t); err != nil {
		return nil, err
	}
	switch op := op.(type) {
	case SetCell:
		col, _, _ := findColumn(t, op.Col)
		return SetCell{Col: op.Col, Row: op.Row, Value: col.Cells[op.Row]}, nil
	case SetColumn:
		old, _, _ := findColumn(t, op.Column.Name)
		return SetColumn{Column: old}, nil
	case InsertColumn:
		return RemoveColumn{Name: op.Column.Name}, nil
	case RemoveColumn:
		col, i, _ := findColumn(t, op.Name)
		return InsertColumn{Index: i, Column: col}, nil
	}
	return nil, refuse(RefusalNotInvertible, "unknown columnar op")
}

// ── the row delta ────────────────────────────────────────────────────────────

// RowChange is how a delta names one row: its key and "added", "changed" or "removed".
type RowChange struct {
	Key    string
	Change string
}

// TableDelta is the top delta (FullRefresh) or the rows a change touched, by
// key, under Scheme — Rows in ordinal (UTF-16) key order.
type TableDelta struct {
	FullRefresh bool
	Scheme      string
	Rows        []RowChange
}

// CellToken is the canonical token a row key is spelled in (the reference's
// Cell.token): type-tagged, a NaN one token and -0 sharing 0's, a decimal its
// canonical text.
func CellToken(c Cell) string {
	switch c.Kind {
	case TypeInt:
		return "i:" + strconv.FormatInt(c.Value.(int64), 10)
	case TypeFloat:
		f := c.Value.(float64)
		switch {
		case math.IsNaN(f):
			return "f:NaN"
		case math.IsInf(f, 1):
			return "f:Inf"
		case math.IsInf(f, -1):
			return "f:-Inf"
		case f == 0:
			f = 0
		}
		return "f:" + canonical.FormatFiniteDouble(f)
	case TypeBool:
		if c.Value.(bool) {
			return "b:1"
		}
		return "b:0"
	case TypeString:
		return "s:" + c.Value.(string)
	case TypeDate:
		return "d:" + c.Value.(string)
	case TypeTimestamp:
		return "t:" + c.Value.(string)
	case TypeDecimal:
		return "m:" + decimalCanonicalOrSelf(c.Value.(string))
	}
	return "n:"
}

func keysOf(t Table, field string) []*string {
	out := make([]*string, rowCount(t))
	col, _, ok := findColumn(t, field)
	if !ok {
		return out
	}
	for i, c := range col.Cells {
		if !isNull(c) {
			k := CellToken(c)
			out[i] = &k
		}
	}
	return out
}

// sameCell is cell equality as the reference decides "unchanged": a NaN never
// equals itself, and the two zeros agree.
func sameCell(a, b Cell) bool {
	if a.Kind != b.Kind {
		return false
	}
	return a.Value == b.Value
}

func utf16Less(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

func rowSet(scheme string, rows []RowChange) TableDelta {
	sorted := append([]RowChange{}, rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return utf16Less(sorted[i].Key, sorted[j].Key) })
	if sorted == nil {
		sorted = []RowChange{}
	}
	return TableDelta{Scheme: scheme, Rows: sorted}
}

var fullRefresh = TableDelta{FullRefresh: true}

// GridEditDelta is the row delta op induces on before under identity by the
// column keyField (scheme column:<keyField>), built from the op and the before
// table, never by diffing two tables. A schema or whole-table op, an op that
// does not apply, and a row with no key are the top delta.
func GridEditDelta(keyField string, before Table, op ColumnOp) TableDelta {
	scheme := "column:" + keyField
	switch op.(type) {
	case InsertColumn, RemoveColumn, ApplyTransform:
		return fullRefresh
	}
	after, err := ApplyColumnOp(op, before)
	if err != nil {
		return fullRefresh
	}
	k0, k1 := keysOf(before, keyField), keysOf(after, keyField)
	switch op := op.(type) {
	case SetCell:
		a, b := k0[op.Row], k1[op.Row]
		if a == nil || b == nil {
			return fullRefresh
		}
		old, _, _ := findColumn(before, op.Col)
		if sameCell(old.Cells[op.Row], op.Value) {
			return rowSet(scheme, nil)
		}
		if *a == *b {
			return rowSet(scheme, []RowChange{{*a, "changed"}})
		}
		return rowSet(scheme, []RowChange{{*a, "removed"}, {*b, "added"}})
	case SetColumn:
		for i := range k0 {
			if k0[i] == nil || k1[i] == nil || *k0[i] != *k1[i] {
				return fullRefresh
			}
		}
		old, _, _ := findColumn(before, op.Column.Name)
		var rows []RowChange
		for i := range k0 {
			if !sameCell(old.Cells[i], op.Column.Cells[i]) {
				rows = append(rows, RowChange{*k0[i], "changed"})
			}
		}
		return rowSet(scheme, rows)
	case AppendRows:
		existing := map[string]bool{}
		for _, k := range k0 {
			if k != nil {
				existing[*k] = true
			}
		}
		seen := map[string]bool{}
		var rows []RowChange
		for _, k := range k1[len(k0):] {
			if k == nil || existing[*k] || seen[*k] {
				return fullRefresh
			}
			seen[*k] = true
			rows = append(rows, RowChange{*k, "added"})
		}
		return rowSet(scheme, rows)
	}
	return fullRefresh
}

// ── the decode half of the codec ─────────────────────────────────────────────

func gridEditError(detail string) *ColumnError {
	return &ColumnError{Code: MalformedShape, Detail: "GridEdit: " + detail}
}

func isIntNumber(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || !isIntToken(n) {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

func decodeOpCell(el any) (Cell, *ColumnError) {
	m, ok := el.(map[string]any)
	if !ok {
		return Cell{}, gridEditError("cell: expected an object")
	}
	kind, _ := m["$type"].(string)
	if kind == "Null" {
		return Null, nil
	}
	v, present := m["v"]
	if !present {
		return Cell{}, gridEditError("cell: missing field: v")
	}
	switch kind {
	case "Int":
		if n, ok := v.(json.Number); ok {
			if i, ok := Int32Value(n); ok {
				return CellInt(int64(i)), nil
			}
		}
		return Cell{}, gridEditError("cell: expected int")
	case "Float":
		if n, ok := v.(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				return CellFloat(f), nil
			}
		}
		return Cell{}, gridEditError("cell: expected float")
	case "Bool":
		if b, ok := v.(bool); ok {
			return CellBool(b), nil
		}
		return Cell{}, gridEditError("cell: expected bool")
	case "Str", "Date", "Timestamp":
		s, ok := v.(string)
		if !ok {
			return Cell{}, gridEditError("cell: expected string")
		}
		return Cell{Kind: map[string]string{"Str": TypeString, "Date": TypeDate, "Timestamp": TypeTimestamp}[kind], Value: s}, nil
	case "Decimal":
		if s, ok := v.(string); ok {
			if c, ok := CellDecimal(s); ok {
				return c, nil
			}
		} else if i, ok := isIntNumber(v); ok {
			return Cell{Kind: TypeDecimal, Value: strconv.FormatInt(i, 10)}, nil
		}
		return Cell{}, gridEditError("cell: not decimal text")
	}
	return Cell{}, gridEditError("unknown cell kind: " + kind)
}

func decodeOpColumn(el any) (Column, *ColumnError) {
	m, ok := el.(map[string]any)
	if !ok {
		return Column{}, gridEditError("column: expected {name, type, cells}")
	}
	name, nok := m["name"].(string)
	ty, tok := m["type"].(string)
	cells, cok := m["cells"].([]any)
	if !nok || !tok || !cok {
		return Column{}, gridEditError("column: expected {name, type, cells}")
	}
	if !columnTypes[ty] {
		return Column{}, gridEditError("unknown column type: " + ty)
	}
	out := make([]Cell, 0, len(cells))
	for _, c := range cells {
		cell, err := decodeOpCell(c)
		if err != nil {
			return Column{}, err
		}
		if !fits(ty, cell) {
			return Column{}, gridEditError("column '" + name + "': a " + cell.Kind + " cell in a " + ty + " column")
		}
		out = append(out, cell)
	}
	return Column{Name: name, Type: ty, Cells: out}, nil
}

// DecodeColumnOpParsed decodes a columnar op from an already-parsed JSON document.
func DecodeColumnOpParsed(el any) (ColumnOp, *ColumnError) {
	m, ok := el.(map[string]any)
	if !ok {
		return nil, gridEditError("op: expected an object")
	}
	kind, _ := m["$type"].(string)
	switch kind {
	case "setCell":
		col, cok := m["col"].(string)
		row, rok := isIntNumber(m["row"])
		valueEl, vok := m["value"]
		if !cok || !rok || !vok || row > math.MaxInt32 || row < math.MinInt32 {
			return nil, gridEditError("setCell: expected {col, row, value}")
		}
		value, err := decodeOpCell(valueEl)
		if err != nil {
			return nil, err
		}
		return SetCell{Col: col, Row: int(row), Value: value}, nil
	case "setColumn", "insertColumn":
		column, err := decodeOpColumn(m["column"])
		if err != nil {
			return nil, err
		}
		if kind == "setColumn" {
			return SetColumn{Column: column}, nil
		}
		index, ok := isIntNumber(m["index"])
		if !ok || index > math.MaxInt32 || index < math.MinInt32 {
			return nil, gridEditError("insertColumn: expected an int index")
		}
		return InsertColumn{Index: int(index), Column: column}, nil
	case "removeColumn":
		name, ok := m["name"].(string)
		if !ok {
			return nil, gridEditError("removeColumn: expected a name")
		}
		return RemoveColumn{Name: name}, nil
	case "appendRows":
		rowsEl, ok := m["rows"].([]any)
		if !ok {
			return nil, gridEditError("appendRows: expected a rows array")
		}
		rows := make([][]RowCell, 0, len(rowsEl))
		for _, rowEl := range rowsEl {
			cellsEl, ok := rowEl.([]any)
			if !ok {
				return nil, gridEditError("appendRows: a row is an array")
			}
			row := make([]RowCell, 0, len(cellsEl))
			for _, cellEl := range cellsEl {
				cm, ok := cellEl.(map[string]any)
				col, cok := cm["col"].(string)
				valueEl, vok := cm["value"]
				if !ok || !cok || !vok {
					return nil, gridEditError("appendRows: expected {col, value}")
				}
				value, err := decodeOpCell(valueEl)
				if err != nil {
					return nil, err
				}
				row = append(row, RowCell{Col: col, Value: value})
			}
			rows = append(rows, row)
		}
		return AppendRows{Rows: rows}, nil
	case "applyTransform":
		pipelineEl, ok := m["pipeline"]
		if !ok {
			return nil, gridEditError("applyTransform: missing field: pipeline")
		}
		pipeline, err := DecodePipelineParsed(pipelineEl)
		if err != nil {
			return nil, err
		}
		return ApplyTransform{Pipeline: pipeline}, nil
	}
	return nil, gridEditError("unknown columnar op: " + kind)
}

// DecodeGridEditParsed decodes a GridEdit from an already-parsed JSON document.
// STRICT: a member outside the four, an empty key or rowKeyField, or an op the
// codec does not know is refused, because an edit applied differently from the
// one sent is worse than one refused.
func DecodeGridEditParsed(el any) (GridEdit, *ColumnError) {
	m, ok := el.(map[string]any)
	if !ok {
		return GridEdit{}, gridEditError("expected an object")
	}
	for k := range m {
		switch k {
		case "$type", "key", "op", "rowKeyField":
		default:
			return GridEdit{}, gridEditError("unknown member '" + k + "'")
		}
	}
	if tag, _ := m["$type"].(string); tag != "GridEdit" {
		return GridEdit{}, gridEditError(`$type must be "GridEdit"`)
	}
	key, ok := m["key"].(string)
	if !ok || key == "" {
		return GridEdit{}, gridEditError("key must be a non-empty string")
	}
	opEl, ok := m["op"]
	if !ok {
		return GridEdit{}, gridEditError("missing op")
	}
	var rowKeyField *string
	if rk, present := m["rowKeyField"]; present {
		s, ok := rk.(string)
		if !ok || s == "" {
			return GridEdit{}, gridEditError("rowKeyField must be a non-empty string")
		}
		rowKeyField = &s
	}
	op, err := DecodeColumnOpParsed(opEl)
	if err != nil {
		return GridEdit{}, err
	}
	return GridEdit{Key: key, Op: op, RowKeyField: rowKeyField}, nil
}

// ── the edit history ─────────────────────────────────────────────────────────

type editEntry struct{ op, inverse ColumnOp }

// GridEditHistory is an edit history over one table. Immutable: every gesture
// returns a new history. An edit's inverse is read from the table it is applied
// to; an edit with none (AppendRows, ApplyTransform) applies and clears what
// could be undone; any new edit clears what could be redone. Bounded: past its
// capacity the oldest edit is no longer undoable.
type GridEditHistory struct {
	table    Table
	capacity int
	undo     []editEntry // most recent first
	redo     []editEntry // most recently undone first
}

// NewGridEditHistory is a history over table keeping at most capacity undoable edits.
func NewGridEditHistory(table Table, capacity int) GridEditHistory {
	return GridEditHistory{table: table, capacity: max(1, capacity)}
}

// Table is the live table.
func (h GridEditHistory) Table() Table { return h.table }

// CanUndo reports whether an undo would apply.
func (h GridEditHistory) CanUndo() bool { return len(h.undo) > 0 }

// CanRedo reports whether a redo would apply.
func (h GridEditHistory) CanRedo() bool { return len(h.redo) > 0 }

func push(e editEntry, stack []editEntry, capacity int) []editEntry {
	out := append([]editEntry{e}, stack...)
	if len(out) > capacity {
		out = out[:capacity]
	}
	return out
}

// Apply applies an edit.
func (h GridEditHistory) Apply(op ColumnOp) (GridEditHistory, *EditRefusal) {
	after, err := ApplyColumnOp(op, h.table)
	if err != nil {
		return h, err
	}
	var undo []editEntry
	if inverse, ierr := InvertColumnOp(op, h.table); ierr == nil {
		undo = push(editEntry{op, inverse}, h.undo, h.capacity)
	}
	return GridEditHistory{table: after, capacity: h.capacity, undo: undo}, nil
}

// Undo applies the most recent undoable edit's inverse.
func (h GridEditHistory) Undo() (GridEditHistory, *EditRefusal) {
	if len(h.undo) == 0 {
		return h, refuse(RefusalNothingToUndo, "nothing to undo")
	}
	top := h.undo[0]
	after, err := ApplyColumnOp(top.inverse, h.table)
	if err != nil {
		return h, err
	}
	return GridEditHistory{table: after, capacity: h.capacity, undo: h.undo[1:], redo: append([]editEntry{top}, h.redo...)}, nil
}

// Redo re-applies the most recently undone op.
func (h GridEditHistory) Redo() (GridEditHistory, *EditRefusal) {
	if len(h.redo) == 0 {
		return h, refuse(RefusalNothingToRedo, "nothing to redo")
	}
	top := h.redo[0]
	after, err := ApplyColumnOp(top.op, h.table)
	if err != nil {
		return h, err
	}
	return GridEditHistory{table: after, capacity: h.capacity, undo: push(top, h.undo, h.capacity), redo: h.redo[1:]}, nil
}
