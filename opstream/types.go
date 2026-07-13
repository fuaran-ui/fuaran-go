// Package opstream is the Go host of the op-stream hash-chained provenance log —
// the twin of the F#/TS/Python op-stream tiers. One stream's applied TreeOp
// edits are an append-only, hash-chained sequence of OpRecord values; the
// SHA-256 chain makes "what did the author do?" and "was the stream tampered
// with?" both answerable from the record sequence alone (the op-stream is the
// source of truth). Plus the in-memory sink and the replay engine that
// reconstructs a tree by folding a chain of ops through the apply engine.
package opstream

import (
	"fmt"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// ── Actor (typed attested provenance) ───────────────────────────────────────

// Actor is who authored an op. Its canonical encoding is folded into the
// record hash, so attribution is tamper-evident. Closed by the unexported
// method (the Go analogue of a sum type).
type Actor interface{ isActor() }

// HumanActor is a person / account id — the load-bearing accountability case.
type HumanActor struct{ ID string }

// AgentActor is an AI author; Model / Version double as corpus-quality metadata.
type AgentActor struct {
	Model   string
	Version string
	ID      string
}

func (HumanActor) isActor() {}
func (AgentActor) isActor() {}

// ActorID returns the stable attribution id (the user id or the agent id).
func ActorID(a Actor) string {
	switch t := a.(type) {
	case HumanActor:
		return t.ID
	case AgentActor:
		return t.ID
	}
	return ""
}

// ── Apply-outcome envelope ──────────────────────────────────────────────────

// OpResult is the outcome captured at append time; both cases fold into the
// chain hash, so flipping a recorded Failure to Success breaks verification.
type OpResult interface{ isOpResult() }

// Success is a successful apply — a bare tag with no payload.
type Success struct{}

// Failure is a recorded apply failure — its code + message fold into the hash.
type Failure struct {
	Code    string
	Message string
}

func (Success) isOpResult() {}
func (Failure) isOpResult() {}

// ── The append-only record + checkpoint ─────────────────────────────────────

// OpRecord is one stream-position's apply trace. Append-only — sinks reject a
// duplicate (StreamID, Sequence) as a structural defect. Sequences begin at 1;
// the PreviousHash of the Sequence==1 record is GenesisPreviousHash. Op is a
// decoded TreeOp. PromptID is nil when absent.
type OpRecord struct {
	StreamID             string
	Sequence             int
	PreviousHash         string
	Hash                 string
	Op                   wire.Obj
	Actor                Actor
	TimestampUnixSeconds int64
	Result               OpResult
	PromptID             *string
}

// Checkpoint is a materialised snapshot at one op-index — replay can resume
// from the nearest checkpoint <= target rather than from genesis.
type Checkpoint struct {
	StreamID             string
	Sequence             int
	PreviousChainHead    string
	SnapshotHash         string
	Snapshot             wire.Node
	TimestampUnixSeconds int64
}

// ── Integrity + replay errors ───────────────────────────────────────────────

// PreviousHashMismatch — a record's PreviousHash does not match the prior
// record's Hash.
type PreviousHashMismatch struct {
	Sequence int
	Expected string
	Actual   string
}

func (e *PreviousHashMismatch) Error() string {
	return fmt.Sprintf("previous-hash mismatch at sequence %d: expected %s, got %s", e.Sequence, e.Expected, e.Actual)
}

// HashMismatch — a record's Hash does not recompute to the hash of its fields.
type HashMismatch struct {
	Sequence int
	Expected string
	Actual   string
}

func (e *HashMismatch) Error() string {
	return fmt.Sprintf("hash mismatch at sequence %d: recomputed %s, stored %s", e.Sequence, e.Expected, e.Actual)
}

// OutOfOrder — records are not in contiguous ascending 1-based sequence order.
type OutOfOrder struct {
	ExpectedSequence int
	ActualSequence   int
}

func (e *OutOfOrder) Error() string {
	return fmt.Sprintf("out-of-order chain: expected sequence %d, got %d", e.ExpectedSequence, e.ActualSequence)
}

// ApplyFailed — a replay failure: the op at Sequence did not apply.
type ApplyFailed struct {
	Sequence int
	Err      error
}

func (e *ApplyFailed) Error() string {
	return fmt.Sprintf("replay failed at sequence %d: %v", e.Sequence, e.Err)
}
