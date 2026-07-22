package serverdriven

import (
	"errors"
	"io"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// A shared counter scenario: a Box holding a Metric (source Static 0) and a
// Button. A "click" on the button bumps the metric's source to the next count
// via an UpdateProp op — exercising the full validate → handler → apply →
// frame path.

const counterTreeJSON = `{"id":"root","kind":{"$type":"Box","children":[` +
	`{"id":"count","kind":{"$type":"Metric","label":"Count","value":{"$type":"Static","value":0}}},` +
	`{"id":"inc","kind":{"$type":"Button","label":"+","onClick":{"$type":"Navigate","route":"/noop"},"variant":"Primary"}}` +
	`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`

func mustDecodeNode(t *testing.T, canonicalJSON string) wire.Node {
	t.Helper()
	node, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	return node
}

// counterHandler bumps a closure-held count and emits an UpdateProp setting
// the metric's source. Rejects an unknown event name (the dispatch gate).
func counterHandler() Handler {
	count := 0
	return func(tree wire.Node, ev Event) ([]wire.Obj, error) {
		if ev.NodeID != "inc" || ev.Event != "click" {
			return nil, errors.New("only the inc button click is handled")
		}
		count++
		return []wire.Obj{{Tag: "UpdateProp", Fields: map[string]wire.Value{
			"path":   wire.Str("Value"),
			"target": wire.Str("count"),
			"value":  wire.Int(int64(count)),
		}}}, nil
	}
}

func newCounterSession(t *testing.T) *Session {
	t.Helper()
	return NewSession(mustDecodeNode(t, counterTreeJSON), counterHandler())
}

func clickInc(connID string, lastSeq int) Event {
	return Event{ConnID: connID, NodeID: "inc", Event: "click", LastSeq: lastSeq}
}

// metricValue reads the counter metric's current Static source value.
func metricValue(t *testing.T, s *Session) int64 {
	t.Helper()
	metric, ok := findNode(s.Tree(), "count")
	if !ok {
		t.Fatal("count node missing")
	}
	src, ok := metric.Kind.Fields["value"].(wire.Obj)
	if !ok {
		t.Fatalf("value not an Obj: %+v", metric.Kind.Fields["value"])
	}
	v, ok := src.Fields["value"].(wire.Int)
	if !ok {
		t.Fatalf("source value not an Int: %+v", src.Fields["value"])
	}
	return int64(v)
}

// maskedTextFrame builds the masked WebSocket text frame a browser client
// sends (payloads under 126 bytes; a fixed mask key).
func maskedTextFrame(payload []byte) []byte {
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	frame := []byte{0x81, 0x80 | byte(len(payload))}
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i&3])
	}
	return frame
}

// wsReadStream is an io.ReadWriter whose Read drains fed bytes (inbound client
// frames) and whose Write discards (outbound pushes are not under assertion in
// the inbound test).
type wsReadStream struct{ in []byte }

func (s *wsReadStream) feed(frame []byte)           { s.in = append(s.in, frame...) }
func (s *wsReadStream) feedRaw(raw []byte)          { s.in = append(s.in, raw...) }
func (s *wsReadStream) Write(p []byte) (int, error) { return len(p), nil }

func (s *wsReadStream) Read(p []byte) (int, error) {
	if len(s.in) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.in)
	s.in = s.in[n:]
	return n, nil
}
