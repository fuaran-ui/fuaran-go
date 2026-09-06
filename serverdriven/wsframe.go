package serverdriven

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
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

// MaxFrameBytes bounds a single INBOUND frame's payload, in bytes.
//
// This is a transport-level cap and it is not the same thing as the §21 wire
// limits. Those bound the STRUCTURE of a document that has already been read;
// this bounds what is read at all. The distinction is the whole point: a frame
// header declares its own payload length, so a peer can claim a length before
// sending a single byte of it, and a reader that believes the claim allocates
// on the strength of an unauthenticated 64-bit integer. Above MaxInt64 the
// conversion to int is negative and makeslice panics; a large positive is a
// fatal out-of-memory. Either way the process dies, in a goroutine net/http
// does not recover, before any authentication has run.
//
// 1 MiB is generous for the traffic this transport carries — a client event is
// a small control message (connId, nodeId, event name, payload string), and
// server frames are written, not read. A host serving genuinely larger inbound
// messages sets its own ceiling with NewWSChannelWithLimit rather than raising
// this one, because the default has to be safe for the host that never thought
// about it.
const MaxFrameBytes = 1 << 20

// maxControlFrameBytes is RFC 6455 §5.5: a control frame (close, ping, pong)
// carries at most 125 bytes and is never fragmented. Enforced because a ping
// payload is echoed straight back as a pong — an unchecked one is an
// amplification primitive as well as an allocation one.
const maxControlFrameBytes = 125

// WebSocket close codes (RFC 6455 §7.4.1) this codec sends on a refusal.
const (
	closeProtocolError = 1002
	closeMessageTooBig = 1009
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

// ProtocolError is a refusal of a malformed or oversized inbound frame. It
// carries the RFC 6455 close code the read loop sends back before hanging up,
// so a refusal is reported to the peer in the protocol's own vocabulary rather
// than as a bare disconnect. Test for it with errors.As.
type ProtocolError struct {
	// CloseCode is the RFC 6455 §7.4.1 status sent in the close frame (1002
	// protocol error, 1009 message too big).
	CloseCode int
	Reason    string
}

func (e *ProtocolError) Error() string { return "serverdriven: " + e.Reason }

func protocolError(code int, reason string) *ProtocolError {
	return &ProtocolError{CloseCode: code, Reason: reason}
}

// writeCloseFrame writes a close frame carrying a status code and reason
// (RFC 6455 §5.5.1: the 2-byte big-endian code, then a UTF-8 reason). Best
// effort — a peer that has already gone away is not an error worth reporting
// above the one that caused the close.
func writeCloseFrame(w io.Writer, code int, reason string) error {
	body := make([]byte, 0, 2+len(reason))
	body = binary.BigEndian.AppendUint16(body, uint16(code))
	body = append(body, reason...)
	if len(body) > maxControlFrameBytes {
		body = body[:maxControlFrameBytes]
	}
	return writeFrame(w, opClose, body)
}

// readFrame reads one WebSocket frame, returning its opcode and unmasked
// payload. A close frame returns errClose. Control frames (ping/pong) are
// returned to the caller to handle (a read loop replies to a ping with a
// pong). Only FIN frames are supported (no continuation).
//
// maxPayload bounds the declared payload length. THE CHECK RUNS BEFORE THE
// ALLOCATION and before the length is narrowed to int, which is the only
// ordering that works: the declared length is an unauthenticated peer-supplied
// uint64, so converting it first is already the bug (0xFFFFFFFFFFFFFFFF
// narrows to -1 and makeslice panics). A breach returns a *ProtocolError
// carrying close code 1009 and reads no payload at all — the connection is
// finished either way, so draining bytes the peer may never send would only
// hand it the stall the cap exists to refuse.
//
// clientToServer additionally requires masking (RFC 6455 §5.1: every
// client-to-server frame MUST be masked) and holds control frames to their
// 125-byte ceiling.
func readFrame(r io.Reader, maxPayload int, clientToServer bool) (opcode byte, payload []byte, err error) {
	head := make([]byte, 2)
	if _, err = io.ReadFull(r, head); err != nil {
		return 0, nil, err
	}
	fin := head[0]&0x80 != 0
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	declared := uint64(head[1] & 0x7F)

	if !fin {
		return 0, nil, protocolError(closeProtocolError, "fragmented frames are not supported")
	}

	switch declared {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		declared = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		declared = binary.BigEndian.Uint64(ext)
	}

	isControl := opcode&0x08 != 0
	if isControl && declared > maxControlFrameBytes {
		return opcode, nil, protocolError(closeProtocolError,
			"control frame payload exceeds the RFC 6455 limit of "+itoa(maxControlFrameBytes)+" bytes")
	}
	if clientToServer && !masked {
		return opcode, nil, protocolError(closeProtocolError,
			"client-to-server frames must be masked (RFC 6455 §5.1)")
	}

	// The cap, before the narrowing conversion and before any allocation.
	if maxPayload > 0 && declared > uint64(maxPayload) {
		return opcode, nil, protocolError(closeMessageTooBig,
			"frame payload of "+utoa(declared)+" bytes exceeds the frame limit of "+itoa(maxPayload)+" bytes")
	}

	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(r, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}

	payload = make([]byte, int(declared))
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

func itoa(n int) string { return strconv.Itoa(n) }

func utoa(n uint64) string { return strconv.FormatUint(n, 10) }
