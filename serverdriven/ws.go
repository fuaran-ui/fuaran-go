package serverdriven

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
)

// The WebSocket backend — a Channel over an io.ReadWriter (a hijacked
// net.Conn in production), plus a net/http upgrade helper. Push writes the
// frame's canonical JSON body (EncodeFrameJSON) as one text frame; the inbound
// read loop decodes client text frames into Events. Bidirectional, unlike SSE.
// Built on the stdlib-only frame codec (wsframe.go) — no third-party module.

// WSChannel is a Channel over a full-duplex byte stream. Writes are guarded by
// a mutex (the read loop and Push may race). The inbound read loop is started
// by ServeWebSocket / Listen.
type WSChannel struct {
	rw      io.ReadWriter
	writeMu sync.Mutex
	handler func(Event)
	closed  bool
}

// NewWSChannel builds a WebSocket channel over an already-upgraded byte stream.
func NewWSChannel(rw io.ReadWriter) *WSChannel {
	return &WSChannel{rw: rw}
}

// Push writes the frame's canonical JSON body as one WebSocket text frame.
func (c *WSChannel) Push(frame Frame) error {
	body, err := EncodeFrameJSON(frame)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return errors.New("serverdriven: channel closed")
	}
	return writeTextFrame(c.rw, []byte(body))
}

// Receive registers the inbound handler (invoked by the read loop for each
// decoded client event).
func (c *WSChannel) Receive(handler func(Event)) { c.handler = handler }

// Close marks the channel closed.
func (c *WSChannel) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.closed = true
	return nil
}

// Listen runs the inbound read loop: decode each client text frame into an
// Event and deliver it to the registered handler; reply to pings with pongs.
// It returns when the peer closes or the stream errors. Run it in its own
// goroutine.
func (c *WSChannel) Listen() error {
	reader := bufio.NewReader(c.rw)
	for {
		opcode, payload, err := readFrame(reader)
		if err != nil {
			if errors.Is(err, errClose) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch opcode {
		case opText:
			ev, decodeErr := DecodeEvent(payload)
			if decodeErr == nil && c.handler != nil {
				c.handler(ev)
			}
		case opPing:
			c.writeMu.Lock()
			_ = writeFrame(c.rw, opPong, payload)
			c.writeMu.Unlock()
		}
	}
}

// ServeWebSocket completes the RFC 6455 handshake on an HTTP request and
// returns the channel over the hijacked connection. The caller starts the read
// loop (Listen) in a goroutine and wires the channel to a Connection. Returns
// an error if the request is not a valid WebSocket upgrade or the
// ResponseWriter cannot be hijacked.
func ServeWebSocket(w http.ResponseWriter, r *http.Request) (*WSChannel, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("serverdriven: not a WebSocket upgrade request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("serverdriven: missing Sec-WebSocket-Key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("serverdriven: ResponseWriter does not support hijacking")
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	accept := computeAcceptKey(key)
	handshake := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := buf.WriteString(handshake); err != nil {
		conn.Close()
		return nil, err
	}
	if err := buf.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return NewWSChannel(conn), nil
}
