package wire

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
)

// The canonical encoder (WIRE_FORMAT.md §2). This is the load-bearing half of
// the codec: two structurally-equal inputs must produce byte-for-byte identical
// output across hosts, so it follows the canonical rules directly and never
// delegates to encoding/json (whose number and key formatting would not match):
//
//   - object keys sorted by Ordinal comparison ("$type" therefore always lands
//     before any lower-case data key — '$' precedes every letter and digit);
//   - integers as plain decimals; finite floats in the shortest-round-trip
//     canonical layout (canonical.FormatFiniteDouble); the specials NaN / ±Inf
//     as quoted sentinels; -0 collapsed to 0 (rule 5);
//   - strings escaped per rule 6 only (canonical.EscapeString);
//   - no insignificant whitespace.
//
// Key comparison note: rule 2's "Ordinal" means UTF-16 code-unit order, which is
// NOT what Go's sort.Strings gives — that is byte-wise over UTF-8, equal to
// code-point order, and the two diverge for keys mixing supplementary-plane and
// high-BMP characters. The wire vocabulary's own keys are ASCII, where the
// orders coincide, but a rule-12 structured payload carries author-supplied
// keys and is where a non-BMP key actually arrives. canonical.SortKeys does the
// conversion; see its doc comment for why the difference is observable.

func encodeValue(v Value) (string, error) {
	var sb strings.Builder
	if err := appendValue(&sb, v); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func appendValue(sb *strings.Builder, v Value) error {
	switch t := v.(type) {
	case Str:
		sb.WriteString(canonical.EscapeString(string(t)))
	case Int:
		appendInt(sb, int64(t))
	case Float:
		appendFloat(sb, float64(t))
	case Bool:
		if t {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case Null:
		sb.WriteString("null")
	case Arr:
		sb.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			if err := appendValue(sb, item); err != nil {
				return err
			}
		}
		sb.WriteByte(']')
	case Obj:
		return appendObj(sb, t)
	case Node:
		return appendNode(sb, t)
	case nil:
		return errors.New("wire: cannot encode a nil Value")
	default:
		return errors.New("wire: cannot encode an unknown Value implementation")
	}
	return nil
}

// appendInt renders an integer per rule 5. Inside ±(2⁵³−1) that is the integer
// layout: plain decimal, no leading zeroes, no point, no exponent. Outside it a
// conformant encoder MUST NOT emit an integer token at all, so the value is
// rendered as the double a conformant decoder will read it back as — which is
// what every host that routes numbers through a double would have produced from
// the same document, and what keeps encode(decode(x)) stable here.
//
// Go is one of the two hosts where this can arise: its Int carries an int64, so
// a 19-digit identifier reaches the encoder intact and would otherwise ride onto
// the wire as a token three other hosts cannot reproduce. Carry such a value as
// a string; this rendering is the floor beneath that instruction, not a
// substitute for it.
func appendInt(sb *strings.Builder, i int64) {
	if i >= -PayloadIntMax && i <= PayloadIntMax {
		sb.WriteString(strconv.FormatInt(i, 10))
		return
	}
	sb.WriteString(canonical.FormatFiniteDouble(float64(i)))
}

// appendFloat renders a float per rule 5: the canonical finite layout, or the
// quoted sentinels for the three specials (RFC 8259 forbids them as bare
// numbers).
func appendFloat(sb *strings.Builder, f float64) {
	switch {
	case math.IsNaN(f):
		sb.WriteString(`"NaN"`)
	case math.IsInf(f, 1):
		sb.WriteString(`"Infinity"`)
	case math.IsInf(f, -1):
		sb.WriteString(`"-Infinity"`)
	default:
		sb.WriteString(canonical.FormatFiniteDouble(f))
	}
}

func appendObj(sb *strings.Builder, o Obj) error {
	keys := make([]string, 0, len(o.Fields)+1)
	for k := range o.Fields {
		keys = append(keys, k)
	}
	if o.Tag != "" {
		keys = append(keys, "$type")
	}
	canonical.SortKeys(keys)
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(canonical.EscapeString(k))
		sb.WriteByte(':')
		if o.Tag != "" && k == "$type" {
			sb.WriteString(canonical.EscapeString(o.Tag))
			continue
		}
		if err := appendValue(sb, o.Fields[k]); err != nil {
			return err
		}
	}
	sb.WriteByte('}')
	return nil
}

func appendNode(sb *strings.Builder, n Node) error {
	keys := make([]string, 0, len(n.Extras)+2)
	keys = append(keys, "id", "kind")
	for k := range n.Extras {
		keys = append(keys, k)
	}
	canonical.SortKeys(keys)
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(canonical.EscapeString(k))
		sb.WriteByte(':')
		var err error
		switch k {
		case "id":
			err = appendValue(sb, Str(n.ID))
		case "kind":
			err = appendValue(sb, n.Kind)
		default:
			err = appendValue(sb, n.Extras[k])
		}
		if err != nil {
			return err
		}
	}
	sb.WriteByte('}')
	return nil
}
