package opstream

import (
	"fmt"
	"sort"
)

// Sink is the durable op-stream sink contract — the Go twin of IOpStreamSink.
// Synchronous: the in-memory sink computes answers directly. Sinks reject a
// duplicate (StreamID, Sequence) as a structural defect — query LatestSequence
// before assigning a sequence.
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

// InMemorySink is the headlessly-testable append-only reference sink.
type InMemorySink struct {
	records map[string][]OpRecord
}

// NewInMemorySink builds an empty in-memory sink.
func NewInMemorySink() *InMemorySink {
	return &InMemorySink{records: make(map[string][]OpRecord)}
}

// Append appends record, rejecting a duplicate (StreamID, Sequence).
func (s *InMemorySink) Append(record OpRecord) error {
	for _, existing := range s.records[record.StreamID] {
		if existing.Sequence == record.Sequence {
			return fmt.Errorf("opstream: duplicate (stream %q, sequence %d)", record.StreamID, record.Sequence)
		}
	}
	s.records[record.StreamID] = append(s.records[record.StreamID], record)
	return nil
}

// Replay returns the streamID records with Sequence in [from, to], ascending.
func (s *InMemorySink) Replay(streamID string, from, to int) []OpRecord {
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
	out := make([]string, 0, len(s.records))
	for id := range s.records {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
