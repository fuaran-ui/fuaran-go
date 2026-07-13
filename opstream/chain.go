package opstream

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The hash-chain primitive — SHA-256 over the canonical delimited pre-image.
// All hosts fold the same byte-identical pre-image and run standard SHA-256, so
// a chain built here reproduces the committed golden hashes
// (wire-format-fixtures/chain/chain-corpus.json) exactly — that byte-for-byte
// reproduction is the whole contract.
//
// The canonical algorithm (the actor is folded in, so attribution is part of
// the integrity chain):
//
//	hash[n] = sha256( previousHash[n] + "|" + payload[n] )
//	payload = {"seq":<sequence-1>,"actor":<actor>,"op":<streamEntry>}
//	streamEntry = {"v":<ChainFormatVersion>,"op":<op>,"ts":<seconds>,
//	               "promptId":<id|null>,"result":<result>}
//	hash[0]'s previousHash is GenesisPreviousHash (sixty-four '0' chars).
//
// Field order in every object is PINNED (v leads streamEntry; payload keys are
// seq / actor / op in that literal order) — this is a hand-written delimited
// pre-image, NOT the sorted-key canonical encoder. Sequence is the public
// 1-based value; the pre-image folds the 0-based record index (sequence-1).

// ChainFormatVersion is the chain FORMAT version, folded FIRST into the hash
// pre-image. It makes the chain self-describing and tamper-evident. Bump it in
// lock-step across every host and the chain-corpus golden whenever the
// pre-image formula, the envelope shape, or the hash function changes.
const ChainFormatVersion = 2

// GenesisPreviousHash is sixty-four '0' characters — the PreviousHash of every
// stream's Sequence==1 record.
const GenesisPreviousHash = "0000000000000000000000000000000000000000000000000000000000000000"

// sha256Hex returns SHA-256 of UTF-8(payload) as 64 lower-case hex characters.
func sha256Hex(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// encodeActor is the canonical JSON of the typed actor — folded into the record
// hash. Field order is pinned (kind first, then case fields).
func encodeActor(actor Actor) string {
	switch t := actor.(type) {
	case HumanActor:
		return `{"kind":"human","id":` + canonical.EscapeString(t.ID) + "}"
	case AgentActor:
		return `{"kind":"agent","model":` + canonical.EscapeString(t.Model) +
			`,"version":` + canonical.EscapeString(t.Version) +
			`,"id":` + canonical.EscapeString(t.ID) + "}"
	}
	return `{"kind":"human","id":""}`
}

// encodeResult is the canonical encoding of the apply outcome. Success is a
// bare tag; a Failure carries its code + message, so flipping outcome breaks
// the hash.
func encodeResult(result OpResult) string {
	switch t := result.(type) {
	case Failure:
		return `{"kind":"failure","code":` + canonical.EscapeString(t.Code) +
			`,"message":` + canonical.EscapeString(t.Message) + "}"
	default: // Success (and the zero value)
		return `{"kind":"success"}`
	}
}

// EncodeStreamEntry is the pinned cross-host provenance envelope — the opaque
// op payload the chain carries. Field order v / op / ts / promptId / result is
// pinned; v sorts first so a reader can lift it with a minimal parse. ts is
// unix SECONDS; promptId is null when absent.
func EncodeStreamEntry(op wire.Obj, timestampUnixSeconds int64, promptID *string, result OpResult) (string, error) {
	opJSON, err := wire.EncodeOp(op)
	if err != nil {
		return "", err
	}
	prompt := "null"
	if promptID != nil {
		prompt = canonical.EscapeString(*promptID)
	}
	return `{"v":` + strconv.Itoa(ChainFormatVersion) +
		`,"op":` + opJSON +
		`,"ts":` + strconv.FormatInt(timestampUnixSeconds, 10) +
		`,"promptId":` + prompt +
		`,"result":` + encodeResult(result) + "}", nil
}

// FormatVersion reads the chain format version from an encoded envelope without
// verifying it (a minimal prefix scan, not a full JSON parse), so a host can
// reject an unrecognised format with a clear error before hash verification.
// Returns (0, false) when no leading v is present (the pre-v2 format).
func FormatVersion(encodedEnvelope string) (int, bool) {
	const prefix = `{"v":`
	if !strings.HasPrefix(encodedEnvelope, prefix) {
		return 0, false
	}
	var digits strings.Builder
	for i := len(prefix); i < len(encodedEnvelope); i++ {
		c := encodedEnvelope[i]
		if c < '0' || c > '9' {
			break
		}
		digits.WriteByte(c)
	}
	if digits.Len() == 0 {
		return 0, false
	}
	v, _ := strconv.Atoi(digits.String())
	return v, true
}

// ComputeHash computes the hash for an op-record. Identical algorithm on every
// host — verification re-derives this and compares. The pre-image is the
// canonical delimited {"seq":…,"actor":…,"op":…} payload with
// op = EncodeStreamEntry(...), hashed as sha256(previousHash + "|" + payload).
// Sequence is the public 1-based value; the payload folds the 0-based record
// index (sequence-1).
func ComputeHash(previousHash string, op wire.Obj, sequence int, timestampUnixSeconds int64, actor Actor, promptID *string, result OpResult) (string, error) {
	entry, err := EncodeStreamEntry(op, timestampUnixSeconds, promptID, result)
	if err != nil {
		return "", err
	}
	payload := `{"seq":` + strconv.Itoa(sequence-1) +
		`,"actor":` + encodeActor(actor) +
		`,"op":` + entry + "}"
	return sha256Hex(previousHash + "|" + payload), nil
}

// SnapshotHash is the content address of a checkpoint snapshot — it binds the
// snapshot to its position in the chain (previousChainHead + sequence + the
// canonical tree, in a delimited pre-image), so a valid snapshot from one
// (head, seq) no longer validates at a different one.
func SnapshotHash(previousChainHead string, sequence int, canonicalTree string) string {
	payload := `{"snapshot":true,"seq":` + strconv.Itoa(sequence) + `,"tree":` + canonicalTree + "}"
	return sha256Hex(previousChainHead + "|" + payload)
}

// VerifyChain walks records in order, asserting (a) the PreviousHash chain
// links and (b) each Hash recomputes to the stored value, over a contiguous
// 1-based sequence from genesis. Returns the first violation, or nil on a clean
// chain.
func VerifyChain(records []OpRecord) error {
	previousHash := GenesisPreviousHash
	expectedSequence := 1
	for _, record := range records {
		if record.Sequence != expectedSequence {
			return &OutOfOrder{ExpectedSequence: expectedSequence, ActualSequence: record.Sequence}
		}
		if record.PreviousHash != previousHash {
			return &PreviousHashMismatch{Sequence: record.Sequence, Expected: previousHash, Actual: record.PreviousHash}
		}
		recomputed, err := ComputeHash(record.PreviousHash, record.Op, record.Sequence,
			record.TimestampUnixSeconds, record.Actor, record.PromptID, record.Result)
		if err != nil {
			return err
		}
		if recomputed != record.Hash {
			return &HashMismatch{Sequence: record.Sequence, Expected: recomputed, Actual: record.Hash}
		}
		previousHash = record.Hash
		expectedSequence++
	}
	return nil
}
