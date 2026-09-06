package opstream

import (
	"errors"
	"fmt"

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
// rejected/lost append without breaking the apply path (durability is
// best-effort) — see AppendNext and ApplyAndPersist for exactly which paths
// invoke it.
type PersistContext struct {
	StreamID    string
	UserID      string
	PromptID    *string
	Timestamp   int64
	OnSinkError func(error)
}

// reportSinkError invokes ctx.OnSinkError when set. Every error return in
// AppendOp and AppendNext goes through this first, so a caller relying on the
// hook to detect a lost append observes every such path — including a
// ComputeHash failure, a previousHashFor gap, and an exhausted
// compare-and-append retry budget, none of which reached the hook before.
func reportSinkError(ctx PersistContext, err error) {
	if ctx.OnSinkError != nil {
		ctx.OnSinkError(err)
	}
}

// previousHashFor returns the chain head the record at sequence links to, or
// an error when the sink reports a GAP: a missing record at sequence-1 while
// sequence > 1 is not genesis. Silently substituting GenesisPreviousHash there
// would write a record that VerifyChain later rejects as a
// PreviousHashMismatch against every record it precedes — a permanent poison
// pill baked into the chain at append time rather than caught at append time.
func previousHashFor(sink Sink, streamID string, sequence int) (string, error) {
	if sequence == 1 {
		return GenesisPreviousHash, nil
	}
	prev := sink.Replay(streamID, sequence-1, sequence-1)
	if len(prev) == 0 {
		return "", fmt.Errorf(
			"opstream: gap in stream %q: no record at sequence %d — refusing to append sequence %d against genesis",
			streamID, sequence-1, sequence)
	}
	return prev[0].Hash, nil
}

// AppendOp computes the chain-linked record for op at the next sequence and
// appends it to the sink via a plain read-then-append, returning the
// persisted record. The actor is the context's user id lifted to a typed
// human actor; the result is Success.
//
// This is the non-CAS primitive: it is what AppendNext falls back to for a
// sink that does not implement CasSink, and it is what AppendNext itself was
// before compare-and-append existed. A caller that drives concurrent writers
// against one stream on a plain Sink must serialise its own calls — nothing
// here protects the LatestSequence-then-Append gap against a race; use
// AppendNext against a CasSink (e.g. InMemorySink) for that.
//
// OnSinkError is invoked before every error return, including a ComputeHash
// failure and a previousHashFor gap — neither reached the hook before.
func AppendOp(sink Sink, ctx PersistContext, op wire.Obj) (OpRecord, error) {
	sequence := sink.LatestSequence(ctx.StreamID) + 1
	previousHash, err := previousHashFor(sink, ctx.StreamID, sequence)
	if err != nil {
		reportSinkError(ctx, err)
		return OpRecord{}, err
	}
	actor := HumanActor{ID: ctx.UserID}
	hash, err := ComputeHash(previousHash, op, sequence, ctx.Timestamp, actor, ctx.PromptID, Success{})
	if err != nil {
		reportSinkError(ctx, err)
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
		reportSinkError(ctx, err)
		return record, err
	}
	return record, nil
}

// maxAppendAttempts bounds AppendNext's compare-and-append retry loop — a
// small constant rather than an unbounded spin, so a pathologically contended
// stream fails loudly (through OnSinkError) instead of retrying forever.
const maxAppendAttempts = 8

// AppendNext computes the chain-linked record for op at the next sequence and
// appends it to sink, returning the persisted record.
//
// When sink implements CasSink, the read of the current chain head and the
// append happen as an atomic compare-and-append (CasSink.AppendIf): each
// attempt reads the head FIRST, then the latest sequence. That order is
// load-bearing — it is what guarantees a concurrent writer landing between the
// two reads is always caught by AppendIf's head comparison rather than
// surfacing as a duplicate-sequence rejection from the sink (see sink.go's
// AppendIf doc comment for why). On a stale head — a concurrent writer landed
// first — the record is rebuilt against the actual head AppendIf reports and
// the append is retried, up to maxAppendAttempts times.
//
// A sink that does not implement CasSink falls back to AppendOp's plain
// read-then-append path, so a third-party Sink implementation keeps working
// unchanged — it is simply not protected against concurrent writers racing the
// same stream.
//
// OnSinkError is invoked before every error return in this function.
func AppendNext(sink Sink, ctx PersistContext, op wire.Obj) (OpRecord, error) {
	casSink, ok := sink.(CasSink)
	if !ok {
		return AppendOp(sink, ctx, op)
	}

	actor := HumanActor{ID: ctx.UserID}
	var lastErr error
	for attempt := 0; attempt < maxAppendAttempts; attempt++ {
		head := casSink.Head(ctx.StreamID)                   // read head first —
		sequence := casSink.LatestSequence(ctx.StreamID) + 1 // — then sequence; see doc comment above

		hash, err := ComputeHash(head, op, sequence, ctx.Timestamp, actor, ctx.PromptID, Success{})
		if err != nil {
			reportSinkError(ctx, err)
			return OpRecord{}, err
		}
		record := OpRecord{
			StreamID:             ctx.StreamID,
			Sequence:             sequence,
			PreviousHash:         head,
			Hash:                 hash,
			Op:                   op,
			Actor:                actor,
			TimestampUnixSeconds: ctx.Timestamp,
			Result:               Success{},
			PromptID:             ctx.PromptID,
		}

		if _, err := casSink.AppendIf(record, head); err != nil {
			var stale *StaleHeadError
			if !errors.As(err, &stale) {
				// Not a race we can usefully retry (e.g. the sink rejected the
				// record outright) — surface it.
				reportSinkError(ctx, err)
				return record, err
			}
			lastErr = err
			continue // rebuild against the actual head reported by stale, next iteration
		}
		return record, nil
	}
	err := fmt.Errorf("opstream: exceeded %d compare-and-append attempts for stream %q: %w",
		maxAppendAttempts, ctx.StreamID, lastErr)
	reportSinkError(ctx, err)
	return OpRecord{}, err
}

// ApplyAndPersist applies op against tree. On success it persists a
// hash-chained OpRecord via AppendNext and returns the updated tree; on an
// apply failure it returns the apply error unchanged (the sink is untouched).
//
// Persistence is best-effort with respect to THIS function's return value: a
// sink append failure — including one that survives AppendNext's bounded
// compare-and-append retries, or a gap AppendNext's non-CAS fallback refuses —
// is surfaced to ctx.OnSinkError but does NOT change what ApplyAndPersist
// returns. A (tree, nil) result means exactly "the apply happened," never
// "the op was durably recorded" — a caller that needs the latter must supply
// OnSinkError and treat its firing as the op having been lost even though this
// function reported success.
func ApplyAndPersist(sink Sink, ctx PersistContext, op wire.Obj, tree wire.Node) (wire.Node, error) {
	applied, err := ops.Apply(op, tree)
	if err != nil {
		return tree, err
	}
	_, _ = AppendNext(sink, ctx, op) // best-effort; OnSinkError observes a reject/exhaustion
	return applied, nil
}
