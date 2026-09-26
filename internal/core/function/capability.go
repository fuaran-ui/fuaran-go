// capability.go — the invocable-capability runtime of the fuaran-go host: the
// Go reimplementation of the reference `Capability` / `CapabilityRegistry`
// contract that sits alongside the signature-searchable registry in the same
// reference module (which is why it lives in this package rather than one of
// its own).
//
// What a capability is: a registrable, invocable unit declared as DATA. Its
// signature is the artifact-function projection (which holes, what value
// spaces, what effect class); its determinism is the capture-keying axis and is
// always derived from the signature's effect, never set independently; its
// placement routes the host body. The wire carries the DECLARATION and a typed
// invocation — never the body.
//
// Reference semantics (canonical = the F# reference host):
//   - default-deny by shape: an arg must address a declared hole that takes a
//     scalar value, and lie in that hole's space; every required hole must be
//     bound; a slot hole is not scalar-invocable. Every refusal is NAMED and
//     returned, never a panic.
//   - the replay key is `id + "#" + fnv1a(addr-sorted "addr=value" string)`, so
//     two invocations with the same args journal and replay under one key and
//     different args do not collide. The hash iterates UTF-16 code units, which
//     is what makes it value-identical to the reference on non-ASCII args.
//   - registry enumeration is id-sorted; stability is part of the contract
//     (it is the discovery surface an agent reads).
//
// Certified against the shared corpus's `laws/capability-laws.json` vectors —
// the (input, expected) pairs the reference's own conformance law family draws
// — by conformance/capability_laws_test.go.
package function

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// EffectClass is the total two-axis effect declaration: what host state a body
// touches, and what makes it non-reproducible. The wire spellings are the
// canonical ones (`pure` / `readsHost` / `writesHost`, and `deterministic` /
// `clock` / `random` / `network`).
type EffectClass struct {
	Host        string `json:"host"`
	Determinism string `json:"determinism"`
}

// The closed host-effect vocabulary.
const (
	HostPure       = "pure"
	HostReadsHost  = "readsHost"
	HostWritesHost = "writesHost"
)

// The closed determinism-source vocabulary. These strings ARE the determinism
// tags a capture-replay seam keys on — there is no second spelling, so no host
// can diverge on a mapping step that does not exist.
const (
	DeterminismDeterministic = "deterministic"
	DeterminismClock         = "clock"
	DeterminismRandom        = "random"
	DeterminismNetwork       = "network"
)

// The closed placement vocabulary — where a capability's body runs.
const (
	PlacementBuildTime         = "buildTime"
	PlacementServer            = "server"
	PlacementClientDeclarative = "clientDeclarative"
	PlacementClientIsland      = "clientIsland"
	PlacementPrecomputed       = "precomputed"
)

// The closed client-island vocabulary — which runtime a ClientIsland body runs in.
const (
	IslandPyodide = "pyodide"
	IslandFable   = "fable"
	IslandJS      = "js"
)

// Placement is where a capability's body runs. Island is set only when
// Kind == PlacementClientIsland and is empty otherwise.
type Placement struct {
	Kind   string `json:"kind"`
	Island string `json:"island"`
}

// Signature is a capability's derived signature: which holes it declares, and
// the effect class of running it. Twin of the reference Signature; the holes
// reuse this package's SigEntry, exactly as the reference shares one entry type
// between the capability surface and the function registry.
type Signature struct {
	Name   string      `json:"name"`
	Holes  []SigEntry  `json:"holes"`
	Effect EffectClass `json:"effect"`
}

// Capability is a registrable, invocable runtime capability. Build one with
// NewCapability so Determinism cannot disagree with the signature's effect.
type Capability struct {
	ID          string    `json:"id"`
	Signature   Signature `json:"signature"`
	Determinism string    `json:"determinism"`
	Placement   Placement `json:"placement"`
}

// InvokeArg is one bound scalar argument of a typed invocation — matched by
// absolute address (hygiene), never by bare name.
type InvokeArg struct {
	Addr  string `json:"addr"`
	Value string `json:"value"`
}

// The named refusal classes. A closed set: every refusal this surface can
// produce names one of these, and the caller can branch on it without parsing
// a message.
const (
	ErrNoSuchCapability    = "noSuchCapability"
	ErrDuplicateCapability = "duplicateCapability"
	ErrUnknownArg          = "unknownArg"
	ErrArgOutOfSpace       = "argOutOfSpace"
	ErrRequiredArgsUnbound = "requiredArgsUnbound"
	ErrUninvocableArg      = "uninvocableArg"
	ErrBodyFailed          = "bodyFailed"
)

// InvokeError names why a typed invocation (or a registration) was refused and,
// where a closed set was expected, enumerates the alternatives. Twin of the
// reference InvokeError.
type InvokeError struct {
	Kind     string   // one of the Err* constants above
	ID       string   // noSuchCapability / duplicateCapability
	Addr     string   // unknownArg / argOutOfSpace / uninvocableArg
	Addrs    []string // requiredArgsUnbound
	Declared []string // unknownArg — the addresses that WOULD have been accepted
	Known    []string // noSuchCapability — the registered ids
	Got      string   // argOutOfSpace — the offending value
	Reason   string   // bodyFailed
}

func (e *InvokeError) Error() string {
	switch e.Kind {
	case ErrNoSuchCapability:
		return fmt.Sprintf("noSuchCapability(%s; known: [%s])", e.ID, strings.Join(e.Known, ", "))
	case ErrDuplicateCapability:
		return fmt.Sprintf("duplicateCapability(%s)", e.ID)
	case ErrUnknownArg:
		return fmt.Sprintf("unknownArg(%s; declared: [%s])", e.Addr, strings.Join(e.Declared, ", "))
	case ErrArgOutOfSpace:
		return fmt.Sprintf("argOutOfSpace(%s=%s)", e.Addr, e.Got)
	case ErrRequiredArgsUnbound:
		return fmt.Sprintf("requiredArgsUnbound([%s])", strings.Join(e.Addrs, ", "))
	case ErrUninvocableArg:
		return fmt.Sprintf("uninvocableArg(%s)", e.Addr)
	case ErrBodyFailed:
		return "bodyFailed(" + e.Reason + ")"
	default:
		return "invokeError(" + e.Kind + ")"
	}
}

// NewCapability builds a capability, deriving Determinism from the signature's
// effect class. The two are never allowed to disagree, which is why this is the
// constructor rather than a struct literal.
func NewCapability(id string, sg Signature, placement Placement) Capability {
	return Capability{
		ID:          id,
		Signature:   sg,
		Determinism: sg.Effect.Determinism,
		Placement:   placement,
	}
}

// DeterminismTag is the label a capability keys its captures on. It is the
// determinism source itself — the vocabulary above IS the tag set, so there is
// no mapping step in which a host could diverge.
func DeterminismTag(c Capability) string { return c.Determinism }

// FNV1a is a 32-bit non-cryptographic content fingerprint (FNV-1a, lower-case
// eight-digit hex).
//
// It iterates UTF-16 CODE UNITS, not Go runes and not UTF-8 bytes: the
// reference host's string is a UTF-16 sequence and hashes one code unit per
// step, so a Go port that folded runes would agree on ASCII and silently
// diverge on everything else — precisely the class of drift the shared vectors
// exist to catch. Go's uint32 multiply wraps at 2^32, which is the arithmetic
// the reference pins.
//
// Non-cryptographic by design: a second pre-image is seconds of search, so this
// belongs on a cache key or a replay key, never under a signature.
func FNV1a(s string) string {
	h := uint32(2166136261)
	for _, unit := range utf16.Encode([]rune(s)) {
		h ^= uint32(unit)
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

// InvocationKey is the effect-identity key the capture seam journals a
// non-deterministic invocation under: the capability id plus a hash of the
// canonical (addr-sorted) `addr=value` argument string, joined with no
// separator — the reference's exact pre-image.
func InvocationKey(c Capability, args []InvokeArg) string {
	sorted := make([]InvokeArg, len(args))
	copy(sorted, args)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Addr < sorted[j].Addr })
	var sb strings.Builder
	for _, a := range sorted {
		sb.WriteString(a.Addr)
		sb.WriteByte('=')
		sb.WriteString(a.Value)
	}
	return c.ID + "#" + FNV1a(sb.String())
}

// SpaceValidate reports whether a candidate string value lies in a value space.
// A nil space is not scalar-valued and never validates — callers distinguish
// that case (a slot hole) before asking.
//
// The integer leg parses as a 32-BIT signed integer, matching the reference's
// own parse: a value outside that range is out of the space whatever the
// declared bounds say. The string-length leg measures UTF-16 code units, for
// the same reason FNV1a folds them.
func SpaceValidate(space *Space, s string) bool {
	if space == nil {
		return false
	}
	switch space.Kind {
	case "intRange":
		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
		if err != nil {
			return false
		}
		return float64(v) >= space.Min && float64(v) <= space.Max
	case "floatRange":
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return false
		}
		return v >= space.Min && v <= space.Max
	case "stringLen":
		n := float64(len(utf16.Encode([]rune(s))))
		return n >= space.Min && n <= space.Max
	case "enum":
		for _, c := range space.Choices {
			if c == s {
				return true
			}
		}
		return false
	case "anyString":
		return true
	default:
		return false
	}
}

// ValidateArgs checks typed args against a capability's signature BEFORE any
// dispatch: every arg must address a declared hole that takes a scalar value
// in-space, and every required hole must be bound. Returns nil on acceptance
// and a named refusal otherwise — never a panic.
//
// The two checks run in the reference's order (args first, then unbound
// required holes), because the vectors pin WHICH refusal an input that trips
// both would produce.
func ValidateArgs(c Capability, args []InvokeArg) *InvokeError {
	holes := c.Signature.Holes
	declared := make([]string, len(holes))
	for i, h := range holes {
		declared[i] = h.Addr
	}

	for _, a := range args {
		var hole *SigEntry
		for i := range holes {
			if holes[i].Addr == a.Addr {
				hole = &holes[i]
				break
			}
		}
		if hole == nil {
			return &InvokeError{Kind: ErrUnknownArg, Addr: a.Addr, Declared: declared}
		}
		if hole.Space == nil {
			// A slot hole: tree-typed, so no scalar value-space to lie in.
			return &InvokeError{Kind: ErrUninvocableArg, Addr: a.Addr}
		}
		if !SpaceValidate(hole.Space, a.Value) {
			return &InvokeError{Kind: ErrArgOutOfSpace, Addr: a.Addr, Got: a.Value}
		}
	}

	bound := make(map[string]bool, len(args))
	for _, a := range args {
		bound[a.Addr] = true
	}
	var unbound []string
	for _, h := range holes {
		if h.Required && !bound[h.Addr] {
			unbound = append(unbound, h.Addr)
		}
	}
	if len(unbound) > 0 {
		return &InvokeError{Kind: ErrRequiredArgsUnbound, Addrs: unbound}
	}
	return nil
}

// CapabilityRegistry is the typed discovery surface an agent enumerates: "what
// compute may I invoke, with what typed args". Default-deny by shape on
// dispatch — only a registered id resolves. Distinct from this package's
// Registry, which indexes artifact FUNCTIONS by signature; the reference keeps
// the same two registries apart for the same reason.
type CapabilityRegistry struct {
	capabilities map[string]Capability
}

// NewCapabilityRegistry returns an empty registry.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{capabilities: map[string]Capability{}}
}

// Register adds a capability — additive, with no silent overwrite: a duplicate
// id is a named refusal.
func (r *CapabilityRegistry) Register(c Capability) *InvokeError {
	if _, exists := r.capabilities[c.ID]; exists {
		return &InvokeError{Kind: ErrDuplicateCapability, ID: c.ID}
	}
	r.capabilities[c.ID] = c
	return nil
}

// TryFind resolves a registered capability by id.
func (r *CapabilityRegistry) TryFind(id string) (Capability, bool) {
	c, ok := r.capabilities[id]
	return c, ok
}

// Enumerate lists the registry in id order. The ORDER is contractual, not
// incidental: it is what makes a discovery listing reproducible across hosts
// and across insertion orders.
func (r *CapabilityRegistry) Enumerate() []Capability {
	out := make([]Capability, 0, len(r.capabilities))
	for _, c := range r.capabilities {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Validate resolves a typed invocation against the registry and validates its
// args (default-deny by shape): an unregistered id is a named refusal that
// enumerates what IS registered, so a caller learns the surface from the
// refusal rather than having to ask twice.
func (r *CapabilityRegistry) Validate(capabilityID string, args []InvokeArg) (Capability, *InvokeError) {
	c, ok := r.TryFind(capabilityID)
	if !ok {
		known := make([]string, 0, len(r.capabilities))
		for _, e := range r.Enumerate() {
			known = append(known, e.ID)
		}
		return Capability{}, &InvokeError{Kind: ErrNoSuchCapability, ID: capabilityID, Known: known}
	}
	if e := ValidateArgs(c, args); e != nil {
		return Capability{}, e
	}
	return c, nil
}
