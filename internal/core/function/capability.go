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
//   - default-deny by shape: an address is bound once (a repeat is refused
//     duplicateArg before any value is read); an arg must address a declared
//     hole that takes a scalar value, and lie in that hole's space; every
//     required hole must be bound; a slot hole ranges over the tree space its
//     slot constraint names (Phase 1873), and a hole with neither a space nor a
//     slot reading (an action hole) is not invocable. Every refusal is NAMED
//     and returned, never a panic.
//   - a value is read by the reference's invariant readers: an integer is
//     `-?digits`, a number `-?digits(.digits)?([eE][+-]?digits)?` — no white
//     space, no `+` — and each value in a space has ONE canonical spelling.
//   - the replay key is `id + "#" + fnv1a(CanonicalFields(addr, value, ...))`
//     over the addr-sorted args, each value keyed by its canonical spelling in
//     its hole's space (Phase 1860, the reference's fuaran-core#225 form): each
//     field escaped and terminated, so the pre-image is injective and two
//     distinct argument sets never share one. The hash iterates UTF-16 code
//     units, which is what makes it value-identical to the reference on
//     non-ASCII args.
//   - the determinism axis is a SET of factors (clock, random, network), its
//     wire label the canonical `+`-joined spelling of the set.
//   - registration runs the reference's admission gate (totality, then
//     well-formedness) and refuses by name; enumeration is id-sorted, and that
//     stability is part of the contract (it is the discovery surface an agent
//     reads).
//
// Certified against the shared corpus's `laws/capability-laws.json` vectors —
// the (input, expected) pairs the reference's own conformance law family draws
// — by conformance/capability_laws_test.go.
package function

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/fuaran-ui/fuaran-go/internal/core/canonical"
)

// MirroredCoreVersion is the Fuaran.Core version whose capability semantics
// this twin mirrors — declared once, here, beside the twin.
//
// The corpus's capability-law vectors (`laws/capability-laws.json`) are emitted
// by Core and stamped with the Core version that emitted them (`kitVersion`).
// This twin certifies against the vectors of the Core it mirrors; a copy
// stamped for any other Core is the cut-to-raise window, and the capability
// leg reports it by name rather than asserting it. Raised to 0.39.0 by Phase
// 2001: the determinism factor set (Core 0.33.0, fuaran-core Phase 319), the
// invariant readers and canonical argument spelling the replay key hashes
// (0.34.0, Phase 295), the admission gate, duplicateArg and the reader's
// `$type` check (0.35.1, Phase 307), and the entry read as its hole kind
// (0.37.0, Phase 409). The policy gate on the registry (0.35.1, Phase 318) is
// not mirrored: this host's registry carries no policy.
const MirroredCoreVersion = "0.39.0"

// EffectClass is the total two-axis effect declaration: what host state a body
// touches, and what makes it non-reproducible. The wire spellings are the
// canonical ones (`pure` / `readsHost` / `writesHost`; the determinism set's
// label, see DeterminismSet.Tag).
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

// The single-factor determinism labels, and the empty set's. A composed class
// is labelled by DeterminismSet.Tag (`clock+random`, …). These strings ARE the
// determinism tags a capture-replay seam keys on — there is no second spelling,
// so no host can diverge on a mapping step that does not exist.
const (
	DeterminismDeterministic = "deterministic"
	DeterminismClock         = "clock"
	DeterminismRandom        = "random"
	DeterminismNetwork       = "network"
)

// DeterminismSet is the determinism axis: the SET of non-deterministic sources
// a body reads, joined by union (Core 0.33.0, fuaran-core Phase 319). The empty
// set is deterministic. Build one by or-ing the factors.
type DeterminismSet uint8

// The three determinism factors, declared in the canonical order every label
// renders them in.
const (
	FactorClock DeterminismSet = 1 << iota
	FactorRandom
	FactorNetwork
)

// determinismFactors pairs each factor with its label, in canonical order.
var determinismFactors = []struct {
	factor DeterminismSet
	name   string
}{
	{FactorClock, DeterminismClock},
	{FactorRandom, DeterminismRandom},
	{FactorNetwork, DeterminismNetwork},
}

// Tag is the set's canonical wire label: `deterministic` for the empty set,
// otherwise its factors in the order clock, random, network joined by `+`
// (`clock`, `clock+random`, `clock+random+network`). Twin of the reference's
// `Effect.determinismTag`.
func (d DeterminismSet) Tag() string {
	var names []string
	for _, f := range determinismFactors {
		if d&f.factor != 0 {
			names = append(names, f.name)
		}
	}
	if len(names) == 0 {
		return DeterminismDeterministic
	}
	return strings.Join(names, "+")
}

// Union is the join of two determinism sets.
func (d DeterminismSet) Union(other DeterminismSet) DeterminismSet { return d | other }

// Covers reports whether d names every factor actual reads — the superset
// relation a declared determinism must stand in to an observed one.
func (d DeterminismSet) Covers(actual DeterminismSet) bool { return actual&^d == 0 }

// ParseDeterminismTag inverts Tag on exactly the canonical labels: a reordered,
// repeated, empty or unknown member is not a label, so a set has one wire
// spelling. Twin of the reference's `Effect.tryDeterminismOfTag`.
func ParseDeterminismTag(tag string) (DeterminismSet, bool) {
	if tag == DeterminismDeterministic {
		return 0, true
	}
	var set DeterminismSet
	for _, name := range strings.Split(tag, "+") {
		found := false
		for _, f := range determinismFactors {
			if f.name == name {
				set |= f.factor
				found = true
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	if set.Tag() != tag {
		return 0, false
	}
	return set, true
}

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
	ErrNonTotalCapability  = "nonTotalCapability"
	ErrIllFormedCapability = "illFormedCapability"
	ErrDuplicateArg        = "duplicateArg"
)

// The declaration faults the admission gate names (the reference's
// `DeclarationFault`, as far as a signature can show one).
const (
	FaultEmptySpace        = "emptySpace"
	FaultNonFiniteBound    = "nonFiniteBound"
	FaultDuplicateHoleAddr = "duplicateHoleAddr"
)

// InvokeError names why a typed invocation (or a registration) was refused and,
// where a closed set was expected, enumerates the alternatives. Twin of the
// reference InvokeError.
type InvokeError struct {
	Kind     string   // one of the Err* constants above
	ID       string   // noSuchCapability / duplicateCapability / nonTotalCapability / illFormedCapability
	Addr     string   // unknownArg / argOutOfSpace / uninvocableArg / duplicateArg; illFormedCapability — the faulted hole
	Addrs    []string // requiredArgsUnbound; nonTotalCapability — the non-total holes
	Declared []string // unknownArg — the addresses that WOULD have been accepted
	Known    []string // noSuchCapability — the registered ids
	Got      string   // argOutOfSpace — the offending value
	Reason   string   // bodyFailed
	Fault    string   // illFormedCapability — one of the Fault* constants above
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
	case ErrNonTotalCapability:
		return fmt.Sprintf("nonTotalCapability(%s; [%s])", e.ID, strings.Join(e.Addrs, ", "))
	case ErrIllFormedCapability:
		return fmt.Sprintf("illFormedCapability(%s; %s at %s)", e.ID, e.Fault, e.Addr)
	case ErrDuplicateArg:
		return fmt.Sprintf("duplicateArg(%s)", e.Addr)
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

// DeterminismTag is the label a capability keys its captures on: its
// determinism set's canonical label, which is what the field holds — a decoded
// capability's label has been checked canonical, so there is no mapping step in
// which a host could diverge.
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

// FoldSep terminates every field of a canonical pre-image: U+0001 (SOH). No
// canonical wire encoding emits it unescaped. Twin of the reference's
// `Hash.foldSep`.
const FoldSep = "\u0001"

// FieldEsc escapes a FoldSep or a FieldEsc a field carries: U+0010 (DLE). Twin
// of the reference's `Hash.fieldEsc`.
const FieldEsc = "\u0010"

// CanonicalField is ONE field of an injective canonical pre-image: every
// FieldEsc and every FoldSep the field carries is escaped by a preceding
// FieldEsc, then the field is terminated by FoldSep. The first UNESCAPED FoldSep
// is therefore always the end of the field, whatever the field contains.
// Escaping FieldEsc first keeps the escapes the second replacement inserts from
// being escaped again. Twin of the reference's `Hash.canonicalField`.
func CanonicalField(s string) string {
	s = strings.ReplaceAll(s, FieldEsc, FieldEsc+FieldEsc)
	s = strings.ReplaceAll(s, FoldSep, FieldEsc+FoldSep)
	return s + FoldSep
}

// CanonicalFields is the canonical pre-image of a field sequence: each field
// through CanonicalField, concatenated. Injective — two field lists with one
// pre-image are one list. Twin of the reference's `Hash.canonicalFields`.
func CanonicalFields(fields []string) string {
	var sb strings.Builder
	for _, f := range fields {
		sb.WriteString(CanonicalField(f))
	}
	return sb.String()
}

// lessUTF16 orders two strings by UTF-16 code unit, the reference's ordinal
// string comparison. Go's `<` compares UTF-8 bytes, which agrees everywhere
// except between a supplementary-plane character (a surrogate pair, D800-DBFF
// first) and a BMP character at or above U+E000 — where the two orders invert.
func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// InvocationKey is the effect-identity key the capture seam journals a
// non-deterministic invocation under: the capability id plus a hash of the
// canonical pre-image of the addr-sorted args — two fields per binding (addr,
// value) through CanonicalFields. Each value is keyed by its CANONICAL spelling
// in its hole's space where it lies there (`05` and `5` in an integer hole are
// one value and one key; Core 0.34.0, fuaran-core Phase 295), by its own
// spelling otherwise. Two invocations with the same args replay the
// same captured value, and the pre-image is injective, so distinct argument
// sets never share one whatever their values contain (Phase 1860; the
// reference's fuaran-core#225 form, which replaced the old separator-free
// `addr=value` join under which [a="1b=2"] and [a="1"; b="2"] collided). That
// two distinct pre-images hash apart is a property of FNV1a and is not claimed.
// The sort is stable and by UTF-16 code unit, as the reference's is.
func InvocationKey(c Capability, args []InvokeArg) string {
	sorted := make([]InvokeArg, len(args))
	copy(sorted, args)
	sort.SliceStable(sorted, func(i, j int) bool { return lessUTF16(sorted[i].Addr, sorted[j].Addr) })
	fields := make([]string, 0, 2*len(sorted))
	for _, a := range sorted {
		fields = append(fields, a.Addr, spelled(c, a))
	}
	return c.ID + "#" + FNV1a(CanonicalFields(fields))
}

// spelled is the spelling an argument is keyed by: its canonical spelling in
// its hole's space, or its own where it has none there.
func spelled(c Capability, a InvokeArg) string {
	for _, h := range c.Signature.Holes {
		if h.Addr == a.Addr {
			if v, ok := Canonical(invocationSpace(h), a.Value); ok {
				return v
			}
			break
		}
	}
	return a.Value
}

// SpaceValidate reports whether a candidate string value lies in a value space:
// exactly when it has a Canonical spelling there. A nil space is not
// scalar-valued and never validates — callers distinguish that case before
// asking (ValidateArgs supplies a slot hole's tree space, and refuses an action
// hole by name).
func SpaceValidate(space *Space, s string) bool {
	_, ok := Canonical(space, s)
	return ok
}

// Canonical is the ONE spelling a value in the space is handed on as, and ok
// false where the string is not in the space (Core 0.34.0, fuaran-core Phase
// 295). An integer is read by the reference's invariant reader — `-?digits`, no
// white space, no `+`, within 32 bits — and written in its shortest decimal
// form (`05` is `5`); a number is read as `-?digits(.digits)?([eE][+-]?digits)?`
// and written in the canonical float layout (`1.50` is `1.5`); every other
// space's value is its own spelling. The string-length leg measures UTF-16 code
// units, for the same reason FNV1a folds them. Twin of the reference's
// `Space.canonical`.
func Canonical(space *Space, s string) (string, bool) {
	if space == nil {
		return "", false
	}
	switch space.Kind {
	case "intRange":
		v, ok := readInt(s)
		if !ok || float64(v) < space.Min || float64(v) > space.Max {
			return "", false
		}
		return strconv.FormatInt(int64(v), 10), true
	case "floatRange":
		v, ok := readFloat(s)
		if !ok || v < space.Min || v > space.Max {
			return "", false
		}
		return canonical.FormatFiniteDouble(v), true
	case "stringLen":
		n := float64(len(utf16.Encode([]rune(s))))
		if n < space.Min || n > space.Max {
			return "", false
		}
		return s, true
	case "enum":
		for _, c := range space.Choices {
			if c == s {
				return s, true
			}
		}
		return "", false
	case "anyString":
		return s, true
	case "slotTree":
		kind, ok := slotKindOf(s)
		if !ok || (space.SlotKind != "" && kind != space.SlotKind) {
			return "", false
		}
		return s, true
	default:
		return "", false
	}
}

// readInt reads an integer argument as the reference's seam does: an optional
// `-` and decimal digits (leading zeros read), within 32 bits.
func readInt(s string) (int32, bool) {
	digits := strings.TrimPrefix(s, "-")
	if digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(v), true
}

// readFloat reads a number argument as the reference's seam does:
// `-?digits(.digits)?([eE][+-]?digits)?`, finite.
func readFloat(s string) (float64, bool) {
	if !floatShape.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if math.IsInf(v, 0) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
		return 0, false
	}
	return v, true
}

var floatShape = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// slotKindOf is the kind tag of a tree argument: ok when the string is a
// well-formed wire document whose top level is an object with a string
// "kind", and not ok for a scalar, a malformed document, or an object with no
// string "kind". It decodes nothing below the tag — decoding the document into
// a node is the host's business. Twin of the reference's `Space.slotKindOf`
// (fuaran-core#229).
func slotKindOf(s string) (string, bool) {
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return "", false
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return "", false
	}
	kind, ok := obj["kind"].(string)
	return kind, ok
}

// invocationSpace is the value space an argument for this hole must lie in.
//
// A slot hole ranges over the tree space constrained to its slot kind: it
// admits a well-formed wire document whose "kind" satisfies that constraint
// (any kind when the slot is unconstrained). From Core 0.37.0 (fuaran-core
// Phase 409) the slot entry IS that constraint — no declared space overrides
// it, and the decoder drops one written in the pre-0.37.0 form. The space is
// supplied here, at validation, so a declaration re-encodes to its canonical
// bytes. Every other hole answers its declared space; nil (an action hole) is
// not invocable.
func invocationSpace(h SigEntry) *Space {
	if h.Kind == "slot" {
		return &Space{Kind: "slotTree", SlotKind: h.Slot}
	}
	return h.Space
}

// ValidateArgs checks typed args against a capability's signature BEFORE any
// dispatch: every address is bound once, every arg must address a declared hole
// that takes a scalar value in-space, and every required hole must be bound.
// Returns nil on acceptance and a named refusal otherwise — never a panic.
//
// The checks run in the reference's order (a repeated address first, at its
// second occurrence and before any value is read — Core 0.35.1, fuaran-core
// Phase 307; then the args in order; then unbound required holes), because the
// vectors pin WHICH refusal an input that trips two would produce.
func ValidateArgs(c Capability, args []InvokeArg) *InvokeError {
	holes := c.Signature.Holes
	declared := make([]string, len(holes))
	for i, h := range holes {
		declared[i] = h.Addr
	}

	seen := make(map[string]bool, len(args))
	for _, a := range args {
		if seen[a.Addr] {
			return &InvokeError{Kind: ErrDuplicateArg, Addr: a.Addr}
		}
		seen[a.Addr] = true
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
		space := invocationSpace(*hole)
		if space == nil {
			// An action hole: no value space, so nothing an argument can lie in.
			return &InvokeError{Kind: ErrUninvocableArg, Addr: a.Addr}
		}
		if space.Kind == "slotTree" {
			// A tree space: an argument that is no tree at all (a scalar, a
			// malformed document) is uninvocable; a tree of the wrong kind is out
			// of the space. The reference's order (fuaran-core#229).
			if _, isTree := slotKindOf(a.Value); !isTree {
				return &InvokeError{Kind: ErrUninvocableArg, Addr: a.Addr}
			}
		}
		if !SpaceValidate(space, a.Value) {
			return &InvokeError{Kind: ErrArgOutOfSpace, Addr: a.Addr, Got: a.Value}
		}
	}

	var unbound []string
	for _, h := range holes {
		if h.Required && !seen[h.Addr] {
			unbound = append(unbound, h.Addr)
		}
	}
	if len(unbound) > 0 {
		return &InvokeError{Kind: ErrRequiredArgsUnbound, Addrs: unbound}
	}
	return nil
}

// maxRepeatCount is the largest count a repeat hole may range over — the cap a
// count space sits under. Twin of the reference's `Space.maxRepeatCount`.
const maxRepeatCount = 1000000

// isCount reports whether a space is a COUNT space a repeat may range over: an
// integer range with 0 <= min <= max <= maxRepeatCount. Twin of the
// reference's `Space.isCount`.
func isCount(s *Space) bool {
	return s != nil && s.Kind == "intRange" && 0 <= s.Min && s.Min <= s.Max && s.Max <= maxRepeatCount
}

// spaceFault names why a space admits no value (FaultEmptySpace) or carries a
// bound no JSON number spells (FaultNonFiniteBound), or is "" for a well-formed
// space. Twin of the reference's `Space.wellFormed`.
func spaceFault(s *Space) string {
	if s == nil {
		return ""
	}
	switch s.Kind {
	case "floatRange":
		if math.IsNaN(s.Min) || math.IsInf(s.Min, 0) || math.IsNaN(s.Max) || math.IsInf(s.Max, 0) {
			return FaultNonFiniteBound
		}
		if s.Min > s.Max {
			return FaultEmptySpace
		}
	case "intRange":
		if s.Min > s.Max {
			return FaultEmptySpace
		}
	case "stringLen":
		if s.Min > s.Max || s.Max < 0 {
			return FaultEmptySpace
		}
	case "enum":
		if len(s.Choices) == 0 {
			return FaultEmptySpace
		}
	}
	return ""
}

// Admission is THE gate a declaration passes to be registered or read (Core
// 0.35.1, fuaran-core Phase 307; the reference's `Capability.admissionOf`):
// totality first — every repeat hole ranges over a count space, refused
// nonTotalCapability naming each one that does not — then well-formedness, the
// first entry in declaration order whose address an earlier entry holds
// (duplicateHoleAddr) or whose space admits no value or has a non-finite bound
// (emptySpace / nonFiniteBound), refused illFormedCapability. Nil admits.
func Admission(id string, sg Signature) *InvokeError {
	var nonTotal []string
	for _, e := range sg.Holes {
		if e.Kind == "repeat" && !isCount(e.Space) {
			nonTotal = append(nonTotal, e.Addr)
		}
	}
	if len(nonTotal) > 0 {
		return &InvokeError{Kind: ErrNonTotalCapability, ID: id, Addrs: nonTotal}
	}
	seen := make(map[string]bool, len(sg.Holes))
	for _, e := range sg.Holes {
		if seen[e.Addr] {
			return &InvokeError{Kind: ErrIllFormedCapability, ID: id, Fault: FaultDuplicateHoleAddr, Addr: e.Addr}
		}
		if fault := spaceFault(invocationSpace(e)); fault != "" {
			return &InvokeError{Kind: ErrIllFormedCapability, ID: id, Fault: fault, Addr: e.Addr}
		}
		seen[e.Addr] = true
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
// id is a named refusal, and so is a declaration the admission gate refuses
// (Admission).
func (r *CapabilityRegistry) Register(c Capability) *InvokeError {
	if _, exists := r.capabilities[c.ID]; exists {
		return &InvokeError{Kind: ErrDuplicateCapability, ID: c.ID}
	}
	if e := Admission(c.ID, c.Signature); e != nil {
		return e
	}
	r.capabilities[c.ID] = c
	return nil
}

// TryFind resolves a registered capability by id.
func (r *CapabilityRegistry) TryFind(id string) (Capability, bool) {
	c, ok := r.capabilities[id]
	return c, ok
}

// Enumerate lists the registry in id order — ordinal, by UTF-16 code unit, as
// the reference's is. The ORDER is contractual, not
// incidental: it is what makes a discovery listing reproducible across hosts
// and across insertion orders.
func (r *CapabilityRegistry) Enumerate() []Capability {
	out := make([]Capability, 0, len(r.capabilities))
	for _, c := range r.capabilities {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return lessUTF16(out[i].ID, out[j].ID) })
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
