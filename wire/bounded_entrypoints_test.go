package wire_test

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dag"
	"github.com/fuaran-ui/fuaran-go/dataframe"
	"github.com/fuaran-ui/fuaran-go/function"
	"github.com/fuaran-ui/fuaran-go/thememanifest"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// Four entry points parsed untrusted text with encoding/json directly, so no
// §21 limit reached them at all. Each is exercised here with a 10 001-deep
// document — the figure the gap report measured — and must answer
// LIMIT_EXCEEDED.
//
// LIMIT_EXCEEDED specifically, not "malformed". §21.2 rule 2 forbids reporting
// a well-formed-but-too-large document as a syntax error, because that sends an
// author to repair the wrong thing: the dataframe path answered NOT_JSON, which
// says the pipeline is broken when the pipeline is merely deep.

// deepJSON builds a document nested `depth` levels under the given key.
func deepJSON(key string, depth int) string {
	return strings.Repeat(`{"`+key+`":`, depth) + `null` + strings.Repeat(`}`, depth)
}

const hostileDepth = 10001

func TestParseBoundedRefusesADeepDocument(t *testing.T) {
	_, err := wire.ParseBounded(deepJSON("a", hostileDepth))
	de, ok := err.(*wire.DecodeError)
	if !ok {
		t.Fatalf("err = %v (%T), want a *wire.DecodeError", err, err)
	}
	if de.Code != wire.CodeLimitExceeded {
		t.Errorf("code = %q, want %q", de.Code, wire.CodeLimitExceeded)
	}
}

func TestParseBoundedRefusesAnOverlongString(t *testing.T) {
	// The axis ParseCanonical never covered: syntactic depth was bounded, but a
	// string of any length passed straight through.
	huge := `{"k":"` + strings.Repeat("x", wire.MaxStringLength+1) + `"}`
	_, err := wire.ParseBounded(huge)
	de, ok := err.(*wire.DecodeError)
	if !ok || de.Code != wire.CodeLimitExceeded {
		t.Fatalf("err = %v, want a LIMIT_EXCEEDED *wire.DecodeError", err)
	}
}

func TestParseBoundedAcceptsAnOrdinaryDocument(t *testing.T) {
	raw, err := wire.ParseBounded(`{"a":[1,2,3],"b":"ok"}`)
	if err != nil {
		t.Fatalf("ParseBounded: %v", err)
	}
	if raw == nil {
		t.Fatal("ParseBounded returned nil for a well-formed document")
	}
}

// ── entry point 1: the host-fed Query path ─────────────────────────────────

func TestDataframeQueryRefusesADeepDocument(t *testing.T) {
	_, ce := dataframe.DecodeSource(deepJSON("source", hostileDepth))
	if ce == nil {
		t.Fatal("a 10 001-deep data source was accepted")
	}
	if ce.Code != dataframe.LimitExceeded {
		t.Errorf("code = %q, want %q — %q says the pipeline is malformed when it is merely deep",
			ce.Code, dataframe.LimitExceeded, ce.Code)
	}
}

// ── entry point 2: the theme manifest ──────────────────────────────────────

func TestThemeManifestRefusesADeepDocument(t *testing.T) {
	_, err := thememanifest.Decode(deepJSON("tokens", hostileDepth))
	if err == nil {
		t.Fatal("a 10 001-deep manifest was accepted")
	}
	de, ok := err.(*wire.DecodeError)
	if !ok || de.Code != wire.CodeLimitExceeded {
		t.Fatalf("err = %v (%T), want a LIMIT_EXCEEDED *wire.DecodeError", err, err)
	}
}

// ── entry point 3: the capability declaration ──────────────────────────────

func TestCapabilityDeclarationRefusesADeepDocument(t *testing.T) {
	_, err := function.DecodeDeclaration(deepJSON("signature", hostileDepth))
	if err == nil {
		t.Fatal("a 10 001-deep capability declaration was accepted")
	}
	de, ok := err.(*wire.DecodeError)
	if !ok || de.Code != wire.CodeLimitExceeded {
		t.Fatalf("err = %v (%T), want a LIMIT_EXCEEDED *wire.DecodeError", err, err)
	}
}

// ── entry point 4: the DAG record envelope ─────────────────────────────────

func TestDagRecordRefusesAnOverlongEnvelopeString(t *testing.T) {
	// Depth was already bounded here (ParseCanonical). The envelope's own
	// STRINGS were not: actor, message and the parent ids are read straight off
	// the parse and never pass through the node or op decoders.
	huge := `{"actor":"` + strings.Repeat("x", wire.MaxStringLength+1) + `"}`
	_, err := dag.DecodeDagRecord(huge)
	if err == nil {
		t.Fatal("a record carrying an unbounded actor string was accepted")
	}
	de, ok := err.(*wire.DecodeError)
	if !ok || de.Code != wire.CodeLimitExceeded {
		t.Fatalf("err = %v (%T), want a LIMIT_EXCEEDED *wire.DecodeError", err, err)
	}
}

func TestDagRecordRefusesADeepDocument(t *testing.T) {
	_, err := dag.DecodeDagRecord(deepJSON("parents", hostileDepth))
	if err == nil {
		t.Fatal("a 10 001-deep record was accepted")
	}
	de, ok := err.(*wire.DecodeError)
	if !ok || de.Code != wire.CodeLimitExceeded {
		t.Fatalf("err = %v (%T), want a LIMIT_EXCEEDED *wire.DecodeError", err, err)
	}
}
