package serverdriven

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// The frame-cap tests. Each one was a process kill before the cap landed: the
// declared length is peer-supplied and unauthenticated, and the reader used it
// to size an allocation directly.
//
// They assert a REFUSAL, not merely "no crash". A reader that quietly returned
// an empty payload would pass a liveness check while losing the framing, and
// the peer would never learn why its connection went away.

// maskedFrame builds a client→server frame by hand: FIN + opcode, the mask
// bit, the length encoding the caller asks for, the mask key, and the masked
// payload. lengthOverride, when non-nil, replaces the computed length bytes —
// which is how a hostile frame DECLARES a payload it never sends.
func maskedFrame(opcode byte, payload []byte, lengthOverride []byte) []byte {
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	frame := []byte{0x80 | opcode}
	if lengthOverride != nil {
		frame = append(frame, lengthOverride...)
	} else if len(payload) < 126 {
		frame = append(frame, 0x80|byte(len(payload)))
	} else {
		frame = append(frame, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	}
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i&3])
	}
	return frame
}

func TestWSHostileFrameLengthIsRefusedNotAllocated(t *testing.T) {
	// The C-1 frame: a 127 marker with length 0xFFFFFFFFFFFFFFFF. Narrowed to
	// int that is -1, and `make([]byte, -1)` panics with "len out of range" in
	// a goroutine net/http does not recover — the process dies, pre-auth.
	hostile := maskedFrame(opText, nil, []byte{
		0x80 | 127,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	})

	_, _, err := readFrame(bytes.NewReader(hostile), MaxFrameBytes, true)

	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("hostile length: err = %v (%T), want a *ProtocolError", err, err)
	}
	if pe.CloseCode != closeMessageTooBig {
		t.Errorf("close code = %d, want %d (message too big)", pe.CloseCode, closeMessageTooBig)
	}
}

func TestWSLargePositiveFrameLengthIsRefused(t *testing.T) {
	// The other half of C-1: a length that narrows to a large POSITIVE int is
	// not a panic but a fatal out-of-memory, which is the same outcome by a
	// slower route. 1 TiB, declared in eight bytes, sent as none.
	hostile := maskedFrame(opText, nil, []byte{
		0x80 | 127,
		0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00,
	})

	_, _, err := readFrame(bytes.NewReader(hostile), MaxFrameBytes, true)

	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.CloseCode != closeMessageTooBig {
		t.Fatalf("1 TiB frame: err = %v, want a *ProtocolError with close code %d", err, closeMessageTooBig)
	}
}

func TestWSFrameAtTheLimitIsAccepted(t *testing.T) {
	// The cap must refuse what is over it and nothing else — a check that
	// refused the boundary case would be a liveness bug dressed as a fix.
	payload := bytes.Repeat([]byte("a"), 200)
	frame := maskedFrame(opText, payload, nil)

	opcode, got, err := readFrame(bytes.NewReader(frame), 200, true)
	if err != nil {
		t.Fatalf("frame exactly at the limit: %v", err)
	}
	if opcode != opText || !bytes.Equal(got, payload) {
		t.Errorf("payload at the limit did not round-trip")
	}

	if _, _, err = readFrame(bytes.NewReader(frame), 199, true); err == nil {
		t.Errorf("one byte over the limit was accepted")
	}
}

func TestWSUnmaskedClientFrameIsRefused(t *testing.T) {
	// RFC 6455 §5.1: every client→server frame MUST be masked. An unmasked one
	// is not a browser, and accepting it lets a peer skip the only framing
	// discipline the protocol imposes on it.
	payload := []byte(`{"nodeId":"inc","event":"click"}`)
	frame := append([]byte{0x81, byte(len(payload))}, payload...) // no mask bit

	_, _, err := readFrame(bytes.NewReader(frame), MaxFrameBytes, true)

	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.CloseCode != closeProtocolError {
		t.Fatalf("unmasked client frame: err = %v, want a *ProtocolError with close code %d",
			err, closeProtocolError)
	}

	// The same bytes read as a SERVER-written frame are legitimate — the rule
	// is directional, and a check that refused both would break the codec's
	// own round trip.
	if _, _, err = readFrame(bytes.NewReader(frame), MaxFrameBytes, false); err != nil {
		t.Errorf("server-written unmasked frame was refused: %v", err)
	}
}

func TestWSOversizedControlFrameIsRefused(t *testing.T) {
	// RFC 6455 §5.5: a control frame carries at most 125 bytes. A ping payload
	// is echoed straight back as a pong, so an unchecked one is an
	// amplification primitive as well as an allocation one.
	payload := bytes.Repeat([]byte("p"), 300)
	frame := maskedFrame(opPing, payload, nil)

	_, _, err := readFrame(bytes.NewReader(frame), MaxFrameBytes, true)

	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.CloseCode != closeProtocolError {
		t.Fatalf("300-byte ping: err = %v, want a *ProtocolError with close code %d",
			err, closeProtocolError)
	}
}

// hostileConn is a ReadWriter that serves one hostile frame and then blocks on
// EOF, recording what the read loop wrote back.
type hostileConn struct {
	in  *bytes.Reader
	out bytes.Buffer
}

func (c *hostileConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *hostileConn) Write(p []byte) (int, error) { return c.out.Write(p) }

func TestWSListenClosesWithStatusOnHostileFrame(t *testing.T) {
	// End to end through the channel: the C-1 frame must produce a close frame
	// carrying 1009 and a returned error, with the test process still alive.
	hostile := maskedFrame(opText, nil, []byte{
		0x80 | 127,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
	})
	conn := &hostileConn{in: bytes.NewReader(hostile)}
	ch := NewWSChannel(conn)

	err := ch.ListenAndRecover()
	if err == nil {
		t.Fatal("Listen returned nil on a hostile frame")
	}

	// The reply is a close frame: FIN+0x8, then the 2-byte status.
	reply := conn.out.Bytes()
	if len(reply) < 4 {
		t.Fatalf("no close frame written back (got %d bytes)", len(reply))
	}
	if reply[0] != 0x88 {
		t.Errorf("reply opcode byte = %#x, want 0x88 (FIN+close)", reply[0])
	}
	code := int(reply[2])<<8 | int(reply[3])
	if code != closeMessageTooBig {
		t.Errorf("close status = %d, want %d", code, closeMessageTooBig)
	}
}

// panickingReader panics on the first read — standing in for any panic raised
// inside the read loop, including one from the host's inbound handler.
type panickingReader struct{}

func (panickingReader) Read([]byte) (int, error) { panic("boom from inside the read loop") }
func (panickingReader) Write(p []byte) (int, error) {
	return len(p), nil
}

func TestWSListenAndRecoverConvertsPanicToError(t *testing.T) {
	// The read loop runs in a goroutine the HOST spawned, which net/http does
	// not recover: a panic there takes the process, not the connection. This
	// is the boundary that makes the documented obligation satisfiable.
	ch := NewWSChannel(panickingReader{})

	err := ch.ListenAndRecover()
	if err == nil {
		t.Fatal("ListenAndRecover returned nil on a panicking read loop")
	}
	if !strings.Contains(err.Error(), "boom from inside the read loop") {
		t.Errorf("recovered error = %q, want it to name the panic", err.Error())
	}
}

func TestWSChannelWithLimitRejectsNonPositive(t *testing.T) {
	// A zero from an uninitialised config must not read as "unbounded" — that
	// is the defect, arriving by omission instead of by decision.
	ch := NewWSChannelWithLimit(&hostileConn{in: bytes.NewReader(nil)}, 0)
	if ch.frameLimit() != MaxFrameBytes {
		t.Errorf("zero limit = %d, want the MaxFrameBytes default %d", ch.frameLimit(), MaxFrameBytes)
	}

	// A literal-constructed channel (no constructor at all) defaults too.
	bare := &WSChannel{rw: &hostileConn{in: bytes.NewReader(nil)}}
	if bare.frameLimit() != MaxFrameBytes {
		t.Errorf("zero-valued channel limit = %d, want %d", bare.frameLimit(), MaxFrameBytes)
	}
}

var _ io.ReadWriter = (*hostileConn)(nil)
