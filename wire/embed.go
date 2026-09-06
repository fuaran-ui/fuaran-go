package wire

// Embedding API — canonical encode/decode of a wire Value nested inside a
// larger canonical document (a DAG record, a teleport envelope, an elicitation
// envelope, a merge verdict). These are thin exported wrappers over the codec
// internals so downstream packages never re-implement the canonical encoder or
// lose the int/float distinction the wire requires.

// EncodeValue canonical-encodes any wire Value (Ordinal key sort, canonical
// number layout, rule-6 escaping) — the same encoder DecodeNode/DecodeOp use.
func EncodeValue(v Value) (string, error) {
	return encodeValue(v)
}

// ParseCanonical parses exactly one JSON document, preserving the int-vs-float
// distinction (integer literals stay Int, decimals/exponents become Float), and
// returns the raw parsed value for embedding decoders to walk. Malformed input
// returns a *DecodeError (INVALID_JSON at "$").
func ParseCanonical(text string) (raw any, err error) {
	defer recoverDecode(&err)
	return parseJSON(text), nil
}

// ParseBounded parses exactly one JSON document under the FULL §21 limit set —
// syntactic depth on the way down, then string length and array/object width
// over the parsed result — and returns the raw value for a downstream decoder
// to walk. A breach is a *DecodeError with code LIMIT_EXCEEDED; malformed input
// is INVALID_JSON at "$".
//
// USE THIS, not encoding/json, at every entry point that reads untrusted text.
//
// ParseCanonical bounds syntactic depth only, because the node and op decoders
// downstream of it apply the remaining §21 axes themselves as they walk. An
// entry point that parses text and then reads the result WITHOUT going through
// those decoders — a column pipeline, a theme manifest, a capability
// declaration — gets no other bound at all, so the limits reach it only if it
// asks. Four such entry points each rolled their own encoding/json call and
// answered a 10 001-deep document with a syntax error, which §21.2 rule 2
// explicitly forbids for a document that is well-formed and merely too large:
// it sends an author to repair the wrong thing.
//
// The cost is one extra iterative pass over an already-parsed document, paid at
// a trust boundary. It is not paid on the node/op path, which is the hot one.
func ParseBounded(text string) (raw any, err error) {
	defer recoverDecode(&err)
	parsed := parseJSON(text)
	checkShape(parsed)
	return parsed, nil
}

// DecodeOpValue decodes a TreeOp from an already-parsed value (as produced by
// ParseCanonical), for an op nested inside a larger document. Malformed input
// returns a *DecodeError.
func DecodeOpValue(raw any) (op Obj, err error) {
	defer recoverDecode(&err)
	w := newWalkState()
	checkShape(raw)
	return decodeOpValue(w, raw, "$"), nil
}

// DecodeNodeValue decodes a Node from an already-parsed value, for a tree
// nested inside a larger document. Malformed input returns a *DecodeError.
func DecodeNodeValue(raw any) (node Node, err error) {
	defer recoverDecode(&err)
	w := newWalkState()
	checkShape(raw)
	return decodeNodeValue(w, raw, "$"), nil
}

// ValueFromParsed converts an already-parsed JSON value (from ParseCanonical)
// into a structural wire Value, tolerantly (unknown shapes preserved
// verbatim, null-lenient). Used for the §15 must-ignore-but-preserve carrier:
// a behind consumer preserves an unknown-kind payload's exact bytes by
// re-encoding this Value.
func ValueFromParsed(raw any) Value {
	return fromJSON(raw)
}
