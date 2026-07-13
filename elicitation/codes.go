// Package elicitation is the Go host of the §18 elicitation artefact — a
// question posed as a live Fuaran tree plus a typed answer contract, resolving
// to exactly one typed outcome. Three codecs: the elicitation envelope
// ({$elicitation, contract, default?, id, timeoutMs?, tree}), the closed
// four-case outcome DU (Answered / Declined / TimedOut / Superseded), and the
// answer-conformance validation. Every object position is strict — undeclared
// keys are refused (default-deny by shape), and the envelope evolves explicitly
// via $elicitation, not by tolerance.
package elicitation

import (
	"strconv"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func itoa(i int) string { return strconv.Itoa(i) }

// The §18 error codes — kept OUT of the core six (like §15's FOREIGN_PROFILE);
// structural failures reuse the §6 codes on the same {code, path, message}
// envelope.
const (
	CodeUnsupportedVersion    wire.DecodeErrorCode = "UNSUPPORTED_VERSION"
	CodeUndeclaredField       wire.DecodeErrorCode = "UNDECLARED_FIELD"
	CodeContractEmpty         wire.DecodeErrorCode = "CONTRACT_EMPTY"
	CodeContractDuplicate     wire.DecodeErrorCode = "CONTRACT_DUPLICATE_FIELD"
	CodeContractUnknownNode   wire.DecodeErrorCode = "CONTRACT_UNKNOWN_NODE"
	CodeAnswerMissingField    wire.DecodeErrorCode = "ANSWER_MISSING_FIELD"
	CodeAnswerUndeclaredField wire.DecodeErrorCode = "ANSWER_UNDECLARED_FIELD"
	CodeAnswerTypeMismatch    wire.DecodeErrorCode = "ANSWER_TYPE_MISMATCH"
	CodeAnswerOutOfSpace      wire.DecodeErrorCode = "ANSWER_OUT_OF_SPACE"
	CodeDefaultNonconformant  wire.DecodeErrorCode = "DEFAULT_NONCONFORMANT"
)

// The elicitation format version this codec accepts.
const FormatVersion = "1"

func fail(code wire.DecodeErrorCode, path, message string) *wire.DecodeError {
	return &wire.DecodeError{Code: code, Path: path, Message: message}
}
