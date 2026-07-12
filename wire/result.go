// Package wire is the canonical Node / TreeOp codec of the fuaran-go host:
// DecodeNode / EncodeNode / DecodeOp / EncodeOp over the structural typed
// model, byte-identical to the shared conformance corpus on round-trip and
// surfacing the six canonical DecodeError codes for malformed input.
package wire

import "fmt"

// DecodeErrorCode is one of the six canonical decode-error codes the wire
// contract defines. Every conformant host surfaces these exact codes (with a
// $-rooted path) for a malformed input, so the reject corpus is host-neutral.
type DecodeErrorCode string

const (
	CodeInvalidJSON   DecodeErrorCode = "INVALID_JSON"
	CodeMissingField  DecodeErrorCode = "MISSING_FIELD"
	CodeWrongType     DecodeErrorCode = "WRONG_TYPE"
	CodeUnknownDUCase DecodeErrorCode = "UNKNOWN_DU_CASE"
	CodeWrongNodeKind DecodeErrorCode = "WRONG_NODE_KIND"
	CodeEmptyNodeID   DecodeErrorCode = "EMPTY_NODE_ID"
)

// DecodeError is the structured, recoverable error a decode returns. Path is the
// $-rooted location of the fault (e.g. "$.kind.text"); the reject corpus asserts
// the Code plus a Path prefix. ExpectedShape is an optional hint enumerating the
// valid cases when a discriminator or bare-string enum is at fault.
type DecodeError struct {
	Code          DecodeErrorCode
	Path          string
	Message       string
	ExpectedShape string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("%s at %s: %s", e.Code, e.Path, e.Message)
}
