// Package serverdriven is the second Go-friendly interactivity tier — the
// server-driven driver (the Phoenix-LiveView model). The Go server holds the
// tree + state, applies TreeOps in response to client events, and streams
// frame diffs — canonical TreeOp lists — over a transport-neutral channel to a
// thin generic browser client; interactions round-trip to Go. The server side
// is render-runtime-free.
//
// The transport is behind the Channel seam: the driver never sees a transport
// type, so a WebSocket backend is a drop-in swap for the SSE one (the "channel
// is a seam" posture applied to the live connection). Every frame carries a
// per-connection Seq — the reconnect-replay key: a bounded per-connection
// buffer re-pushes frames newer than the client's last-applied Seq across a
// transport drop (Connection.Resync).
package serverdriven

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Frame is one server→client push: the canonical TreeOps produced by one
// driver step, tagged with the per-connection op sequence (the reconnect
// key). The Go host ships the TreeOps themselves — a conformant client applies
// them — rather than the F# host's lowered DOM patches; this keeps the
// headless host render-runtime-free.
type Frame struct {
	Seq int
	Ops []wire.Obj
}

// EncodeFrameJSON renders a frame as canonical JSON: {"ops":[<TreeOp>,…],
// "seq":N}. The op list reuses the canonical op encoder (sorted keys,
// canonical numbers), and "ops" sorts before "seq" under the Ordinal key
// order, so the body is itself canonical.
func EncodeFrameJSON(f Frame) (string, error) {
	var sb strings.Builder
	sb.WriteString(`{"ops":[`)
	for i, op := range f.Ops {
		if i > 0 {
			sb.WriteByte(',')
		}
		s, err := wire.EncodeOp(op)
		if err != nil {
			return "", err
		}
		sb.WriteString(s)
	}
	sb.WriteString(`],"seq":`)
	sb.WriteString(strconv.Itoa(f.Seq))
	sb.WriteByte('}')
	return sb.String(), nil
}

// EncodeSSE renders a frame as one Server-Sent-Events wire frame: an id: line
// (the op sequence — the reconnect Last-Event-ID key), the patch event type,
// the single-line JSON data: line (the canonical body has no embedded
// newlines), and the blank line that terminates the event.
func EncodeSSE(f Frame) (string, error) {
	body, err := EncodeFrameJSON(f)
	if err != nil {
		return "", err
	}
	return "id: " + strconv.Itoa(f.Seq) + "\nevent: patch\ndata: " + body + "\n\n", nil
}

// Event is one client→server interaction: the raw (nodeId, event, payload)
// the client sends — the driver does NOT trust it (see Session.Step's G1
// validation). LastSeq is the client's last-applied frame sequence, threaded
// on every event so a reconnecting transport can drive Resync.
type Event struct {
	ConnID  string
	NodeID  string
	Event   string
	Payload string
	LastSeq int
}

// wireEvent is the JSON shape the generic client sends (client→server is not
// canonical wire — it is a small control message, so encoding/json is apt).
type wireEvent struct {
	ConnID  string `json:"connId"`
	NodeID  string `json:"nodeId"`
	Event   string `json:"event"`
	Payload string `json:"payload"`
	LastSeq int    `json:"lastSeq"`
}

// DecodeEvent parses an inbound client event message.
func DecodeEvent(raw []byte) (Event, error) {
	var we wireEvent
	if err := json.Unmarshal(raw, &we); err != nil {
		return Event{}, err
	}
	return Event{
		ConnID:  we.ConnID,
		NodeID:  we.NodeID,
		Event:   we.Event,
		Payload: we.Payload,
		LastSeq: we.LastSeq,
	}, nil
}
