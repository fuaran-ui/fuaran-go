package wire

import (
	"strings"
	"testing"
)

// WIRE_FORMAT.md §21.7 — the total-document byte ceiling.
//
// The vector is HOST-LOCAL by design and not a corpus fixture: committing
// 32 MiB of padding to a shared repository to assert one integer comparison is
// a poor trade, and unlike the depth bounds this is not a recursion hazard. So
// this pair IS this host's conformance evidence for §21.7, which is why both
// halves are here rather than only the refusal.

// documentOfBytes builds a syntactically valid node document padded to exactly
// n UTF-8 bytes.
//
// The padding goes inside a STRING literal rather than being whitespace,
// deliberately: the ceiling is checked before the parser runs, so whitespace
// would exercise the same comparison — but a string keeps the document one a
// parser would otherwise have to walk, which is the cost §21.7 exists to
// refuse up front.
func documentOfBytes(n int) string {
	prefix := `{"id":"n","kind":{"$type":"Markdown","text":"`
	suffix := `"}}`
	return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
}

func TestRefusesADocumentOneBytePastTheCeiling(t *testing.T) {
	doc := documentOfBytes(MaxDocumentBytes + 1)
	if len(doc) != MaxDocumentBytes+1 {
		t.Fatalf("the fixture is %d bytes, not %d", len(doc), MaxDocumentBytes+1)
	}
	_, err := DecodeNode(doc)
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("expected a *DecodeError, got %v", err)
	}
	if de.Code != CodeLimitExceeded {
		t.Errorf("code = %s, want %s", de.Code, CodeLimitExceeded)
	}
	// The breach is a property of the DOCUMENT, so the path is the root —
	// there is no position inside it to name, nothing having been parsed.
	if de.Path != "$" {
		t.Errorf("path = %q, want %q", de.Path, "$")
	}
}

func TestRefusesAnOverCeilingOpDocumentToo(t *testing.T) {
	// Both public entry points, because both allocate. A ceiling on one of
	// them is a ceiling on neither in practice.
	_, err := DecodeOp(documentOfBytes(MaxDocumentBytes + 1))
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("expected a *DecodeError, got %v", err)
	}
	if de.Code != CodeLimitExceeded || de.Path != "$" {
		t.Errorf("got %s at %q, want LIMIT_EXCEEDED at $", de.Code, de.Path)
	}
}

func TestADocumentAtExactlyTheCeilingIsNotRefusedForItsSize(t *testing.T) {
	// The at-the-limit half, which is the half a refusal-only suite passes
	// while enforcing the ceiling one byte too tightly. This document's string
	// is far past §21.6, so it IS refused — but by the string bound rather
	// than by the size ceiling, and the two are told apart by the message,
	// since both report at "$".
	doc := documentOfBytes(MaxDocumentBytes)
	if len(doc) != MaxDocumentBytes {
		t.Fatalf("the fixture is %d bytes, not %d", len(doc), MaxDocumentBytes)
	}
	_, err := DecodeNode(doc)
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("expected a *DecodeError, got %v", err)
	}
	if strings.Contains(de.Message, "UTF-8 bytes") {
		t.Errorf("a document AT the ceiling was refused as an over-size document: %s", de.Message)
	}
	// ...and it did reach the parser, which is what "the size check did not
	// fire" means operationally.
	if !strings.Contains(de.Message, "MaxStringLength") {
		t.Errorf("expected the string bound to be the refusal that fired, got: %s", de.Message)
	}
}

func TestAnOrdinaryDocumentIsUnaffectedByTheCeiling(t *testing.T) {
	// Verify the probe: the ceiling must not be reachable by an ordinary tree,
	// or the two tests above would pass on a host that refused everything.
	if _, err := DecodeNode(`{"id":"n","kind":{"$type":"Markdown","text":"hello"}}`); err != nil {
		t.Fatalf("an ordinary document was refused: %v", err)
	}
}
