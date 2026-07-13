package teleport

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// rawBundle wraps arbitrary envelope JSON into an FT1 string (no digest
// recomputation) — for exercising the decode-side reject paths.
func rawBundle(t *testing.T, envelopeJSON string) string {
	t.Helper()
	c, err := deflateBytes([]byte(envelopeJSON))
	if err != nil {
		t.Fatalf("deflate: %v", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(c)
}

const zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"
const validTree = `{"id":"a","kind":{"$type":"Skeleton","rows":1}}`

func mustDecodeNode(t *testing.T, s string) wire.Node {
	t.Helper()
	n, err := wire.DecodeNode(s)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	return n
}

// The reference exemplar: an onboarding wizard shape (a Box + heading + form),
// with a few state keys, a 2-op history window, and a chain head.
func exemplar(t *testing.T) Bundle {
	tree := mustDecodeNode(t, `{"id":"root","kind":{"$type":"Box","children":[`+
		`{"id":"h","kind":{"$type":"Heading","level":1,"text":{"$type":"Literal","text":"Onboarding"},"variant":"Standard"}},`+
		`{"id":"name","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"Step 1"}}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`)
	chainHead := strings.Repeat("ab", 32) // 64-hex
	return Bundle{
		Tree: tree,
		State: map[string]wire.Value{
			"step":  wire.Int(2),
			"name":  wire.Str("Ada"),
			"ratio": wire.Float(0.75),
		},
		History: []wire.Obj{
			{Tag: "UpdateProp", Fields: map[string]wire.Value{"path": wire.Str("Text"), "target": wire.Str("name"), "value": wire.Str("Step 2")}},
			{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("h")}},
		},
		ChainHead: &chainHead,
	}
}

func TestRoundTripByteExactAndDeterministic(t *testing.T) {
	b := exemplar(t)
	s1, err := Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(s1, "FT1.") {
		t.Errorf("missing FT1. prefix: %q", s1[:8])
	}
	// Determinism: the same bundle encodes to the same string.
	s2, _ := Encode(b)
	if s1 != s2 {
		t.Error("Encode is not deterministic")
	}
	// Round-trip: decode then re-encode reproduces the exact string.
	decoded, err := Decode(s1)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	reencoded, err := Encode(decoded)
	if err != nil {
		t.Fatalf("re-Encode: %v", err)
	}
	if reencoded != s1 {
		t.Errorf("round trip not byte-exact:\n got %s\nwant %s", reencoded, s1)
	}
	// The decoded bundle preserved its parts.
	if v, _ := decoded.State["step"].(wire.Int); v != 2 {
		t.Errorf("state.step = %v, want 2", decoded.State["step"])
	}
	if len(decoded.History) != 2 || decoded.ChainHead == nil {
		t.Errorf("history/chainHead lost: %+v", decoded)
	}
}

// Digest binds every field: two bundles differing only in state encode
// differently, and a hand-built envelope whose digest is stale fails
// DigestMismatch.
func TestDigestMismatch(t *testing.T) {
	a := exemplar(t)
	b := exemplar(t)
	b.State = map[string]wire.Value{"step": wire.Int(99)}
	sa, _ := Encode(a)
	sb, _ := Encode(b)
	if sa == sb {
		t.Fatal("bundles differing in state encoded identically — digest does not bind state")
	}

	// A well-formed envelope (bundle ok, digest 64-hex, tree present) but a
	// stale/wrong digest → DigestMismatch (step 4, after shape, before decode).
	stale := rawBundle(t, `{"bundle":"teleport@1","digest":"`+zeroDigest+`","tree":`+validTree+`}`)
	_, err := Decode(stale)
	var te *Error
	if !asErr(err, &te) || te.Kind != KindDigestMismatch {
		t.Errorf("got %v, want DigestMismatch", err)
	}
}

// A tree with a duplicate node id (valid digest) fails TreeInvalid at step 6.
func TestTreeInvalidRefusesBadIdentity(t *testing.T) {
	dupTree := mustDecodeNode(t, `{"id":"root","kind":{"$type":"Box","children":[`+
		`{"id":"dup","kind":{"$type":"Skeleton","rows":1}},`+
		`{"id":"dup","kind":{"$type":"Skeleton","rows":2}}`+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`)
	s, err := Encode(Bundle{Tree: dupTree})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, err = Decode(s)
	var te *Error
	if !asErr(err, &te) || te.Kind != KindTreeInvalid {
		t.Errorf("got %v, want TreeInvalid", err)
	}
}

func TestRejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  ErrorKind
	}{
		{"missing prefix", "not-a-bundle", KindInvalidFormat},
		{"bad base64", "FT1.!!!!", KindInvalidFormat},
		{"oversize input", "FT1." + strings.Repeat("A", maxInput), KindOversize},
		{"unsupported version", rawBundle(t, `{"bundle":"teleport@2","digest":"`+zeroDigest+`","tree":`+validTree+`}`), KindUnsupportedVer},
		{"missing tree", rawBundle(t, `{"bundle":"teleport@1","digest":"`+zeroDigest+`"}`), KindInvalidEnvelope},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Decode(c.input)
			var te *Error
			if !asErr(err, &te) || te.Kind != c.want {
				t.Errorf("got %v, want %s", err, c.want)
			}
		})
	}
}

func TestBudgetGuard(t *testing.T) {
	b := exemplar(t)
	s, err := EncodeWithin(b, BudgetQRComfortable)
	if err != nil {
		t.Fatalf("exemplar should fit the QR-comfortable budget: %v (len=%d)", err, len(s))
	}
	// A pathological tiny budget refuses with Oversize.
	if _, err := EncodeWithin(b, 10); err == nil {
		t.Error("a 10-char budget should refuse")
	} else if te, ok := err.(*Error); !ok || te.Kind != KindOversize {
		t.Errorf("expected Oversize, got %v", err)
	}
}

func asErr(err error, target **Error) bool {
	te, ok := err.(*Error)
	if ok {
		*target = te
	}
	return ok
}
