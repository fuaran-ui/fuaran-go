package dataframe

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
)

// The pure columnar reference evaluator — a fold over the pipeline threading a
// row-oriented working frame, with pinned semantics every host reproduces
// byte-for-byte: null/NA propagates through arithmetic + comparison (Kleene
// three-valued logic); int+int stays int, any float widens; sort is stable and
// nulls sort last regardless of direction; groups appear in first-appearance
// order; count is the non-null count; round is half-away-from-zero; division by
// zero is null (not an error). Totality: every verb returns a value or a named
// *EvalError, never panics.

// A row is a slice of cells co-indexed with the frame's columns.
type row = []Cell

// Frame is the row-oriented working frame.
type Frame struct {
	Cols Schema
	Rows []row
}

func toFrame(t Table) Frame {
	byName := make(map[string]Column, len(t.Columns))
	for _, c := range t.Columns {
		byName[c.Name] = c
	}
	n := 0
	if len(t.Columns) > 0 {
		n = len(t.Columns[0].Cells)
	}
	rows := make([]row, n)
	for i := 0; i < n; i++ {
		r := make(row, len(t.Schema))
		for j, e := range t.Schema {
			if c, ok := byName[e.Name]; ok && i < len(c.Cells) {
				r[j] = c.Cells[i]
			} else {
				r[j] = Null
			}
		}
		rows[i] = r
	}
	return Frame{Cols: append(Schema(nil), t.Schema...), Rows: rows}
}

func ofFrame(f Frame) Table {
	columns := make([]Column, len(f.Cols))
	for ci, e := range f.Cols {
		cells := make([]Cell, len(f.Rows))
		for ri, r := range f.Rows {
			cells[ri] = r[ci]
		}
		columns[ci] = Column{Name: e.Name, Type: e.Type, Cells: cells}
	}
	return Table{Schema: append(Schema(nil), f.Cols...), Columns: columns}
}

func colIndex(cols Schema, name string) int {
	for i, e := range cols {
		if e.Name == name {
			return i
		}
	}
	return -1
}

func colType(cols Schema, name string) string {
	for _, e := range cols {
		if e.Name == name {
			return e.Type
		}
	}
	return ""
}

func available(cols Schema) []string {
	out := make([]string, len(cols))
	for i, e := range cols {
		out[i] = e.Name
	}
	return out
}

func unknownColumn(name string, cols Schema) *EvalError {
	return &EvalError{Code: UnknownColumn, Detail: "unknown column '" + name + "'"}
}

func evalErr(code, detail string) *EvalError { return &EvalError{Code: code, Detail: detail} }

// ── pinned scalar semantics ──────────────────────────────────────────────────

func cellString(c Cell) string {
	switch c.Kind {
	case TypeInt:
		return strconv.FormatInt(c.Value.(int64), 10)
	case TypeFloat:
		return canonical.FormatFiniteDouble(c.Value.(float64))
	case TypeBool:
		if c.Value.(bool) {
			return "true"
		}
		return "false"
	case TypeString, TypeDate, TypeTimestamp:
		return c.Value.(string)
	}
	return "" // null
}

func asNum(c Cell) (float64, bool) {
	switch c.Kind {
	case TypeInt:
		return float64(c.Value.(int64)), true
	case TypeFloat:
		return c.Value.(float64), true
	}
	return 0, false
}

// compareCells is a total comparison between two present, same-family cells;
// ok=false when incomparable.
func compareCells(a, b Cell) (int, bool) {
	if an, aok := asNum(a); aok {
		if bn, bok := asNum(b); bok {
			return sign(an - bn), true
		}
	}
	if a.Kind == TypeBool && b.Kind == TypeBool {
		return sign(b2i(a.Value.(bool)) - b2i(b.Value.(bool))), true
	}
	if a.Kind == b.Kind && (a.Kind == TypeString || a.Kind == TypeDate || a.Kind == TypeTimestamp) {
		return strCmp(a.Value.(string), b.Value.(string)), true
	}
	return 0, false
}

func sign(x float64) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return 0
}

func b2i(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func strCmp(a, b string) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

// cellEq is present-value comparison equality (int 5 == float 5.0).
func cellEq(a, b Cell) bool {
	if isNull(a) || isNull(b) {
		return false
	}
	c, ok := compareCells(a, b)
	return ok && c == 0
}

func arith(op string, a, b Cell) (Cell, *EvalError) {
	if isNull(a) || isNull(b) {
		return Null, nil
	}
	x, xok := asNum(a)
	y, yok := asNum(b)
	if !xok || !yok {
		return Cell{}, evalErr(TypeError, "arithmetic on a non-numeric operand")
	}
	bothInt := a.Kind == TypeInt && b.Kind == TypeInt
	switch op {
	case "add":
		if bothInt {
			return CellInt(int64(x) + int64(y)), nil
		}
		return CellFloat(x + y), nil
	case "sub":
		if bothInt {
			return CellInt(int64(x) - int64(y)), nil
		}
		return CellFloat(x - y), nil
	case "mul":
		if bothInt {
			return CellInt(int64(x) * int64(y)), nil
		}
		return CellFloat(x * y), nil
	case "div":
		if y == 0 {
			return Null, nil
		}
		return CellFloat(x / y), nil
	case "mod":
		if !bothInt {
			return Cell{}, evalErr(TypeError, "mod requires integer operands")
		}
		if int64(y) == 0 {
			return Null, nil
		}
		return CellInt(int64(x) % int64(y)), nil
	}
	return Cell{}, evalErr(TypeError, "not an arithmetic operator")
}

func comparison(op string, a, b Cell) (Cell, *EvalError) {
	if isNull(a) || isNull(b) {
		return Null, nil
	}
	c, ok := compareCells(a, b)
	if !ok {
		return Cell{}, evalErr(TypeError, "comparison between incompatible types")
	}
	var r bool
	switch op {
	case "eq":
		r = c == 0
	case "ne":
		r = c != 0
	case "lt":
		r = c < 0
	case "le":
		r = c <= 0
	case "gt":
		r = c > 0
	case "ge":
		r = c >= 0
	}
	return CellBool(r), nil
}

func logical(op string, a, b Cell) (Cell, *EvalError) {
	asBool := func(c Cell) (bool, bool, *EvalError) { // value, present, err
		if c.Kind == TypeBool {
			return c.Value.(bool), true, nil
		}
		if isNull(c) {
			return false, false, nil
		}
		return false, false, evalErr(TypeError, "logical operator on a non-bool operand")
	}
	xv, xp, e := asBool(a)
	if e != nil {
		return Cell{}, e
	}
	yv, yp, e := asBool(b)
	if e != nil {
		return Cell{}, e
	}
	switch op {
	case "and":
		if (xp && !xv) || (yp && !yv) {
			return CellBool(false), nil
		}
		if xp && xv && yp && yv {
			return CellBool(true), nil
		}
		return Null, nil
	case "or":
		if (xp && xv) || (yp && yv) {
			return CellBool(true), nil
		}
		if xp && !xv && yp && !yv {
			return CellBool(false), nil
		}
		return Null, nil
	}
	return Cell{}, evalErr(TypeError, "not a logical operator")
}

// stringPred is the ordinal substring predicates (contains / startsWith /
// endsWith). Null propagates (matching comparison); a non-string operand is a
// typed error. Ordinal byte-wise matching is the cross-host pin.
func stringPred(op string, a, b Cell) (Cell, *EvalError) {
	if isNull(a) || isNull(b) {
		return Null, nil
	}
	if a.Kind != TypeString || b.Kind != TypeString {
		return Cell{}, evalErr(TypeError, "string predicate on a non-string operand")
	}
	s := a.Value.(string)
	t := b.Value.(string)
	var r bool
	switch op {
	case "contains":
		r = strings.Contains(s, t)
	case "startsWith":
		r = strings.HasPrefix(s, t)
	case "endsWith":
		r = strings.HasSuffix(s, t)
	}
	return CellBool(r), nil
}

// civilDays is days since the civil epoch (1970-01-01 = 0) for the first 10
// chars (YYYY-MM-DD) of a date-like cell — days-from-civil as pure integer
// math (dateDiffDays). No host date library: determinism across hosts.
func civilDays(c Cell) (int64, *EvalError) {
	var s string
	switch c.Kind {
	case TypeDate, TypeTimestamp, TypeString:
		s = c.Value.(string)
	default:
		return 0, evalErr(TypeError, "dateDiffDays expects date/timestamp/string operands")
	}
	bad := func() (int64, *EvalError) {
		return 0, evalErr(TypeError, "dateDiffDays: '"+s+"' is not YYYY-MM-DD[...]")
	}
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return bad()
	}
	part := func(lo, ln int) (int64, bool) {
		v, err := strconv.ParseInt(s[lo:lo+ln], 10, 64)
		return v, err == nil
	}
	y, ok1 := part(0, 4)
	m, ok2 := part(5, 2)
	d, ok3 := part(8, 2)
	if !ok1 || !ok2 || !ok3 {
		return bad()
	}
	if m <= 2 {
		y--
	}
	era := y
	if y < 0 {
		era = y - 399
	}
	era /= 400
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468, nil
}

func castCell(ty string, c Cell) (Cell, *EvalError) {
	if isNull(c) {
		return Null, nil
	}
	switch ty {
	case TypeString:
		return CellStr(cellString(c)), nil
	case TypeFloat:
		if n, ok := asNum(c); ok {
			return CellFloat(n), nil
		}
		if c.Kind == TypeString {
			if f, err := strconv.ParseFloat(c.Value.(string), 64); err == nil {
				return CellFloat(f), nil
			}
			return Cell{}, evalErr(TypeError, "cannot cast '"+c.Value.(string)+"' to float")
		}
		return Cell{}, evalErr(TypeError, "cannot cast to float")
	case TypeInt:
		switch c.Kind {
		case TypeInt:
			return c, nil
		case TypeFloat:
			return CellInt(int64(c.Value.(float64))), nil
		case TypeBool:
			if c.Value.(bool) {
				return CellInt(1), nil
			}
			return CellInt(0), nil
		case TypeString:
			if i, err := strconv.ParseInt(c.Value.(string), 10, 64); err == nil {
				return CellInt(i), nil
			}
			return Cell{}, evalErr(TypeError, "cannot cast '"+c.Value.(string)+"' to int")
		}
		return Cell{}, evalErr(TypeError, "cannot cast to int")
	case TypeBool:
		switch c.Kind {
		case TypeBool:
			return c, nil
		case TypeInt:
			return CellBool(c.Value.(int64) != 0), nil
		}
		return Cell{}, evalErr(TypeError, "cannot cast to bool")
	case TypeDate:
		if c.Kind == TypeDate {
			return c, nil
		}
		if c.Kind == TypeString {
			return CellDate(c.Value.(string)), nil
		}
		return Cell{}, evalErr(TypeError, "cannot cast to date")
	case TypeTimestamp:
		if c.Kind == TypeTimestamp {
			return c, nil
		}
		if c.Kind == TypeString {
			return CellTimestamp(c.Value.(string)), nil
		}
		return Cell{}, evalErr(TypeError, "cannot cast to timestamp")
	}
	return Cell{}, evalErr(TypeError, "unknown cast type "+ty)
}

func roundHalfAway(x float64) float64 {
	if x >= 0 {
		return math.Floor(x + 0.5)
	}
	return math.Ceil(x - 0.5)
}

func applyScalar(fn string, args []Cell) (Cell, *EvalError) {
	arity := func(n int) *EvalError {
		if len(args) != n {
			return evalErr(ArityError, "function '"+fn+"' expects "+strconv.Itoa(n)+" args, got "+strconv.Itoa(len(args)))
		}
		return nil
	}
	switch fn {
	case "abs", "round", "floor", "ceil", "length", "lower", "upper":
		if e := arity(1); e != nil {
			return Cell{}, e
		}
		c := args[0]
		if isNull(c) {
			return Null, nil
		}
		switch fn {
		case "abs":
			switch c.Kind {
			case TypeInt:
				v := c.Value.(int64)
				if v < 0 {
					v = -v
				}
				return CellInt(v), nil
			case TypeFloat:
				return CellFloat(math.Abs(c.Value.(float64))), nil
			}
			return Cell{}, evalErr(TypeError, "abs of a non-numeric")
		case "round", "floor", "ceil":
			n, ok := asNum(c)
			if !ok {
				return Cell{}, evalErr(TypeError, fn+" of a non-numeric")
			}
			switch fn {
			case "round":
				return CellFloat(roundHalfAway(n)), nil
			case "floor":
				return CellFloat(math.Floor(n)), nil
			default:
				return CellFloat(math.Ceil(n)), nil
			}
		}
		if c.Kind != TypeString {
			return Cell{}, evalErr(TypeError, fn+" of a non-string")
		}
		s := c.Value.(string)
		switch fn {
		case "length":
			return CellInt(int64(len([]rune(s)))), nil
		case "lower":
			return CellStr(toLower(s)), nil
		default:
			return CellStr(toUpper(s)), nil
		}
	case "substr":
		if e := arity(3); e != nil {
			return Cell{}, e
		}
		s, start, length := args[0], args[1], args[2]
		if isNull(s) {
			return Null, nil
		}
		sv, ok1 := s.Value.(string)
		stv, ok2 := start.Value.(int64)
		lnv, ok3 := length.Value.(int64)
		if s.Kind == TypeString && ok1 && start.Kind == TypeInt && ok2 && length.Kind == TypeInt && ok3 {
			runes := []rune(sv)
			st := clampInt(int(stv), 0, len(runes))
			ln := clampInt(int(lnv), 0, len(runes)-st)
			return CellStr(string(runes[st : st+ln])), nil
		}
		return Cell{}, evalErr(TypeError, "substr expects (string, int, int)")
	case "datePart":
		if e := arity(2); e != nil {
			return Cell{}, e
		}
		part, val := args[0], args[1]
		if isNull(val) {
			return Null, nil
		}
		if part.Kind == TypeString && (val.Kind == TypeString || val.Kind == TypeDate || val.Kind == TypeTimestamp) {
			ds := val.Value.(string)
			spans := map[string][2]int{"year": {0, 4}, "month": {5, 2}, "day": {8, 2}}
			span, ok := spans[part.Value.(string)]
			if !ok {
				return Cell{}, evalErr(TypeError, "datePart: unknown part '"+part.Value.(string)+"'")
			}
			lo, ln := span[0], span[1]
			if len(ds) >= lo+ln {
				if i, err := strconv.ParseInt(ds[lo:lo+ln], 10, 64); err == nil {
					return CellInt(i), nil
				}
				return Cell{}, evalErr(TypeError, "datePart: unparseable component")
			}
			return Cell{}, evalErr(TypeError, "datePart: too short")
		}
		return Cell{}, evalErr(TypeError, "datePart expects (string part, date/timestamp/string)")
	case "concat":
		// Variadic; any null arg propagates (compose coalesce for
		// treat-as-empty). Non-string args stringify via the same rendering
		// as a cast to string.
		if len(args) == 0 {
			return Cell{}, evalErr(ArityError, "function 'concat' expects at least 1 arg, got 0")
		}
		var sb strings.Builder
		for _, c := range args {
			if isNull(c) {
				return Null, nil
			}
			sb.WriteString(cellString(c))
		}
		return CellStr(sb.String()), nil
	case "trim":
		if e := arity(1); e != nil {
			return Cell{}, e
		}
		c := args[0]
		if isNull(c) {
			return Null, nil
		}
		if c.Kind != TypeString {
			return Cell{}, evalErr(TypeError, "trim of a non-string")
		}
		// Pinned ASCII set — NOT the full Unicode whitespace class, which
		// diverges between hosts (U+0085 et al.).
		return CellStr(strings.Trim(c.Value.(string), " \t\r\n")), nil
	case "replace":
		if e := arity(3); e != nil {
			return Cell{}, e
		}
		if isNull(args[0]) || isNull(args[1]) || isNull(args[2]) {
			return Null, nil
		}
		if args[0].Kind != TypeString || args[1].Kind != TypeString || args[2].Kind != TypeString {
			return Cell{}, evalErr(TypeError, "replace expects (string, string, string)")
		}
		subj := args[0].Value.(string)
		find := args[1].Value.(string)
		repl := args[2].Value.(string)
		// Pinned: empty `find` returns the subject unchanged.
		if find == "" {
			return CellStr(subj), nil
		}
		return CellStr(strings.ReplaceAll(subj, find, repl)), nil
	case "dateDiffDays":
		if e := arity(2); e != nil {
			return Cell{}, e
		}
		if isNull(args[0]) || isNull(args[1]) {
			return Null, nil
		}
		da, ea := civilDays(args[0])
		if ea != nil {
			return Cell{}, ea
		}
		db, eb := civilDays(args[1])
		if eb != nil {
			return Cell{}, eb
		}
		return CellInt(db - da), nil
	}
	return Cell{}, evalErr(TypeError, "unknown scalar fn "+fn)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func evalExpr(cols Schema, r row, e ColExpr) (Cell, *EvalError) {
	switch x := e.(type) {
	case Col:
		i := colIndex(cols, x.Name)
		if i < 0 {
			return Cell{}, unknownColumn(x.Name, cols)
		}
		return r[i], nil
	case Lit:
		return x.Cell, nil
	case Binary:
		a, ea := evalExpr(cols, r, x.Left)
		if ea != nil {
			return Cell{}, ea
		}
		b, eb := evalExpr(cols, r, x.Right)
		if eb != nil {
			return Cell{}, eb
		}
		switch x.Op {
		case "add", "sub", "mul", "div", "mod":
			return arith(x.Op, a, b)
		case "eq", "ne", "lt", "le", "gt", "ge":
			return comparison(x.Op, a, b)
		case "contains", "startsWith", "endsWith":
			return stringPred(x.Op, a, b)
		default:
			return logical(x.Op, a, b)
		}
	case Not:
		c, ce := evalExpr(cols, r, x.Expr)
		if ce != nil {
			return Cell{}, ce
		}
		if c.Kind == TypeBool {
			return CellBool(!c.Value.(bool)), nil
		}
		if isNull(c) {
			return Null, nil
		}
		return Cell{}, evalErr(TypeError, "not of a non-bool")
	case Coalesce:
		for _, sub := range x.Exprs {
			c, ce := evalExpr(cols, r, sub)
			if ce != nil {
				return Cell{}, ce
			}
			if !isNull(c) {
				return c, nil
			}
		}
		return Null, nil
	case Case:
		for _, wt := range x.Cases {
			w, ce := evalExpr(cols, r, wt.When)
			if ce != nil {
				return Cell{}, ce
			}
			if w.Kind == TypeBool && w.Value.(bool) {
				return evalExpr(cols, r, wt.Then)
			}
		}
		return evalExpr(cols, r, x.ElseExpr)
	case Cast:
		c, ce := evalExpr(cols, r, x.Expr)
		if ce != nil {
			return Cell{}, ce
		}
		return castCell(x.Type, c)
	case ApplyFn:
		argv := make([]Cell, len(x.Args))
		for i, a := range x.Args {
			c, ce := evalExpr(cols, r, a)
			if ce != nil {
				return Cell{}, ce
			}
			argv[i] = c
		}
		return applyScalar(x.Fn, argv)
	case Param:
		return Cell{}, evalErr(UnboundParam, "unbound parameter '"+x.Name+"'")
	case InList:
		sv, ce := evalExpr(cols, r, x.Subject)
		if ce != nil {
			return Cell{}, ce
		}
		if isNull(sv) {
			return Null, nil
		}
		// SQL three-valued membership: any equal => true; no match having
		// seen a null item => null; else false.
		sawNull := false
		for _, it := range x.Items {
			iv, ce := evalExpr(cols, r, it)
			if ce != nil {
				return Cell{}, ce
			}
			if isNull(iv) {
				sawNull = true
				continue
			}
			c, ok := compareCells(sv, iv)
			if !ok {
				return Cell{}, evalErr(TypeError, "in: comparison between incompatible types")
			}
			if c == 0 {
				return CellBool(true), nil
			}
		}
		if sawNull {
			return Null, nil
		}
		return CellBool(false), nil
	case IsNull:
		v, ce := evalExpr(cols, r, x.Expr)
		if ce != nil {
			return Cell{}, ce
		}
		return CellBool(isNull(v)), nil
	case InParam:
		// List params resolve by substitution before evaluation — one that
		// reaches the evaluator is unbound, same strictness as a scalar Param.
		return Cell{}, evalErr(UnboundParam, "unbound parameter '"+x.Name+"'")
	}
	return Cell{}, evalErr(TypeError, "unknown ColExpr")
}

// ── type inference / aggregates ──────────────────────────────────────────────

func inferType(cells []Cell) string {
	for _, c := range cells {
		if t := typeOf(c); t != "" {
			return t
		}
	}
	return TypeString
}

func presentNums(cells []Cell) []float64 {
	var out []float64
	for _, c := range cells {
		if n, ok := asNum(c); ok {
			out = append(out, n)
		}
	}
	return out
}

func aggCells(fn, srcType string, cells []Cell) Cell {
	var present []Cell
	for _, c := range cells {
		if !isNull(c) {
			present = append(present, c)
		}
	}
	switch fn {
	case "count":
		return CellInt(int64(len(present)))
	case "first":
		if len(cells) > 0 {
			return cells[0]
		}
		return Null
	case "last":
		if len(cells) > 0 {
			return cells[len(cells)-1]
		}
		return Null
	case "sum":
		nums := presentNums(cells)
		if len(nums) == 0 {
			return Null
		}
		if srcType == TypeInt {
			var s int64
			for _, n := range nums {
				s += int64(n)
			}
			return CellInt(s)
		}
		var s float64
		for _, n := range nums {
			s += n
		}
		return CellFloat(s)
	case "mean":
		nums := presentNums(cells)
		if len(nums) == 0 {
			return Null
		}
		var s float64
		for _, n := range nums {
			s += n
		}
		return CellFloat(s / float64(len(nums)))
	case "stddev":
		nums := presentNums(cells)
		if len(nums) == 0 {
			return Null
		}
		n := float64(len(nums))
		var s float64
		for _, x := range nums {
			s += x
		}
		mean := s / n
		var v float64
		for _, x := range nums {
			v += (x - mean) * (x - mean)
		}
		return CellFloat(math.Sqrt(v / n))
	case "median":
		nums := presentNums(cells)
		if len(nums) == 0 {
			return Null
		}
		sort.Float64s(nums)
		n := len(nums)
		mid := n / 2
		if n%2 == 1 {
			return CellFloat(nums[mid])
		}
		return CellFloat((nums[mid-1] + nums[mid]) / 2.0)
	}
	// min / max
	if len(present) == 0 {
		return Null
	}
	isMin := fn == "min"
	acc := present[0]
	for _, b := range present[1:] {
		c, ok := compareCells(acc, b)
		if ok && !(isMin == (c <= 0)) {
			acc = b
		}
	}
	return acc
}

func aggType(fn, srcType string) string {
	switch fn {
	case "count":
		return TypeInt
	case "mean", "median", "stddev":
		return TypeFloat
	}
	return srcType
}

// ── sort comparator (stable; nulls last regardless of direction) ─────────────

func rowKeyCompare(cols Schema, by []OrderKey, r1, r2 row) int {
	for _, o := range by {
		i := colIndex(cols, o.Col)
		if i < 0 {
			continue
		}
		a, b := r1[i], r2[i]
		an, bn := isNull(a), isNull(b)
		var c int
		switch {
		case an && bn:
			c = 0
		case an:
			c = 1
		case bn:
			c = -1
		default:
			cc, ok := compareCells(a, b)
			if !ok {
				c = 0
			} else if o.Dir == "asc" {
				c = cc
			} else {
				c = -cc
			}
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// ── structural key for grouping / distinct ──────────────────────────────────

func cellKey(c Cell) string {
	switch c.Kind {
	case TypeInt:
		return "i\x00" + strconv.FormatInt(c.Value.(int64), 10)
	case TypeFloat:
		return "f\x00" + canonical.FormatFiniteDouble(c.Value.(float64))
	case TypeBool:
		if c.Value.(bool) {
			return "b\x001"
		}
		return "b\x000"
	case TypeString, TypeDate, TypeTimestamp:
		return c.Kind + "\x00" + c.Value.(string)
	}
	return "n"
}

func rowKey(cells []Cell) string {
	out := ""
	for _, c := range cells {
		out += cellKey(c) + "\x01"
	}
	return out
}
