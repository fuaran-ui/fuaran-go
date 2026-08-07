package serverdriven

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The Origin policy on the WebSocket upgrade. The same-origin policy does not
// cover WebSockets, so without this check a page on any origin can open a
// socket to this server carrying the victim's cookies — cross-site WebSocket
// hijacking. These tests pin the default (same-origin), the widening knobs, and
// the one property that makes a refusal useful: the connection is NOT hijacked,
// so the host can still write a status.

// originConn is a net.Conn over an in-memory buffer — enough for the handshake
// bytes ServeWebSocket writes on the success path.
type originConn struct {
	bytes.Buffer
	closed bool
}

func (c *originConn) Close() error                  { c.closed = true; return nil }
func (c *originConn) LocalAddr() net.Addr           { return nil }
func (c *originConn) RemoteAddr() net.Addr          { return nil }
func (c *originConn) SetDeadline(_ time.Time) error { return nil }
func (c *originConn) SetReadDeadline(t time.Time) error {
	return c.SetDeadline(t)
}
func (c *originConn) SetWriteDeadline(t time.Time) error {
	return c.SetDeadline(t)
}

// originRecorder is an http.ResponseWriter that can be hijacked, and records
// whether it was — the assertion that a refusal happens BEFORE the hijack.
type originRecorder struct {
	*httptest.ResponseRecorder
	conn     *originConn
	hijacked bool
}

func newOriginRecorder() *originRecorder {
	return &originRecorder{ResponseRecorder: httptest.NewRecorder(), conn: &originConn{}}
}

func (w *originRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	rw := bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn))
	return w.conn, rw, nil
}

// upgradeRequest builds a well-formed handshake to host, with the given Origin
// ("" omits the header entirely).
func upgradeRequest(host, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+"/live", nil)
	r.Host = host
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestUpgradeOriginPolicy(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		opts   UpgradeOptions
		allow  bool
	}{
		// The default (zero-value) policy: same-origin only.
		{"same origin allowed", "app.example.com", "https://app.example.com", UpgradeOptions{}, true},
		{"same origin with port", "app.example.com:8443", "https://app.example.com:8443", UpgradeOptions{}, true},
		{"host match is case-insensitive", "app.example.com", "https://APP.Example.COM", UpgradeOptions{}, true},
		// Scheme is deliberately not compared (TLS-terminating proxies).
		{"scheme mismatch still same-origin", "app.example.com", "http://app.example.com", UpgradeOptions{}, true},
		{"cross origin refused by default", "app.example.com", "https://evil.example", UpgradeOptions{}, false},
		// The prefix/suffix confusions an attacker reaches for first.
		{"suffix of the host refused", "app.example.com", "https://evil-app.example.com", UpgradeOptions{}, false},
		{"host as a subdomain of an attacker domain refused", "app.example.com", "https://app.example.com.evil.test", UpgradeOptions{}, false},
		{"different port is a different origin", "app.example.com:8443", "https://app.example.com:9999", UpgradeOptions{}, false},

		// A missing Origin is allowed by default — see UpgradeOptions.
		{"missing origin allowed by default", "app.example.com", "", UpgradeOptions{}, true},
		{"missing origin refused when denied", "app.example.com", "", UpgradeOptions{DenyMissingOrigin: true}, false},

		// "null" is never same-origin; a sandboxed iframe can mint it at will.
		{"null origin refused by default", "app.example.com", "null", UpgradeOptions{}, false},
		{"null origin allowed only when listed", "app.example.com", "null",
			UpgradeOptions{AllowedOrigins: []string{"null"}}, true},

		// The widening knobs.
		{"allowlisted cross origin allowed", "app.example.com", "https://partner.example",
			UpgradeOptions{AllowedOrigins: []string{"https://partner.example"}}, true},
		{"allowlist entry match is case-insensitive", "app.example.com", "https://Partner.Example",
			UpgradeOptions{AllowedOrigins: []string{"https://partner.example"}}, true},
		{"unlisted origin still refused", "app.example.com", "https://evil.example",
			UpgradeOptions{AllowedOrigins: []string{"https://partner.example"}}, false},
		{"a bare host name never matches", "app.example.com", "https://partner.example",
			UpgradeOptions{AllowedOrigins: []string{"partner.example"}}, false},
		{"wildcard allows anything", "app.example.com", "https://evil.example",
			UpgradeOptions{AllowedOrigins: []string{"*"}}, true},
		// The allowlist is ADDITIVE: naming a partner must not lock out the
		// origin this server itself serves.
		{"same origin survives a non-empty allowlist", "app.example.com", "https://app.example.com",
			UpgradeOptions{AllowedOrigins: []string{"https://partner.example"}}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newOriginRecorder()
			ch, err := ServeWebSocketWithOptions(w, upgradeRequest(tc.host, tc.origin), tc.opts)

			if !tc.allow {
				if !errors.Is(err, ErrOriginNotAllowed) {
					t.Fatalf("origin %q against host %q: got err %v, want ErrOriginNotAllowed", tc.origin, tc.host, err)
				}
				if ch != nil {
					t.Error("a refused upgrade returned a channel")
				}
				// The point of refusing before the hijack: the host can still
				// write a 403 on an untouched ResponseWriter.
				if w.hijacked {
					t.Error("a refused upgrade hijacked the connection — the host can no longer write a status")
				}
				return
			}

			if err != nil {
				t.Fatalf("origin %q against host %q: unexpected error %v", tc.origin, tc.host, err)
			}
			if ch == nil {
				t.Fatal("allowed upgrade returned no channel")
			}
			if !w.hijacked {
				t.Error("allowed upgrade did not hijack the connection")
			}
			if got := w.conn.String(); !strings.HasPrefix(got, "HTTP/1.1 101 Switching Protocols\r\n") {
				t.Errorf("handshake not written: %q", got)
			}
		})
	}
}

// ServeWebSocket (the no-options entry point) must carry the safe default, not
// a permissive one — the whole point of the zero value.
func TestServeWebSocketDefaultsToSameOrigin(t *testing.T) {
	w := newOriginRecorder()
	_, err := ServeWebSocket(w, upgradeRequest("app.example.com", "https://evil.example"))
	if !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("ServeWebSocket admitted a cross-origin upgrade: err = %v", err)
	}
	if w.hijacked {
		t.Error("ServeWebSocket hijacked the connection for a refused origin")
	}

	w2 := newOriginRecorder()
	if _, err := ServeWebSocket(w2, upgradeRequest("app.example.com", "https://app.example.com")); err != nil {
		t.Fatalf("ServeWebSocket refused a same-origin upgrade: %v", err)
	}
}

// The Origin check must not weaken the handshake validation that precedes it:
// a malformed upgrade is still rejected on its own terms, whatever its Origin.
func TestUpgradeValidationPrecedesOrigin(t *testing.T) {
	r := upgradeRequest("app.example.com", "https://app.example.com")
	r.Header.Del("Sec-WebSocket-Key")

	w := newOriginRecorder()
	_, err := ServeWebSocket(w, r)
	if err == nil {
		t.Fatal("an upgrade with no Sec-WebSocket-Key was accepted")
	}
	if errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("wrong rejection reason: %v", err)
	}
}
