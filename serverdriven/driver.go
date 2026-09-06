package serverdriven

import (
	"strings"

	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The server-side driver: it holds the current tree, validates each inbound
// event default-deny by shape (the G1 trust boundary), lets the host handler
// decide the TreeOps, applies them via the Phase-415 apply engine to keep the
// server tree authoritative, and returns the applied ops as the frame content.
// The state beyond the tree lives in the host handler's closure — the natural
// Go shape (the driver owns the tree; the host owns any model).

// RejectReason classifies a refused event (parity with the sibling driver's
// reject vocabulary). A reject mutates no state and pushes no frame.
type RejectReason string

const (
	// ReasonUnknownNode — the event targets a node absent from the current
	// server tree (a stale or forged id).
	ReasonUnknownNode RejectReason = "UnknownNode"
	// ReasonIllegitimateEvent — the event is not one the node's kind accepts
	// (a Button does not accept a value-change, etc.).
	ReasonIllegitimateEvent RejectReason = "IllegitimateEvent"
	// ReasonDispatchDenied — the host handler refused the event, or produced
	// TreeOps that do not apply to the current tree (default-deny by shape).
	ReasonDispatchDenied RejectReason = "DispatchDenied"
	// ReasonCapabilityDenied — a driven op was refused by the capability gate
	// (it introduced a Mount declaring capabilities the host has not granted).
	ReasonCapabilityDenied RejectReason = "CapabilityDenied"
	// ReasonHandlerPanicked — the host handler panicked. Recovered at this
	// boundary and reported as a refusal: the connection survives, the tree is
	// untouched, and the failure is named rather than taking the process.
	ReasonHandlerPanicked RejectReason = "HandlerPanicked"
)

// Reject is a structured refusal: a reason plus a human/AI-readable detail.
type Reject struct {
	Reason  RejectReason
	NodeID  string
	Message string
	// MissingCapabilities names the ungranted ids for a
	// ReasonCapabilityDenied reject; empty for every other reason.
	MissingCapabilities []string
}

// Handler is the host's per-event decision function: given the current tree
// and a structurally-validated event, it returns the TreeOps to apply (the
// frame), or an error to refuse the event (the dispatch gate — default-deny).
// The host holds its model in the handler's closure.
type Handler func(tree wire.Node, ev Event) ([]wire.Obj, error)

// legitimateEvents returns the event names a node's kind accepts (the G1
// event-legitimacy check). Mirrors the sibling driver's table; a kind not
// listed accepts nothing (default-deny — only interactive kinds take events).
func legitimateEvents(kind string) map[string]bool {
	switch kind {
	case "Button":
		return map[string]bool{"click": true}
	case "Select":
		return map[string]bool{"change": true}
	case "Form":
		return map[string]bool{"submit": true, "change": true, "input": true}
	case "Filters":
		return map[string]bool{"change": true, "input": true, "click": true}
	case "FileUpload":
		return map[string]bool{"change": true, "file-read": true}
	case "Tabs":
		return map[string]bool{"click": true, "change": true}
	case "Stepper":
		return map[string]bool{"click": true, "change": true}
	case "Disclosure":
		return map[string]bool{"click": true, "change": true, "toggle": true}
	default:
		return nil
	}
}

// Session is the server-held tree plus the host event handler. It is not
// safe for concurrent Step calls — a Connection owns one Session and
// serialises events (a live connection is one evolving model).
type Session struct {
	tree    wire.Node
	handler Handler
	gate    *CapabilityGate
	onPanic func(nodeID string, recovered any)
}

// NewSession builds a session over an initial tree and the host's event
// handler. No capability gate is applied until WithCapabilityGate is called —
// see the gate.go header for why this host opts in rather than denying by
// default.
func NewSession(tree wire.Node, handler Handler) *Session {
	return &Session{tree: tree, handler: handler}
}

// WithCapabilityGate turns on the authority gate: every op the handler produces
// is vetted before it can touch the tree, and one introducing a Mount whose
// declared capabilities are not granted is refused as CapabilityDenied.
// Builder-style — call it at construction.
func (s *Session) WithCapabilityGate(gate CapabilityGate) *Session {
	s.gate = &gate
	return s
}

// WithOnHandlerPanic registers a sink for a recovered handler panic. The reject
// already names it; this is for the host that wants the recovered value and a
// stack in its own logs, which the reject deliberately does not carry (a panic
// message can hold anything, and it goes to a client).
func (s *Session) WithOnHandlerPanic(sink func(nodeID string, recovered any)) *Session {
	s.onPanic = sink
	return s
}

// Tree is the current server-held tree.
func (s *Session) Tree() wire.Node { return s.tree }

// Step drives one inbound event: validate structurally (G1), run the host
// handler for the ops, apply each op to advance the server tree, and return
// the applied ops (the frame content). A refused event returns a non-nil
// *Reject and leaves the tree untouched. An empty op list is a legitimate
// no-op (the caller pushes no frame).
//
// NEVER PANICS, and that is now true rather than merely written down. The
// handler is host code this package cannot vouch for, and a panic from it used
// to propagate — on the WebSocket transport, straight out of a goroutine
// net/http does not recover, ending the process. It is recovered here, at the
// boundary that knows which event caused it, and reported as a
// HandlerPanicked reject: the connection survives, the tree is untouched.
func (s *Session) Step(ev Event) ([]wire.Obj, *Reject) {
	node, found := findNode(s.tree, ev.NodeID)
	if !found {
		return nil, &Reject{Reason: ReasonUnknownNode, NodeID: ev.NodeID,
			Message: "unknown node '" + ev.NodeID + "' (stale or forged id)"}
	}
	if !legitimateEvents(node.Kind.Tag)[ev.Event] {
		return nil, &Reject{Reason: ReasonIllegitimateEvent, NodeID: ev.NodeID,
			Message: "event '" + ev.Event + "' is not legitimate for a " + node.Kind.Tag}
	}

	opsList, err, panicked := s.runHandler(ev)
	if panicked != nil {
		return nil, panicked
	}
	if err != nil {
		return nil, &Reject{Reason: ReasonDispatchDenied, NodeID: ev.NodeID,
			Message: "dispatch denied for node '" + ev.NodeID + "': " + err.Error()}
	}

	// Authority gate — every driven op is vetted before it can touch the tree.
	// A denied op names the ungranted capabilities, so the host learns which
	// grant is missing rather than only that something was refused.
	if s.gate != nil {
		for _, op := range opsList {
			if missing := s.gate.opMissingCapabilities(op); len(missing) > 0 {
				return nil, &Reject{
					Reason:              ReasonCapabilityDenied,
					NodeID:              ev.NodeID,
					Message:             "driven op refused by the capability gate; ungranted: " + strings.Join(missing, ", "),
					MissingCapabilities: missing,
				}
			}
		}
	}

	// Apply each op to advance the authoritative tree; an op the handler
	// produced that does not apply is a rejected step (never a panic, no
	// partial mutation — the tree only advances on a fully-applying set).
	next := s.tree
	for _, op := range opsList {
		applied, applyErr := ops.Apply(op, next)
		if applyErr != nil {
			return nil, &Reject{Reason: ReasonDispatchDenied, NodeID: ev.NodeID,
				Message: "handler produced an inapplicable op: " + applyErr.Error()}
		}
		next = applied
	}
	s.tree = next
	return opsList, nil
}

// findNode locates a node by id anywhere in the tree, walking the same
// sub-node positions the apply engine addresses (children, boundary/switch
// children, fragment bodies, state surfaces).
func findNode(node wire.Node, target string) (wire.Node, bool) {
	if node.ID == target {
		return node, true
	}
	for _, child := range subNodes(node) {
		if found, ok := findNode(child, target); ok {
			return found, true
		}
	}
	return wire.Node{}, false
}

// subNodes enumerates a node's immediate sub-node positions.
func subNodes(node wire.Node) []wire.Node {
	var out []wire.Node
	fields := node.Kind.Fields
	if arr, ok := fields["children"].(wire.Arr); ok {
		for _, item := range arr {
			if c, ok := asNode(item); ok {
				out = append(out, c)
			}
		}
	}
	for _, key := range []string{"child", "fallback", "default", "body"} {
		if c, ok := asNode(fields[key]); ok {
			out = append(out, c)
		}
	}
	if cases, ok := fields["cases"].(wire.Arr); ok {
		for _, item := range cases {
			if caseObj, ok := item.(wire.Obj); ok {
				if c, ok := asNode(caseObj.Fields["child"]); ok {
					out = append(out, c)
				}
			}
		}
	}
	if state, ok := node.Extras["state"].(wire.Obj); ok {
		for _, key := range []string{"onLoading", "onEmpty"} {
			if c, ok := asNode(state.Fields[key]); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

// asNode coerces a child value to a node envelope (typed-decoded children are
// wire.Node; structurally-decoded children are raw tagged Objs).
func asNode(value wire.Value) (wire.Node, bool) {
	switch t := value.(type) {
	case wire.Node:
		return t, true
	case wire.Obj:
		id, idOK := t.Fields["id"].(wire.Str)
		kind, kindOK := t.Fields["kind"].(wire.Obj)
		if !idOK || !kindOK {
			return wire.Node{}, false
		}
		extras := make(map[string]wire.Value)
		for _, key := range []string{"state", "style", "accessibility"} {
			if v, ok := t.Fields[key]; ok {
				extras[key] = v
			}
		}
		return wire.Node{ID: string(id), Kind: kind, Extras: extras}, true
	}
	return wire.Node{}, false
}

// runHandler calls the host handler under a recover boundary.
//
// The recovered value is NOT put in the reject message. A panic string can
// carry anything the handler happened to be holding — a query, a token, a file
// path — and a reject travels to the client. The host gets the value through
// WithOnHandlerPanic, where it belongs; the client gets the fact.
func (s *Session) runHandler(ev Event) (opsList []wire.Obj, err error, panicked *Reject) {
	defer func() {
		if r := recover(); r != nil {
			if s.onPanic != nil {
				s.onPanic(ev.NodeID, r)
			}
			opsList = nil
			err = nil
			panicked = &Reject{
				Reason:  ReasonHandlerPanicked,
				NodeID:  ev.NodeID,
				Message: "the host handler panicked while handling '" + ev.Event + "' on node '" + ev.NodeID + "'",
			}
		}
	}()
	opsList, err = s.handler(s.tree, ev)
	return opsList, err, nil
}
