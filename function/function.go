// Package function is the fuaran-go host of the signature-searchable function
// registry (Phase 558): the Go reimplementation of the F# reference
// Fuaran.Core.FunctionRegistry.findBySignature (Phase 50/512) plus the
// deterministic compose-path resolution (the twin of the Python fuaran_py
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
package function

import (
	"fmt"
	"sort"
	"strings"
)

// MatchMode is how strictly an entry's signature must match a query.
type MatchMode string

const (
	// Subsumes is the "everything I can run with this context" query.
	Subsumes MatchMode = "Subsumes"
	// Exact is the "the function with precisely these holes" query.
	Exact MatchMode = "Exact"
)

// Space is a hole's value-space — the type domain of a value argument. The
// language-neutral wire form the goldens carry (intRange / floatRange /
// stringLen / enum / anyString); Min/Max are the range bounds, Choices the enum
// set. A nil *Space means the hole carries no value-space (a slot hole).
type Space struct {
	Kind    string   `json:"kind"`
	Min     float64  `json:"min"`
	Max     float64  `json:"max"`
	Choices []string `json:"choices"`
}

// SigEntry is one hole in a function signature — matched by absolute Addr
// (hygiene). A value/repeat hole carries a Space; a slot hole carries a Slot
// node-kind constraint ("" = unconstrained / not a slot). An ACTION hole
// carries neither: it declares only the effect CEILING of the handler a host
// will later bind into it (the handler is a closure, lives host-side, and never
// travels on the wire). Nil means the hole declares no ceiling, which is the
// case for every non-action hole. Twin of the reference SigEntry.
type SigEntry struct {
	Addr         string       `json:"addr"`
	Name         string       `json:"name"`
	Kind         string       `json:"kind"` // value | slot | repeat | action
	Space        *Space       `json:"space"`
	Slot         string       `json:"slot"`
	ActionEffect *EffectClass `json:"actionEffect"`
	Required     bool         `json:"required"`
}

// FunctionEntry is a registered function: an id, the node-kind it produces
// (ResultType), and its required-hole shape. Twin of F# FunctionEntry.
type FunctionEntry struct {
	ID         string     `json:"id"`
	ResultType string     `json:"resultType"`
	Holes      []SigEntry `json:"holes"`
}

// SignatureQuery is a signature search: the node-kind to produce (nil = any, a
// produce-axis wildcard) plus the context holes on offer. Twin of F#
// SignatureQuery.
type SignatureQuery struct {
	ResultType *string
	Available  []SigEntry
}

// Registry is a signature-typed function registry — the artifact-function
// catalogue, queried by signature. byResult is the result-type index maintained
// additively so a "produces a Box" query narrows before the hole-shape filter.
// Twin of F# FunctionRegistry.
type Registry struct {
	entries  map[string]FunctionEntry
	byResult map[string]map[string]struct{}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		entries:  map[string]FunctionEntry{},
		byResult: map[string]map[string]struct{}{},
	}
}

// Register adds an entry — additive, no silent overwrite (a duplicate id is a
// named error). Maintains both the id map and the result-type index. Twin of F#
// FunctionRegistry.register.
func (r *Registry) Register(e FunctionEntry) error {
	if _, dup := r.entries[e.ID]; dup {
		return fmt.Errorf("function %q is already registered", e.ID)
	}
	r.entries[e.ID] = e
	ids := r.byResult[e.ResultType]
	if ids == nil {
		ids = map[string]struct{}{}
		r.byResult[e.ResultType] = ids
	}
	ids[e.ID] = struct{}{}
	return nil
}

// Get returns the registered entry for an id, or ok=false.
func (r *Registry) Get(id string) (FunctionEntry, bool) {
	e, ok := r.entries[id]
	return e, ok
}

// ── value-space + slot subsumption (available ⊆ required) ────────────────────

// spaceSubsumes reports whether required subsumes available — is every value the
// context can supply acceptable to the function? (available ⊆ required.)
// Same-constructor ranges compare by bounds; an enum subsumes a subset enum; an
// anyString required space subsumes any string-valued space. Cross-type never
// subsumes. Twin of F# spaceSubsumes (the canonical reference).
func spaceSubsumes(required, available *Space) bool {
	if required == nil || available == nil {
		return false
	}
	switch required.Kind {
	case "intRange", "floatRange":
		return available.Kind == required.Kind && required.Min <= available.Min && available.Max <= required.Max
	case "stringLen":
		return available.Kind == "stringLen" && required.Min <= available.Min && available.Max <= required.Max
	case "enum":
		return available.Kind == "enum" && subset(available.Choices, required.Choices)
	case "anyString":
		return available.Kind == "stringLen" || available.Kind == "enum" || available.Kind == "anyString"
	default:
		return false
	}
}

func subset(sub, sup []string) bool {
	set := make(map[string]struct{}, len(sup))
	for _, v := range sup {
		set[v] = struct{}{}
	}
	for _, v := range sub {
		if _, ok := set[v]; !ok {
			return false
		}
	}
	return true
}

// slotSubsumes: an unconstrained required slot ("") accepts any; a constrained
// one needs the same kind. Twin of F# slotSubsumes.
func slotSubsumes(required, available string) bool {
	if required == "" {
		return true
	}
	return available != "" && required == available
}

func spaceEqual(a, b *Space) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case "intRange", "floatRange", "stringLen":
		return a.Min == b.Min && a.Max == b.Max
	case "enum":
		if len(a.Choices) != len(b.Choices) {
			return false
		}
		for i := range a.Choices {
			if a.Choices[i] != b.Choices[i] {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// holeSatisfied: is a single required hole satisfied by the matching
// available-context entry (same address)? Twin of F# holeSatisfied.
func holeSatisfied(req, av SigEntry) bool {
	if req.Kind != av.Kind {
		return false
	}
	if req.Kind == "slot" {
		return slotSubsumes(req.Slot, av.Slot)
	}
	return spaceSubsumes(req.Space, av.Space)
}

func requiredHoles(e FunctionEntry) []SigEntry {
	out := make([]SigEntry, 0, len(e.Holes))
	for _, h := range e.Holes {
		if h.Required {
			out = append(out, h)
		}
	}
	return out
}

func indexByAddr(entries []SigEntry) map[string]SigEntry {
	m := make(map[string]SigEntry, len(entries))
	for _, e := range entries {
		m[e.Addr] = e
	}
	return m
}

func matchesQuery(mode MatchMode, query SignatureQuery, entry FunctionEntry) bool {
	if query.ResultType != nil && *query.ResultType != entry.ResultType {
		return false
	}
	availByAddr := indexByAddr(query.Available)
	required := requiredHoles(entry)

	if mode == Subsumes {
		for _, req := range required {
			av, ok := availByAddr[req.Addr]
			if !ok || !holeSatisfied(req, av) {
				return false
			}
		}
		return true
	}

	// Exact — the required-hole address set equals the context set, shape-equal.
	if len(required) != len(query.Available) {
		return false
	}
	for _, req := range required {
		av, ok := availByAddr[req.Addr]
		if !ok || req.Kind != av.Kind || !spaceEqual(req.Space, av.Space) || req.Slot != av.Slot {
			return false
		}
	}
	return true
}

// FindBySignature returns every registered function whose signature matches the
// query under mode. A non-nil result type narrows via the byResult index first;
// a nil result type scans all entries. Survivors are returned id-stable
// (lexicographic). Twin of F# FunctionRegistry.findBySignature.
func (r *Registry) FindBySignature(mode MatchMode, query SignatureQuery) []FunctionEntry {
	var candidateIDs []string
	if query.ResultType != nil {
		for id := range r.byResult[*query.ResultType] {
			candidateIDs = append(candidateIDs, id)
		}
	} else {
		for id := range r.entries {
			candidateIDs = append(candidateIDs, id)
		}
	}
	sort.Strings(candidateIDs)

	out := make([]FunctionEntry, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		e := r.entries[id]
		if matchesQuery(mode, query, e) {
			out = append(out, e)
		}
	}
	return out
}

// ── deterministic composition (the Pattern-Bank fast path) ───────────────────

// ComposeStep is one function applied in a composition — its id + the slot it
// fills (nil at the root).
type ComposeStep struct {
	FunctionID string  `json:"functionId"`
	FillsSlot  *string `json:"fillsSlot"`
}

// ComposeResult is a deterministic composition reaching the target (OK true, the
// ordered Steps, root last) or a typed no-path (OK false, a Reason). Twin of the
// Python ComposePath / NoPath sum.
type ComposeResult struct {
	OK     bool          `json:"ok"`
	Steps  []ComposeStep `json:"steps,omitempty"`
	Reason string        `json:"reason,omitempty"`
}

// Compose chains functions to produce output from the inputs context
// deterministically, or returns a typed no-path. A direct signature match is a
// single step; an unfilled slot hole is recursively composed from the same
// context. No model call, no guess. Twin of the Python FunctionRegistry.compose.
func (r *Registry) Compose(output string, inputs []SigEntry, mode MatchMode, maxDepth int) ComposeResult {
	steps := r.composeSteps(output, inputs, mode, maxDepth, map[string]struct{}{})
	if steps == nil {
		return ComposeResult{
			OK:     false,
			Reason: fmt.Sprintf("no deterministic function chain reaches '%s' from the given context", output),
		}
	}
	return ComposeResult{OK: true, Steps: steps}
}

func (r *Registry) composeSteps(output string, available []SigEntry, mode MatchMode, depth int, seen map[string]struct{}) []ComposeStep {
	if depth <= 0 {
		return nil
	}
	if _, ok := seen[output]; ok {
		return nil
	}

	// Direct match: a function producing output whose every required hole is in context.
	direct := r.FindBySignature(mode, SignatureQuery{ResultType: &output, Available: available})
	if len(direct) > 0 {
		return []ComposeStep{{FunctionID: direct[0].ID, FillsSlot: nil}}
	}

	seenNext := make(map[string]struct{}, len(seen)+1)
	for k := range seen {
		seenNext[k] = struct{}{}
	}
	seenNext[output] = struct{}{}
	byAddr := indexByAddr(available)

	producers := make([]string, 0, len(r.byResult[output]))
	for id := range r.byResult[output] {
		producers = append(producers, id)
	}
	sort.Strings(producers)

	for _, id := range producers {
		entry := r.entries[id]
		var sub []ComposeStep
		satisfiable := true
		for _, hole := range requiredHoles(entry) {
			if av, ok := byAddr[hole.Addr]; ok && holeSatisfied(hole, av) {
				continue
			}
			if hole.Kind == "slot" && hole.Slot != "" {
				child := r.composeSteps(hole.Slot, available, mode, depth-1, seenNext)
				if child == nil {
					satisfiable = false
					break
				}
				addr := hole.Addr
				child[len(child)-1] = ComposeStep{FunctionID: child[len(child)-1].FunctionID, FillsSlot: &addr}
				sub = append(sub, child...)
			} else {
				satisfiable = false
				break
			}
		}
		if satisfiable {
			return append(sub, ComposeStep{FunctionID: id, FillsSlot: nil})
		}
	}
	return nil
}

// ── registry-shape attestation (548-style cross-host drift guard) ────────────

func spaceDesc(s *Space) string {
	if s == nil {
		return "-"
	}
	switch s.Kind {
	case "intRange", "stringLen":
		return fmt.Sprintf("%s(%d,%d)", s.Kind, int(s.Min), int(s.Max))
	case "floatRange":
		return fmt.Sprintf("floatRange(%v,%v)", s.Min, s.Max)
	case "enum":
		return "enum(" + strings.Join(s.Choices, "|") + ")"
	default:
		return "anyString"
	}
}

func holeDesc(h SigEntry) string {
	slot := h.Slot
	if slot == "" {
		slot = "-"
	}
	req := "opt"
	if h.Required {
		req = "req"
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s", h.Addr, h.Kind, spaceDesc(h.Space), slot, req)
}

func entryDesc(e FunctionEntry) string {
	holes := make([]string, len(e.Holes))
	for i, h := range e.Holes {
		holes[i] = holeDesc(h)
	}
	return fmt.Sprintf("%s|%s|%s", e.ID, e.ResultType, strings.Join(holes, ";"))
}

// RegistrySignatureShape returns the canonical per-entry shape descriptors of a
// registry, sorted — the 548-style attestation surface. A host whose registry
// model drops a hole field, reorders holes, or mistypes a space produces a
// divergent descriptor, so a cross-host shape drift fails the conformance gate
// with the entry named, rather than silently diverging.
func (r *Registry) RegistrySignatureShape() []string {
	out := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, entryDesc(e))
	}
	sort.Strings(out)
	return out
}
