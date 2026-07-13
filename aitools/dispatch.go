package aitools

import "github.com/fuaran-ui/fuaran-go/wire"

// The default-deny-by-shape dispatch gate (FGP 3). An agent driving the tree
// proposes Action effects; the host decides whether each may run. The gate
// consults an action's wire discriminator ($type) before any effectful action
// dispatches. It is default-deny: an effect shape the host has not explicitly
// permitted is refused, so a new or unexpected effect can never fire by
// omission. The gate is a policy only — it never executes an action.

var actionCaseSet = func() map[string]bool {
	m := make(map[string]bool)
	for _, c := range wire.ActionCases() {
		m[c] = true
	}
	return m
}()

// GatedEffectShapes are the outward/host effects that require explicit host
// permission (a host reasons about these when granting).
var GatedEffectShapes = map[string]bool{
	"Dispatch": true, "Navigate": true, "AiTool": true, "ReadFileBody": true,
	"Notify": true, "WriteToClipboard": true, "Invoke": true,
}

// InertShapes are structural, side-effect-free composition — safe to permit
// broadly. Chain is a sequence (its members are gated individually); SetState
// mutates only the local MVU state.
var InertShapes = map[string]bool{"Chain": true, "SetState": true}

// IsGatedEffect reports whether a shape is an outward/host effect that must be
// explicitly permitted.
func IsGatedEffect(shape string) bool { return GatedEffectShapes[shape] }

// DispatchDecision is the gate's verdict for one action shape.
type DispatchDecision struct {
	Shape   string
	Allowed bool
	Reason  string
}

// DispatchGate is a default-deny-by-shape policy gate. The zero value denies
// every action shape.
type DispatchGate struct {
	allowed map[string]bool
}

// DenyAll returns a gate that denies every shape.
func DenyAll() DispatchGate { return DispatchGate{allowed: map[string]bool{}} }

// Permitting returns a gate permitting exactly the named shapes.
func Permitting(shapes ...string) DispatchGate {
	allowed := make(map[string]bool, len(shapes))
	for _, s := range shapes {
		allowed[s] = true
	}
	return DispatchGate{allowed: allowed}
}

// PermissiveInert permits only the inert structural shapes (Chain / SetState);
// every outward effect stays denied.
func PermissiveInert() DispatchGate {
	allowed := make(map[string]bool, len(InertShapes))
	for s := range InertShapes {
		allowed[s] = true
	}
	return DispatchGate{allowed: allowed}
}

// WithPermitted returns a gate with the additional shapes permitted.
func (g DispatchGate) WithPermitted(shapes ...string) DispatchGate {
	allowed := make(map[string]bool, len(g.allowed)+len(shapes))
	for s := range g.allowed {
		allowed[s] = true
	}
	for _, s := range shapes {
		allowed[s] = true
	}
	return DispatchGate{allowed: allowed}
}

// AuthorizeShape is the verdict for a bare action-shape string (default-deny).
func (g DispatchGate) AuthorizeShape(shape string) DispatchDecision {
	if !actionCaseSet[shape] {
		return DispatchDecision{Shape: shape, Allowed: false, Reason: "unknown action shape '" + shape + "'"}
	}
	if g.allowed[shape] {
		return DispatchDecision{Shape: shape, Allowed: true, Reason: "explicitly permitted"}
	}
	return DispatchDecision{Shape: shape, Allowed: false, Reason: "default-deny by shape (not permitted)"}
}

// Authorize is the verdict for a decoded Action object (reads its $type tag).
func (g DispatchGate) Authorize(action wire.Obj) DispatchDecision {
	if action.Tag == "" {
		return DispatchDecision{Shape: "<malformed>", Allowed: false, Reason: "not a discriminated action object"}
	}
	return g.AuthorizeShape(action.Tag)
}
