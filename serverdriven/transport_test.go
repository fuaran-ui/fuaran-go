package serverdriven

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// The transport-neutrality + reconnect-parity proof: the SAME driver scenario
// runs unchanged across the InMemory, SSE, and WebSocket channels, and every
// transport carries byte-identical frame bodies — steady-state and on
// reconnect-replay. This is what "one driver, SSE + WS backends" means: the
// driver only sees the Channel seam; the transports differ only in framing.

// wsBuf is an io.ReadWriter over a write buffer (the read side is unused for a
// push-only transport-framing assertion).
type wsBuf struct{ w bytes.Buffer }

func (b *wsBuf) Write(p []byte) (int, error) { return b.w.Write(p) }
func (b *wsBuf) Read(p []byte) (int, error)  { return 0, nil }

// frameBodiesInMemory renders the InMemory channel's recorded frames to their
// canonical JSON bodies.
func frameBodiesInMemory(t *testing.T, ch *InMemoryChannel) []string {
	t.Helper()
	var out []string
	for _, f := range ch.Pushed() {
		body, err := EncodeFrameJSON(f)
		if err != nil {
			t.Fatalf("EncodeFrameJSON: %v", err)
		}
		out = append(out, body)
	}
	return out
}

// frameBodiesSSE extracts the JSON body from each SSE event in the buffer.
func frameBodiesSSE(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for _, event := range strings.Split(raw, "\n\n") {
		for _, line := range strings.Split(event, "\n") {
			if body, ok := strings.CutPrefix(line, "data: "); ok {
				out = append(out, body)
			}
		}
	}
	return out
}

// frameBodiesWS decodes each WebSocket text frame in the buffer to its body.
func frameBodiesWS(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	r := bufio.NewReader(bytes.NewReader(raw))
	for {
		opcode, payload, err := readFrame(r)
		if err != nil {
			break // EOF at the end of the buffer
		}
		if opcode == opText {
			out = append(out, string(payload))
		}
	}
	return out
}

// driveAcrossChannel runs the fixed counter scenario over one channel, then
// reconnect-replays from lastSeq=1, and returns the connection so the caller
// can inspect the channel.
func driveScenario(t *testing.T, conn *Connection) {
	t.Helper()
	for i := 0; i < 3; i++ {
		conn.Handle(clickInc("c1", i))
	}
	if _, err := conn.Resync(1); err != nil { // reconnect: replay frames 2,3
		t.Fatalf("Resync: %v", err)
	}
}

func TestTransportNeutralityAndReconnectParity(t *testing.T) {
	// InMemory.
	memCh := &InMemoryChannel{}
	driveScenario(t, NewConnection("c1", newCounterSession(t), memCh))
	memBodies := frameBodiesInMemory(t, memCh)

	// SSE (framing written to a buffer).
	var sseBuf bytes.Buffer
	driveScenario(t, NewConnection("c1", newCounterSession(t), NewSSEChannel(&sseBuf)))
	sseBodies := frameBodiesSSE(t, sseBuf.String())

	// WebSocket (framing written to a buffer).
	wb := &wsBuf{}
	driveScenario(t, NewConnection("c1", newCounterSession(t), NewWSChannel(wb)))
	wsBodies := frameBodiesWS(t, wb.w.Bytes())

	// Each scenario pushes 3 steady-state frames + 2 replayed = 5.
	if len(memBodies) != 5 {
		t.Fatalf("expected 5 frame bodies, got %d", len(memBodies))
	}
	assertEqualBodies(t, "SSE", memBodies, sseBodies)
	assertEqualBodies(t, "WS", memBodies, wsBodies)

	// Sanity: the replayed tail (last 2) are exactly frames seq 2 and 3.
	if !strings.Contains(memBodies[3], `"seq":2`) || !strings.Contains(memBodies[4], `"seq":3`) {
		t.Errorf("reconnect replay tail not seq 2,3: %q %q", memBodies[3], memBodies[4])
	}
}

func assertEqualBodies(t *testing.T, name string, want, got []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d frame bodies, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s frame %d diverged:\n got %s\nwant %s", name, i, got[i], want[i])
		}
	}
}

// The WS read loop decodes a masked client event frame and drives the
// connection — the inbound half of the bidirectional transport.
func TestWSInboundDrivesConnection(t *testing.T) {
	// The read stream carries masked client frames the browser would send.
	var stream wsReadStream
	stream.feed(maskedTextFrame([]byte(`{"connId":"c1","nodeId":"inc","event":"click","lastSeq":0}`)))
	stream.feedRaw([]byte{0x88, 0x00}) // a close frame ends the read loop

	ch := NewWSChannel(&stream)
	session := newCounterSession(t)
	NewConnection("c1", session, ch)

	if err := ch.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	// The inbound event drove a Step: the metric advanced to 1.
	if got := metricValue(t, session); got != 1 {
		t.Errorf("inbound WS event did not drive the session: metric=%d, want 1", got)
	}
}
