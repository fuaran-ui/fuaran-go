package wire

// WIRE_FORMAT.md §21 resource limits.
//
// Go's defect here was conformance rather than safety, and the tests are shaped
// by that. Unlike the sibling hosts this one never crashed — goroutine stacks
// grow and encoding/json caps nesting first — so the interesting assertions are
// that a document past the limit is REFUSED (it used to decode happily at 1 000
// levels against a limit of 24) and that the refusal carries the RIGHT CODE
// (it used to say INVALID_JSON, which §21.2 rule 2 forbids for a well-formed
// document).
//
// Every bound is asserted from BOTH sides of its boundary. A limit that refused
// everything would pass a refusal-only suite, and rule 1 is explicit that
// refusing a conformant document is non-conformance rather than caution.

import (
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	boxOpen = `{"id":"n","kind":{"$type":"Box","role":"Group",` +
		`"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"children":[`
	boxLeaf = `{"id":"leaf","kind":{"$type":"Box","role":"Group",` +
		`"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"children":[]}}`
)

// nestedNodes builds a chain of n nested Box nodes, innermost an empty Box.
func nestedNodes(n int) string {
	return strings.Repeat(boxOpen, n-1) + boxLeaf + strings.Repeat("]}}", n-1)
}

// nestedBatch builds a chain of n nested Batch ops, innermost a RemoveNode.
func nestedBatch(n int) string {
	return strings.Repeat(`{"$type":"Batch","ops":[`, n-1) +
		`{"$type":"RemoveNode","target":"x"}` +
		strings.Repeat("]}", n-1)
}

func decodeErr(t *testing.T, err error) *DecodeError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a refusal, got a successful decode")
	}
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("expected *DecodeError, got %T: %v", err, err)
	}
	return de
}

// ── the node-depth bound ────────────────────────────────────────────────────

func TestAcceptsTreeAtExactlyMaxNodeDepth(t *testing.T) {
	// Rule 1 — refusing a conformant document is non-conformance, not caution.
	if _, err := DecodeNode(nestedNodes(MaxNodeDepth)); err != nil {
		t.Fatalf("a tree at exactly MaxNodeDepth must decode, got: %v", err)
	}
}

func TestRefusesTreeOneLevelPastMaxNodeDepth(t *testing.T) {
	de := decodeErr(t, mustFail(DecodeNode(nestedNodes(MaxNodeDepth+1))))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("want %s, got %s (%s)", CodeLimitExceeded, de.Code, de.Message)
	}
	if !strings.Contains(de.Message, strconv.Itoa(MaxNodeDepth)) {
		t.Fatalf("the message should name the bound, got: %s", de.Message)
	}
}

func TestDeepTreeIsRefusedAsLimitNotAsInvalidJSON(t *testing.T) {
	// The measured defect: at 1 000 levels this host used to DECODE, and past
	// its own cap it reported INVALID_JSON — rule 2's exact prohibition, because
	// the document is well-formed and merely too deep.
	for _, depth := range []int{1000, 5000, 20000} {
		de := decodeErr(t, mustFail(DecodeNode(nestedNodes(depth))))
		if de.Code != CodeLimitExceeded {
			t.Fatalf("depth %d: want %s, got %s", depth, CodeLimitExceeded, de.Code)
		}
	}
}

// ── the op-decoder axis ─────────────────────────────────────────────────────

func TestAcceptsNestedBatchAtExactlyMaxNodeDepth(t *testing.T) {
	if _, err := DecodeOp(nestedBatch(MaxNodeDepth)); err != nil {
		t.Fatalf("nested Batch at exactly the limit must decode, got: %v", err)
	}
}

func TestRefusesNestedBatchPastTheLimit(t *testing.T) {
	de := decodeErr(t, mustFailObj(DecodeOp(nestedBatch(MaxNodeDepth+1))))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("want %s, got %s (%s)", CodeLimitExceeded, de.Code, de.Message)
	}
}

func TestOpAxisIsCountedSeparatelyFromTheNodeAxis(t *testing.T) {
	// A Batch chain at the op limit whose payload node is at the node limit must
	// decode. If the two shared one counter this would breach at the sum — which
	// is the plausible wrong implementation every other assertion here would
	// still pass.
	inner := `{"$type":"ReplaceRoot","node":` + nestedNodes(MaxNodeDepth) + `}`
	doc := strings.Repeat(`{"$type":"Batch","ops":[`, MaxNodeDepth-1) +
		inner + strings.Repeat("]}", MaxNodeDepth-1)
	if _, err := DecodeOp(doc); err != nil {
		t.Fatalf("the two axes must be counted separately, got: %v", err)
	}
}

// ── the syntactic and linear bounds ─────────────────────────────────────────

func TestRefusesBareNestingPastMaxJSONDepth(t *testing.T) {
	n := MaxJSONDepth + 1
	de := decodeErr(t, mustFail(DecodeNode(strings.Repeat("[", n)+strings.Repeat("]", n))))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("want %s, got %s", CodeLimitExceeded, de.Code)
	}
}

func TestBareNestingAtExactlyMaxJSONDepthFailsOnShapeNotOnTheLimit(t *testing.T) {
	// Not a valid node, so it must fail — but on SHAPE, not on the limit. This
	// is what stops the syntactic guard sitting one level too tight, which a
	// refusal-only test could never detect.
	n := MaxJSONDepth
	de := decodeErr(t, mustFail(DecodeNode(strings.Repeat("[", n)+strings.Repeat("]", n))))
	if de.Code == CodeLimitExceeded {
		t.Fatalf("a document at exactly the limit must not be refused as over-limit")
	}
}

func TestStillCallsGenuinelyMalformedInputInvalidJSON(t *testing.T) {
	// Non-vacuity for the classification: it must distinguish, not relabel.
	de := decodeErr(t, mustFail(DecodeNode("}{ not json")))
	if de.Code != CodeInvalidJSON {
		t.Fatalf("want %s, got %s", CodeInvalidJSON, de.Code)
	}
}

func TestRefusesAnOverLongArray(t *testing.T) {
	doc := "[" + strings.Repeat("1,", MaxArrayLength) + "1]"
	de := decodeErr(t, mustFail(DecodeNode(doc)))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("want %s, got %s", CodeLimitExceeded, de.Code)
	}
}

func TestRefusesAnOverLongString(t *testing.T) {
	doc := `{"id":"x","kind":{"$type":"Text","text":"` +
		strings.Repeat("a", MaxStringLength+1) + `"}}`
	de := decodeErr(t, mustFail(DecodeNode(doc)))
	if de.Code != CodeLimitExceeded {
		t.Fatalf("want %s, got %s", CodeLimitExceeded, de.Code)
	}
}

// ── the walk state is per-call, not shared ──────────────────────────────────

func TestConcurrentDecodesDoNotShareCounters(t *testing.T) {
	// This is the test that justifies threading *walkState through the decoder
	// rather than using package-level counters like the single-threaded sibling
	// hosts. Under `go test -race` a shared counter fails here outright; without
	// -race it would still corrupt the bound, so the assertion is on the RESULT
	// and not merely on the absence of a race report.
	//
	// Half the goroutines decode a conformant tree and must succeed; half decode
	// an over-limit one and must be refused. A shared counter makes the two
	// interfere in both directions.
	const workers = 64
	var wg sync.WaitGroup
	errs := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if _, err := DecodeNode(nestedNodes(MaxNodeDepth)); err != nil {
					errs <- "conformant tree refused under concurrency: " + err.Error()
				}
				return
			}
			if _, err := DecodeNode(nestedNodes(MaxNodeDepth + 1)); err == nil {
				errs <- "over-limit tree accepted under concurrency"
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestARefusedDecodeDoesNotPoisonTheNext(t *testing.T) {
	if _, err := DecodeNode(nestedNodes(MaxNodeDepth + 1)); err == nil {
		t.Fatal("expected the over-limit tree to be refused")
	}
	if _, err := DecodeNode(nestedNodes(MaxNodeDepth)); err != nil {
		t.Fatalf("a conformant tree must still decode afterwards, got: %v", err)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func mustFail(_ Node, err error) error { return err }

func mustFailObj(_ Obj, err error) error { return err }
