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

// ParseBounded parses exactly one JSON document under the FULL §21 limit set
// and returns the raw value for a downstream decoder to walk. It applies, in
// this order:
//
//  1. §21.7 document bytes (MaxDocumentBytes, UTF-8 bytes of the whole input),
//     BEFORE the parse allocates, at "$";
//  2. §21.2 syntactic JSON depth (MaxJSONDepth), on the way down;
//  3. §21.6 string length, then array length and object member count, over the
//     parsed result.
//
// A breach of any is a *DecodeError with code LIMIT_EXCEEDED; malformed input
// is INVALID_JSON at "$". The node-count and tree-depth limits (§21.1) do not
// apply: this document is not a node tree.
//
// USE THIS, not encoding/json, at every entry point that reads untrusted text.
//
// Where the document-bytes bound lives (Phase 2742): HERE, once. The readers
// that call ParseBounded — the column pipeline, the theme manifest, the
// capability declaration and the DAG record — each used to bound shape and
// never size, because a size check was a separate call the reader had to
// remember (CheckDocumentBytes, which only the theme manifest made). Putting
// the check inside the parse means a new reader cannot forget it and no reader
// carries its own copy. CheckDocumentBytes stays for a caller that must refuse
// before it does other work with the text.
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
// The cost is one length comparison and one extra iterative pass over an
// already-parsed document, paid at a trust boundary. It is not paid on the
// node/op path, which is the hot one.
func ParseBounded(text string) (raw any, err error) {
	defer recoverDecode(&err)
	checkDocumentBytes(text)
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

// Field rules for the write path (Phase 1925). An apply engine that writes a
// value into a kind field reads it through the SAME rule the decoder reads the
// member with, so an UpdateProp admits exactly what a decode admits. Each takes
// a decoded wire Value, re-reads its canonical bytes, and returns the value in
// the decoder's form or a *DecodeError carrying the decoder's own code.

// DecodeIntSlot reads v through the §7.1 integer-slot rule: a finite integral
// number inside the signed 32-bit range, whatever its spelling (3.0 is the
// integer 3). A fractional, non-finite or out-of-range value is WRONG_TYPE.
func DecodeIntSlot(v Value) (Value, error) {
	return readThrough(decodeInt, v)
}

// DecodeSkeletonRows reads v as `Skeleton.rows`: §7.1 first, then the §21.9
// bound, which a breach answers with LIMIT_EXCEEDED.
func DecodeSkeletonRows(v Value) (Value, error) {
	return readThrough(decodeSkeletonRows, v)
}

func readThrough(dec fieldDecoder, v Value) (out Value, err error) {
	text, encErr := encodeValue(v)
	if encErr != nil {
		return nil, &DecodeError{Code: CodeWrongType, Path: "$value", Message: encErr.Error()}
	}
	defer recoverDecode(&err)
	return dec(nil, parseJSON(text), "$value"), nil
}
