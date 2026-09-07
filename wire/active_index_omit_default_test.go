package wire

import "testing"

// Phase 1585 — Tabs.activeIndex is omit-at-default, and the explicit form is still read.
//
// The sibling of `stacked_omit_default_test.go`, landed one step behind it.
// Until 1585 the encoder always emitted `activeIndex` while every host's decoder
// already restored `Static 0` on absence — a tolerance five hosts happened to
// share rather than a stated rule. The IDL now declares the member
// `omitDefault Static{value=0}`, so the omission is the CONTRACT: the encoder
// omits at that identity, the decoder restores it, and the corpus's tabs
// fixtures carry the shorter bytes.
//
// The half worth testing is the one a corpus of re-emitted fixtures cannot
// state, because every fixture carrying the identity now omits the member: that
// a document carrying the OLD explicit `{"$type":"Static","value":0}` still
// decodes to exactly the same document.
//
// The last two cases are the ones a bool-valued member does not have. The
// identity is one inhabitant of a union whose payload domain is unbounded, so a
// drop written on the tag alone would discard a document's authored tab — and,
// for a writable binding, its write-back destination with it, leaving an inert
// control.

const (
	tabsChild = `{"id":"p1","kind":{"$type":"Markdown","text":"one"}}`

	tabsOmitted = `{"id":"t1","kind":{"$type":"Tabs","children":[` + tabsChild + `]}}`

	tabsExplicitZero = `{"id":"t1","kind":{"$type":"Tabs",` +
		`"activeIndex":{"$type":"Static","value":0},"children":[` + tabsChild + `]}}`

	tabsExplicitOne = `{"id":"t1","kind":{"$type":"Tabs",` +
		`"activeIndex":{"$type":"Static","value":1},"children":[` + tabsChild + `]}}`

	tabsStateBound = `{"id":"t1","kind":{"$type":"Tabs",` +
		`"activeIndex":{"$type":"State","key":"pane"},"children":[` + tabsChild + `]}}`
)

func reencodeTabs(t *testing.T, raw string) string {
	t.Helper()
	node, err := DecodeNode(raw)
	if err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	out, err := EncodeNode(node)
	if err != nil {
		t.Fatalf("re-encoding %s: %v", raw, err)
	}
	return out
}

func TestActiveIndexOmittedFormRoundTrips(t *testing.T) {
	if got := reencodeTabs(t, tabsOmitted); got != tabsOmitted {
		t.Errorf("the canonical form must carry no `activeIndex`:\n got %s\nwant %s", got, tabsOmitted)
	}
}

// Read-compat: the pre-phase spelling reaches the same document, and normalises
// to the shorter bytes rather than standing as a second canonical form.
func TestActiveIndexExplicitIdentityDecodesIdentically(t *testing.T) {
	before := reencodeTabs(t, tabsExplicitZero)
	after := reencodeTabs(t, tabsOmitted)
	if before != after {
		t.Errorf("the explicit identity must decode to the same document:\n explicit %s\n omitted  %s", before, after)
	}
	if before != tabsOmitted {
		t.Errorf("the explicit identity must NORMALISE to the omitted form:\n got %s\nwant %s", before, tabsOmitted)
	}
}

func TestActiveIndexOtherStaticIsStillCarried(t *testing.T) {
	if got := reencodeTabs(t, tabsExplicitOne); got != tabsExplicitOne {
		t.Errorf("`Static 1` differs from the identity default and must ride the wire:\n got %s\nwant %s", got, tabsExplicitOne)
	}
}

func TestActiveIndexNonStaticBindingIsStillCarried(t *testing.T) {
	if got := reencodeTabs(t, tabsStateBound); got != tabsStateBound {
		t.Errorf("a writable binding is not the identity — dropping it would make the control inert:\n got %s\nwant %s", got, tabsStateBound)
	}
}
