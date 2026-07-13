package opstream

import (
	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Replay + apply-and-persist. ApplyTo folds an OpRecord sequence through the
// apply engine; ApplyAndPersist applies one op then persists a hash-chained
// record on success. Replay does NOT verify the hash chain — VerifyChain is the
// orthogonal integrity concern (replay drives apply; verification proves the
// stream was not tampered with).

// ApplyTo applies every record to initialTree in order, returning the final
// tree or the first apply failure (*ApplyFailed with the offending record's
// sequence).
func ApplyTo(initialTree wire.Node, records []OpRecord) (wire.Node, error) {
	tree := initialTree
	for _, record := range records {
		applied, err := ops.Apply(record.Op, tree)
		if err != nil {
			return initialTree, &ApplyFailed{Sequence: record.Sequence, Err: err}
		}
		tree = applied
	}
	return tree, nil
}

// ReplayStream reads records for streamID in [from, to] from the sink and folds
// them through the apply engine starting at initialTree. Resume from a
// checkpoint by passing its snapshot as initialTree and checkpoint.Sequence+1
// as from; a to of 0 or below means "up to the sink's latest".
func ReplayStream(sink Sink, streamID string, initialTree wire.Node, from, to int) (wire.Node, error) {
	upTo := to
	if upTo <= 0 {
		upTo = sink.LatestSequence(streamID)
	}
	return ApplyTo(initialTree, sink.Replay(streamID, from, upTo))
}

// PersistContext threads per-op correlation + sink-error context into a
// persisted record. Now returns Unix-epoch seconds (UTC) — injected so callers
// pin a deterministic timestamp into the chain; nil means the caller must set
// Timestamp explicitly (there is no ambient clock in this stdlib-pure package,
// mirroring the workflow's no-Date.now discipline). OnSinkError observes a
// rejected append without breaking the apply path (durability is best-effort).
type PersistContext struct {
	StreamID    string
	UserID      string
	PromptID    *string
	Timestamp   int64
	OnSinkError func(error)
}

// previousHashFor returns the chain head the record at sequence links to.
func previousHashFor(sink Sink, streamID string, sequence int) string {
	if sequence == 1 {
		return GenesisPreviousHash
	}
	prev := sink.Replay(streamID, sequence-1, sequence-1)
	if len(prev) == 0 {
		// A sink invariant violation; best-effort — a later VerifyChain surfaces
		// the gap as OutOfOrder / PreviousHashMismatch.
		return GenesisPreviousHash
	}
	return prev[0].Hash
}

// AppendOp computes the chain-linked record for op at the next sequence and
// appends it to the sink, returning the persisted record. The actor is the
// context's user id lifted to a typed human actor; the result is Success.
func AppendOp(sink Sink, ctx PersistContext, op wire.Obj) (OpRecord, error) {
	sequence := sink.LatestSequence(ctx.StreamID) + 1
	previousHash := previousHashFor(sink, ctx.StreamID, sequence)
	actor := HumanActor{ID: ctx.UserID}
	hash, err := ComputeHash(previousHash, op, sequence, ctx.Timestamp, actor, ctx.PromptID, Success{})
	if err != nil {
		return OpRecord{}, err
	}
	record := OpRecord{
		StreamID:             ctx.StreamID,
		Sequence:             sequence,
		PreviousHash:         previousHash,
		Hash:                 hash,
		Op:                   op,
		Actor:                actor,
		TimestampUnixSeconds: ctx.Timestamp,
		Result:               Success{},
		PromptID:             ctx.PromptID,
	}
	if err := sink.Append(record); err != nil {
		if ctx.OnSinkError != nil {
			ctx.OnSinkError(err)
		}
		return record, err
	}
	return record, nil
}

// ApplyAndPersist applies op against tree. On success it persists a
// hash-chained OpRecord to the sink and returns the updated tree; on an apply
// failure it returns the apply error unchanged (the sink is untouched). A sink
// append failure is surfaced via ctx.OnSinkError but does NOT propagate —
// durability is best-effort, and the returned tree is the successful apply.
func ApplyAndPersist(sink Sink, ctx PersistContext, op wire.Obj, tree wire.Node) (wire.Node, error) {
	applied, err := ops.Apply(op, tree)
	if err != nil {
		return tree, err
	}
	_, _ = AppendOp(sink, ctx, op) // best-effort; OnSinkError observes a reject
	return applied, nil
}
