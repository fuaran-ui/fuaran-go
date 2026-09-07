package wire

import "testing"

// Phase 1585 — Chart.stacked is omit-at-default, and the explicit form is still read.
//
// Until 1585 the encoder always emitted `stacked` while every host's decoder
// already restored `false` on absence — a tolerance five hosts happened to share
// rather than a stated rule. The IDL now declares the member `omitDefault false`,
// so the omission is the CONTRACT: the encoder omits at `false`, the decoder
// restores `false`, and the corpus's chart fixtures carry the shorter bytes.
//
// The half worth testing here is the one a corpus of re-emitted fixtures cannot
// state, because every fixture in it now omits the member: that a document
// carrying the OLD explicit `"stacked": false` still decodes to exactly the same
// document. This host's model is structural, so that half is not free — the
// decoder must DROP the explicit default, or the pre-phase spelling would
// re-encode verbatim and become a second canonical form.

const (
	stackedOmitted = `{"id":"c1","kind":{"$type":"Chart","kind":"Bar",` +
		`"source":{"$type":"Static","value":[]},"xField":"quarter","yFields":["revenue"]}}`

	stackedExplicitFalse = `{"id":"c1","kind":{"$type":"Chart","kind":"Bar",` +
		`"source":{"$type":"Static","value":[]},"stacked":false,"xField":"quarter","yFields":["revenue"]}}`

	stackedExplicitTrue = `{"id":"c1","kind":{"$type":"Chart","kind":"Bar",` +
		`"source":{"$type":"Static","value":[]},"stacked":true,"xField":"quarter","yFields":["revenue"]}}`
)

func reencodeChart(t *testing.T, raw string) string {
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

func TestStackedOmittedFormRoundTrips(t *testing.T) {
	if got := reencodeChart(t, stackedOmitted); got != stackedOmitted {
		t.Errorf("the canonical form must carry no `stacked`:\n got %s\nwant %s", got, stackedOmitted)
	}
}

// Read-compat: the pre-phase spelling reaches the same document, and normalises
// to the shorter bytes rather than standing as a second canonical form.
func TestStackedExplicitFalseDecodesIdentically(t *testing.T) {
	before := reencodeChart(t, stackedExplicitFalse)
	after := reencodeChart(t, stackedOmitted)
	if before != after {
		t.Errorf("the explicit default must decode to the same document:\n explicit %s\n omitted  %s", before, after)
	}
	if before != stackedOmitted {
		t.Errorf("the explicit default must NORMALISE to the omitted form:\n got %s\nwant %s", before, stackedOmitted)
	}
}

func TestStackedTrueIsStillCarried(t *testing.T) {
	if got := reencodeChart(t, stackedExplicitTrue); got != stackedExplicitTrue {
		t.Errorf("`true` differs from the identity default and must ride the wire:\n got %s\nwant %s", got, stackedExplicitTrue)
	}
}
