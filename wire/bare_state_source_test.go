package wire

import (
	"strings"
	"testing"
)

// WIRE_FORMAT.md §16 — a bare `{"$type":"State","key":k}` in a Transform's
// `source` slot is a LIVE source over the EMPTY initial snapshot.
//
// This host refused it until now, surfacing the columnar codec's missing-field
// didactic, which was the right answer while nothing else could fill the slot.
// Under §24.4 a SIBLING reader's declaration fills it, so the refusal was
// rejecting the most direct spelling of "I read this key and carry no data of my
// own" — the one FUARAN106's remedy text tells an author to write.
//
// There is no corpus fixture for the bare spelling yet (the corpus keeps the
// `"defaultValue":[]` spelling deliberately, because respelling a shared gate
// would redden a host that has not adopted this), so the pin lives here.

const (
	bareStateSource  = `{"$type":"State","key":"members"}`
	emptyStateSource = `{"$type":"State","defaultValue":[],"key":"members"}`
)

// badgeWithSource wraps a Transform whose source slot is the given canonical
// JSON, in the canonical member order the encoder emits.
func badgeWithSource(source string) string {
	return `{"id":"member-count","kind":{"$type":"Badge","label":{"$type":"Bound","binding":` +
		`{"$type":"Transform","pipeline":[{"$type":"groupBy","aggs":[{"fn":"count","name":"n","of":"team"}],"keys":[]}],` +
		`"source":` + source + `}},"variant":"Info"}}`
}

// TestBareStateTransformSourceDecodes is the acceptance pin: the bare wrapper
// decodes, and — because the binding is PRESERVED rather than normalised — it
// re-encodes to the bytes it arrived as. The two spellings are one dialect and
// neither is rewritten into the other, so the round-trip is what proves the
// decoded binding kept its `defaultValue` ABSENT.
func TestBareStateTransformSourceDecodes(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"the bare wrapper carries no payload member", bareStateSource},
		{"the empty-array spelling says the same thing", emptyStateSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := badgeWithSource(tc.source)
			node, err := DecodeNode(doc)
			if err != nil {
				t.Fatalf("decode refused a §16 live source: %v", err)
			}
			got, err := EncodeNode(node)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			if got != doc {
				t.Fatalf("re-encode is not byte-identical:\n got %s\nwant %s", got, doc)
			}
		})
	}
}

// TestBareStateTransformSourceStillRefusesMalformedSources is the go-red half.
// An assertion that only ever passes cannot tell a decoder that ACCEPTS the bare
// wrapper from one that stopped checking the slot at all — so the same slot is
// handed shapes §16 does not sanction, and each must still refuse at its own
// path.
func TestBareStateTransformSourceStillRefusesMalformedSources(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		// A non-binding object with neither `columns` nor `ref` is still the
		// missing-field didactic the 815 posture raises.
		{"a columnar source missing its columns", `{"schema":[]}`},
		// A Static envelope carrying no payload is NOT what §16 widened: the
		// widening names the State wrapper, whose key IS the live slot. A
		// `Static` with nothing in it names nothing at all.
		{"a Static envelope carrying no payload", `{"$type":"Static"}`},
		// Ragged carried data still fails its snapshot decode, unchanged.
		{"carried rows that are not objects", `{"$type":"State","defaultValue":[1,2],"key":"members"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeNode(badgeWithSource(tc.source))
			if err == nil {
				t.Fatal("the source was accepted — the widening is not scoped to the bare State wrapper")
			}
			de, ok := err.(*DecodeError)
			if !ok {
				t.Fatalf("want a typed DecodeError, got %T: %v", err, err)
			}
			if !strings.HasPrefix(de.Path, "$") {
				t.Fatalf("refusal path %q is not $-rooted", de.Path)
			}
		})
	}
}
