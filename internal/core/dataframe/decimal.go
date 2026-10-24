package dataframe

import "github.com/Fuaran-Core/fuaran-core-go/decimal"

// The exact decimal (fuaran-core Phase 276; the evaluator's arithmetic over it,
// Phase 277). A Decimal cell carries its CANONICAL TEXT, never a host decimal or
// float: the digits are the value, and text is the one representation every host
// agrees on.
//
// The implementation is the Core spine's (github.com/Fuaran-Core/fuaran-core-go/decimal),
// certified against the reference's decimal laws in its own repository; this file
// forwards to it under the names the evaluator, the codec and the verbs use, so the
// Compute twin's arithmetic is the spine's arithmetic. The rounding-mode spellings
// are the spine's constants read as the wire strings the codec decodes.

// The rounding modes — the wire spellings of the seven-mode set (closed: an
// unknown spelling is refused at decode).
const (
	RoundHalfEven = string(decimal.RoundHalfEven)
	RoundHalfUp   = string(decimal.RoundHalfUp)
	RoundHalfDown = string(decimal.RoundHalfDown)
	RoundUp       = string(decimal.RoundUp)
	RoundDown     = string(decimal.RoundDown)
	RoundCeiling  = string(decimal.RoundCeiling)
	RoundFloor    = string(decimal.RoundFloor)
)

var roundingModes = set(RoundHalfEven, RoundHalfUp, RoundHalfDown, RoundUp, RoundDown, RoundCeiling, RoundFloor)

// MaxRoundingScale is the largest scale a rounding may name. A quotient is
// computed to the scale it names, so the bound is a resource limit rather than
// a statement about decimals.
const MaxRoundingScale = decimal.MaxScale

// DecimalCanonical is the canonical form of s, or ok=false where s is not
// decimal text.
func DecimalCanonical(s string) (string, bool) { return decimal.Canonical(s) }

// DecimalCompare is the numeric order of two decimal texts (-1 / 0 / 1), or
// ok=false where either is not decimal text. Canonical or not: 1.50 = 1.5.
func DecimalCompare(a, b string) (int, bool) { return decimal.Compare(a, b) }

// DecimalAdd is the exact sum in canonical form, or ok=false where either is
// not decimal text.
func DecimalAdd(a, b string) (string, bool) { return decimal.Add(a, b) }

func decimalSub(a, b string) (string, bool) { return decimal.Sub(a, b) }

func decimalMul(a, b string) (string, bool) { return decimal.Mul(a, b) }

// decimalRem is the remainder of a truncating division, with the dividend's
// sign — the integer mod's convention, and exact. ok=false for a zero divisor
// as well as for text that is not decimal (callers check the divisor first).
func decimalRem(a, b string) (string, bool) { return decimal.Rem(a, b) }

func decimalAbs(a string) (string, bool) { return decimal.Abs(a) }

func decimalIsZero(a string) bool { return decimal.IsZero(a) }

// decimalQuantize brings a decimal to scale places under mode — the value
// itself where it already has no more places than that.
func decimalQuantize(mode string, scale int, a string) (string, bool) {
	return decimal.Quantize(decimal.RoundingMode(mode), scale, a)
}

// decimalDivide is dividend / divisor correctly rounded to scale places under
// mode. isNull reports a zero divisor; ok=false where an operand is not
// decimal text.
func decimalDivide(mode string, scale int, a, b string) (q string, isNull bool, ok bool) {
	return decimal.Divide(decimal.RoundingMode(mode), scale, a, b)
}

// DecimalToFloat is the nearest float64 to a decimal text, or ok=false where it
// is not decimal text OR its magnitude is past the float range — an infinity is
// not the nearest float to any decimal. The one place the type rounds, for the
// results that are a float by declaration (mean, median, stddev, a cast).
func DecimalToFloat(s string) (float64, bool) { return decimal.ToFloat(s) }

// decimalOfFloat is the decimal text of a FINITE float — its shortest
// round-trip digits laid out without an exponent. ok=false for NaN and the
// infinities.
func decimalOfFloat(f float64) (string, bool) { return decimal.OfFloat(f) }

// decimalTruncateToInt64 is the integer part (toward zero), or ok=false past
// int64.
func decimalTruncateToInt64(a string) (int64, bool) { return decimal.TruncateToInt64(a) }
