package merge

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Policy cases the foldFixtures corpus does not pin (the corpus leg is
// conformance/fold_test.go).

func foldOps(t *testing.T, texts ...string) []wire.Obj {
	t.Helper()
	out := make([]wire.Obj, len(texts))
	for i, s := range texts {
		op, err := wire.DecodeOp(s)
		if err != nil {
			t.Fatalf("decode op %d: %v", i, err)
		}
		out[i] = op
	}
	return out
}

func pane(id string) string {
	return `{"id":"` + id + `","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"x"}}}`
}

// A Box "root" holding the given panes.
func boxOf(t *testing.T, panes ...string) wire.Node {
	children := ""
	for i, p := range panes {
		if i > 0 {
			children += ","
		}
		children += pane(p)
	}
	return decode(t, `{"id":"root","kind":{"$type":"Box","children":[`+children+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`)
}

func insertOp(id string) string {
	return `{"$type":"InsertChild","child":` + pane(id) + `,"parentId":"root"}`
}

// A delta whose own op does not apply is refused before anything is judged,
// naming the side and the index of the first failing op.
func TestFoldDeltaDoesNotApplyNamesSideAndIndex(t *testing.T) {
	base := boxOf(t, "p")
	good := foldOps(t, `{"$type":"PatchStyle","style":{"tone":"Brand"},"target":"p"}`)
	bad := foldOps(t, `{"$type":"PatchStyle","style":{"tone":"Brand"},"target":"p"}`,
		`{"$type":"RemoveNode","target":"absent"}`)
	_, fault := ReconcileFold(base, good, bad)
	if fault == nil || fault.Kind != FaultDeltaDoesNotApply || fault.Side != FoldSideB || fault.Index != 1 {
		t.Fatalf("want DeltaDoesNotApply B[1], got %+v", fault)
	}
	_, fault = ReconcileFold(base, bad, good)
	if fault == nil || fault.Kind != FaultDeltaDoesNotApply || fault.Side != FoldSideA || fault.Index != 1 {
		t.Fatalf("want DeltaDoesNotApply A[1], got %+v", fault)
	}
}

// Two empty deltas derive the base, with no script.
func TestFoldEmptyDeltasDeriveTheBase(t *testing.T) {
	base := boxOf(t, "p")
	r, fault := ReconcileFold(base, nil, nil)
	if fault != nil {
		t.Fatalf("refused: %+v", fault)
	}
	if encode(t, r.Tree) != encode(t, base) || len(r.Script) != 0 || r.Agreements != 0 || r.Interleaves != 0 {
		t.Fatalf("empty fold changed something: %+v", r)
	}
}

// Two appends under one parent interleave by the child-order rule: the runs
// keep their own order and merge by heads. Where the fold's order (A then B)
// differs from the rule's, the merge records one ReorderChildren, and the
// derivation is the same whichever delta is named first.
func TestFoldInterleaveRecordsTheScript(t *testing.T) {
	base := boxOf(t, "k")
	a := foldOps(t, insertOp("z"))
	b := foldOps(t, insertOp("m"))
	r, fault := ReconcileFold(base, a, b)
	if fault != nil {
		t.Fatalf("refused: %+v", fault)
	}
	if r.Interleaves != 1 || len(r.Script) != 1 {
		t.Fatalf("want one interleaved parent and a one-op script, got %+v", r)
	}
	script, err := wire.EncodeOp(r.Script[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"$type":"ReorderChildren","newOrder":["k","m","z"],"parentId":"root"}`; script != want {
		t.Errorf("script = %s, want %s", script, want)
	}
	if got, want := encode(t, r.Tree), encode(t, boxOf(t, "k", "m", "z")); got != want {
		t.Errorf("derived tree = %s, want %s", got, want)
	}

	// Named the other way round, the fold's order is already the rule's.
	swapped, fault := ReconcileFold(base, b, a)
	if fault != nil {
		t.Fatalf("swapped refused: %+v", fault)
	}
	if len(swapped.Script) != 0 || encode(t, swapped.Tree) != encode(t, r.Tree) {
		t.Errorf("swapped derivation differs: script %d ops, tree %s", len(swapped.Script), encode(t, swapped.Tree))
	}
}

// An agreement is counted once per distinct op, however many addresses the
// pair collides on.
func TestFoldAgreementCountsDistinctOps(t *testing.T) {
	base := boxOf(t, "p")
	same := foldOps(t, `{"$type":"RemoveNode","target":"p"}`)
	r, fault := ReconcileFold(base, same, same)
	if fault != nil {
		t.Fatalf("refused: %+v", fault)
	}
	if r.Agreements != 1 {
		t.Errorf("agreements = %d, want 1", r.Agreements)
	}
}

// The envelope's entries are ordered canonically and exact duplicates are
// removed, so the input order of the refusals does not reach the bytes.
func TestFoldEnvelopeIsCanonicalAndDeduplicated(t *testing.T) {
	l := foldOps(t, `{"$type":"PatchStyle","style":{"tone":"Brand"},"target":"p"}`)[0]
	r := foldOps(t, `{"$type":"PatchStyle","style":{"tone":"Success"},"target":"p"}`)[0]
	slot := FoldRefusal{Conflict: OpConflict{Left: l, Right: r, Address: "p", Shape: ShapeSlotClash, Slot: "style.tone"}, Class: FoldConflict}
	whole := FoldRefusal{Conflict: OpConflict{Left: l, Right: r, Address: "p", Shape: ShapeConcurrentUpdate}, Class: FoldConflict}
	x, err := EncodeFoldEnvelope([]FoldRefusal{slot, whole, slot})
	if err != nil {
		t.Fatal(err)
	}
	y, err := EncodeFoldEnvelope([]FoldRefusal{whole, slot})
	if err != nil {
		t.Fatal(err)
	}
	if x != y {
		t.Fatalf("envelope depends on input order:\n%s\n%s", x, y)
	}
	want := `[{"address":"p","class":"Conflict","left":{"$type":"PatchStyle","style":{"tone":"Brand"},"target":"p"},` +
		`"right":{"$type":"PatchStyle","style":{"tone":"Success"},"target":"p"},"shape":"ConcurrentUpdate"},` +
		`{"address":"p","class":"Conflict","left":{"$type":"PatchStyle","style":{"tone":"Brand"},"target":"p"},` +
		`"right":{"$type":"PatchStyle","style":{"tone":"Success"},"target":"p"},"shape":"SlotClash","slot":"style.tone"}]`
	if x != want {
		t.Errorf("envelope = %s\nwant      %s", x, want)
	}
}
