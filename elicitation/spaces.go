package elicitation

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Value spaces reuse the platform's $type-tagged space vocabulary (§18.1).
// Every object position is strict — an undeclared key is UNDECLARED_FIELD (in
// Ordinal key order for canonical input).

// Space is a decoded value space.
type Space struct {
	Kind   string // intRange | floatRange | stringLen | enum | anyString
	Min    float64
	Max    float64
	Values []string
}

func objOf(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func strOf(v any) (string, bool)         { s, ok := v.(string); return s, ok }
func arrOf(v any) ([]any, bool)          { a, ok := v.([]any); return a, ok }

func numOf(v any) (float64, bool) {
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func isJSONNumber(v any) bool { _, ok := v.(json.Number); return ok }

// isWholeInt32 reports whether a JSON number classifies as an integer by value
// (whole-valued, within 32-bit signed range) — the §18.2 rule.
func isWholeInt32(v any) bool {
	f, ok := numOf(v)
	if !ok {
		return false
	}
	return f == math.Floor(f) && f >= math.MinInt32 && f <= math.MaxInt32
}

// strictKeys rejects any key on obj not in declared, first offender in Ordinal
// order (== document order for canonical input).
func strictKeys(obj map[string]any, declared map[string]bool, path string) *wire.DecodeError {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !declared[k] {
			return fail(CodeUndeclaredField, path+"."+k, "undeclared key '"+k+"'")
		}
	}
	return nil
}

var (
	rangeKeys = map[string]bool{"$type": true, "min": true, "max": true}
	enumKeys  = map[string]bool{"$type": true, "values": true}
	anyKeys   = map[string]bool{"$type": true}
)

// decodeSpace decodes a value space at path.
func decodeSpace(raw any, path string) (Space, *wire.DecodeError) {
	obj, ok := objOf(raw)
	if !ok {
		return Space{}, fail(wire.CodeWrongType, path, "expected a space object")
	}
	rawTag, ok := obj["$type"]
	if !ok {
		return Space{}, fail(wire.CodeMissingField, path+".$type", "missing $type discriminator")
	}
	tag, ok := rawTag.(string)
	if !ok {
		return Space{}, fail(wire.CodeWrongType, path+".$type", "$type must be a string")
	}
	switch tag {
	case "intRange", "floatRange", "stringLen":
		if e := strictKeys(obj, rangeKeys, path); e != nil {
			return Space{}, e
		}
		minV, ok1 := numOf(obj["min"])
		maxV, ok2 := numOf(obj["max"])
		if !ok1 {
			return Space{}, fail(wire.CodeMissingField, path+".min", "missing/invalid 'min'")
		}
		if !ok2 {
			return Space{}, fail(wire.CodeMissingField, path+".max", "missing/invalid 'max'")
		}
		if minV > maxV {
			return Space{}, fail(wire.CodeWrongType, path, "min must be <= max")
		}
		return Space{Kind: tag, Min: minV, Max: maxV}, nil
	case "enum":
		if e := strictKeys(obj, enumKeys, path); e != nil {
			return Space{}, e
		}
		rawValues, ok := arrOf(obj["values"])
		if !ok || len(rawValues) == 0 {
			return Space{}, fail(wire.CodeWrongType, path+".values", "enum.values must be a non-empty string array")
		}
		values := make([]string, len(rawValues))
		for i, item := range rawValues {
			s, ok := strOf(item)
			if !ok {
				return Space{}, fail(wire.CodeWrongType, path+".values", "enum.values must be strings")
			}
			values[i] = s
		}
		return Space{Kind: "enum", Values: values}, nil
	case "anyString":
		if e := strictKeys(obj, anyKeys, path); e != nil {
			return Space{}, e
		}
		return Space{Kind: "anyString"}, nil
	default:
		return Space{}, fail(wire.CodeUnknownDUCase, path+".$type", "unrecognised value-space '"+tag+"'")
	}
}

// encodeSpace re-encodes a space as a canonical wire Value (for envelope
// round-trip). Whole-valued bounds re-encode as Int (canonical collapses a
// whole Float to the same bytes), so the round-trip is byte-exact.
func encodeSpace(s Space) wire.Value {
	numVal := func(f float64) wire.Value {
		if f == math.Floor(f) && f >= math.MinInt64 && f <= math.MaxInt64 {
			return wire.Int(int64(f))
		}
		return wire.Float(f)
	}
	switch s.Kind {
	case "intRange", "floatRange", "stringLen":
		return wire.Obj{Tag: s.Kind, Fields: map[string]wire.Value{"max": numVal(s.Max), "min": numVal(s.Min)}}
	case "enum":
		arr := make(wire.Arr, len(s.Values))
		for i, v := range s.Values {
			arr[i] = wire.Str(v)
		}
		return wire.Obj{Tag: "enum", Fields: map[string]wire.Value{"values": arr}}
	default: // anyString
		return wire.Obj{Tag: "anyString", Fields: map[string]wire.Value{}}
	}
}

// conformsToSpace checks a JSON answer value against a space, returning the
// §18.2 error code (type-vs-space first, then in-space) or nil. path is the
// answer value's location.
func conformsToSpace(value any, s Space, path string) *wire.DecodeError {
	switch s.Kind {
	case "intRange":
		if !isJSONNumber(value) {
			return fail(CodeAnswerTypeMismatch, path, "expected an integer for an intRange field")
		}
		if !isWholeInt32(value) {
			return fail(CodeAnswerTypeMismatch, path, "value is not a 32-bit integer")
		}
		f, _ := numOf(value)
		if f < s.Min || f > s.Max {
			return fail(CodeAnswerOutOfSpace, path, "integer outside its intRange")
		}
	case "floatRange":
		if !isJSONNumber(value) {
			return fail(CodeAnswerTypeMismatch, path, "expected a number for a floatRange field")
		}
		f, _ := numOf(value)
		if f < s.Min || f > s.Max {
			return fail(CodeAnswerOutOfSpace, path, "number outside its floatRange")
		}
	case "stringLen":
		str, ok := strOf(value)
		if !ok {
			return fail(CodeAnswerTypeMismatch, path, "expected a string for a stringLen field")
		}
		n := float64(len([]rune(str)))
		if n < s.Min || n > s.Max {
			return fail(CodeAnswerOutOfSpace, path, "string length outside its stringLen bound")
		}
	case "enum":
		str, ok := strOf(value)
		if !ok {
			return fail(CodeAnswerTypeMismatch, path, "expected a string for an enum field")
		}
		for _, v := range s.Values {
			if v == str {
				return nil
			}
		}
		return fail(CodeAnswerOutOfSpace, path, "string outside its enum")
	case "anyString":
		if _, ok := strOf(value); !ok {
			return fail(CodeAnswerTypeMismatch, path, "expected a string for an anyString field")
		}
	}
	return nil
}
