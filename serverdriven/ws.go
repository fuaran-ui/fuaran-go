package serverdriven

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
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
	rw          io.ReadWriter
	writeMu     sync.Mutex
	handler     func(Event)
	closed      bool
	maxFrame    int
	readTimeout time.Duration
	pingEvery   time.Duration
	onDrop      func(what string, err error)
}

// DefaultReadTimeout bounds how long the read loop waits for the NEXT frame
// before treating the connection as dead.
//
// A read deadline is the only thing that distinguishes a quiet peer from a
// vanished one. Without it a half-open connection — the client's laptop lid
// closed, a NAT entry expired, a cable pulled — parks a goroutine and a file
// descriptor forever, and nothing in the process ever notices: TCP will not
// tell you, because from its side nothing has happened.
//
// It is paired with DefaultPingInterval rather than standing alone, because a
// deadline on its own would kill a perfectly healthy IDLE connection. The
// server pings; a conformant client's pong refreshes the deadline; a peer that
// answers nothing for a whole timeout window is gone.
const DefaultReadTimeout = 60 * time.Second

// DefaultPingInterval is how often the read loop sends a server ping to keep a
// healthy idle connection inside the read deadline. Comfortably under
// DefaultReadTimeout so a single lost pong is not fatal.
const DefaultPingInterval = 25 * time.Second

// NewWSChannel builds a WebSocket channel over an already-upgraded byte stream,
// bounding inbound frames at MaxFrameBytes and applying the default read
// deadline / ping interval.
func NewWSChannel(rw io.ReadWriter) *WSChannel {
	return &WSChannel{
		rw:          rw,
		maxFrame:    MaxFrameBytes,
		readTimeout: DefaultReadTimeout,
		pingEvery:   DefaultPingInterval,
	}
}

// NewWSChannelWithLimit is NewWSChannel with an explicit inbound frame cap, for
// a host whose client genuinely sends larger messages. A non-positive limit
// falls back to MaxFrameBytes rather than meaning "unbounded": an unbounded
// reader is the defect this cap exists to close, and a zero value arriving from
// an uninitialised config must not silently reinstate it.
func NewWSChannelWithLimit(rw io.ReadWriter, maxFrameBytes int) *WSChannel {
	ch := NewWSChannel(rw)
	if maxFrameBytes > 0 {
		ch.maxFrame = maxFrameBytes
	}
	return ch
}

// SetReadTimeout overrides the per-frame read deadline and the server ping
// interval. Call it BEFORE Listen — the loop reads both once at entry, and a
// channel is not safe for concurrent reconfiguration.
//
// A non-positive readTimeout disables the deadline entirely. That is a real
// choice for a host that keeps liveness elsewhere (its own heartbeat, a proxy
// idle timeout), and it is deliberately not the default: the default has to be
// safe for the host that never thought about it. A non-positive pingEvery
// leaves the deadline in place and stops the server pinging, which only makes
// sense when the CLIENT is the one keeping the connection warm.
func (c *WSChannel) SetReadTimeout(readTimeout, pingEvery time.Duration) {
	c.readTimeout = readTimeout
	c.pingEvery = pingEvery
}

// SetOnDrop registers a sink for everything the read loop discards: a client
// frame that will not decode, a pong reply that could not be written.
//
// These were silent. An undecodable frame was skipped by an `err == nil` guard
// with no else branch, and a failed pong was discarded into `_` — so a client
// sending malformed events, or a socket already half-dead, looked from the
// server exactly like a client sending nothing at all. The one thing an
// operator needs at that moment is to know the difference.
//
// The sink is advisory: the loop reports and carries on. A single malformed
// frame is not grounds to end a connection, and a host that wants it to be can
// close the channel from the sink. Call it BEFORE Listen.
func (c *WSChannel) SetOnDrop(sink func(what string, err error)) { c.onDrop = sink }

func (c *WSChannel) reportDrop(what string, err error) {
	if c.onDrop != nil {
		c.onDrop(what, err)
	}
}

// readDeadliner is the part of net.Conn the read loop needs. A hijacked
// connection satisfies it; an in-memory test stream does not, and gets no
// deadline rather than an error — the deadline is a property of a socket, and
// a pipe that cannot expire is not a failure to report.
type readDeadliner interface {
	SetReadDeadline(t time.Time) error
}

// refreshReadDeadline pushes the read deadline out by the configured timeout.
// Called before every frame read and again on every pong, so any evidence of a
// live peer resets the clock.
func (c *WSChannel) refreshReadDeadline() {
	if c.readTimeout <= 0 {
		return
	}
	if d, ok := c.rw.(readDeadliner); ok {
		_ = d.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
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

// Close marks the channel closed AND closes the underlying stream when it owns
// one.
//
// Flipping a bool was not a close. The socket stayed open, the read loop stayed
// blocked in Read, and the goroutine and file descriptor behind it were never
// reclaimed — so a host that dutifully called Close on every finished
// connection still leaked one per connection. Closing the stream is also what
// unblocks a Read that is parked with no deadline, which is what makes
// cancellation work at all.
//
// A stream that is not an io.Closer (an in-memory test buffer) is left alone:
// there is nothing to close, and that is not an error.
func (c *WSChannel) Close() error {
	c.writeMu.Lock()
	alreadyClosed := c.closed
	c.closed = true
	c.writeMu.Unlock()
	if alreadyClosed {
		return nil
	}
	if closer, ok := c.rw.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Listen runs the inbound read loop: decode each client text frame into an
// Event and deliver it to the registered handler; reply to pings with pongs.
// It returns when the peer closes or the stream errors. Run it in its own
// goroutine.
//
// A frame that breaches the protocol — oversized, unmasked, an outsized control
// payload — is answered with a close frame carrying the RFC 6455 status and
// then returned as a *ProtocolError. The connection is finished at that point;
// the loop does not attempt to resynchronise on a stream whose framing it can
// no longer trust.
//
// MUST RUN UNDER A RECOVER. This loop runs in a goroutine of the host's
// choosing, and net/http recovers panics only in the goroutine it started for
// a request — not in one the handler spawned. A panic here therefore takes the
// PROCESS down, not the connection. The registered inbound handler is host
// code and this package cannot vouch for it, so use ListenAndRecover unless
// the host has its own recover boundary around the call.
func (c *WSChannel) Listen() error {
	return c.ListenContext(context.Background())
}

// ListenContext is Listen bound to a context: cancelling ctx closes the
// underlying stream, which unblocks the parked Read and ends the loop.
//
// Closing the socket IS the cancellation mechanism, not a shortcut around one.
// A blocking Read on a net.Conn cannot be interrupted by anything else — there
// is no select over it — so a loop that only checked ctx.Err() between frames
// would go on waiting forever for a frame the cancelled peer will never send,
// which is precisely the shutdown hang cancellation exists to prevent.
//
// The loop also sends a server ping every ping interval, so an idle but
// healthy connection keeps refreshing its own read deadline. Every inbound
// frame, and every pong, pushes the deadline out again.
func (c *WSChannel) ListenContext(ctx context.Context) error {
	done := make(chan struct{})
	defer close(done)

	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = c.Close()
			case <-done:
			}
		}()
	}

	if c.pingEvery > 0 {
		go c.pingLoop(done)
	}

	reader := bufio.NewReader(c.rw)
	for {
		c.refreshReadDeadline()
		opcode, payload, err := readFrame(reader, c.frameLimit(), true)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The stream error is the consequence of the cancellation, not
				// an independent failure; report the cause.
				return ctxErr
			}
			if errors.Is(err, errClose) || errors.Is(err, io.EOF) {
				return nil
			}
			var pe *ProtocolError
			if errors.As(err, &pe) {
				c.writeMu.Lock()
				_ = writeCloseFrame(c.rw, pe.CloseCode, pe.Reason)
				c.writeMu.Unlock()
			}
			return err
		}
		switch opcode {
		case opText:
			ev, decodeErr := DecodeEvent(payload)
			if decodeErr != nil {
				c.reportDrop("undecodable client event frame", decodeErr)
				continue
			}
			if c.handler != nil {
				c.handler(ev)
			}
		case opPing:
			c.writeMu.Lock()
			writeErr := writeFrame(c.rw, opPong, payload)
			c.writeMu.Unlock()
			if writeErr != nil {
				c.reportDrop("pong reply failed", writeErr)
			}
		case opPong:
			// Evidence of a live peer — the deadline is refreshed at the top of
			// the next iteration, which is what "per pong" means here.
		}
	}
}

// pingLoop sends a server ping every ping interval until the read loop ends.
// A write failure is left to the read side to notice: a dead socket fails the
// next read, and racing to report the same death twice buys nothing.
func (c *WSChannel) pingLoop(done <-chan struct{}) {
	ticker := time.NewTicker(c.pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			c.writeMu.Lock()
			closed := c.closed
			if !closed {
				_ = writeFrame(c.rw, opPing, nil)
			}
			c.writeMu.Unlock()
			if closed {
				return
			}
		}
	}
}

// frameLimit is the channel's inbound cap, defaulting a zero-valued struct
// (one built by literal rather than by the constructors) to MaxFrameBytes.
func (c *WSChannel) frameLimit() int {
	if c.maxFrame <= 0 {
		return MaxFrameBytes
	}
	return c.maxFrame
}

// ListenAndRecover runs Listen under a recover boundary, converting a panic
// from the read loop or from the host's inbound handler into an ordinary
// error return.
//
// This is the wrapper the "MUST run under a recover" obligation above names,
// supplied rather than merely documented because the failure it prevents is
// total: a panic in a host-spawned goroutine is not recovered by net/http, so
// one malformed connection would otherwise end the process serving every other
// one. Use it unless the host has its own boundary.
//
// The recovered value is reported, not swallowed: the returned error names the
// panic so the host can log it and, for a runtime error, still see what broke.
func (c *WSChannel) ListenAndRecover() (err error) {
	return c.ListenAndRecoverContext(context.Background())
}

// ListenAndRecoverContext is ListenContext under the same recover boundary as
// ListenAndRecover.
func (c *WSChannel) ListenAndRecoverContext(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if recovered, ok := r.(error); ok {
				err = fmt.Errorf("serverdriven: websocket read loop panicked: %w", recovered)
				return
			}
			err = fmt.Errorf("serverdriven: websocket read loop panicked: %v", r)
		}
	}()
	return c.ListenContext(ctx)
}

// ErrOriginNotAllowed is returned when the handshake's Origin fails the
// UpgradeOptions policy. It is returned BEFORE the connection is hijacked, so
// the ResponseWriter is untouched and the host can still write a status
// (403 is the usual choice). Test for it with errors.Is.
var ErrOriginNotAllowed = errors.New("serverdriven: origin not allowed")

// UpgradeOptions configures the upgrade handshake's Origin policy.
//
// THE ZERO VALUE IS THE SAFE DEFAULT: same-origin only. The same-origin policy
// does not cover WebSockets, so a browser will happily let a page on any origin
// open a socket to this server — with the victim's cookies attached. A helper
// that upgraded whatever it was handed unless told otherwise would give every
// host a cross-site WebSocket hijacking bug by omission, silently, so the
// unconfigured policy is the restrictive one and widening it is the deliberate
// act.
//
// HOST OBLIGATION. Widening this policy is a security decision the host owns,
// and Origin is the only signal separating a victim's browser from an
// attacker's page. Before adding an entry, be sure the socket either carries no
// ambient authority (no cookies, no HTTP auth, no client certificate) or
// authenticates every peer independently of the browser's ambient credentials.
type UpgradeOptions struct {
	// AllowedOrigins widens the policy beyond same-origin. Same-origin is
	// ALWAYS allowed and needs no entry here — the list is additive, so adding
	// a partner origin can never lock out the page this server serves.
	//
	// Each entry is a fully-serialised origin ("https://app.example.com" —
	// scheme, host, and port when non-default), matched case-insensitively; a
	// bare host name never matches. Two values carry teeth:
	//
	//   - "*" disables the check entirely and re-opens the hijacking hole for
	//     any cookie-authenticated socket. Correct only for a socket that
	//     carries no ambient authority at all.
	//   - "null" matches the literal "Origin: null" that sandboxed iframes,
	//     file:// documents and some redirect chains send. An attacker can mint
	//     a null origin at will — a sandboxed iframe is enough — so it is never
	//     treated as same-origin, and allowing it is nearly as broad as "*".
	AllowedOrigins []string

	// DenyMissingOrigin refuses a handshake carrying no Origin header at all.
	// The default (false) ALLOWS it, deliberately:
	//
	// Origin is a defence against browsers, and only browsers. RFC 6455
	// requires a browser client to send Origin on every handshake, so an absent
	// header means the peer is not a browser — a CLI, a service, a mobile app,
	// a test — which are exactly the clients a headless host exists to serve.
	// Refusing them by default would break that common case to buy nothing: a
	// non-browser peer is not bound by the same-origin policy and can simply
	// send whichever Origin the allowlist accepts, so the check never held it
	// back. Authentication, not Origin, is what keeps a non-browser peer out.
	//
	// Set it when the socket is only ever opened by page JavaScript, where a
	// header-less handshake is by definition not the client you shipped.
	DenyMissingOrigin bool
}

// originAllowed applies the UpgradeOptions policy to one request.
func originAllowed(r *http.Request, opts UpgradeOptions) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return !opts.DenyMissingOrigin
	}
	if sameOrigin(origin, r.Host) {
		return true
	}
	for _, allowed := range opts.AllowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

// sameOrigin reports whether the serialised Origin names the same host as the
// request.
//
// It compares HOST ONLY, not scheme. A TLS-terminating proxy or load balancer
// leaves this server seeing plain HTTP while the browser used https, so a
// strict scheme comparison would reject every legitimate same-origin upgrade
// behind one — a check that fails closed on correct traffic gets switched off,
// which is worse than the narrower one it replaced. A host needing
// scheme-exactness names the exact origin in AllowedOrigins.
//
// "null" and any other unparseable or host-less value are never same-origin;
// they match only an explicit AllowedOrigins entry.
func sameOrigin(origin, host string) bool {
	if host == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// ServeWebSocket completes the RFC 6455 handshake on an HTTP request and
// returns the channel over the hijacked connection, under the DEFAULT origin
// policy — same-origin only (see UpgradeOptions). The caller starts the read
// loop (Listen) in a goroutine and wires the channel to a Connection. Returns
// an error if the request is not a valid WebSocket upgrade, its Origin is not
// allowed, or the ResponseWriter cannot be hijacked.
func ServeWebSocket(w http.ResponseWriter, r *http.Request) (*WSChannel, error) {
	return ServeWebSocketWithOptions(w, r, UpgradeOptions{})
}

// ServeWebSocketWithOptions is ServeWebSocket with an explicit origin policy.
// Read UpgradeOptions before widening it: the zero value is same-origin, and
// every widening is a decision the host owns.
func ServeWebSocketWithOptions(w http.ResponseWriter, r *http.Request, opts UpgradeOptions) (*WSChannel, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("serverdriven: not a WebSocket upgrade request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("serverdriven: missing Sec-WebSocket-Key")
	}
	// Checked BEFORE the hijack: a refused upgrade must leave the
	// ResponseWriter intact so the host can still write a 403.
	if !originAllowed(r, opts) {
		return nil, ErrOriginNotAllowed
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
