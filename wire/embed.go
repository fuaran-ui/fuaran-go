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

// DecodeOpValue decodes a TreeOp from an already-parsed value (as produced by
// ParseCanonical), for an op nested inside a larger document. Malformed input
// returns a *DecodeError.
func DecodeOpValue(raw any) (op Obj, err error) {
	defer recoverDecode(&err)
	return decodeOpValue(raw, "$"), nil
}

// DecodeNodeValue decodes a Node from an already-parsed value, for a tree
// nested inside a larger document. Malformed input returns a *DecodeError.
func DecodeNodeValue(raw any) (node Node, err error) {
	defer recoverDecode(&err)
	return decodeNodeValue(raw, "$"), nil
}

// ValueFromParsed converts an already-parsed JSON value (from ParseCanonical)
// into a structural wire Value, tolerantly (unknown shapes preserved
// verbatim, null-lenient). Used for the §15 must-ignore-but-preserve carrier:
// a behind consumer preserves an unknown-kind payload's exact bytes by
// re-encoding this Value.
func ValueFromParsed(raw any) Value {
	return fromJSON(raw)
}
