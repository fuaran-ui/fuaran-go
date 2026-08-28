package renderer

import (
	"sort"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The `Binding.State` SEEDING pass — `WIRE_FORMAT.md` §24.4.
//
// §24.1 says what a declared default resolves to FOR THE READER THAT CARRIES
// IT. §24.4 says what it means for every OTHER reader of the same slot: a
// `Binding.State` carrying a `defaultValue` DECLARES the value of its slot, so
// a grid bound to `$state.members` and carrying the rows, beside a badge whose
// `Transform` derives over the same key and carries nothing, read the same
// rows.
//
// It is a RENDER-parity obligation, not a codec one (§24.6): the bytes
// round-trip identically with or without the rule, which is exactly why no
// codec family catches a host that has not adopted it. `render(tree, data) →
// bytes` stays a pure function — a seed is authored data that travels in the
// document, so resolving it costs this host no session state and does not move
// the library-not-a-runtime line, on the same reasoning that admitted the
// declared-default resolution in `resolveBinding`.
//
// The five rules, ported from the SPECIFICATION rather than from either
// reference implementation, and each answering a question two readers of one
// key raise that one does not:
//
//  1. WHO DECLARES — any `Binding.State` with a present `defaultValue`, in any
//     slot. There is no separate declaration form and no new namespace.
//  2. PRECEDENCE — host value > written value > seed. A seed is the value of a
//     slot before anything else has said anything, never an override; this host
//     holds no written values, so it lays the seeds UNDER the caller's own
//     `BindingSources` and the caller wins every key it names.
//  3. ORDER-INDEPENDENCE — seeding happens over the WHOLE tree before any
//     binding resolves, so a badge that appears before the grid declaring the
//     rows is not a special case and document order carries no meaning.
//  4. TWO DECLARATIONS OF ONE KEY — a disagreement is `FUARAN106` (a validator
//     concern, not this one), but a renderer must still be deterministic and
//     takes the FIRST declaration in tree order. An EMPTY declaration declares
//     nothing: it is the value an unseeded slot already has.
//  5. A HOST-RESERVED KEY IS NEVER SEEDED — a seed is a tree-originated write,
//     and §12's reserved `host.` namespace refuses those on every path.
//
// THE WALK IS STRUCTURAL, over the decoded value graph, rather than a typed
// per-slot walk. A structural descent finds a `State` binding in any slot,
// including one a later `Spec` case adds, so it carries no forward-coupling
// duty a new binding-bearing field could silently break.

// HostReservedStatePrefix names the HOST-OWNED state namespace (§12): a
// tree-originated write naming one of these keys is refused, so a
// tree-originated SEED naming one must be too.
const HostReservedStatePrefix = "host."

// CollectStateSeeds returns the value each `$state.<key>` slot carries before
// anything else has said anything — rule 1 filtered by rules 4 and 5.
//
// Nil when the tree declares nothing, so an unseeded tree costs one walk and no
// allocation.
func CollectStateSeeds(node wire.Node) map[string]wire.Value {
	var seeds map[string]wire.Value
	seedWalkValue(node, &seeds)
	return seeds
}

// WithStateSeeds lays a tree's seeds UNDER a caller's own binding sources
// (rule 2: the caller wins every key it names). The caller's map is never
// mutated — a host may reuse one across renders — and is returned unchanged
// when the tree declares nothing.
func WithStateSeeds(node wire.Node, sources BindingSources) BindingSources {
	seeds := CollectStateSeeds(node)
	if len(seeds) == 0 {
		return sources
	}
	merged := make(BindingSources, len(seeds)+len(sources))
	for k, v := range seeds {
		merged[k] = v
	}
	for k, v := range sources {
		merged[k] = v
	}
	return merged
}

// seedWalkValue descends one decoded value, recording the first declaration of
// each key.
//
// Object members are visited in SORTED KEY ORDER, which is the canonical
// document's own member order (the encoder emits the same ordinal sort), so
// "first in tree order" is a property of the BYTES rather than of this host's
// map iteration — Go randomises the latter, and a rule that decided which of
// two conflicting declarations wins by hash seed would be deterministic in
// name only. Array order is the wire's own and is honoured as it stands.
func seedWalkValue(v wire.Value, seeds *map[string]wire.Value) {
	switch t := v.(type) {
	case wire.Node:
		seedWalkValue(t.Kind, seeds)
		seedWalkFields(t.Extras, seeds)
	case wire.Arr:
		for _, item := range t {
			seedWalkValue(item, seeds)
		}
	case wire.Obj:
		if t.Tag == "State" {
			recordSeed(t, seeds)
		}
		// Keep descending whatever the tag: a `Local` re-sync source, an `I18n`
		// argument, or a `Transform` param's `from` can nest another binding
		// underneath this one.
		seedWalkFields(t.Fields, seeds)
	}
}

func seedWalkFields(fields map[string]wire.Value, seeds *map[string]wire.Value) {
	if len(fields) == 0 {
		return
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		seedWalkValue(fields[name], seeds)
	}
}

// recordSeed applies rules 1, 4 and 5 to one `Binding.State` object.
func recordSeed(obj wire.Obj, seeds *map[string]wire.Value) {
	key, ok := obj.Fields["key"].(wire.Str)
	if !ok {
		return
	}
	declared, present := obj.Fields["defaultValue"]
	if !present {
		return // rule 1 — a reader that declares nothing seeds nothing.
	}
	if strings.HasPrefix(string(key), HostReservedStatePrefix) {
		return // rule 5.
	}
	if isEmptyStateDeclaration(declared) {
		return // rule 4 — an empty declaration declares nothing.
	}
	if *seeds == nil {
		*seeds = make(map[string]wire.Value, 4)
	}
	if _, taken := (*seeds)[string(key)]; taken {
		return // rule 4 — the FIRST declaration in tree order wins.
	}
	(*seeds)[string(key)] = declared
}

// isEmptyStateDeclaration reports the EMPTY table, which is what a seed must
// not be.
//
// `"defaultValue": []` is the identity of the seeding lattice, not a claim
// about content: an unseeded slot already resolves to the empty table, so an
// empty declaration adds nothing an absent one does not already say. Both
// consequences are load-bearing rather than tidy. It must not WIN the
// first-declaration race — `{"$type":"State","key":"members","defaultValue":[]}`
// is how a `Transform` source slot says "I read this key and carry no data of
// my own", so a badge spelling it before the grid that carries the rows would
// otherwise seed the slot EMPTY and make §24.4's rule 3 false. And it must not
// CONFLICT, or that same pair would raise `FUARAN106` against the grid beside
// it — an Error on the very document the seeding rule exists to make work.
func isEmptyStateDeclaration(v wire.Value) bool {
	switch t := v.(type) {
	case wire.Arr:
		return len(t) == 0
	case wire.Obj:
		// The canonical columnar spelling of the same nothing.
		if cols, ok := t.Fields["columns"].(wire.Obj); ok {
			return len(cols.Fields) == 0
		}
	}
	return false
}
