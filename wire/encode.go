package wire

import (
	"errors"
	"math"
	"sort"
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
// Key comparison note: Go's sort.Strings is byte-wise over UTF-8, which equals
// Unicode-code-point order; .NET StringComparer.Ordinal compares UTF-16 code
// units. The two diverge only for keys mixing supplementary-plane and high-BMP
// characters — the wire vocabulary's keys are ASCII, where all three orders
// coincide.

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
		sb.WriteString(strconv.FormatInt(int64(t), 10))
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
	sort.Strings(keys)
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
	sort.Strings(keys)
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
