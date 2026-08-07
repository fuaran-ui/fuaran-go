package serverdriven

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"net/url"
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
