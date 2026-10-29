package dataframe

import (
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	canonical "github.com/Fuaran-Core/fuaran-core-go/json"
)

// The unit algebra's parse and canonical render (WIRE_FORMAT §3.6.26; twin of
// the reference's Fuaran.Core.Unit, fuaran-core Phase 426). A schema entry's
// `unit` is read to a Unit and written back as its ONE canonical text, so two
// spellings of one product (`s-1.m`, `m/s`) carry one set of bytes. This host
// needs the parse, the render and equality, and (Phase 2208) the algebra the
// dataframe evaluator's unit check reads: the product, quotient and integer
// power of units, dimensions, and the exact conversion factor. The atom table, the alias, the prefix try-order and
// the non-ratio / arbitrary sets are the reference's in full, so each refused
// token is refused in the reference's class.

// unitAtom is one factor's atom: an admitted symbol (or a bracketed currency)
// under an optional SI prefix.
type unitAtom struct {
	Symbol string
	Prefix string
}

// unitFactor is an atom with its non-zero exponent, within ±2147483647.
type unitFactor struct {
	Atom unitAtom
	Exp  int64
}

// Unit is a unit of measure held in its canonical form: factors ordered by
// symbol then prefix (ordinally), equal atoms merged, zero exponents dropped.
// Two units are equal exactly when they are one product (`km/h` and `m/s` are
// different units). The zero value is the dimensionless unit `1`.
type Unit struct {
	factors []unitFactor
}

// Equal reports whether two units are one product.
func (u Unit) Equal(v Unit) bool {
	if len(u.factors) != len(v.factors) {
		return false
	}
	for i := range u.factors {
		if u.factors[i] != v.factors[i] {
			return false
		}
	}
	return true
}

// String is the unit's one canonical text: the positive-exponent factors joined
// by `.`, then each negative-exponent factor as `/` and the factor; an exponent
// written as its magnitude and omitted where it is 1; `1` for the dimensionless
// unit, and a leading `/` where every exponent is negative.
func (u Unit) String() string {
	text := func(f unitFactor) string {
		e := f.Exp
		if e < 0 {
			e = -e
		}
		s := f.Atom.Prefix + f.Atom.Symbol
		if e != 1 {
			s += strconv.FormatInt(e, 10)
		}
		return s
	}
	var num, den []string
	for _, f := range u.factors {
		if f.Exp > 0 {
			num = append(num, text(f))
		} else {
			den = append(den, text(f))
		}
	}
	if len(num) == 0 && len(den) == 0 {
		return "1"
	}
	var sb strings.Builder
	sb.WriteString(strings.Join(num, "."))
	for _, d := range den {
		sb.WriteString("/")
		sb.WriteString(d)
	}
	return sb.String()
}

// Unit refusal classes — the reference's UnitRefusal cases.
const (
	UnitEmpty            = "Empty"
	UnitMalformed        = "Malformed"
	UnitUnknownAtom      = "UnknownAtom"
	UnitAnnotation       = "Annotation"
	UnitArbitraryUnit    = "ArbitraryUnit"
	UnitNonRatioUnit     = "NonRatioUnit"
	UnitPrefixNotAllowed = "PrefixNotAllowed"
	UnitNumericFactor    = "NumericFactor"
)

// UnitRefusal says why a text is not a unit. Every class but UnitEmpty names
// the offending token as written (Malformed: what stands there, "" at the end)
// and its byte position; Malformed also names what the grammar expected.
type UnitRefusal struct {
	Class    string
	Token    string
	Position int
	Expected string
}

// Error is the refusal's text, worded as the reference's codec words it.
func (r *UnitRefusal) Error() string {
	at := func(what string) string {
		return what + " '" + r.Token + "' at position " + strconv.Itoa(r.Position)
	}
	switch r.Class {
	case UnitEmpty:
		return "the text is empty"
	case UnitMalformed:
		return "expected " + r.Expected + " at position " + strconv.Itoa(r.Position) + ", found '" + r.Token + "'"
	case UnitUnknownAtom:
		return at("unknown unit symbol")
	case UnitAnnotation:
		return at("annotation")
	case UnitArbitraryUnit:
		return at("arbitrary unit")
	case UnitNonRatioUnit:
		return at("non-ratio unit")
	case UnitPrefixNotAllowed:
		return at("prefix not allowed on")
	}
	return at("numeric factor")
}

// unitAtomDef is an admitted atom: whether SI prefixes apply, its scale
// relative to the coherent base (metre, GRAM, second, ampere, kelvin, mole,
// candela) as Num/Den, and its exponents over [length, mass, time, current,
// temperature, amount, luminous intensity] — the reference's AtomDef.
type unitAtomDef struct {
	Metric bool
	Num    int64
	Den    int64
	Dims   [7]int64
}

// unitAtoms is every admitted atom by its canonical symbol (the reference's
// UnitVocabulary.atoms).
var unitAtoms = map[string]unitAtomDef{
	// The seven SI base dimensions; mass at the gram, UCUM's base.
	"m":   {true, 1, 1, [7]int64{1, 0, 0, 0, 0, 0, 0}},
	"g":   {true, 1, 1, [7]int64{0, 1, 0, 0, 0, 0, 0}},
	"s":   {true, 1, 1, [7]int64{0, 0, 1, 0, 0, 0, 0}},
	"A":   {true, 1, 1, [7]int64{0, 0, 0, 1, 0, 0, 0}},
	"K":   {true, 1, 1, [7]int64{0, 0, 0, 0, 1, 0, 0}},
	"mol": {true, 1, 1, [7]int64{0, 0, 0, 0, 0, 1, 0}},
	"cd":  {true, 1, 1, [7]int64{0, 0, 0, 0, 0, 0, 1}},
	// The SI coherent derived units (degree Celsius excepted: it is affine).
	"rad": {true, 1, 1, [7]int64{0, 0, 0, 0, 0, 0, 0}},
	"sr":  {true, 1, 1, [7]int64{0, 0, 0, 0, 0, 0, 0}},
	"Hz":  {true, 1, 1, [7]int64{0, 0, -1, 0, 0, 0, 0}},
	"N":   {true, 1000, 1, [7]int64{1, 1, -2, 0, 0, 0, 0}},
	"Pa":  {true, 1000, 1, [7]int64{-1, 1, -2, 0, 0, 0, 0}},
	"J":   {true, 1000, 1, [7]int64{2, 1, -2, 0, 0, 0, 0}},
	"W":   {true, 1000, 1, [7]int64{2, 1, -3, 0, 0, 0, 0}},
	"C":   {true, 1, 1, [7]int64{0, 0, 1, 1, 0, 0, 0}},
	"V":   {true, 1000, 1, [7]int64{2, 1, -3, -1, 0, 0, 0}},
	"F":   {true, 1, 1000, [7]int64{-2, -1, 4, 2, 0, 0, 0}},
	"Ohm": {true, 1000, 1, [7]int64{2, 1, -3, -2, 0, 0, 0}},
	"S":   {true, 1, 1000, [7]int64{-2, -1, 3, 2, 0, 0, 0}},
	"Wb":  {true, 1000, 1, [7]int64{2, 1, -2, -1, 0, 0, 0}},
	"T":   {true, 1000, 1, [7]int64{0, 1, -2, -1, 0, 0, 0}},
	"H":   {true, 1000, 1, [7]int64{2, 1, -2, -2, 0, 0, 0}},
	"lm":  {true, 1, 1, [7]int64{0, 0, 0, 0, 0, 0, 1}},
	"lx":  {true, 1, 1, [7]int64{-2, 0, 0, 0, 0, 0, 1}},
	"Bq":  {true, 1, 1, [7]int64{0, 0, -1, 0, 0, 0, 0}},
	"Gy":  {true, 1, 1, [7]int64{2, 0, -2, 0, 0, 0, 0}},
	"Sv":  {true, 1, 1, [7]int64{2, 0, -2, 0, 0, 0, 0}},
	"kat": {true, 1, 1, [7]int64{0, 0, -1, 0, 0, 1, 0}},
	// Metric non-SI units.
	"L":   {true, 1, 1000, [7]int64{3, 0, 0, 0, 0, 0, 0}},
	"t":   {true, 1000000, 1, [7]int64{0, 1, 0, 0, 0, 0, 0}},
	"bar": {true, 100000000, 1, [7]int64{-1, 1, -2, 0, 0, 0, 0}},
	// Non-metric units: time, ratios, and the international customary set.
	"min":     {false, 60, 1, [7]int64{0, 0, 1, 0, 0, 0, 0}},
	"h":       {false, 3600, 1, [7]int64{0, 0, 1, 0, 0, 0, 0}},
	"d":       {false, 86400, 1, [7]int64{0, 0, 1, 0, 0, 0, 0}},
	"wk":      {false, 604800, 1, [7]int64{0, 0, 1, 0, 0, 0, 0}},
	"a":       {false, 31557600, 1, [7]int64{0, 0, 1, 0, 0, 0, 0}},
	"%":       {false, 1, 100, [7]int64{0, 0, 0, 0, 0, 0, 0}},
	"[ppm]":   {false, 1, 1000000, [7]int64{0, 0, 0, 0, 0, 0, 0}},
	"[ppb]":   {false, 1, 1000000000, [7]int64{0, 0, 0, 0, 0, 0, 0}},
	"[in_i]":  {false, 127, 5000, [7]int64{1, 0, 0, 0, 0, 0, 0}},
	"[ft_i]":  {false, 381, 1250, [7]int64{1, 0, 0, 0, 0, 0, 0}},
	"[yd_i]":  {false, 1143, 1250, [7]int64{1, 0, 0, 0, 0, 0, 0}},
	"[mi_i]":  {false, 201168, 125, [7]int64{1, 0, 0, 0, 0, 0, 0}},
	"[lb_av]": {false, 45359237, 100000, [7]int64{0, 1, 0, 0, 0, 0, 0}},
	"[oz_av]": {false, 45359237, 1600000, [7]int64{0, 1, 0, 0, 0, 0, 0}},
}

// unitAliases are spellings read as another atom: UCUM's lower-case litre.
var unitAliases = map[string]string{"l": "L"}

// unitPrefixPowers are UCUM's twenty prefixes and their powers of ten, in the
// reference's TRY ORDER: `da` first, so that `dam` reads as the decametre.
var unitPrefixPowers = []struct {
	Prefix string
	Power  int64
}{
	{"da", 1}, {"Y", 24}, {"Z", 21}, {"E", 18}, {"P", 15}, {"T", 12}, {"G", 9}, {"M", 6}, {"k", 3}, {"h", 2},
	{"d", -1}, {"c", -2}, {"m", -3}, {"u", -6}, {"n", -9}, {"p", -12}, {"f", -15}, {"a", -18}, {"z", -21}, {"y", -24},
}

// unitNonRatio are UCUM's special units whose conversion is not a ratio.
var unitNonRatio = set(
	"Cel", "[degF]", "[degRe]", "[pH]", "Np", "B", "B[SPL]", "B[V]", "B[mV]", "B[uV]", "B[10.nV]",
	"B[W]", "B[kW]", "bit_s", "[p'diop]", "%[slope]", "[hp_X]", "[hp_C]", "[hp_M]", "[hp_Q]",
	"[kp_X]", "[kp_C]", "[kp_M]", "[kp_Q]",
)

// unitArbitrary are UCUM's arbitrary units, read before the currency rule.
var unitArbitrary = set(
	"[iU]", "[IU]", "[arb'U]", "[USP'U]", "[GPL'U]", "[MPL'U]", "[APL'U]", "[beth'U]", "[anti'Xa'U]",
	"[todd'U]", "[dye'U]", "[smgy'U]", "[bdsk'U]", "[ka'U]", "[knk'U]", "[mclg'U]", "[tb'U]",
	"[CCID_50]", "[TCID_50]", "[EID_50]", "[PFU]", "[FFU]", "[CFU]", "[IR]", "[BAU]", "[AU]",
	"[Amb'a'1'U]", "[PNU]", "[Lf]", "[D'ag'U]", "[FEU]", "[ELU]", "[EU]",
)

func isUpperASCII(c byte) bool { return c >= 'A' && c <= 'Z' }

// isCurrency is an ISO 4217 code's SHAPE in brackets (`[GBP]`), not one of the
// arbitrary units.
func isCurrency(tok string) bool {
	return len(tok) == 5 && tok[0] == '[' && tok[4] == ']' &&
		isUpperASCII(tok[1]) && isUpperASCII(tok[2]) && isUpperASCII(tok[3]) && !unitArbitrary[tok]
}

// tryAtom is the canonical symbol of an admitted atom, through the aliases.
func tryAtom(tok string) (string, bool) {
	if c, ok := unitAliases[tok]; ok {
		tok = c
	}
	_, ok := unitAtoms[tok]
	return tok, ok
}

func unitRefusal(class, tok string, pos int) *UnitRefusal {
	return &UnitRefusal{Class: class, Token: tok, Position: pos}
}

func unitMalformed(found string, pos int, expected string) *UnitRefusal {
	return &UnitRefusal{Class: UnitMalformed, Token: found, Position: pos, Expected: expected}
}

// resolveAtom resolves one symbol token to an atom, or to its refusal class.
func resolveAtom(tok string, pos int) (unitAtom, *UnitRefusal) {
	if sym, ok := tryAtom(tok); ok {
		return unitAtom{Symbol: sym}, nil
	}
	switch {
	case unitNonRatio[tok]:
		return unitAtom{}, unitRefusal(UnitNonRatioUnit, tok, pos)
	case unitArbitrary[tok]:
		return unitAtom{}, unitRefusal(UnitArbitraryUnit, tok, pos)
	case isCurrency(tok):
		return unitAtom{Symbol: tok}, nil
	}
	for _, pp := range unitPrefixPowers {
		p := pp.Prefix
		if len(tok) <= len(p) || !strings.HasPrefix(tok, p) {
			continue
		}
		rest := tok[len(p):]
		if sym, ok := tryAtom(rest); ok {
			if unitAtoms[sym].Metric {
				return unitAtom{Symbol: sym, Prefix: p}, nil
			}
			return unitAtom{}, unitRefusal(UnitPrefixNotAllowed, tok, pos)
		}
		switch {
		case unitNonRatio[rest]:
			return unitAtom{}, unitRefusal(UnitNonRatioUnit, tok, pos)
		case unitArbitrary[rest]:
			return unitAtom{}, unitRefusal(UnitArbitraryUnit, tok, pos)
		case isCurrency(rest):
			return unitAtom{Symbol: rest, Prefix: p}, nil
		}
	}
	return unitAtom{}, unitRefusal(UnitUnknownAtom, tok, pos)
}

const maxUnitExp = int64(2147483647)

const unitExpRange = "an exponent within ±2147483647"

func atomLess(a, b unitAtom) bool {
	if c := canonical.OrdinalCompare(a.Symbol, b.Symbol); c != 0 {
		return c < 0
	}
	return canonical.OrdinalLess(a.Prefix, b.Prefix)
}

// combineFactors merges b, scaled by sign, into a — equal atoms summed, zero
// exponents dropped, canonical order — or ok=false where an exponent leaves
// ±2147483647.
func combineFactors(a []unitFactor, sign int64, b []unitFactor) ([]unitFactor, bool) {
	m := make(map[unitAtom]int64, len(a)+len(b))
	for _, f := range a {
		m[f.Atom] = f.Exp
	}
	for _, f := range b {
		sum := m[f.Atom] + sign*f.Exp
		if sum > maxUnitExp || sum < -maxUnitExp {
			return nil, false
		}
		m[f.Atom] = sum
	}
	out := make([]unitFactor, 0, len(m))
	for k, e := range m {
		if e != 0 {
			out = append(out, unitFactor{Atom: k, Exp: e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return atomLess(out[i].Atom, out[j].Atom) })
	return out, true
}

// unitTerm is one open term of the parse: its product so far, the sign the next
// component takes, the sign the whole term takes in its enclosing term, and
// where its `(` stood.
type unitTerm struct {
	acc    []unitFactor
	next   int64
	outer  int64
	opened int
}

func isUnitLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isUnitDigit(c byte) bool  { return c >= '0' && c <= '9' }

// charAt is the character standing at pos ("" at the end), for a refusal.
func charAt(text string, pos int) string {
	if pos >= len(text) {
		return ""
	}
	r, size := utf8.DecodeRuneInString(text[pos:])
	if r == utf8.RuneError && size <= 1 {
		return text[pos : pos+1]
	}
	return string(r)
}

// annotation refuses a `{...}` at pos: an Annotation when it closes, else
// Malformed.
func annotation(text string, pos int) *UnitRefusal {
	end := strings.IndexByte(text[pos:], '}')
	if end < 0 {
		return unitMalformed(text[pos:], pos, "'}' closing the annotation")
	}
	return unitRefusal(UnitAnnotation, text[pos:pos+end+1], pos)
}

// ParseUnit reads a unit from its text (UCUM's case-sensitive syntax over the
// admitted subset): atoms under an optional prefix, each with an optional signed
// integer exponent; products `.`, left-associative quotients `/` (with an
// optional leading `/` at the start of a term), parentheses, and the factor `1`.
// No white space is read. An iterative parse over an explicit stack of open
// terms, as the reference's.
func ParseUnit(text string) (Unit, *UnitRefusal) {
	n := len(text)
	if n == 0 {
		return Unit{}, &UnitRefusal{Class: UnitEmpty}
	}
	stack := []unitTerm{{next: 1, outer: 1}}
	top := func() *unitTerm { return &stack[len(stack)-1] }
	pos := 0
	expectComponent := true
	termStart := true
	for {
		if expectComponent {
			if pos >= n {
				return Unit{}, unitMalformed("", pos, "a unit")
			}
			c := text[pos]
			switch {
			case termStart && c == '/':
				top().next = -1
				pos++
				termStart = false
			case c == '(':
				stack = append(stack, unitTerm{next: 1, outer: top().next, opened: pos})
				pos++
				termStart = true
			case isUnitDigit(c):
				start := pos
				for pos < n && isUnitDigit(text[pos]) {
					pos++
				}
				if pos < n && (text[pos] == '*' || text[pos] == '^') {
					pos++
					if pos < n && (text[pos] == '+' || text[pos] == '-') {
						pos++
					}
					for pos < n && isUnitDigit(text[pos]) {
						pos++
					}
				}
				tok := text[start:pos]
				if tok != "1" {
					return Unit{}, unitRefusal(UnitNumericFactor, tok, start)
				}
				expectComponent = false
				termStart = false
			case c == '{':
				return Unit{}, annotation(text, pos)
			case isUnitLetter(c) || c == '[' || c == '%':
				start := pos
				for pos < n && (isUnitLetter(text[pos]) || text[pos] == '_' || text[pos] == '\'' ||
					text[pos] == '%' || text[pos] == '[') {
					if text[pos] == '[' {
						end := strings.IndexByte(text[pos:], ']')
						if end < 0 {
							return Unit{}, unitMalformed(text[pos:], pos, "']' closing the symbol")
						}
						pos += end + 1
					} else {
						pos++
					}
				}
				tok := text[start:pos]
				atom, r := resolveAtom(tok, start)
				if r != nil {
					return Unit{}, r
				}
				expStart := pos
				sign := int64(1)
				if pos < n && (text[pos] == '+' || text[pos] == '-') {
					if text[pos] == '-' {
						sign = -1
					}
					pos++
				}
				digitsStart := pos
				value := int64(0)
				over := false
				for pos < n && isUnitDigit(text[pos]) {
					if !over {
						value = value*10 + int64(text[pos]-'0')
						if value > maxUnitExp {
							over = true
						}
					}
					pos++
				}
				switch {
				case pos == digitsStart && pos > expStart:
					return Unit{}, unitMalformed(charAt(text, pos), pos, "the digits of an exponent")
				case over:
					return Unit{}, unitMalformed(text[expStart:pos], expStart, unitExpRange)
				}
				e := int64(1)
				if pos != expStart {
					e = sign * value
				}
				var comp []unitFactor
				if e != 0 {
					comp = []unitFactor{{Atom: atom, Exp: e}}
				}
				t := top()
				acc, ok := combineFactors(t.acc, t.next, comp)
				if !ok {
					return Unit{}, unitMalformed(text[start:pos], start, unitExpRange)
				}
				t.acc = acc
				expectComponent = false
				termStart = false
			default:
				return Unit{}, unitMalformed(charAt(text, pos), pos, "a unit")
			}
			continue
		}
		if pos >= n {
			if len(stack) == 1 {
				return Unit{factors: stack[0].acc}, nil
			}
			return Unit{}, unitMalformed("", pos, "')' closing the '(' at "+strconv.Itoa(top().opened))
		}
		switch c := text[pos]; c {
		case '.':
			top().next = 1
			pos++
			expectComponent = true
		case '/':
			top().next = -1
			pos++
			expectComponent = true
		case ')':
			if len(stack) < 2 {
				return Unit{}, unitMalformed(")", pos, "'.', '/' or the end")
			}
			at := pos
			pos++
			inner := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			outer := top()
			acc, ok := combineFactors(outer.acc, inner.outer, inner.acc)
			if !ok {
				return Unit{}, unitMalformed(")", at, unitExpRange)
			}
			outer.acc = acc
		case '{':
			return Unit{}, annotation(text, pos)
		default:
			expected := "'.', '/' or the end"
			if len(stack) > 1 {
				expected = "'.', '/' or ')'"
			}
			return Unit{}, unitMalformed(charAt(text, pos), pos, expected)
		}
	}
}

// ── the algebra (Phase 2208) ────────────────────────────────────────────────
//
// What the evaluator's unit check reads (the reference's Unit.mul / div / pow /
// compatible / conversionFactor, fuaran-core#426). Where the reference raises
// because an exponent would leave ±2147483647, these answer ok=false and the
// caller names the refusal.

// MulUnit is the product of two units.
func MulUnit(a, b Unit) (Unit, bool) {
	f, ok := combineFactors(a.factors, 1, b.factors)
	return Unit{factors: f}, ok
}

// DivUnit is the quotient a / b.
func DivUnit(a, b Unit) (Unit, bool) {
	f, ok := combineFactors(a.factors, -1, b.factors)
	return Unit{factors: f}, ok
}

// PowUnit is u to the integer power n (dimensionless at n = 0).
func PowUnit(u Unit, n int64) (Unit, bool) {
	if n == 0 {
		return Unit{}, true
	}
	out := make([]unitFactor, len(u.factors))
	for i, f := range u.factors {
		e := f.Exp * n
		if (n != 0 && e/n != f.Exp) || e > maxUnitExp || e < -maxUnitExp {
			return Unit{}, false
		}
		out[i] = unitFactor{Atom: f.Atom, Exp: e}
	}
	return Unit{factors: out}, true
}

// dimension is what a unit measures: non-zero exponents by base dimension, and
// one dimension per currency.
func (u Unit) dimension() map[string]int64 {
	m := map[string]int64{}
	for _, f := range u.factors {
		if isCurrency(f.Atom.Symbol) {
			m["["+f.Atom.Symbol] += f.Exp
			continue
		}
		for i, x := range unitAtoms[f.Atom.Symbol].Dims {
			m[strconv.Itoa(i)] += x * f.Exp
		}
	}
	for k, x := range m {
		if x == 0 {
			delete(m, k)
		}
	}
	return m
}

// CompatibleUnits reports whether two units measure one dimension, so a value
// in one has a value in the other. Every currency is a dimension of its own.
func CompatibleUnits(a, b Unit) bool {
	da, db := a.dimension(), b.dimension()
	if len(da) != len(db) {
		return false
	}
	for k, x := range da {
		if db[k] != x {
			return false
		}
	}
	return true
}

func bigPow(x int64, k int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(x), big.NewInt(k), nil)
}

// scale is the unit's scale relative to the coherent base, num/den in lowest
// terms (the reference's scale).
func (u Unit) scale() (*big.Int, *big.Int) {
	num, den := big.NewInt(1), big.NewInt(1)
	ten := int64(0)
	for _, f := range u.factors {
		for _, pp := range unitPrefixPowers {
			if pp.Prefix == f.Atom.Prefix {
				ten += pp.Power * f.Exp
			}
		}
		if isCurrency(f.Atom.Symbol) {
			continue
		}
		a := unitAtoms[f.Atom.Symbol]
		if f.Exp > 0 {
			num.Mul(num, bigPow(a.Num, f.Exp))
			den.Mul(den, bigPow(a.Den, f.Exp))
		} else {
			num.Mul(num, bigPow(a.Den, -f.Exp))
			den.Mul(den, bigPow(a.Num, -f.Exp))
		}
	}
	if ten > 0 {
		num.Mul(num, bigPow(10, ten))
	} else if ten < 0 {
		den.Mul(den, bigPow(10, -ten))
	}
	g := new(big.Int).GCD(nil, nil, num, den)
	return num.Quo(num, g), den.Quo(den, g)
}

// ConversionFactor is the exact factor num/den that turns a value in source
// into the same quantity in target (km/h to m/s is 5/18); ok=false where the
// two are not compatible.
func ConversionFactor(source, target Unit) (num, den *big.Int, ok bool) {
	if !CompatibleUnits(source, target) {
		return nil, nil, false
	}
	q, ok := DivUnit(source, target)
	if !ok {
		return nil, nil, false
	}
	num, den = q.scale()
	return num, den, true
}

// ratioToFloat reads a ratio into a float literal as the reference does: each
// side to its nearest double, then divided.
func ratioToFloat(num, den *big.Int) float64 {
	n, _ := new(big.Float).SetInt(num).Float64()
	d, _ := new(big.Float).SetInt(den).Float64()
	return n / d
}
