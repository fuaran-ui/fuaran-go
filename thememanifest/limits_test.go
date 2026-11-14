package thememanifest

// Phase 2721 — the manifest decoder reads under WIRE_FORMAT.md §21.1's syntactic
// limits: JSON depth, string length, array length, object members and document
// bytes (the node bounds do not apply: a manifest has no nodes). Each bound is
// pinned twice — a manifest AT it decodes, a manifest ONE PAST it is refused as
// LIMIT_EXCEEDED (§21.2 rules 1 and 2) — so neither an off-by-one that refuses
// a conformant manifest nor one that admits a hostile one can pass.

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// nestedObjects builds a DTCG tree `depth` objects deep with a scalar at the
// bottom: {"a":{"a":…{"a":1}…}}. Only `{` and `[` count toward §21.1's JSON
// depth, so the scalar adds nothing.
func nestedObjects(depth int) string {
	return strings.Repeat(`{"a":`, depth-1) + `{"a":1}` + strings.Repeat("}", depth-1)
}

// tokenWithValue is a one-token DTCG tree whose $value is `value`.
func tokenWithValue(value string) string {
	return `{"t":{"$type":"color","$value":"` + value + `"}}`
}

// manifestWithRoles is a manifest wrapper whose `roles` array has n elements.
func manifestWithRoles(n int) string {
	return `{"roles":[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `],"tokens":{}}`
}

// treeWithMembers is a DTCG tree object with n members.
func treeWithMembers(n int) string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"k` + itoa(i) + `":0`)
	}
	b.WriteString("}")
	return b.String()
}

// documentOfBytes is a manifest exactly n UTF-8 bytes long: a small tree
// followed by insignificant whitespace, so no other bound is near its edge.
func documentOfBytes(n int) string {
	head := `{"t":{"$type":"color","$value":"#000000"}}`
	return head + strings.Repeat(" ", n-len(head))
}

func requireLimitExceeded(t *testing.T, name string, err error, mentions string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: one past the bound decoded; want LIMIT_EXCEEDED", name)
	}
	var de *wire.DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("%s: want *wire.DecodeError, got %T: %v", name, err, err)
	}
	if de.Code != wire.CodeLimitExceeded {
		t.Fatalf("%s: want LIMIT_EXCEEDED, got %s (%s)", name, de.Code, de.Message)
	}
	if de.Path != "$" {
		t.Fatalf("%s: want path $, got %s", name, de.Path)
	}
	if !strings.Contains(de.Message, mentions) {
		t.Fatalf("%s: the refusal should name %s, got %q", name, mentions, de.Message)
	}
}

func TestManifestDecodeHoldsEverySyntacticBoundAtTheEdgeAndOnePast(t *testing.T) {
	cases := []struct {
		name     string
		atBound  string
		pastIt   string
		mentions string
	}{
		{"JSON depth", nestedObjects(wire.MaxJSONDepth), nestedObjects(wire.MaxJSONDepth + 1), "MaxJSONDepth"},
		{
			"string length",
			tokenWithValue(strings.Repeat("x", wire.MaxStringLength)),
			tokenWithValue(strings.Repeat("x", wire.MaxStringLength+1)),
			"MaxStringLength",
		},
		{"array length", manifestWithRoles(wire.MaxArrayLength), manifestWithRoles(wire.MaxArrayLength + 1), "MaxArrayLength"},
		{"object members", treeWithMembers(wire.MaxArrayLength), treeWithMembers(wire.MaxArrayLength + 1), "MaxArrayLength"},
		{"document bytes", documentOfBytes(wire.MaxDocumentBytes), documentOfBytes(wire.MaxDocumentBytes + 1), "MaxDocumentBytes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(c.atBound); err != nil {
				t.Fatalf("%s: a manifest AT the bound was refused (§21.2 rule 1): %v", c.name, err)
			}
			_, err := Decode(c.pastIt)
			requireLimitExceeded(t, c.name, err, c.mentions)
		})
	}
}

func TestStringLengthIsCountedInCodePointsNotBytes(t *testing.T) {
	// §21.6: an astral character is ONE code point and four UTF-8 bytes.
	if _, err := Decode(tokenWithValue(strings.Repeat("\U0001D11E", wire.MaxStringLength))); err != nil {
		t.Fatalf("a %d-code-point astral string was refused: %v", wire.MaxStringLength, err)
	}
}

func TestDocumentBytesAreUTF8BytesNotCodePoints(t *testing.T) {
	// §21.7 measures carriage: an astral character costs four bytes here.
	astral := strings.Repeat("\U0001D11E", wire.MaxDocumentBytes/4/40)
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 40; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"k` + itoa(i) + `":"` + astral + `"`)
	}
	b.WriteString("}")
	doc := b.String()
	if len(doc) <= wire.MaxDocumentBytes {
		t.Fatalf("probe is %d bytes; it must exceed %d to test anything", len(doc), wire.MaxDocumentBytes)
	}
	_, err := Decode(doc)
	requireLimitExceeded(t, "astral document bytes", err, "MaxDocumentBytes")
}
