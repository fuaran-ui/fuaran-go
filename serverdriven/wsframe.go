package serverdriven

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
)

// A minimal RFC 6455 WebSocket text-frame codec — stdlib only (no third-party
// module). The server writes unmasked text frames; a client sends masked
// frames, which the reader unmasks. This is the genuinely-stdlib way to ship a
// WebSocket backend under the repo's no-third-party mandate: the handshake is
// HTTP + SHA-1 + base64, and the framing is a few dozen lines over io.
//
// Scope: the text (0x1), close (0x8), ping (0x9), and pong (0xA) opcodes — the
// set a server-driven live connection needs. Continuation/binary frames are
// out of scope (the wire carries single canonical-JSON text frames).

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket frame opcodes (the subset this codec handles).
const (
	opText  = 0x1
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xA
)

// computeAcceptKey derives the Sec-WebSocket-Accept response value from the
// client's Sec-WebSocket-Key (RFC 6455 §4.2.2): base64(sha1(key + magic)).
func computeAcceptKey(key string) string {
	h := sha1.New()
	io.WriteString(h, key+wsMagic)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// writeTextFrame writes payload as a single unmasked server→client text frame
// (FIN set, opcode 0x1).
func writeTextFrame(w io.Writer, payload []byte) error {
	return writeFrame(w, opText, payload)
}

func writeFrame(w io.Writer, opcode byte, payload []byte) error {
	header := make([]byte, 0, 10)
	header = append(header, 0x80|opcode) // FIN + opcode
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n)) // server frames are unmasked (mask bit 0)
	case n < 65536:
		header = append(header, 126)
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header = append(header, 127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// errClose signals a WebSocket close frame was read.
var errClose = errors.New("serverdriven: websocket connection closed")

// readFrame reads one WebSocket frame, returning its opcode and unmasked
// payload. A close frame returns errClose. Control frames (ping/pong) are
// returned to the caller to handle (a read loop replies to a ping with a
// pong). Only FIN frames are supported (no continuation).
func readFrame(r io.Reader) (opcode byte, payload []byte, err error) {
	head := make([]byte, 2)
	if _, err = io.ReadFull(r, head); err != nil {
		return 0, nil, err
	}
	fin := head[0]&0x80 != 0
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := int(head[1] & 0x7F)

	if !fin {
		return 0, nil, errors.New("serverdriven: fragmented frames are not supported")
	}

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint64(ext))
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(r, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}

	payload = make([]byte, length)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i&3]
		}
	}
	if opcode == opClose {
		return opClose, payload, errClose
	}
	return opcode, payload, nil
}
