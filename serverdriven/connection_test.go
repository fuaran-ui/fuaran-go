package serverdriven

import "testing"

func TestConnectionPushesFramesAndAdvancesSeq(t *testing.T) {
	ch := &InMemoryChannel{}
	conn := NewConnection("c1", newCounterSession(t), ch)

	conn.Handle(clickInc("c1", 0))
	conn.Handle(clickInc("c1", 1))

	if conn.Sequence() != 2 {
		t.Errorf("sequence = %d, want 2", conn.Sequence())
	}
	pushed := ch.Pushed()
	if len(pushed) != 2 {
		t.Fatalf("pushed %d frames, want 2", len(pushed))
	}
	if pushed[0].Seq != 1 || pushed[1].Seq != 2 {
		t.Errorf("frame seqs = %d,%d, want 1,2", pushed[0].Seq, pushed[1].Seq)
	}
}

func TestConnectionInboundViaChannelDeliversToHandle(t *testing.T) {
	ch := &InMemoryChannel{}
	NewConnection("c1", newCounterSession(t), ch)
	// A real transport delivers events through the channel's Receive handler.
	ch.Send(clickInc("c1", 0))
	if len(ch.Pushed()) != 1 {
		t.Errorf("channel-delivered event did not drive a frame: %d pushed", len(ch.Pushed()))
	}
	// An event for a different connId is ignored.
	ch.Send(clickInc("other", 1))
	if len(ch.Pushed()) != 1 {
		t.Errorf("event for a foreign connId was handled: %d pushed", len(ch.Pushed()))
	}
}

func TestConnectionRejectRecordedAndNoFrame(t *testing.T) {
	ch := &InMemoryChannel{}
	var rejects []Reject
	conn := NewConnection("c1", newCounterSession(t), ch,
		WithOnReject(func(r Reject) { rejects = append(rejects, r) }))

	conn.Handle(Event{ConnID: "c1", NodeID: "ghost", Event: "click"})
	if len(ch.Pushed()) != 0 {
		t.Error("a rejected event pushed a frame")
	}
	if len(rejects) != 1 || rejects[0].Reason != ReasonUnknownNode {
		t.Errorf("reject not recorded: %+v", rejects)
	}
	if conn.Sequence() != 0 {
		t.Errorf("sequence advanced on a reject: %d", conn.Sequence())
	}
}

func TestResyncRepushesFramesNewerThanLastSeq(t *testing.T) {
	ch := &InMemoryChannel{}
	conn := NewConnection("c1", newCounterSession(t), ch)
	for i := 0; i < 3; i++ {
		conn.Handle(clickInc("c1", i))
	}
	// Client last applied seq 1 → a reconnect replays frames 2 and 3.
	before := len(ch.Pushed())
	n, err := conn.Resync(1)
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if n != 2 {
		t.Errorf("replayed %d frames, want 2", n)
	}
	replayed := ch.Pushed()[before:]
	if len(replayed) != 2 || replayed[0].Seq != 2 || replayed[1].Seq != 3 {
		t.Errorf("replayed frames = %+v, want seq 2,3", replayed)
	}
	// A client already current (lastSeq == head) replays nothing.
	if n, _ := conn.Resync(3); n != 0 {
		t.Errorf("a current client replayed %d frames, want 0", n)
	}
}

func TestReplayBufferEvictsOldestAtCapacity(t *testing.T) {
	ch := &InMemoryChannel{}
	conn := NewConnection("c1", newCounterSession(t), ch, WithReplayBufferCapacity(2))
	for i := 0; i < 5; i++ {
		conn.Handle(clickInc("c1", i))
	}
	// Only the last 2 frames (seq 4,5) are retained; a reconnect from seq 0
	// gets the retained tail, not the whole history.
	before := len(ch.Pushed())
	n, _ := conn.Resync(0)
	if n != 2 {
		t.Fatalf("retained-tail replay = %d frames, want 2 (bounded buffer)", n)
	}
	replayed := ch.Pushed()[before:]
	if replayed[0].Seq != 4 || replayed[1].Seq != 5 {
		t.Errorf("retained frames = seq %d,%d, want 4,5", replayed[0].Seq, replayed[1].Seq)
	}
}
