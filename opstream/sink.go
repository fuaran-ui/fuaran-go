package opstream

import (
	"fmt"
	"sort"
	"sync"
)

// Sink is the durable op-stream sink contract — the Go twin of IOpStreamSink.
// Synchronous: the in-memory sink computes answers directly. Sinks reject a
// duplicate (StreamID, Sequence) as a structural defect — query LatestSequence
// before assigning a sequence.
//
// A Sink implementation is not required to serialise concurrent Append calls
// against one another — a caller driving several writers against one stream
// must coordinate itself (see CasSink below for a sink that does the
// coordination for you).
type Sink interface {
	// Append appends record; errors on a duplicate (StreamID, Sequence).
	Append(record OpRecord) error
	// Replay returns the records for streamID with Sequence in [from, to]
	// inclusive, ascending; empty when none are in range.
	Replay(streamID string, from, to int) []OpRecord
	// LatestSequence returns the highest sequence observed in streamID; 0 if empty.
	LatestSequence(streamID string) int
	// Streams returns the distinct stream ids the sink holds records for.
	Streams() []string
}

// StaleHeadError is returned by CasSink.AppendIf when expectedHead no longer
// names the stream's current chain head — a concurrent writer landed a record
// first. Actual is the head to rebuild against and retry with.
type StaleHeadError struct {
	StreamID string
	Expected string
	Actual   string
}

func (e *StaleHeadError) Error() string {
	return fmt.Sprintf("opstream: stale head for stream %q: expected %s, actual %s", e.StreamID, e.Expected, e.Actual)
}

// AppendReceipt is what a successful CasSink.AppendIf hands back — the position
// the record actually landed at.
type AppendReceipt struct {
	StreamID string
	Sequence int
	Hash     string
}

// CasSink is a Sink that additionally offers a compare-and-append primitive:
// AppendIf lands record only when the stream's current head still matches
// expectedHead, atomically with the append. It is a SEPARATE, optional
// interface rather than an addition to Sink — Sink is a shipped contract third
// parties already implement, and a sink that cannot offer compare-and-append
// (e.g. one backed by a store with no native CAS) remains a valid Sink without
// implementing this. AppendNext (replay.go) prefers CasSink when a sink
// implements it and falls back to the plain read-then-append path otherwise.
type CasSink interface {
	Sink
	// Head returns the current chain head (the Hash of the latest record) for
	// streamID, or GenesisPreviousHash when the stream is empty.
	Head(streamID string) string
	// AppendIf appends record only if the stream's current head still equals
	// expectedHead, atomically with the read. On success it returns the
	// receipt for the landed record. On a stale head it appends nothing and
	// returns a *StaleHeadError naming the actual head to rebuild and retry
	// against.
	AppendIf(record OpRecord, expectedHead string) (AppendReceipt, error)
}

// InMemorySink is the headlessly-testable append-only reference sink.
// Safe for concurrent use by multiple goroutines — every method that touches
// the underlying maps holds mu.
type InMemorySink struct {
	mu      sync.Mutex
	records map[string][]OpRecord
	heads   map[string]string // streamID -> current chain head (Hash of the latest record)
}

// NewInMemorySink builds an empty in-memory sink.
func NewInMemorySink() *InMemorySink {
	return &InMemorySink{records: make(map[string][]OpRecord), heads: make(map[string]string)}
}

// Append appends record, rejecting a duplicate (StreamID, Sequence).
func (s *InMemorySink) Append(record OpRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(record)
}

// appendLocked is the shared append body; callers must hold s.mu.
func (s *InMemorySink) appendLocked(record OpRecord) error {
	for _, existing := range s.records[record.StreamID] {
		if existing.Sequence == record.Sequence {
			return fmt.Errorf("opstream: duplicate (stream %q, sequence %d)", record.StreamID, record.Sequence)
		}
	}
	s.records[record.StreamID] = append(s.records[record.StreamID], record)
	s.heads[record.StreamID] = record.Hash
	return nil
}

// Replay returns the streamID records with Sequence in [from, to], ascending.
func (s *InMemorySink) Replay(streamID string, from, to int) []OpRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []OpRecord
	for _, r := range s.records[streamID] {
		if r.Sequence >= from && r.Sequence <= to {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

// LatestSequence returns the highest sequence in streamID, or 0.
func (s *InMemorySink) LatestSequence(streamID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := 0
	for _, r := range s.records[streamID] {
		if r.Sequence > latest {
			latest = r.Sequence
		}
	}
	return latest
}

// Streams returns the distinct stream ids, sorted for determinism.
func (s *InMemorySink) Streams() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.records))
	for id := range s.records {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Head returns the current chain head for streamID (the Hash of its latest
// record, maintained incrementally on append rather than by rescanning), or
// GenesisPreviousHash when the stream is empty.
func (s *InMemorySink) Head(streamID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if head, ok := s.heads[streamID]; ok {
		return head
	}
	return GenesisPreviousHash
}

// AppendIf appends record only if streamID's current head still equals
// expectedHead — the compare and the append happen under the same lock, so two
// concurrent AppendIf calls against the same stream can never both succeed
// against a head that only one of them actually observed.
func (s *InMemorySink) AppendIf(record OpRecord, expectedHead string) (AppendReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actual, ok := s.heads[record.StreamID]
	if !ok {
		actual = GenesisPreviousHash
	}
	if actual != expectedHead {
		return AppendReceipt{}, &StaleHeadError{StreamID: record.StreamID, Expected: expectedHead, Actual: actual}
	}
	if err := s.appendLocked(record); err != nil {
		return AppendReceipt{}, err
	}
	return AppendReceipt{StreamID: record.StreamID, Sequence: record.Sequence, Hash: record.Hash}, nil
}

var _ CasSink = (*InMemorySink)(nil)
