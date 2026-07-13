package serverdriven

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The end-to-end loop, proven in Go without a browser: the frame carries
// canonical TreeOps, so a conformant client re-renders by applying them with
// the same apply engine every host ships (this host's own ops package is that
// engine). A client holding its own copy of the tree, applying each pushed
// frame's ops, must converge to the exact server-held tree — dispatched event
// → server apply → frame → client apply → identical tree. That is what "drives
// a generic client end-to-end" means for a host that ships wire data, not DOM
// patches: the Go server authors no client code.
func TestEndToEndClientConvergesToServerTree(t *testing.T) {
	ch := &InMemoryChannel{}
	server := newCounterSession(t)
	conn := NewConnection("c1", server, ch)

	// The client starts from the same initial tree (first paint / the islands
	// hydrate payload).
	clientTree := mustDecodeNode(t, counterTreeJSON)

	for i := 0; i < 4; i++ {
		conn.Handle(clickInc("c1", i))
	}

	// The client applies every frame the server pushed, in order.
	for _, frame := range ch.Pushed() {
		for _, op := range frame.Ops {
			applied, err := ops.Apply(op, clientTree)
			if err != nil {
				t.Fatalf("client failed to apply a server frame op: %v", err)
			}
			clientTree = applied
		}
	}

	serverJSON, err := wire.EncodeNode(server.Tree())
	if err != nil {
		t.Fatalf("encode server tree: %v", err)
	}
	clientJSON, err := wire.EncodeNode(clientTree)
	if err != nil {
		t.Fatalf("encode client tree: %v", err)
	}
	if clientJSON != serverJSON {
		t.Errorf("client tree diverged from the server tree:\n client %s\n server %s", clientJSON, serverJSON)
	}
	// And the value actually advanced (the loop did real work).
	if got := metricValue(t, server); got != 4 {
		t.Errorf("server metric = %d after 4 clicks, want 4", got)
	}
}

// A client that reconnects mid-stream — applying the retained-buffer replay
// from its last-applied Seq — also converges, with no double-application of
// already-seen frames.
func TestEndToEndReconnectConverges(t *testing.T) {
	ch := &InMemoryChannel{}
	server := newCounterSession(t)
	conn := NewConnection("c1", server, ch)

	clientTree := mustDecodeNode(t, counterTreeJSON)
	clientLastSeq := 0
	applyFrom := func(frames []Frame) {
		for _, frame := range frames {
			if frame.Seq <= clientLastSeq {
				continue // idempotent: never re-apply a frame already seen
			}
			for _, op := range frame.Ops {
				applied, err := ops.Apply(op, clientTree)
				if err != nil {
					t.Fatalf("apply: %v", err)
				}
				clientTree = applied
			}
			clientLastSeq = frame.Seq
		}
	}

	// Two events land and the client applies them.
	conn.Handle(clickInc("c1", 0))
	conn.Handle(clickInc("c1", 1))
	applyFrom(ch.Pushed())

	// The client drops after seq 1. Two more events land while it is away.
	dropMark := len(ch.Pushed())
	conn.Handle(clickInc("c1", clientLastSeq))
	conn.Handle(clickInc("c1", clientLastSeq))

	// The client reconnects reporting its last-applied Seq; the server replays
	// the frames it missed, and the client applies the replayed tail.
	replayMark := len(ch.Pushed())
	if _, err := conn.Resync(clientLastSeq); err != nil {
		t.Fatalf("Resync: %v", err)
	}
	_ = dropMark
	applyFrom(ch.Pushed()[replayMark:])

	serverJSON, _ := wire.EncodeNode(server.Tree())
	clientJSON, _ := wire.EncodeNode(clientTree)
	if clientJSON != serverJSON {
		t.Errorf("client did not converge after reconnect:\n client %s\n server %s", clientJSON, serverJSON)
	}
	if metricValue(t, server) != 4 {
		t.Errorf("server metric = %d, want 4", metricValue(t, server))
	}
}
