package serverdriven

import (
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
)

// Reject is a structured refusal: a reason plus a human/AI-readable detail.
type Reject struct {
	Reason  RejectReason
	NodeID  string
	Message string
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
}

// NewSession builds a session over an initial tree and the host's event
// handler.
func NewSession(tree wire.Node, handler Handler) *Session {
	return &Session{tree: tree, handler: handler}
}

// Tree is the current server-held tree.
func (s *Session) Tree() wire.Node { return s.tree }

// Step drives one inbound event: validate structurally (G1), run the host
// handler for the ops, apply each op to advance the server tree, and return
// the applied ops (the frame content). A refused event returns a non-nil
// *Reject and leaves the tree untouched. An empty op list is a legitimate
// no-op (the caller pushes no frame). Never panics.
func (s *Session) Step(ev Event) ([]wire.Obj, *Reject) {
	node, found := findNode(s.tree, ev.NodeID)
	if !found {
		return nil, &Reject{ReasonUnknownNode, ev.NodeID,
			"unknown node '" + ev.NodeID + "' (stale or forged id)"}
	}
	if !legitimateEvents(node.Kind.Tag)[ev.Event] {
		return nil, &Reject{ReasonIllegitimateEvent, ev.NodeID,
			"event '" + ev.Event + "' is not legitimate for a " + node.Kind.Tag}
	}

	opsList, err := s.handler(s.tree, ev)
	if err != nil {
		return nil, &Reject{ReasonDispatchDenied, ev.NodeID,
			"dispatch denied for node '" + ev.NodeID + "': " + err.Error()}
	}

	// Apply each op to advance the authoritative tree; an op the handler
	// produced that does not apply is a rejected step (never a panic, no
	// partial mutation — the tree only advances on a fully-applying set).
	next := s.tree
	for _, op := range opsList {
		applied, applyErr := ops.Apply(op, next)
		if applyErr != nil {
			return nil, &Reject{ReasonDispatchDenied, ev.NodeID,
				"handler produced an inapplicable op: " + applyErr.Error()}
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
