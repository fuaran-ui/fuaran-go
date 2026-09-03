// Package dag is the branching op-stream DAG-record codec. The linear op-stream
// carries a chain of TreeOp edits; the branching DAG generalises it to a
// content-addressed, multi-parent record so divergent edit histories (an AI
// branch + a human branch) can fork and later merge. This is the Go conformant
// host of that record's canonical wire form — the sibling of the F#/TS/Python
// DAG codecs.
//
// The wire shape is a plain (non-$type) object whose keys sort in Ordinal order
// (actor < hash < op < outcomeHash < parents < promptId < resultEnvelope <
// streamId < timestamp < tombstoned). Byte-stable round-trip
// (EncodeDagRecord(DecodeDagRecord(x)) == x) is the conformance property.
//
// # The typed actor (Phase 1144), and what this host does not do
//
// The record's attribution member was a bare-string userId until Phase 1144
// replaced it with the typed actor the linear chain has carried since Phase 320
// — the same Human | Agent value, in the same PINNED canonical encoding
// (opstream.EncodeActor), nested verbatim exactly as the op is. Top-level keys
// are Ordinal-sorted, so actor sorts to the FRONT where userId sat at the back.
//
// The reference host folds that member into the DAG content address, at the same
// position in the Phase-408 delimited envelope:
//
//	…,"ts":<unix>,"userId":"alice",                      "promptId":…,"result":…   (408)
//	…,"ts":<unix>,"actor":{"kind":"human","id":"alice"}, "promptId":…,"result":…   (1144)
//
// Every DAG address was therefore re-minted, and PRE-1144 ADDRESSES DO NOT CARRY
// FORWARD — a pre-1144 hash is not reproducible under the new pre-image and is
// not a valid parent link for a post-1144 node.
//
// fuaran-go is a CODEC host for this artefact: it mints no DAG content address
// and verifies none (the only pre-images this module computes are the LINEAR
// chain's, in package opstream), so Hash is an opaque string it round-trips.
// There was no Go pre-image to re-derive; the reference envelope above is
// recorded rather than implemented, and a Go DAG addresser would be a new
// capability rather than part of this adoption.
//
// Decoding is deliberately NOT dual-read: a pre-1144 userId envelope is refused
// BY NAME rather than lifted to a Human, because a lift would mint a record
// carrying a stored hash no host can reproduce — turning a clear refusal here
// into a silent verification failure somewhere else.
package dag

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/opstream"
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
	StreamID string
	Hash     string
	Parents  []string
	Op       wire.Obj
	// Actor is who authored the op — the typed actor (Phase 1144, replacing the
	// bare UserID). Nested on the wire in its own pinned member order.
	Actor       opstream.Actor
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
// Ordinal order; the optional outcomeHash / promptId are included only when
// present. The nested op re-encodes through the shared TreeOp encoder and the
// nested actor through opstream.EncodeActor, so the output is byte-identical to
// the sibling hosts.
//
// The top-level object is assembled here rather than handed whole to
// wire.EncodeValue, because that encoder sorts EVERY object's keys and the
// nested actor's member order is deliberately pinned rather than sorted. Each
// member's VALUE still comes from the shared encoder, so nothing re-implements
// canonical rendering; only the outer key order is assembled, and it is asserted
// Ordinal-sorted by TestDagRecordTopLevelKeysAreOrdinalSorted.
func EncodeDagRecord(record Record) (string, error) {
	parents := make(wire.Arr, len(record.Parents))
	for i, p := range record.Parents {
		parents[i] = wire.Str(p)
	}
	opJSON, err := wire.EncodeValue(record.Op)
	if err != nil {
		return "", err
	}
	parentsJSON, err := wire.EncodeValue(parents)
	if err != nil {
		return "", err
	}
	envJSON, err := wire.EncodeValue(envelopeObj(record.Result))
	if err != nil {
		return "", err
	}

	members := []struct{ key, value string }{
		{"actor", opstream.EncodeActor(record.Actor)},
		{"hash", canonical.EscapeString(record.Hash)},
		{"op", opJSON},
		{"parents", parentsJSON},
		{"resultEnvelope", envJSON},
		{"streamId", canonical.EscapeString(record.StreamID)},
		{"timestamp", strconv.FormatInt(record.Timestamp, 10)},
		{"tombstoned", strconv.FormatBool(record.Tombstoned)},
	}
	if record.OutcomeHash != nil {
		members = append(members, struct{ key, value string }{
			"outcomeHash", canonical.EscapeString(*record.OutcomeHash)})
	}
	if record.PromptID != nil {
		members = append(members, struct{ key, value string }{
			"promptId", canonical.EscapeString(*record.PromptID)})
	}
	sort.SliceStable(members, func(i, j int) bool { return members[i].key < members[j].key })

	out := make([]byte, 0, 256)
	out = append(out, '{')
	for i, m := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, canonical.EscapeString(m.key)...)
		out = append(out, ':')
		out = append(out, m.value...)
	}
	out = append(out, '}')
	return string(out), nil
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

	// A pre-1144 envelope is refused BY NAME, not lifted — see the package doc.
	// Checked before the required-field sweep so a record carrying the retired
	// member names it, rather than reporting a bare missing 'actor'.
	if _, hasActor := obj["actor"]; !hasActor {
		if _, legacy := obj["userId"]; legacy {
			return Record{}, &wire.DecodeError{Code: wire.CodeMissingField, Path: "$.actor",
				Message: "pre-1144 record — 'userId' was replaced by the typed 'actor', " +
					"and DAG content addresses do not carry forward"}
		}
	}

	for _, required := range []string{"actor", "hash", "op", "parents", "streamId", "timestamp"} {
		if _, present := obj[required]; !present {
			return Record{}, &wire.DecodeError{Code: wire.CodeMissingField, Path: "$." + required,
				Message: "missing required field '" + required + "'"}
		}
	}

	actor, err := decodeActor(obj["actor"])
	if err != nil {
		return Record{}, err
	}

	op, err := wire.DecodeOpValue(obj["op"])
	if err != nil {
		return Record{}, rerootErr(err, "$.op")
	}

	rec := Record{
		StreamID:  asString(obj["streamId"]),
		Hash:      asString(obj["hash"]),
		Op:        op,
		Actor:     actor,
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

// decodeActor reads the nested canonical actor object into the typed
// opstream.Actor. Every defect is NAMED, never defaulted — the actor is inside
// the reference host's content address, so a guessed one silently invalidates
// the record's own hash. A non-object is WRONG_TYPE, a missing kind or case
// field is MISSING_FIELD, and a kind outside the closed pair is UNKNOWN_DU_CASE.
func decodeActor(raw any) (opstream.Actor, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, &wire.DecodeError{Code: wire.CodeWrongType, Path: "$.actor",
			Message: "expected a canonical actor object at $.actor"}
	}
	kind, err := actorString(obj, "kind")
	if err != nil {
		return nil, err
	}
	switch kind {
	case "human":
		id, err := actorString(obj, "id")
		if err != nil {
			return nil, err
		}
		return opstream.HumanActor{ID: id}, nil
	case "agent":
		model, err := actorString(obj, "model")
		if err != nil {
			return nil, err
		}
		version, err := actorString(obj, "version")
		if err != nil {
			return nil, err
		}
		id, err := actorString(obj, "id")
		if err != nil {
			return nil, err
		}
		return opstream.AgentActor{Model: model, Version: version, ID: id}, nil
	default:
		return nil, &wire.DecodeError{Code: wire.CodeUnknownDUCase, Path: "$.actor.kind",
			Message:       "unknown actor kind '" + kind + "' (expected human | agent)",
			ExpectedShape: "human | agent"}
	}
}

func actorString(obj map[string]any, key string) (string, error) {
	v, present := obj[key]
	if !present {
		return "", &wire.DecodeError{Code: wire.CodeMissingField, Path: "$.actor." + key,
			Message: "actor is missing required field '" + key + "'"}
	}
	s, ok := v.(string)
	if !ok {
		return "", &wire.DecodeError{Code: wire.CodeWrongType, Path: "$.actor." + key,
			Message: "expected a string at $.actor." + key}
	}
	return s, nil
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
