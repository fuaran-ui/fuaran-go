package serverdriven

// One live connection binds a Session to a Channel. On each inbound event it
// steps the driver, advances the op sequence, and pushes a Frame when the step
// produced ops. A rejected step pushes nothing and leaves the session
// unchanged — the reject is surfaced through the OnReject sink. The connection
// owns the connection's mutable session (a connection is inherently stateful —
// one evolving tree).

// DefaultReplayBufferCapacity bounds the per-connection reconnect-replay
// buffer, in frames. At capacity the OLDEST frame is evicted, so a
// never-reconnecting client cannot grow server memory without limit; a client
// reconnecting from behind the retained window gets a partial replay.
const DefaultReplayBufferCapacity = 512

// Connection drives one Session through one Channel, buffering frames for
// reconnect-replay. It is single-goroutine per connection (Handle is not
// re-entrant); a real transport serialises a connection's inbound events.
type Connection struct {
	connID    string
	session   *Session
	channel   Channel
	seq       int
	buffer    []Frame
	bufferCap int
	onReject  func(Reject)
}

// ConnectionOption configures a Connection at construction.
type ConnectionOption func(*Connection)

// WithReplayBufferCapacity overrides the default replay-buffer cap.
func WithReplayBufferCapacity(capacity int) ConnectionOption {
	return func(c *Connection) {
		if capacity > 0 {
			c.bufferCap = capacity
		}
	}
}

// WithOnReject registers a sink for rejected steps (the always-on audit hook;
// the structured error frame back to the client is a documented follow-on).
func WithOnReject(sink func(Reject)) ConnectionOption {
	return func(c *Connection) { c.onReject = sink }
}

// NewConnection binds a session to a channel and wires the channel's inbound
// handler to Handle (delivering only events addressed to this connId).
func NewConnection(connID string, session *Session, channel Channel, opts ...ConnectionOption) *Connection {
	c := &Connection{
		connID:    connID,
		session:   session,
		channel:   channel,
		bufferCap: DefaultReplayBufferCapacity,
	}
	for _, opt := range opts {
		opt(c)
	}
	channel.Receive(func(ev Event) {
		if ev.ConnID == "" || ev.ConnID == connID {
			c.Handle(ev)
		}
	})
	return c
}

// Session returns the current server-held session.
func (c *Connection) Session() *Session { return c.session }

// Sequence returns the current op sequence pushed to this connection.
func (c *Connection) Sequence() int { return c.seq }

// Handle steps the connection with one inbound event: drive the session,
// advance the op sequence, and push a Frame when the step produced ops. A
// rejected step records the reject and changes nothing.
func (c *Connection) Handle(ev Event) error {
	opsList, reject := c.session.Step(ev)
	if reject != nil {
		if c.onReject != nil {
			c.onReject(*reject)
		}
		return nil
	}
	if len(opsList) == 0 {
		return nil // a legitimate no-op — no frame, no seq advance.
	}
	c.seq++
	frame := Frame{Seq: c.seq, Ops: opsList}
	c.bufferFrame(frame)
	return c.channel.Push(frame)
}

// bufferFrame appends to the replay buffer, evicting the oldest frame at
// capacity.
func (c *Connection) bufferFrame(frame Frame) {
	if len(c.buffer) >= c.bufferCap && len(c.buffer) > 0 {
		c.buffer = c.buffer[1:]
	}
	c.buffer = append(c.buffer, frame)
}

// Resync re-pushes every RETAINED buffered frame newer than lastSeq — the
// transport-agnostic reconnect replay. A backend calls this when a client
// reconnects carrying its last-applied sequence, so a brief transport drop
// loses no state. The buffer is bounded: a client reconnecting from behind the
// retained window gets only the retained tail. Returns the number of frames
// replayed, or the first push error.
func (c *Connection) Resync(lastSeq int) (int, error) {
	replayed := 0
	for _, frame := range c.buffer {
		if frame.Seq > lastSeq {
			if err := c.channel.Push(frame); err != nil {
				return replayed, err
			}
			replayed++
		}
	}
	return replayed, nil
}
