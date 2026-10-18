package dataframe

// The static typer and THE derived-column typing rule (fuaran-core Phase 338).
//
// A typing says what a node's PRESENT values can be, decided from a step's
// schema and nothing else. Three states rather than two: a node that never
// produces a present value (a null literal) is compatible with every type, and
// folding it into "unknown" would cost the type of every Case whose else is a
// null literal.
//
// ONE rule types a Derive's column and an Unpivot's value column:
//
//   - where the typer decides the column (Of ty), ty IS its type on every frame —
//     full, empty or all-null alike; a present cell is stored as it was produced
//     (an int in a float or decimal column widens on the wire);
//   - where no present value can occur (Absent), the type is string;
//   - where the typer cannot decide (Unknown), the CELLS decide, by the widening
//     join (int and float join at float, int and decimal at decimal), string when
//     none is present, and two types no widening relates are refused;
//   - a float beside a decimal in one column is REFUSED by name, never widened:
//     statically where the expression's own arms carry both, and as a
//     whole-column refusal where only the cells show it.

type typingKind int

const (
	typAbsent typingKind = iota
	typOf
	typUnknown
)

type typing struct {
	kind typingKind
	ty   string
}

var (
	absent  = typing{kind: typAbsent}
	unknown = typing{kind: typUnknown}
)

func typOfT(ty string) typing { return typing{kind: typOf, ty: ty} }

func (t typing) is(ty string) bool { return t.kind == typOf && t.ty == ty }

// joinTyping is the least typing covering both: Absent is the identity, two
// equal types agree, anything else is Unknown. Deliberately NOT the widening
// join the cells take: Of t is an exactness claim, so int ⊔ float is Unknown.
func joinTyping(a, b typing) typing {
	switch {
	case a.kind == typAbsent:
		return b
	case b.kind == typAbsent:
		return a
	case a.kind == typOf && b.kind == typOf && a.ty == b.ty:
		return a
	}
	return unknown
}

func joinAll(ts []typing) typing {
	acc := absent
	for _, t := range ts {
		acc = joinTyping(acc, t)
	}
	return acc
}

func numericTyping(t typing) bool { return t.is(TypeInt) || t.is(TypeFloat) }
func exactTyping(t typing) bool   { return t.is(TypeInt) || t.is(TypeDecimal) }

// decimalPair is two exact operands at least one of which is a decimal.
func decimalPair(a, b typing) bool {
	return exactTyping(a) && exactTyping(b) && (a.is(TypeDecimal) || b.is(TypeDecimal))
}

func binaryTyping(op string, a, b typing) typing {
	// Null propagates through every operator but the logical pair BEFORE any
	// type is examined.
	if op != "and" && op != "or" && (a.kind == typAbsent || b.kind == typAbsent) {
		return absent
	}
	switch op {
	case "add", "sub", "mul":
		switch {
		case a.is(TypeInt) && b.is(TypeInt):
			return typOfT(TypeInt)
		case numericTyping(a) && numericTyping(b):
			return typOfT(TypeFloat)
		case decimalPair(a, b):
			return typOfT(TypeDecimal)
		}
		return unknown
	case "div":
		// A decimal div is refused on every row (it names Quotient).
		if numericTyping(a) && numericTyping(b) {
			return typOfT(TypeFloat)
		}
		return unknown
	case "mod":
		switch {
		case a.is(TypeInt) && b.is(TypeInt):
			return typOfT(TypeInt)
		case decimalPair(a, b):
			return typOfT(TypeDecimal)
		}
		return unknown
	case "eq", "ne", "lt", "le", "gt", "ge":
		comparable := (numericTyping(a) && numericTyping(b)) || (exactTyping(a) && exactTyping(b))
		for _, ty := range []string{TypeString, TypeDate, TypeTimestamp, TypeBool} {
			comparable = comparable || (a.is(ty) && b.is(ty))
		}
		if comparable {
			return typOfT(TypeBool)
		}
		return unknown
	case "and", "or":
		boolLike := func(t typing) bool { return t.kind == typAbsent || t.is(TypeBool) }
		if boolLike(a) && boolLike(b) {
			return joinTyping(a, b)
		}
		return unknown
	case "contains", "startsWith", "endsWith":
		if a.is(TypeString) && b.is(TypeString) {
			return typOfT(TypeBool)
		}
		return unknown
	}
	return unknown
}

// applyFnTyping types a scalar function. An arity the function refuses is an
// error on every row, so Absent; a null argument propagates exactly where the
// function propagates it.
func applyFnTyping(fn string, args []typing) typing {
	anyAbsent := false
	for _, t := range args {
		if t.kind == typAbsent {
			anyAbsent = true
		}
	}
	unary := func(decide func(typing) typing) typing {
		if len(args) != 1 || args[0].kind == typAbsent {
			return absent
		}
		return decide(args[0])
	}
	switch fn {
	case "abs":
		return unary(func(t typing) typing {
			if t.is(TypeInt) || t.is(TypeFloat) || t.is(TypeDecimal) {
				return t
			}
			return unknown
		})
	case "round", "floor", "ceil":
		// Over a decimal these are scale-0 Rounded and answer a decimal; over an
		// operand the typer cannot decide, neither can it decide these.
		return unary(func(t typing) typing {
			switch {
			case t.is(TypeDecimal):
				return t
			case t.kind == typUnknown:
				return unknown
			}
			return typOfT(TypeFloat)
		})
	case "length":
		return unary(func(typing) typing { return typOfT(TypeInt) })
	case "lower", "upper", "trim":
		return unary(func(typing) typing { return typOfT(TypeString) })
	case "substr":
		if len(args) != 3 || args[0].kind == typAbsent {
			return absent
		}
		return typOfT(TypeString)
	case "datePart":
		if len(args) != 2 || args[1].kind == typAbsent {
			return absent
		}
		return typOfT(TypeInt)
	case "concat":
		if len(args) == 0 || anyAbsent {
			return absent
		}
		return typOfT(TypeString)
	case "replace":
		if len(args) != 3 || anyAbsent {
			return absent
		}
		return typOfT(TypeString)
	case "dateDiffDays":
		if len(args) != 2 || anyAbsent {
			return absent
		}
		return typOfT(TypeInt)
	}
	return unknown
}

// typingOf is the static typing of an expression over a schema. A Param is
// Unknown (its cell is the env's); a column the schema does not carry is
// Unknown.
func typingOf(cols Schema, e ColExpr) typing {
	switch x := e.(type) {
	case Col:
		if ty := colType(cols, x.Name); ty != "" {
			return typOfT(ty)
		}
		return unknown
	case Lit:
		if ty := typeOf(x.Cell); ty != "" {
			return typOfT(ty)
		}
		return absent
	case Param:
		return unknown
	case Binary:
		return binaryTyping(x.Op, typingOf(cols, x.Left), typingOf(cols, x.Right))
	case Not:
		switch t := typingOf(cols, x.Expr); {
		case t.kind == typAbsent:
			return absent
		case t.is(TypeBool):
			return t
		}
		return unknown
	case Coalesce:
		ts := make([]typing, len(x.Exprs))
		for i, s := range x.Exprs {
			ts[i] = typingOf(cols, s)
		}
		return joinAll(ts)
	case Case:
		ts := []typing{typingOf(cols, x.ElseExpr)}
		for _, wt := range x.Cases {
			ts = append(ts, typingOf(cols, wt.Then))
		}
		return joinAll(ts)
	case Cast:
		// A cast's present values are always of the target type.
		if typingOf(cols, x.Expr).kind == typAbsent {
			return absent
		}
		return typOfT(x.Type)
	case ApplyFn:
		ts := make([]typing, len(x.Args))
		for i, a := range x.Args {
			ts[i] = typingOf(cols, a)
		}
		return applyFnTyping(x.Fn, ts)
	case InList:
		return membershipTyping(typingOf(cols, x.Subject))
	case InParam:
		return membershipTyping(typingOf(cols, x.Subject))
	case IsNull:
		return typOfT(TypeBool)
	case Quotient:
		a, b := typingOf(cols, x.Dividend), typingOf(cols, x.Divisor)
		switch {
		case a.kind == typAbsent || b.kind == typAbsent:
			return absent
		case exactTyping(a) && exactTyping(b):
			return typOfT(TypeDecimal)
		}
		return unknown
	case Rounded:
		switch a := typingOf(cols, x.Expr); {
		case a.kind == typAbsent:
			return absent
		case exactTyping(a):
			return typOfT(TypeDecimal)
		}
		return unknown
	}
	return unknown
}

// membershipTyping: a null subject answers null, everything else a boolean.
func membershipTyping(subject typing) typing {
	if subject.kind == typAbsent {
		return absent
	}
	return typOfT(TypeBool)
}

// armsHave reports whether an arm the derived value is drawn from — through
// Case and Coalesce, whose answer IS one of their operands' — carries ty.
func armsHave(ty string, cols Schema, e ColExpr) bool {
	switch x := e.(type) {
	case Coalesce:
		for _, s := range x.Exprs {
			if armsHave(ty, cols, s) {
				return true
			}
		}
		return false
	case Case:
		if armsHave(ty, cols, x.ElseExpr) {
			return true
		}
		for _, wt := range x.Cases {
			if armsHave(ty, cols, wt.Then) {
				return true
			}
		}
		return false
	}
	return typingOf(cols, e).is(ty)
}

type derivedKind int

const (
	derivedDecided derivedKind = iota
	derivedByCells
	derivedRefused
)

type derivedTyping struct {
	kind derivedKind
	ty   string
}

// deriveTyping is how a Derive of e over cols types its column.
func deriveTyping(cols Schema, e ColExpr) derivedTyping {
	if armsHave(TypeFloat, cols, e) && armsHave(TypeDecimal, cols, e) {
		return derivedTyping{kind: derivedRefused}
	}
	switch t := typingOf(cols, e); t.kind {
	case typOf:
		return derivedTyping{kind: derivedDecided, ty: t.ty}
	case typAbsent:
		return derivedTyping{kind: derivedDecided, ty: TypeString}
	}
	return derivedTyping{kind: derivedByCells}
}

// joinColumnType is the least column type two types both widen into: the type
// itself, or int joined with float / decimal; ok=false for any other pair,
// float beside decimal included.
func joinColumnType(a, b string) (string, bool) {
	switch {
	case a == b:
		return a, true
	case a == TypeInt && (b == TypeFloat || b == TypeDecimal):
		return b, true
	case b == TypeInt && (a == TypeFloat || a == TypeDecimal):
		return a, true
	}
	return "", false
}

// unpivotTyping types an Unpivot's value column over the value columns'
// declared types — the same rule, the value columns being its arms: their
// widening join where the schema decides it, string for no value column, a
// float beside a decimal refused, and a pair no widening relates left to the
// cells.
func unpivotTyping(types []string) derivedTyping {
	hasFloat, hasDecimal := false, false
	for _, t := range types {
		hasFloat = hasFloat || t == TypeFloat
		hasDecimal = hasDecimal || t == TypeDecimal
	}
	if hasFloat && hasDecimal {
		return derivedTyping{kind: derivedRefused}
	}
	if len(types) == 0 {
		return derivedTyping{kind: derivedDecided, ty: TypeString}
	}
	acc := types[0]
	for _, t := range types[1:] {
		j, ok := joinColumnType(acc, t)
		if !ok {
			return derivedTyping{kind: derivedByCells}
		}
		acc = j
	}
	return derivedTyping{kind: derivedDecided, ty: acc}
}

// floatBesideDecimal is the refusal a derived column holding a float beside a
// decimal meets, naming the column and the cast that resolves it.
func floatBesideDecimal(column string) *EvalError {
	return evalErr(TypeError, "derived column '"+column+"' joins a float and a decimal: cast one to the other's "+
		"type first - Cast(decimal, ...) to keep the digits exact, Cast(float, ...) to compute approximately")
}

// typeFromCells is the cells' half of the rule: the widening join over the
// present cells, string when none is present, and the named refusals where the
// cells hold a float beside a decimal, or two types with no widening between
// them.
func typeFromCells(column string, cells []Cell) (string, *EvalError) {
	acc := ""
	sawFloat, sawDecimal := false, false
	var disjointA, disjointB string
	for _, c := range cells {
		t := typeOf(c)
		if t == "" {
			continue
		}
		sawFloat = sawFloat || t == TypeFloat
		sawDecimal = sawDecimal || t == TypeDecimal
		if acc == "" {
			acc = t
			continue
		}
		if j, ok := joinColumnType(acc, t); ok {
			acc = j
		} else if disjointA == "" {
			disjointA, disjointB = acc, t
		}
	}
	switch {
	case sawFloat && sawDecimal:
		return "", floatBesideDecimal(column)
	case disjointA != "":
		return "", evalErr(TypeError, "derived column '"+column+"' holds a "+disjointA+" beside a "+disjointB+
			", which no column type holds together: cast one to the other's type first")
	case acc == "":
		return TypeString, nil
	}
	return acc, nil
}

// columnTypeBy is a derived column's type under its typing: the decided type,
// or the cells' answer.
func columnTypeBy(dt derivedTyping, column string, cells []Cell) (string, *EvalError) {
	switch dt.kind {
	case derivedDecided:
		return dt.ty, nil
	case derivedRefused:
		return "", floatBesideDecimal(column)
	}
	return typeFromCells(column, cells)
}
