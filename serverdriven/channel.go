package serverdriven

// The transport seam. The driver pushes server→client Frames through a Channel
// and receives client→server Events from it. NO transport type leaks into the
// driver — it only ever sees this interface, which is what makes a WebSocket
// backend a drop-in swap for the SSE one. The seam is per-connection (one
// channel = one live connection); a multiplexing backend owns the connId →
// Channel registry above this seam.

// Channel is the transport seam a backend implements.
type Channel interface {
	// Push sends a frame to the connected client.
	Push(frame Frame) error
	// Receive registers the handler the transport invokes for each inbound
	// client event.
	Receive(handler func(Event))
	// Close tears the connection down.
	Close() error
}

// InMemoryChannel is the headlessly-testable reference channel the SSE / WS
// backends specialise. Pushed records every frame sent to the client (assert
// against it); Send simulates an inbound client event.
type InMemoryChannel struct {
	pushed  []Frame
	handler func(Event)
	closed  bool
}

// Pushed returns every frame pushed to the client, in order.
func (c *InMemoryChannel) Pushed() []Frame { return c.pushed }

// Closed reports whether Close has been called.
func (c *InMemoryChannel) Closed() bool { return c.closed }

// Send simulates an inbound client→server event (what a real transport's POST
// / WS-message receiver delivers to the registered handler).
func (c *InMemoryChannel) Send(ev Event) {
	if c.handler != nil {
		c.handler(ev)
	}
}

// Push records the frame (InMemory never fails).
func (c *InMemoryChannel) Push(frame Frame) error {
	c.pushed = append(c.pushed, frame)
	return nil
}

// Receive registers the inbound handler.
func (c *InMemoryChannel) Receive(handler func(Event)) { c.handler = handler }

// Close marks the channel closed.
func (c *InMemoryChannel) Close() error {
	c.closed = true
	return nil
}
