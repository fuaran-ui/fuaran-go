// Package function is the fuaran-go host of the signature-searchable function
// registry (Phase 558): the Go reimplementation of the F# reference
// Fuaran.Core.FunctionRegistry.findBySignature (Phase 50/512) plus the
// deterministic compose-path resolution (the twin of the Python fuaran_ui
// function registry, Phase 523).
//
// Composition-by-lookup, not composition-by-generation: register functions by
// the node-kind they produce and the typed holes they require, then ask the
// registry "what can I run to produce X with the context I have?" — a total,
// in-memory structural search, no model call, no server — and compose a result
// by chaining matched functions rather than prompting. This is the Pattern
// Bank's deterministic no-model-call fast path.
//
// Reference semantics (canonical = F#):
//   - a query is (resultType, available) — the node-kind to produce (nil = any)
//     plus the context holes on offer; only a function's REQUIRED holes gate a
//     match; matching is by absolute address.
//   - Subsumes — result type matches (or wildcard) and every required hole is
//     satisfiable from context (available ⊆ required for value spaces, a
//     slot-kind match for slots).
//   - Exact — the required-hole address set equals the context set and each pair
//     is shape-equal (kind + space + slot).
//   - candidates return in deterministic lexicographic id order (no ranking).
//   - a compose that cannot reach the target returns a typed NoPath, never a
//     guess.
//
// Certified against the shared wire-format-fixtures/function-registry goldens —
// shape-identical resolution across the F#, py, ts, go, rs hosts. NOTE on the
// one host divergence: the F# reference spaceSubsumes treats an anyString
// required space as subsuming an enum available; the Python host does not. This
// host follows the F# reference (the canonical semantics); the shared goldens
// deliberately avoid that single edge so every host agrees on every fixture.
//
// The implementation is the Core twin in internal/core/function, kept behind an
// import boundary (see CONTRIBUTING.md, "The Core boundary"). This package forwards
// every exported identifier to it unchanged, and keeps the bounded text parse of
// DecodeDeclaration, which applies this host's wire limits.
package function

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/function"
)

// Match modes.
const (
	Subsumes = core.Subsumes
	Exact    = core.Exact
)

// Effect hosts, determinism tags, placements and island kinds.
const (
	HostPure       = core.HostPure
	HostReadsHost  = core.HostReadsHost
	HostWritesHost = core.HostWritesHost

	DeterminismDeterministic = core.DeterminismDeterministic
	DeterminismClock         = core.DeterminismClock
	DeterminismRandom        = core.DeterminismRandom
	DeterminismNetwork       = core.DeterminismNetwork

	PlacementServer            = core.PlacementServer
	PlacementBuildTime         = core.PlacementBuildTime
	PlacementPrecomputed       = core.PlacementPrecomputed
	PlacementClientDeclarative = core.PlacementClientDeclarative
	PlacementClientIsland      = core.PlacementClientIsland

	IslandPyodide = core.IslandPyodide
	IslandFable   = core.IslandFable
	IslandJS      = core.IslandJS
)

// Invocation error codes.
const (
	ErrNoSuchCapability    = core.ErrNoSuchCapability
	ErrDuplicateCapability = core.ErrDuplicateCapability
	ErrUnknownArg          = core.ErrUnknownArg
	ErrRequiredArgsUnbound = core.ErrRequiredArgsUnbound
	ErrArgOutOfSpace       = core.ErrArgOutOfSpace
	ErrUninvocableArg      = core.ErrUninvocableArg
	ErrBodyFailed          = core.ErrBodyFailed
)

// The registry, signature and capability types.
type (
	MatchMode          = core.MatchMode
	Space              = core.Space
	SigEntry           = core.SigEntry
	FunctionEntry      = core.FunctionEntry
	SignatureQuery     = core.SignatureQuery
	Registry           = core.Registry
	ComposeStep        = core.ComposeStep
	ComposeResult      = core.ComposeResult
	EffectClass        = core.EffectClass
	Placement          = core.Placement
	Signature          = core.Signature
	Capability         = core.Capability
	InvokeArg          = core.InvokeArg
	InvokeError        = core.InvokeError
	CapabilityRegistry = core.CapabilityRegistry
)

// NewRegistry returns an empty function registry.
func NewRegistry() *Registry { return core.NewRegistry() }

// NewCapability constructs a capability, deriving its determinism tag from the
// signature's effect.
func NewCapability(id string, sg Signature, placement Placement) Capability {
	return core.NewCapability(id, sg, placement)
}

// DeterminismTag is the capability's wire determinism tag.
func DeterminismTag(c Capability) string { return core.DeterminismTag(c) }

// FNV1a is the 32-bit FNV-1a hash of s, rendered as eight lower-case hex digits.
func FNV1a(s string) string { return core.FNV1a(s) }

// InvocationKey is the effect-identity key the capture seam journals an
// invocation under.
func InvocationKey(c Capability, args []InvokeArg) string { return core.InvocationKey(c, args) }

// SpaceValidate reports whether s lies in the value space (a nil space admits
// every value).
func SpaceValidate(space *Space, s string) bool { return core.SpaceValidate(space, s) }

// ValidateArgs checks an invocation's arguments against the capability's
// signature.
func ValidateArgs(c Capability, args []InvokeArg) *InvokeError { return core.ValidateArgs(c, args) }

// NewCapabilityRegistry returns an empty capability registry.
func NewCapabilityRegistry() *CapabilityRegistry { return core.NewCapabilityRegistry() }

// EncodeDeclaration renders a capability declaration in canonical form.
func EncodeDeclaration(c Capability) (string, error) { return core.EncodeDeclaration(c) }
