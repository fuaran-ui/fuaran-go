package wire

import (
	"strings"
	"testing"
)

// Float-SEQUENCE non-finite sentinels (Phase 1099).
//
// §5 requires every host to EMIT the quoted "NaN" / "Infinity" / "-Infinity"
// sentinels for a non-finite number, and §7 requires a decoder to ACCEPT them at
// a float slot. A float SEQUENCE element is a float slot, so `Sparkline.source`
// `[1,"NaN",3]` must decode here exactly as it does on the TypeScript and Rust
// hosts — both of which route their sequence elements through the same scalar
// reader (`requireFloat` in fuaran-ts's ops decoder; `as_float` behind
// `StaticSlot::FloatSeq` in fuaran-rs's), so the accept set is one set rather
// than two that happen to agree.
//
// This host once had the second shape: `expectNumber` — the choke point serving
// every typed float slot AND every float-sequence element — did not read the
// sentinels while `floatStatic` did, so a document this host encoded was one it
// could not read back. The fix routed both through one reader; these tests are
// what stop the paths separating again. The corpus round-trip walk covers the
// ACCEPT side (`nodes/spark-nonfinite-sentinel.json`); what it cannot see is the
// admission BOUNDARY, and a per-element reader that case-folded or accepted any
// string would pass the round-trip while diverging from every sibling host.

// TestFloatSeqAcceptsNonFiniteSentinels — the accept side, per sentinel, with the
// value carried through re-encode so the round trip is proven to CLOSE rather
// than merely to parse.
func TestFloatSeqAcceptsNonFiniteSentinels(t *testing.T) {
	for _, sentinel := range []string{"NaN", "Infinity", "-Infinity"} {
		t.Run(sentinel, func(t *testing.T) {
			doc := `{"id":"s","kind":{"$type":"Sparkline","source":{"$type":"Static","value":[1,"` +
				sentinel + `",3]}}}`
			node, err := DecodeNode(doc)
			if err != nil {
				t.Fatalf("a float-sequence element %q was rejected: %v", sentinel, err)
			}
			// The element must be a FLOAT, not the sentinel string: the bare
			// overflowing literal `-1e999` already decodes to Float(-Inf), so a
			// Str answer here would hand a consumer a float on one path and a
			// string on the other for the same number.
			source, _ := node.Kind.Fields["source"].(Obj)
			arr, ok := source.Fields["value"].(Arr)
			if !ok || len(arr) != 3 {
				t.Fatalf("expected a 3-element float sequence, got %#v", source.Fields["value"])
			}
			if _, isFloat := arr[1].(Float); !isFloat {
				t.Errorf("element 1 decoded as %T, want wire.Float", arr[1])
			}
			out, err := EncodeNode(node)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if out != doc {
				t.Errorf("round trip did not close\n got: %s\nwant: %s", out, doc)
			}
		})
	}
}

// TestFloatSeqSentinelAdmissionIsExact — the reject side. §7's three tokens are
// EXACT: a mis-cased spelling, or any other string, stays WRONG_TYPE at the
// element's own path. This is what distinguishes "routed through the same
// reader" from "accepts sentinels somehow", and it is the property a host that
// leaves sequence elements untyped silently loses.
func TestFloatSeqSentinelAdmissionIsExact(t *testing.T) {
	for _, bad := range []string{`"nan"`, `"NAN"`, `"Nan"`, `"infinity"`, `"hello"`, `true`, `null`, `{}`} {
		t.Run(bad, func(t *testing.T) {
			doc := `{"id":"n1","kind":{"$type":"Sparkline","source":{"$type":"Static","value":[1,` + bad + `,3]}}}`
			if _, err := DecodeNode(doc); err == nil {
				t.Fatalf("a float-sequence element %s was ACCEPTED; §7's sentinel set is exactly "+
					`"NaN" / "Infinity" / "-Infinity"`, bad)
			} else {
				if !strings.Contains(err.Error(), "WRONG_TYPE") {
					t.Errorf("want WRONG_TYPE, got: %v", err)
				}
				// The path must name the ELEMENT, not merely the slot — that is
				// what tells an author which of fifty points is wrong.
				if !strings.Contains(err.Error(), "$.kind.source") || !strings.Contains(err.Error(), "[1]") {
					t.Errorf("want a path naming element [1] under $.kind.source, got: %v", err)
				}
			}
		})
	}
}
