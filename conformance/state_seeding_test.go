package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/renderer"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// `Binding.State` slot seeding — WIRE_FORMAT.md §24.4, and its §24.6
// conformance leg.
//
// §24.6 is a RENDER-parity obligation, not a codec one: the bytes round-trip
// identically with or without the rule, so every codec family here passes on a
// host that has not adopted it. That is exactly why this file asserts a DERIVED
// VALUE rather than bytes — it is the only leg that can tell an adopting host
// from a non-adopting one.
//
// Measured on this host before the pass landed: the badge below rendered EMPTY,
// because its own `Transform` source declares `"defaultValue": []` and nothing
// filled the slot the grid beside it carries the rows for. After: `2`, the same
// value the two reference tiers pin.

const seededPairFixture = "shared-source-seeded-pair"

// badgeText lifts the Info badge's rendered text out of a fragment. Narrow on
// purpose: it matches the emitted element and nothing else, so a renderer that
// stopped emitting the badge fails rather than matching some other span.
func badgeText(t *testing.T, html string) string {
	t.Helper()
	const open = `class="fuaran-badge fuaran-badge-info">`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("no Info badge in the rendered fragment: %s", html)
	}
	rest := html[i+len(open):]
	end := strings.Index(rest, "<")
	if end < 0 {
		t.Fatalf("unterminated badge element in the rendered fragment")
	}
	return rest[:end]
}

func seededPairTree(t *testing.T) wire.Node {
	t.Helper()
	corpus, _ := loadCorpus(t)
	raw, err := os.ReadFile(filepath.Join(corpus, "nodes", seededPairFixture+".json"))
	if err != nil {
		t.Skipf("corpus fixture %s not found: %v", seededPairFixture, err)
	}
	node, err := wire.DecodeNode(string(raw))
	if err != nil {
		t.Fatalf("decoding %s: %v", seededPairFixture, err)
	}
	return node
}

// TestSeededPairRendersTheDeclaredCount is the §24.6 render-parity assertion:
// one declared table under `$state.members`, read by a grid's `source` and by a
// badge's `Transform`, resolves the badge's derivation over the grid's two rows.
//
// The value is the assertion, not the markup — `2` is what the reference tiers
// render for this fixture, so a host that agrees on the bytes and disagrees here
// is exactly the divergence §24.4 was written to close.
func TestSeededPairRendersTheDeclaredCount(t *testing.T) {
	node := seededPairTree(t)

	if got := badgeText(t, renderer.RenderHTML(node, nil)); got != "2" {
		t.Fatalf("seeded derivation = %q, want %q (the grid declares two rows under $state.members)", got, "2")
	}

	// The islands surface must not differ: one document would otherwise render
	// two values depending only on whether a region was marked an island.
	islands, err := renderer.RenderWithIslands(node, nil, map[string]string{})
	if err != nil {
		t.Fatalf("islands render: %v", err)
	}
	if got := badgeText(t, islands); got != "2" {
		t.Fatalf("islands derivation = %q, want %q", got, "2")
	}
}

// TestSeededPairAssertionIsSensitiveToTheDerivedValue is the go-red half of the
// assertion above. An assertion nobody has watched fail is a claim about the
// author's confidence, not about the renderer — so the same badge is measured
// under a host value that makes the derivation say something ELSE, and it must
// move. Both perturbations are legitimate documents; neither changes a byte of
// the tree.
func TestSeededPairAssertionIsSensitiveToTheDerivedValue(t *testing.T) {
	node := seededPairTree(t)

	oneRow := wire.Arr{wire.Obj{Fields: map[string]wire.Value{"team": wire.Str("Solo")}}}
	if got := badgeText(t, renderer.RenderHTML(node, renderer.BindingSources{"members": oneRow})); got != "1" {
		t.Fatalf("a one-row host value should derive %q, got %q — the badge is not reading the slot at all", "1", got)
	}
	if got := badgeText(t, renderer.RenderHTML(node, renderer.BindingSources{"members": wire.Arr{}})); got == "2" {
		t.Fatal("an EMPTY host value still derived 2 — the assertion above would pass on a host that ignores the slot")
	}
}

// TestStateSeedingRules walks §24.4's five rules. Each subtest carries the
// deliberately mis-seeded case alongside the correct one, so every rule has an
// observed red: a rule asserted only in its passing direction cannot tell a
// working implementation from an absent one.
func TestStateSeedingRules(t *testing.T) {
	decode := func(t *testing.T, doc string) wire.Node {
		t.Helper()
		n, err := wire.DecodeNode(doc)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return n
	}
	metric := func(id, key, declaration string) string {
		return `{"id":"` + id + `","kind":{"$type":"Metric","label":"L","value":{"$type":"State",` +
			declaration + `"key":"` + key + `"}}}`
	}
	box := func(children ...string) string {
		return `{"id":"root","kind":{"$type":"Box","children":[` + strings.Join(children, ",") +
			`],"layout":{"$type":"Auto"},"role":"Dashboard"}}`
	}
	seedOf := func(t *testing.T, doc, key string) (wire.Value, bool) {
		t.Helper()
		v, ok := renderer.CollectStateSeeds(decode(t, doc))[key]
		return v, ok
	}

	// Rule 1 — WHO DECLARES: any `Binding.State` with a PRESENT `defaultValue`.
	t.Run("rule1-a-present-default-declares-and-an-absent-one-does-not", func(t *testing.T) {
		if v, ok := seedOf(t, metric("m", "users", `"defaultValue":7,`), "users"); !ok || v != wire.Int(7) {
			t.Fatalf("a present default did not seed its slot: %v (present=%v)", v, ok)
		}
		if _, ok := seedOf(t, metric("m", "users", ``), "users"); ok {
			t.Fatal("a State carrying NO defaultValue seeded its slot — a reader that declares nothing declares nothing")
		}
	})

	// Rule 2 — PRECEDENCE: host value > written value > seed. This host holds
	// no written values, so the pair that matters is host vs seed.
	t.Run("rule2-the-host-value-wins-over-the-seed", func(t *testing.T) {
		node := decode(t, metric("m", "users", `"defaultValue":7,`))
		merged := renderer.WithStateSeeds(node, renderer.BindingSources{"users": wire.Int(99)})
		if merged["users"] != wire.Int(99) {
			t.Fatalf("the seed overrode the host's own value: %v — a seed is the value before anything else has said anything, never an override", merged["users"])
		}
		if renderer.WithStateSeeds(node, nil)["users"] != wire.Int(7) {
			t.Fatal("the seed did not reach a caller that named nothing")
		}
		// The caller's own map is never mutated: a host may reuse one across
		// renders, and a seeding pass that wrote into it would leak the first
		// tree's declarations into the second's render.
		callers := renderer.BindingSources{}
		renderer.WithStateSeeds(node, callers)
		if len(callers) != 0 {
			t.Fatalf("the caller's sources map was mutated: %v", callers)
		}
	})

	// Rule 3 — ORDER-INDEPENDENCE: seeding runs over the WHOLE tree before any
	// binding resolves, so a reader that appears before the declaration is not
	// a special case.
	t.Run("rule3-document-order-carries-no-meaning", func(t *testing.T) {
		declaring := metric("declares", "users", `"defaultValue":7,`)
		reading := metric("reads", "users", ``)
		after, okA := seedOf(t, box(declaring, reading), "users")
		before, okB := seedOf(t, box(reading, declaring), "users")
		if !okA || !okB || after != before {
			t.Fatalf("the seed depended on document order: declaration-first=%v(%v) reader-first=%v(%v)", after, okA, before, okB)
		}
	})

	// Rule 4 — TWO DECLARATIONS OF ONE KEY. A disagreement is FUARAN106's to
	// name; a renderer must still be deterministic and takes the FIRST in tree
	// order. And an EMPTY declaration declares nothing.
	t.Run("rule4-first-declaration-wins-and-an-empty-one-declares-nothing", func(t *testing.T) {
		first := metric("first", "k", `"defaultValue":1,`)
		second := metric("second", "k", `"defaultValue":2,`)
		if v, _ := seedOf(t, box(first, second), "k"); v != wire.Int(1) {
			t.Fatalf("conflicting declarations resolved to %v, want the FIRST in tree order (1)", v)
		}
		if v, _ := seedOf(t, box(second, first), "k"); v != wire.Int(2) {
			t.Fatalf("reversing the pair resolved to %v, want the FIRST in tree order (2) — the walk is not order-following", v)
		}

		// The empty declaration must not WIN the race, or a badge spelling
		// `"defaultValue": []` before the grid that carries the rows would seed
		// the slot EMPTY and make rule 3 false.
		empty := metric("empty", "rows", `"defaultValue":[],`)
		carrying := `{"id":"g","kind":{"$type":"DataGrid","columns":[{"field":"team","kind":{"$type":"Text"},"label":"Team"}],` +
			`"rowKeyField":"team","source":{"$type":"State","defaultValue":[{"team":"Ops"}],"key":"rows"}}}`
		v, ok := seedOf(t, box(empty, carrying), "rows")
		if !ok {
			t.Fatal("an empty declaration ahead of a carrying one left the slot unseeded — it won the race it must not enter")
		}
		if rows, isArr := v.(wire.Arr); !isArr || len(rows) != 1 {
			t.Fatalf("the seed is %v, want the one row the sibling declared", v)
		}
		// And on its own it seeds nothing at all: it is the value an unseeded
		// slot already has.
		if _, ok := seedOf(t, box(empty), "rows"); ok {
			t.Fatal("an empty declaration seeded its slot")
		}
	})

	// Rule 5 — A HOST-RESERVED KEY IS NEVER SEEDED. A seed is a tree-originated
	// write, and §12's reserved namespace refuses those on every path; the wire
	// must not gain a way around a deliberate floor.
	t.Run("rule5-a-host-reserved-key-is-never-seeded", func(t *testing.T) {
		if _, ok := seedOf(t, metric("m", "host.users", `"defaultValue":7,`), "host.users"); ok {
			t.Fatal("a host-reserved key was seeded from the tree")
		}
		// The identical declaration on an ordinary key DOES seed, so the subtest
		// above is measuring the prefix rather than a broken walk.
		if _, ok := seedOf(t, metric("m", "users", `"defaultValue":7,`), "users"); !ok {
			t.Fatal("the control declaration did not seed — rule 5's evidence is vacuous")
		}
	})
}
