package serverdriven

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── H-36 · the inbound path is serialised ──────────────────────────────────

// countingWriter is a stand-in for the streaming ResponseWriter an SSE
// connection holds. It records every write and — crucially — is NOT itself
// synchronised, so a race in the layer above shows up here rather than being
// hidden by a thread-safe sink.
type countingWriter struct {
	writes int
	bytes  int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	w.bytes += len(p)
	return len(p), nil
}

func TestSSEInboundIsSerialisedUnderConcurrentEvents(t *testing.T) {
	// Two rapid clicks. Before the connection lock, each companion POST ran on
	// its own net/http goroutine and Handle → Step → Push raced itself over the
	// session tree, the seq counter, the replay buffer and the ResponseWriter.
	// Run under -race: the assertions below are secondary, the detector is the
	// primary instrument.
	sink := &countingWriter{}
	ch := NewSSEChannel(sink)
	session := newCounterSession(t)
	conn := NewConnection("c1", session, ch)

	const clicks = 64
	var wg sync.WaitGroup
	wg.Add(clicks)
	start := make(chan struct{})
	for i := 0; i < clicks; i++ {
		go func() {
			defer wg.Done()
			<-start
			ch.Inbound(Event{ConnID: "c1", NodeID: "inc", Event: "click"})
		}()
	}
	close(start)
	wg.Wait()

	// Every click produced exactly one frame, so the sequence is exactly the
	// click count: no lost seq (two goroutines reading the same value) and no
	// duplicate (two incrementing past each other).
	if got := conn.Sequence(); got != clicks {
		t.Errorf("sequence after %d concurrent clicks = %d, want %d", clicks, got, clicks)
	}
	if sink.writes != clicks {
		t.Errorf("SSE writes = %d, want %d (one whole frame per click)", sink.writes, clicks)
	}
}

func TestSSEPushIsSerialisedAgainstDirectHostPushes(t *testing.T) {
	// A host pushing out-of-band (a heartbeat) alongside the driven frames.
	// An SSE frame is several lines; two unguarded writers interleave BYTES,
	// so the client sees one corrupt event rather than two good ones.
	sink := &countingWriter{}
	ch := NewSSEChannel(sink)
	session := newCounterSession(t)
	NewConnection("c1", session, ch)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 32; i++ {
			ch.Inbound(Event{ConnID: "c1", NodeID: "inc", Event: "click"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 32; i++ {
			_ = ch.Push(Frame{Seq: 9000 + i})
		}
	}()
	wg.Wait()

	if sink.writes != 64 {
		t.Errorf("writes = %d, want 64 whole frames", sink.writes)
	}
}

func TestSSEPushAfterCloseIsRefused(t *testing.T) {
	// Writing into a response the host has finished with is not a no-op — it is
	// a write to a hijacked or recycled connection.
	ch := NewSSEChannel(&countingWriter{})
	if err := ch.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := ch.Push(Frame{Seq: 1}); err == nil {
		t.Error("Push after Close succeeded; want a refusal")
	}
}

// ── H-35 · a socket that closes, deadlines, cancellation ───────────────────

// blockingConn never yields a byte until it is closed — a half-open connection
// exactly as the peer's vanished laptop presents it.
type blockingConn struct {
	release  chan struct{}
	closed   chan struct{}
	closeOne sync.Once
	deadline time.Time
	mu       sync.Mutex
	writes   int
}

func newBlockingConn() *blockingConn {
	return &blockingConn{release: make(chan struct{}), closed: make(chan struct{})}
}

func (c *blockingConn) Read(p []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, io.EOF
	case <-c.release:
		return 0, io.EOF
	}
}

func (c *blockingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
	return len(p), nil
}

func (c *blockingConn) Close() error {
	c.closeOne.Do(func() { close(c.closed) })
	return nil
}

func (c *blockingConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	return nil
}

func (c *blockingConn) lastDeadline() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline
}

func (c *blockingConn) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

func TestWSCloseClosesTheUnderlyingSocket(t *testing.T) {
	// Close used to flip a bool. The socket stayed open, the read loop stayed
	// parked in Read, and the goroutine and descriptor behind it were never
	// reclaimed — one leak per connection, on the path a careful host takes.
	conn := newBlockingConn()
	ch := NewWSChannel(conn)

	done := make(chan error, 1)
	go func() { done <- ch.Listen() }()

	// Give the loop a moment to park in Read, then close.
	time.Sleep(20 * time.Millisecond)
	if err := ch.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return after Close — the socket was not closed")
	}

	select {
	case <-conn.closed:
	default:
		t.Error("Close did not close the underlying stream")
	}
}

func TestWSListenContextCancellationEndsTheLoop(t *testing.T) {
	// A blocking Read on a socket cannot be interrupted by anything but a
	// close, so a loop that only checked ctx.Err() between frames would wait
	// forever for a frame the cancelled peer will never send.
	conn := newBlockingConn()
	ch := NewWSChannel(conn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ch.ListenContext(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Listen returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling the context did not end the read loop")
	}
}

func TestWSListenSetsAReadDeadlineBeforeEachFrame(t *testing.T) {
	// Without a deadline a half-open connection parks a goroutine and a file
	// descriptor forever, and nothing in the process ever notices.
	conn := newBlockingConn()
	ch := NewWSChannel(conn)
	ch.SetReadTimeout(500*time.Millisecond, 0) // no ping, so only the deadline is under test

	before := time.Now()
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(conn.release)
	}()
	if err := ch.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	deadline := conn.lastDeadline()
	if deadline.IsZero() {
		t.Fatal("no read deadline was set")
	}
	if !deadline.After(before) {
		t.Errorf("read deadline %v is not after the loop start %v", deadline, before)
	}
}

func TestWSDisabledReadTimeoutSetsNoDeadline(t *testing.T) {
	// A host keeping liveness elsewhere can turn the deadline off. It is a real
	// choice, and deliberately not the default.
	conn := newBlockingConn()
	ch := NewWSChannel(conn)
	ch.SetReadTimeout(0, 0)

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(conn.release)
	}()
	if err := ch.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if !conn.lastDeadline().IsZero() {
		t.Error("a disabled read timeout still set a deadline")
	}
}

func TestWSServerPingsKeepAnIdleConnectionWarm(t *testing.T) {
	// The deadline alone would kill a healthy idle connection. The server ping
	// is what makes the pair safe.
	conn := newBlockingConn()
	ch := NewWSChannel(conn)
	ch.SetReadTimeout(2*time.Second, 15*time.Millisecond)

	go func() {
		time.Sleep(120 * time.Millisecond)
		close(conn.release)
	}()
	if err := ch.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if got := conn.writeCount(); got < 2 {
		t.Errorf("server pings written = %d, want at least 2 over the idle window", got)
	}
}

// ── H-35 · the SSE companion POST body is bounded ──────────────────────────

func TestDecodeEventRequestRefusesAnOversizedBody(t *testing.T) {
	// r.Body is an unbounded stream from an unauthenticated peer; io.ReadAll on
	// it is a memory-exhaustion primitive reachable with nothing but the URL.
	huge := strings.NewReader(`{"nodeId":"inc","event":"click","payload":"` +
		strings.Repeat("x", 4096) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/dispatch", huge)
	rec := httptest.NewRecorder()

	_, err := DecodeEventRequestWithLimit(rec, req, 512)
	if err == nil {
		t.Fatal("an oversized body was accepted")
	}
	var maxErr *http.MaxBytesError
	if !errors.As(err, &maxErr) {
		t.Errorf("err = %v (%T), want a *http.MaxBytesError so the host can answer 413", err, err)
	}
}

func TestDecodeEventRequestDecodesAnOrdinaryBody(t *testing.T) {
	body := strings.NewReader(`{"connId":"c1","nodeId":"inc","event":"click","lastSeq":7}`)
	req := httptest.NewRequest(http.MethodPost, "/dispatch", body)
	rec := httptest.NewRecorder()

	ev, err := DecodeEventRequest(rec, req)
	if err != nil {
		t.Fatalf("DecodeEventRequest: %v", err)
	}
	if ev.NodeID != "inc" || ev.Event != "click" || ev.LastSeq != 7 {
		t.Errorf("decoded event = %+v, want the inc click at lastSeq 7", ev)
	}
}

func TestDecodeEventRequestNonPositiveLimitFallsBackToTheDefault(t *testing.T) {
	// A zero from an uninitialised config must not read as "unbounded".
	body := strings.NewReader(`{"nodeId":"inc","event":"click"}`)
	req := httptest.NewRequest(http.MethodPost, "/dispatch", body)
	if _, err := DecodeEventRequestWithLimit(httptest.NewRecorder(), req, 0); err != nil {
		t.Fatalf("DecodeEventRequestWithLimit(0): %v", err)
	}
}

// ── L-D6 · the read loop reports what it drops ─────────────────────────────

// scriptedConn serves a fixed byte script then EOF.
type scriptedConn struct {
	in *strings.Reader
}

func (c *scriptedConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *scriptedConn) Write(p []byte) (int, error) { return len(p), nil }

func TestWSListenReportsAnUndecodableClientFrame(t *testing.T) {
	// An `err == nil` guard with no else branch made a client sending garbage
	// indistinguishable from a client sending nothing.
	frame := maskedFrame(opText, []byte("this is not JSON at all"), nil)
	conn := &scriptedConn{in: strings.NewReader(string(frame))}
	ch := NewWSChannel(conn)
	ch.SetReadTimeout(0, 0)

	var dropped []string
	ch.SetOnDrop(func(what string, err error) { dropped = append(dropped, what) })

	if err := ch.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "undecodable") {
		t.Errorf("drops reported = %v, want one undecodable-event report", dropped)
	}
}
