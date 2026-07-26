package diff

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/ops"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// decode is a test helper: parse a canonical wire node or fail the test.
func decode(t *testing.T, canonicalJSON string) wire.Node {
	t.Helper()
	n, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		t.Fatalf("DecodeNode(%s): %v", canonicalJSON, err)
	}
	return n
}

// enc canonically encodes a node (equality oracle).
func enc(t *testing.T, n wire.Node) string {
	t.Helper()
	s, err := wire.EncodeNode(n)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	return s
}

// applyAll folds a diff script over before and returns the resulting tree,
// failing on any apply error (a correct script never errors).
func applyAll(t *testing.T, before wire.Node, script []wire.Obj) wire.Node {
	t.Helper()
	tree := before
	for i, op := range script {
		next, err := ops.Apply(op, tree)
		if err != nil {
			t.Fatalf("op #%d (%s) failed to apply: %v", i, op.Tag, err)
		}
		tree = next
	}
	return tree
}

// assertRoundTrip is the defining property: Apply(Diff(a,b), a) == b.
func assertRoundTrip(t *testing.T, a, b wire.Node) []wire.Obj {
	t.Helper()
	script := Diff(a, b)
	got := applyAll(t, a, script)
	if enc(t, got) != enc(t, b) {
		t.Errorf("round-trip failed:\n  diff produced %d op(s)\n  got  %s\n  want %s",
			len(script), enc(t, got), enc(t, b))
	}
	return script
}

// ── tree builders (mirror the rs diff.rs card fixture) ───────────────────────

func card(title, metricLabel string) string {
	return `{"id":"card","kind":{"$type":"Box","children":[` +
		`{"id":"h","kind":{"$type":"Heading","level":1,"text":"` + title + `","variant":"Standard"}},` +
		`{"id":"m","kind":{"$type":"Metric","label":"` + metricLabel + `","value":{"$type":"Static","value":1}}}` +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Card"}}`
}

// ── unit tests (op-choice parity with the rs producer) ───────────────────────

func TestIdenticalTreesDiffToNoOps(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	if script := Diff(a, a); len(script) != 0 {
		t.Errorf("identical trees diffed to %d op(s), want 0", len(script))
	}
}

func TestChangedLeafLocalisesAndReplaysExactly(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	b := decode(t, card("Sales", "Profit")) // only the metric label changed
	script := assertRoundTrip(t, a, b)
	if len(script) == 0 {
		t.Fatal("expected a non-empty script for a changed leaf")
	}
	// It does NOT replace the whole root — the edit localises to the metric.
	for _, op := range script {
		if op.Tag == "ReplaceRoot" {
			t.Errorf("a localised leaf change emitted ReplaceRoot; want a seat-local replace")
		}
	}
	// The seat-local replace is RemoveNode(m) + InsertChild(card, 1, m').
	if len(script) != 2 || script[0].Tag != "RemoveNode" || script[1].Tag != "InsertChild" {
		t.Errorf("expected [RemoveNode, InsertChild], got %s", opTags(script))
	}
}

func TestChangedHeadingReplaysExactly(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	b := decode(t, card("Marketing", "Revenue"))
	assertRoundTrip(t, a, b)
}

func TestDifferentRootIDReplacesWholeTree(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	b := decode(t, `{"id":"other","kind":{"$type":"Heading","level":1,"text":"Hi","variant":"Standard"}}`)
	script := assertRoundTrip(t, a, b)
	if len(script) != 1 || script[0].Tag != "ReplaceRoot" {
		t.Errorf("different root id: expected a single ReplaceRoot, got %s", opTags(script))
	}
}

func TestRemovedChildReplaysExactly(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	// b drops the metric child, keeping only the heading.
	b := decode(t, `{"id":"card","kind":{"$type":"Box","children":[`+
		`{"id":"h","kind":{"$type":"Heading","level":1,"text":"Sales","variant":"Standard"}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Card"}}`)
	assertRoundTrip(t, a, b)
}

func TestInsertedChildReplaysExactly(t *testing.T) {
	// a has only the heading; b adds the metric — the child-id lists differ,
	// so the parent escalates to a whole-node replace (still round-trips).
	a := decode(t, `{"id":"card","kind":{"$type":"Box","children":[`+
		`{"id":"h","kind":{"$type":"Heading","level":1,"text":"Sales","variant":"Standard"}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Card"}}`)
	b := decode(t, card("Sales", "Revenue"))
	assertRoundTrip(t, a, b)
}

func TestReorderedChildrenReplaysExactly(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	// b swaps the two children (same ids, different order).
	b := decode(t, `{"id":"card","kind":{"$type":"Box","children":[`+
		`{"id":"m","kind":{"$type":"Metric","label":"Revenue","value":{"$type":"Static","value":1}}},`+
		`{"id":"h","kind":{"$type":"Heading","level":1,"text":"Sales","variant":"Standard"}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Card"}}`)
	assertRoundTrip(t, a, b)
}

func TestNestedLayoutRecursesPerSeat(t *testing.T) {
	// A Box holding two child Boxes; only the deeper metric changes. The diff
	// must recurse through both layout levels rather than replacing the root.
	inner := func(label string) string {
		return `{"id":"inner","kind":{"$type":"Box","children":[` +
			`{"id":"m","kind":{"$type":"Metric","label":"` + label + `","value":{"$type":"Static","value":1}}}` +
			`],"layout":{"$type":"Auto"},"role":"Group"}}`
	}
	root := func(label string) string {
		return `{"id":"root","kind":{"$type":"Box","children":[` + inner(label) +
			`],"layout":{"$type":"Auto"},"role":"Dashboard"}}`
	}
	a := decode(t, root("Revenue"))
	b := decode(t, root("Profit"))
	script := assertRoundTrip(t, a, b)
	for _, op := range script {
		if op.Tag == "ReplaceRoot" {
			t.Errorf("nested change emitted ReplaceRoot; expected a seat-local replace under inner")
		}
	}
	// RemoveNode(m) + InsertChild(inner, 0, m').
	if len(script) != 2 || script[1].Tag != "InsertChild" {
		t.Fatalf("expected a seat-local replace, got %s", opTags(script))
	}
	if pid, _ := script[1].Fields["parentId"].(wire.Str); string(pid) != "inner" {
		t.Errorf("InsertChild parentId = %q, want \"inner\"", pid)
	}
}

func TestDiffBatchedWraps(t *testing.T) {
	a := decode(t, card("Sales", "Revenue"))
	b := decode(t, card("Sales", "Profit"))
	batched := DiffBatched(a, b)
	if len(batched) != 1 || batched[0].Tag != "Batch" {
		t.Fatalf("expected a single Batch op, got %s", opTags(batched))
	}
	// A batched script still satisfies the round-trip law.
	got := applyAll(t, a, batched)
	if enc(t, got) != enc(t, b) {
		t.Errorf("batched round-trip failed:\n got  %s\n want %s", enc(t, got), enc(t, b))
	}
	// A no-op / single-op diff stays unwrapped.
	if bare := DiffBatched(a, a); len(bare) != 0 {
		t.Errorf("DiffBatched of identical trees = %d op(s), want 0", len(bare))
	}
}

func opTags(script []wire.Obj) string {
	tags := make([]string, len(script))
	for i, op := range script {
		tags[i] = op.Tag
	}
	return "[" + strings.Join(tags, ", ") + "]"
}

// ── generated property test: Apply(Diff(a,b), a) == b ────────────────────────

// genTree builds a deterministic pseudo-random layout tree of leaves and nested
// Boxes. Ids are stable per position so a mutated variant keeps matching ids
// where the structure is unchanged — exercising the recurse-vs-replace split.
func genTree(rng *rand.Rand, id string, depth int) wire.Node {
	if depth <= 0 || rng.Intn(3) == 0 {
		return genLeaf(rng, id)
	}
	nChildren := rng.Intn(4) // 0..3
	var kids []string
	for i := 0; i < nChildren; i++ {
		child := genTree(rng, fmt.Sprintf("%s-%d", id, i), depth-1)
		kids = append(kids, mustEncodeNode(child))
	}
	return decodeMust(`{"id":"` + id + `","kind":{"$type":"Box","children":[` +
		strings.Join(kids, ",") + `],"layout":{"$type":"Auto"},"role":"Group"}}`)
}

func genLeaf(rng *rand.Rand, id string) wire.Node {
	switch rng.Intn(3) {
	case 0:
		return decodeMust(`{"id":"` + id + `","kind":{"$type":"Markdown","text":"` + word(rng) + `"}}`)
	case 1:
		return decodeMust(`{"id":"` + id + `","kind":{"$type":"Heading","level":` +
			fmt.Sprint(1+rng.Intn(3)) + `,"text":"` + word(rng) + `","variant":"Standard"}}`)
	default:
		return decodeMust(`{"id":"` + id + `","kind":{"$type":"Skeleton","rows":` + fmt.Sprint(1+rng.Intn(4)) + `}}`)
	}
}

func word(rng *rand.Rand) string {
	words := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	return words[rng.Intn(len(words))]
}

func mustEncodeNode(n wire.Node) string {
	s, err := wire.EncodeNode(n)
	if err != nil {
		panic(err)
	}
	return s
}

func decodeMust(canonicalJSON string) wire.Node {
	n, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		panic(fmt.Sprintf("decodeMust(%s): %v", canonicalJSON, err))
	}
	return n
}

// TestRoundTripProperty asserts Apply(Diff(a,b), a) == b over many generated
// tree pairs. Each pair shares a seed-derived root so that both fully-unrelated
// trees (root-id divergence → ReplaceRoot) and structurally-related mutations
// (localised seat replaces + recursion) are exercised. Deterministic: a fixed
// seed sequence makes any failure reproducible.
func TestRoundTripProperty(t *testing.T) {
	for seed := int64(1); seed <= 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		// Two independently-generated trees. Half the time force a shared root
		// id so the interesting same-root recursion path dominates.
		a := genTree(rng, "root", 3)
		var b wire.Node
		if seed%2 == 0 {
			b = genTree(rng, "root", 3) // shared root id — localised diff
		} else {
			b = genTree(rng, fmt.Sprintf("root-%d", seed), 3) // may diverge at root
		}
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			assertRoundTrip(t, a, b)
			// And the identity: a tree diffs to nothing against itself.
			if s := Diff(a, a); len(s) != 0 {
				t.Errorf("Diff(a,a) = %d op(s), want 0", len(s))
			}
		})
	}
}

// ── cross-host golden set ────────────────────────────────────────────────────
//
// A small (before, after) → op-script golden, authored for the Go host. No
// shared cross-host diff-golden fixture exists in wire-format-fixtures/ yet
// (the rs/py producers assert the round-trip law with in-language cases, not a
// committed shared golden). These goldens pin the Go op choices so a regression
// is caught here; reconciling them into a shared wire-format-fixtures/diff/
// family that rs and py also certify against is tracked as follow-up (see the
// phase Outcome note). The op scripts below match the rs producer's choices for
// the deterministic cases (ReplaceRoot on root-id change; RemoveNode+InsertChild
// seat-local replace on a localised change).

func TestCrossHostGoldens(t *testing.T) {
	cases := []struct {
		name     string
		before   string
		after    string
		wantOps  []string // canonical-encoded op scripts (via wire.EncodeOp)
		wantRoot bool     // expect exactly one ReplaceRoot
	}{
		{
			name:   "identical",
			before: card("Sales", "Revenue"),
			after:  card("Sales", "Revenue"),
			// empty script
		},
		{
			name:    "leaf-metric-relabelled",
			before:  card("Sales", "Revenue"),
			after:   card("Sales", "Profit"),
			wantOps: nil, // asserted structurally below; exact bytes pinned via golden field
		},
		{
			name:     "root-id-changed",
			before:   card("Sales", "Revenue"),
			after:    `{"id":"other","kind":{"$type":"Heading","level":1,"text":"Hi","variant":"Standard"}}`,
			wantRoot: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := decode(t, c.before)
			b := decode(t, c.after)
			script := assertRoundTrip(t, a, b) // correctness first

			if c.name == "identical" && len(script) != 0 {
				t.Errorf("identical golden produced %d op(s), want 0", len(script))
			}
			if c.wantRoot {
				if len(script) != 1 || script[0].Tag != "ReplaceRoot" {
					t.Errorf("root-id golden: want single ReplaceRoot, got %s", opTags(script))
				}
			}
			// Pin the exact canonical op bytes for the leaf-relabel case.
			if c.name == "leaf-metric-relabelled" {
				wantScript := []string{
					`{"$type":"RemoveNode","target":"m"}`,
					// 0.4.0: InsertChild appends, and `m` was already the last
					// child, so no ReorderChildren is needed to restore order.
					`{"$type":"InsertChild","child":{"id":"m","kind":{"$type":"Metric","label":"Profit","value":{"$type":"Static","value":1}}},"parentId":"card"}`,
				}
				assertGoldenBytes(t, script, wantScript)
			}
		})
	}
}

func assertGoldenBytes(t *testing.T, script []wire.Obj, want []string) {
	t.Helper()
	if len(script) != len(want) {
		t.Fatalf("golden op count = %d, want %d (%s)", len(script), len(want), opTags(script))
	}
	for i, op := range script {
		got, err := wire.EncodeOp(op)
		if err != nil {
			t.Fatalf("EncodeOp #%d: %v", i, err)
		}
		if got != want[i] {
			t.Errorf("golden op #%d bytes:\n got  %s\n want %s", i, got, want[i])
		}
	}
}
