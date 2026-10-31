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

// ── the style facet (Phase 2314) ─────────────────────────────────────────────

// styledCard is card("Sales", "Revenue") with a style on the root and on the
// metric; an empty style string leaves that slot absent.
func styledCard(rootStyle, metricStyle string) string {
	withStyle := func(style string) string {
		if style == "" {
			return ""
		}
		return `,"style":` + style
	}
	return `{"id":"card","kind":{"$type":"Box","children":[` +
		`{"id":"h","kind":{"$type":"Heading","level":1,"text":"Sales","variant":"Standard"}},` +
		`{"id":"m","kind":{"$type":"Metric","label":"Revenue","value":{"$type":"Static","value":1}}` + withStyle(metricStyle) + `}` +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Card"}` + withStyle(rootStyle) + `}`
}

func TestStyleOnlyChangeIsOneStyleEdit(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		want          string
	}{
		{
			name:   "one member of a leaf",
			before: styledCard("", `{"tone":"Success","weight":"Spacious"}`),
			after:  styledCard("", `{"tone":"Critical","weight":"Spacious"}`),
			want:   `{"$type":"PatchStyle","style":{"tone":"Critical"},"target":"m"}`,
		},
		{
			name:   "two members of the root, one back to its default",
			before: styledCard(`{"tone":"Brand","weight":"Spacious"}`, ""),
			after:  styledCard(`{"emphasis":"Loud","tone":"Brand"}`, ""),
			want:   `{"$type":"PatchStyle","style":{"emphasis":"Loud","weight":"Standard"},"target":"card"}`,
		},
		{
			name:   "every member moves",
			before: styledCard("", `{"direction":"ltr","emphasis":"Loud","role":"Data","tone":"Success","voice":"Display","weight":"Spacious"}`),
			after:  styledCard("", `{"direction":"rtl","emphasis":"Quiet","role":"Caption","tone":"Info","voice":"Structural","weight":"Compact"}`),
			want:   `{"$type":"UpdateStyle","style":{"direction":"rtl","emphasis":"Quiet","role":"Caption","tone":"Info","voice":"Structural","weight":"Compact"},"target":"m"}`,
		},
		{
			name:   "back to the absent slot",
			before: styledCard("", `{"emphasis":"Loud","tone":"Warning"}`),
			after:  styledCard("", ""),
			want:   `{"$type":"PatchStyle","style":{"emphasis":"Normal","tone":"Default"},"target":"m"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := assertRoundTrip(t, decode(t, c.before), decode(t, c.after))
			assertGoldenBytes(t, script, []string{c.want})
		})
	}
}

func TestStyleAndContentChangeKeepsTheSeatReplace(t *testing.T) {
	a := decode(t, styledCard("", `{"tone":"Success"}`))
	b := decode(t, strings.Replace(styledCard("", `{"tone":"Critical"}`), "Revenue", "Profit", 1))
	if got := opTags(assertRoundTrip(t, a, b)); got != "[RemoveNode, InsertChild]" {
		t.Errorf("a style change beside a content change: got %s, want the seat replace", got)
	}
}

func TestRootStyleChangeBesideAChildChange(t *testing.T) {
	a := decode(t, styledCard(`{"tone":"Brand"}`, ""))
	b := decode(t, strings.Replace(styledCard(`{"tone":"Info"}`, ""), "Revenue", "Profit", 1))
	if got := opTags(assertRoundTrip(t, a, b)); got != "[PatchStyle, RemoveNode, InsertChild]" {
		t.Errorf("a root restyle beside a child change: got %s, want the style edit then the child's seat replace", got)
	}
}

// TestRestyleRoundTripProperty restyles random nodes of generated trees and
// asserts the round-trip law, and that a restyle diffs to style edits alone.
func TestRestyleRoundTripProperty(t *testing.T) {
	members := []struct {
		name   string
		tokens []string
	}{
		{"direction", []string{"auto", "ltr", "rtl"}},
		{"emphasis", []string{"Quiet", "Normal", "Loud"}},
		{"role", []string{"None", "Eyebrow", "Data", "Lede", "Caption"}},
		{"tone", []string{"Default", "Subdued", "Brand", "Success", "Warning", "Critical", "Info"}},
		{"voice", []string{"Default", "Display", "Structural"}},
		{"weight", []string{"Compact", "Standard", "Spacious"}},
	}
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a := genTree(rng, "root", 3)
		var ids []string
		var walk func(n wire.Node)
		walk = func(n wire.Node) {
			ids = append(ids, n.ID)
			kids, _ := structuralChildren(n)
			for _, k := range kids {
				walk(k)
			}
		}
		walk(a)
		b := a
		for i := 0; i < 1+rng.Intn(3); i++ {
			delta := map[string]wire.Value{}
			for _, m := range members {
				if rng.Intn(2) == 0 {
					delta[m.name] = wire.Str(m.tokens[rng.Intn(len(m.tokens))])
				}
			}
			if len(delta) == 0 {
				continue
			}
			op := styleOp("PatchStyle", ids[rng.Intn(len(ids))], delta)
			next, err := ops.Apply(op, b)
			if err != nil {
				t.Fatalf("seed %d: restyle failed to apply: %v", seed, err)
			}
			b = next
		}
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			for _, op := range assertRoundTrip(t, a, b) {
				if op.Tag != "PatchStyle" && op.Tag != "UpdateStyle" {
					t.Errorf("a restyle diffed to %s, want style edits only", op.Tag)
				}
			}
		})
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
// A small (before, after) → op-script golden, authored for the Go host and
// runnable without the corpus. The shared cross-host family is
// wire-format-fixtures/diff/goldens.json, certified in conformance/diff_test.go.
// The op scripts below match the rs producer's choices for the deterministic
// cases (ReplaceRoot on root-id change; RemoveNode+InsertChild seat-local
// replace on a localised change).

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

// Phase 2527 — a trait-only edit is one op, and the seat replace is not reached.
func TestTraitOnlyChangeIsOneTraitOp(t *testing.T) {
	node := func(envelope string) wire.Node {
		t.Helper()
		n, err := wire.DecodeNode(`{` + envelope + `"id":"m","kind":{"$type":"Markdown","text":"x"}}`)
		if err != nil {
			t.Fatalf("DecodeNode: %v", err)
		}
		return n
	}
	cases := []struct {
		name, before, after, want string
	}{
		{"accessibility set", ``, `"accessibility":{"role":"status"},`,
			`{"$type":"UpdateAccessibility","accessibility":{"role":"status"},"target":"m"}`},
		{"accessibility removed", `"accessibility":{"role":"status"},`, ``,
			`{"$type":"UpdateAccessibility","accessibility":{},"target":"m"}`},
		{"tooltip set", ``, `"tooltip":"t",`, `{"$type":"UpdateTooltip","target":"m","tooltip":"t"}`},
		{"tooltip cleared", `"tooltip":"t",`, ``, `{"$type":"UpdateTooltip","target":"m"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := Diff(node(c.before), node(c.after))
			if len(script) != 1 {
				t.Fatalf("want one op, got %d", len(script))
			}
			got, err := wire.EncodeOp(script[0])
			if err != nil {
				t.Fatalf("EncodeOp: %v", err)
			}
			if got != c.want {
				t.Errorf("got %s\nwant %s", got, c.want)
			}
		})
	}
}
