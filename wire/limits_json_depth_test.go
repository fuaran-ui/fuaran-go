package wire

// Phase 2721 — §21.1 counts every `{` and `[` toward JSON depth and nothing
// else, so a scalar at the bottom of a container MaxJSONDepth deep sits AT the
// bound. The shared shape guard (checkShape) used to test every frame, scalars
// included, and so refused that document on every reader it serves. These pin
// the fix on readers other than the theme manifest: ParseBounded's own callers
// and the node decoder. The fix only loosens: one past the bound is still
// refused, by name.

import (
	"strings"
	"testing"
)

// arraysAroundScalar is `depth` nested arrays with one scalar at the bottom.
func arraysAroundScalar(depth int) string {
	return strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
}

func TestParseBoundedAcceptsAScalarAtTheBottomOfMaxJSONDepth(t *testing.T) {
	if _, err := ParseBounded(arraysAroundScalar(MaxJSONDepth)); err != nil {
		t.Fatalf("a document at exactly MaxJSONDepth was refused (§21.2 rule 1): %v", err)
	}
	de := decodeErr(t, mustFailRaw(ParseBounded(arraysAroundScalar(MaxJSONDepth+1))))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("one past MaxJSONDepth: want %s, got %s", CodeLimitExceeded, de.Code)
	}
}

func TestNodeDecodeFailsOnShapeNotOnTheLimitForAScalarAtMaxJSONDepth(t *testing.T) {
	// Not a valid node, so it must fail — on SHAPE. Before the fix the node
	// decoder's shape guard called it over-limit.
	de := decodeErr(t, mustFail(DecodeNode(arraysAroundScalar(MaxJSONDepth))))
	if de.Code == CodeLimitExceeded {
		t.Fatalf("a document at exactly MaxJSONDepth must not be refused as over-limit: %s", de.Message)
	}
	de = decodeErr(t, mustFail(DecodeNode(arraysAroundScalar(MaxJSONDepth+1))))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("one past MaxJSONDepth: want %s, got %s", CodeLimitExceeded, de.Code)
	}
}

func TestCheckDocumentBytesRefusesOnePastTheBoundByName(t *testing.T) {
	if err := CheckDocumentBytes(strings.Repeat(" ", MaxDocumentBytes)); err != nil {
		t.Fatalf("a document of exactly MaxDocumentBytes was refused: %v", err)
	}
	de := decodeErr(t, CheckDocumentBytes(strings.Repeat(" ", MaxDocumentBytes+1)))
	if de.Code != CodeLimitExceeded || de.Path != "$" || !strings.Contains(de.Message, "MaxDocumentBytes") {
		t.Fatalf("want LIMIT_EXCEEDED at $ naming MaxDocumentBytes, got %s at %s: %s", de.Code, de.Path, de.Message)
	}
}

func mustFailRaw(_ any, err error) error { return err }
