// Package dag is the branching op-stream DAG-record codec. The linear op-stream
// carries a chain of TreeOp edits; the branching DAG generalises it to a
// content-addressed, multi-parent record so divergent edit histories (an AI
// branch + a human branch) can fork and later merge. This is the Go conformant
// host of that record's canonical wire form — the sibling of the F#/TS/Python
// DAG codecs.
//
// The wire shape is a plain (non-$type) object whose keys sort in Ordinal order
// (hash < op < outcomeHash < parents < promptId < resultEnvelope < streamId <
// timestamp < tombstoned < userId). Byte-stable round-trip
// (EncodeDagRecord(DecodeDagRecord(x)) == x) is the conformance property.
package dag

import (
	"encoding/json"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// ResultEnvelope is the apply outcome a DAG record carries: Success or Failure.
type ResultEnvelope struct {
	Kind    string // "Success" | "Failure"
	Code    string
	Message string
}

// Success is the common no-payload success envelope.
var Success = ResultEnvelope{Kind: "Success"}

// Record is a content-addressed, multi-parent op-stream record (the DAG
// generalisation of the linear OpRecord). Parents is in author order (head =
// primary parent); OutcomeHash is set only on a merge node; a single-parent
// record is the degenerate linear step and a zero-parent record is a genesis.
type Record struct {
	StreamID    string
	Hash        string
	Parents     []string
	Op          wire.Obj
	UserID      string
	Timestamp   int64
	Result      ResultEnvelope
	Tombstoned  bool
	OutcomeHash *string // nil = absent (a non-merge node)
	PromptID    *string // nil = absent
}

func envelopeObj(env ResultEnvelope) wire.Obj {
	if env.Kind == "Failure" {
		return wire.Obj{Tag: "Failure", Fields: map[string]wire.Value{
			"code": wire.Str(env.Code), "message": wire.Str(env.Message),
		}}
	}
	return wire.Obj{Tag: "Success", Fields: map[string]wire.Value{}}
}

// EncodeDagRecord encodes a Record to its canonical wire JSON. Keys emit in
// Ordinal order via the shared canonical encoder; the optional outcomeHash /
// promptId are included only when present. The nested op re-encodes through the
// shared TreeOp encoder, so the output is byte-identical to the sibling hosts.
func EncodeDagRecord(record Record) (string, error) {
	parents := make(wire.Arr, len(record.Parents))
	for i, p := range record.Parents {
		parents[i] = wire.Str(p)
	}
	fields := map[string]wire.Value{
		"hash":           wire.Str(record.Hash),
		"op":             record.Op,
		"parents":        parents,
		"resultEnvelope": envelopeObj(record.Result),
		"streamId":       wire.Str(record.StreamID),
		"timestamp":      wire.Int(record.Timestamp),
		"tombstoned":     wire.Bool(record.Tombstoned),
		"userId":         wire.Str(record.UserID),
	}
	if record.OutcomeHash != nil {
		fields["outcomeHash"] = wire.Str(*record.OutcomeHash)
	}
	if record.PromptID != nil {
		fields["promptId"] = wire.Str(*record.PromptID)
	}
	return wire.EncodeValue(wire.Obj{Fields: fields})
}

// DecodeDagRecord decodes a canonical-wire DAG-record document. Never panics;
// returns a *wire.DecodeError on any wire-shape violation.
func DecodeDagRecord(text string) (Record, error) {
	raw, err := wire.ParseCanonical(text)
	if err != nil {
		return Record{}, err
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return Record{}, &wire.DecodeError{Code: wire.CodeWrongType, Path: "$", Message: "expected an object at $"}
	}
	for _, required := range []string{"hash", "op", "parents", "streamId", "timestamp", "userId"} {
		if _, present := obj[required]; !present {
			return Record{}, &wire.DecodeError{Code: wire.CodeMissingField, Path: "$." + required,
				Message: "missing required field '" + required + "'"}
		}
	}

	op, err := wire.DecodeOpValue(obj["op"])
	if err != nil {
		return Record{}, rerootErr(err, "$.op")
	}

	rec := Record{
		StreamID:  asString(obj["streamId"]),
		Hash:      asString(obj["hash"]),
		Op:        op,
		UserID:    asString(obj["userId"]),
		Timestamp: asInt(obj["timestamp"]),
		Result:    Success,
	}
	rawParents, ok := obj["parents"].([]any)
	if !ok {
		return Record{}, &wire.DecodeError{Code: wire.CodeWrongType, Path: "$.parents", Message: "expected an array at $.parents"}
	}
	for _, p := range rawParents {
		rec.Parents = append(rec.Parents, asString(p))
	}
	if env, ok := obj["resultEnvelope"].(map[string]any); ok {
		rec.Result = decodeEnvelope(env)
	}
	if t, ok := obj["tombstoned"].(bool); ok {
		rec.Tombstoned = t
	}
	if oh, ok := obj["outcomeHash"]; ok {
		s := asString(oh)
		rec.OutcomeHash = &s
	}
	if pid, ok := obj["promptId"]; ok {
		s := asString(pid)
		rec.PromptID = &s
	}
	return rec, nil
}

func decodeEnvelope(env map[string]any) ResultEnvelope {
	if tag, _ := env["$type"].(string); tag == "Failure" {
		return ResultEnvelope{Kind: "Failure", Code: asString(env["code"]), Message: asString(env["message"])}
	}
	return Success
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int64 {
	// ParseCanonical yields numbers as json.Number (UseNumber), preserving the
	// int/float distinction.
	if n, ok := v.(json.Number); ok {
		i, _ := n.Int64()
		return i
	}
	return 0
}

func rerootErr(err error, prefix string) error {
	if de, ok := err.(*wire.DecodeError); ok {
		return &wire.DecodeError{Code: de.Code, Path: prefix + de.Path[1:], Message: de.Message, ExpectedShape: de.ExpectedShape}
	}
	return err
}
