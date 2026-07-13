package serverdriven

import (
	"io"
	"net/http"
	"strconv"
)

// The Server-Sent-Events backend — a Channel over an io.Writer, plus a
// net/http streaming helper. Push writes the SSE wire frame (EncodeSSE); the
// id: line carries the op sequence, so a browser EventSource replays
// Last-Event-ID on reconnect, which the host maps to Connection.Resync.
//
// SSE is one-directional (server→client); the inbound client→client path is a
// companion POST endpoint the host wires to the connection's Handle. The
// framing (EncodeSSE) is the pure, testable core; the HTTP helpers are thin
// glue.

// SSEChannel is a Channel that writes frames as SSE events to an io.Writer,
// flushing after each frame when the writer supports it (an
// http.ResponseWriter does). Receive stores the inbound handler for the
// companion POST path to call.
type SSEChannel struct {
	w       io.Writer
	flush   func()
	handler func(Event)
	closed  bool
}

// NewSSEChannel builds an SSE channel over w. If w is an http.Flusher, each
// frame is flushed immediately (the long-lived streaming response).
func NewSSEChannel(w io.Writer) *SSEChannel {
	ch := &SSEChannel{w: w}
	if f, ok := w.(http.Flusher); ok {
		ch.flush = f.Flush
	}
	return ch
}

// Push writes the frame as an SSE event.
func (c *SSEChannel) Push(frame Frame) error {
	sse, err := EncodeSSE(frame)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(c.w, sse); err != nil {
		return err
	}
	if c.flush != nil {
		c.flush()
	}
	return nil
}

// Receive registers the inbound handler (invoked by the companion POST path).
func (c *SSEChannel) Receive(handler func(Event)) { c.handler = handler }

// Close marks the channel closed.
func (c *SSEChannel) Close() error {
	c.closed = true
	return nil
}

// Inbound delivers a client event to the registered handler — the companion
// POST endpoint (or a test) calls this.
func (c *SSEChannel) Inbound(ev Event) {
	if c.handler != nil {
		c.handler(ev)
	}
}

// ServeSSE writes the SSE response headers and returns the channel bound to
// the streaming response, ready for a Connection. The caller keeps the request
// goroutine alive (e.g. blocking on the request context) for as long as the
// connection should stream; a Last-Event-ID header (the client's reconnect
// cursor) is returned so the host can call Connection.Resync.
func ServeSSE(w http.ResponseWriter, r *http.Request) (*SSEChannel, int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	lastSeq := 0
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lastSeq = n
		}
	}
	return NewSSEChannel(w), lastSeq
}
