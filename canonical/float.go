// Package canonical implements the canonical-JSON encoding primitives the Fuaran
// UI wire format requires: number form, key sort, and string escaping. The
// number form is the make-or-break of any host (WIRE_FORMAT.md §5) — this is the
// first brick fuaran-go ships.
package canonical

import (
	"strconv"
	"strings"
)

// FormatFiniteDouble renders a finite float64 in the canonical Fuaran wire form,
// matching .NET Double.ToString("R", InvariantCulture):
//
//   - shortest round-trip significant digits (Go's strconv shortest form shares
//     the .NET / V8 / CPython digit sequence — all David-Gay-family);
//   - fixed-point layout iff the leading-digit exponent is in [-4, 16];
//   - otherwise scientific with an uppercase 'E', an always-present exponent
//     sign, and a ≥2-digit zero-padded exponent;
//   - negative zero collapses to "0".
//
// The caller must ensure f is finite — NaN and ±Inf are not representable on the
// wire and are rejected upstream. This mirrors fuaran-py's format_finite_double
// and the F# host's formatFiniteDouble; the corpus metric-float-* fixtures pin
// the divergence-zone values.
func FormatFiniteDouble(f float64) string {
	if f == 0 {
		return "0" // collapses -0 to 0
	}

	// Shortest round-trip digits via the scientific form d[.ddd]e±XX.
	s := strconv.FormatFloat(f, 'e', -1, 64)

	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}

	eIdx := strings.IndexByte(s, 'e')
	mantissa := s[:eIdx]
	exp, _ := strconv.Atoi(s[eIdx+1:])

	intPart := mantissa
	fracPart := ""
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		intPart = mantissa[:dot]
		fracPart = mantissa[dot+1:]
	}
	digits := intPart + fracPart // significant digits, first digit has place value 10^exp

	var out string
	if exp >= -4 && exp <= 16 {
		out = fixedPoint(digits, exp)
	} else {
		out = scientific(digits, exp)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// fixedPoint lays significant digits out with a decimal point, where the first
// digit has place value 10^exp (exp in [-4, 16]).
func fixedPoint(digits string, exp int) string {
	if exp >= 0 {
		intLen := exp + 1
		if len(digits) <= intLen {
			return digits + strings.Repeat("0", intLen-len(digits))
		}
		return digits[:intLen] + "." + digits[intLen:]
	}
	// exp < 0: 0.<(-exp-1) zeros><digits>
	return "0." + strings.Repeat("0", -exp-1) + digits
}

// scientific lays significant digits out as d[.ddd]E±XX with the .NET exponent
// conventions (uppercase E, mandatory sign, ≥2 zero-padded exponent digits).
func scientific(digits string, exp int) string {
	var mant string
	if len(digits) == 1 {
		mant = digits
	} else {
		mant = digits[:1] + "." + digits[1:]
	}

	sign := "+"
	mag := exp
	if exp < 0 {
		sign = "-"
		mag = -exp
	}
	expStr := strconv.Itoa(mag)
	if len(expStr) < 2 {
		expStr = "0" + expStr
	}
	return mant + "E" + sign + expStr
}
