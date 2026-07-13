package serverdriven

import (
	"bytes"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func sampleFrame() Frame {
	return Frame{Seq: 7, Ops: []wire.Obj{
		{Tag: "UpdateProp", Fields: map[string]wire.Value{
			"path": wire.Str("Source"), "target": wire.Str("count"), "value": wire.Int(1),
		}},
		{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("x")}},
	}}
}

func TestEncodeFrameJSONIsCanonical(t *testing.T) {
	got, err := EncodeFrameJSON(sampleFrame())
	if err != nil {
		t.Fatalf("EncodeFrameJSON: %v", err)
	}
	want := `{"ops":[{"$type":"UpdateProp","path":"Source","target":"count","value":1},{"$type":"RemoveNode","target":"x"}],"seq":7}`
	if got != want {
		t.Errorf("frame JSON =\n %s\nwant\n %s", got, want)
	}
}

func TestEncodeSSEShape(t *testing.T) {
	got, err := EncodeSSE(sampleFrame())
	if err != nil {
		t.Fatalf("EncodeSSE: %v", err)
	}
	body, _ := EncodeFrameJSON(sampleFrame())
	want := "id: 7\nevent: patch\ndata: " + body + "\n\n"
	if got != want {
		t.Errorf("SSE frame =\n%q\nwant\n%q", got, want)
	}
}

func TestDecodeEvent(t *testing.T) {
	ev, err := DecodeEvent([]byte(`{"connId":"c1","nodeId":"inc","event":"click","payload":"","lastSeq":4}`))
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	if ev.ConnID != "c1" || ev.NodeID != "inc" || ev.Event != "click" || ev.LastSeq != 4 {
		t.Errorf("decoded event = %+v", ev)
	}
}

// The RFC 6455 §1.3 worked example: this key derives this accept value.
func TestComputeAcceptKeyRFCVector(t *testing.T) {
	got := computeAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("accept key = %q, want %q", got, want)
	}
}

func TestWSTextFrameRoundTrip(t *testing.T) {
	// A server-written (unmasked) frame reads back intact.
	var buf bytes.Buffer
	payload := []byte(`{"ops":[],"seq":3}`)
	if err := writeTextFrame(&buf, payload); err != nil {
		t.Fatalf("writeTextFrame: %v", err)
	}
	opcode, got, err := readFrame(&buf)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != opText || !bytes.Equal(got, payload) {
		t.Errorf("round trip: opcode=%x payload=%q, want text %q", opcode, got, payload)
	}
}

func TestWSReadUnmasksClientFrame(t *testing.T) {
	// A browser sends masked frames; the reader must unmask. Build a masked
	// text frame by hand and assert it reads back to the plaintext.
	payload := []byte(`{"connId":"c1","nodeId":"inc","event":"click","lastSeq":0}`)
	mask := [4]byte{0xAA, 0xBB, 0xCC, 0xDD}
	frame := []byte{0x81, 0x80 | byte(len(payload))} // FIN+text, mask bit + len (<126)
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i&3])
	}
	opcode, got, err := readFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if opcode != opText || !bytes.Equal(got, payload) {
		t.Errorf("unmask: got %q, want %q", got, payload)
	}
}

func TestWSReadCloseFrame(t *testing.T) {
	_, _, err := readFrame(bytes.NewReader([]byte{0x88, 0x00})) // FIN+close, no payload
	if err != errClose {
		t.Errorf("close frame err = %v, want errClose", err)
	}
}
