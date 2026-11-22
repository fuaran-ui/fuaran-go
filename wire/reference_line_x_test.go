package wire

import (
	"errors"
	"testing"
)

// Phase 2654 — ChartAnnotation.ReferenceLineX shares ReferenceLine's finite-value
// narrowing at decode.
//
// The x reference line names a place on a continuous x axis exactly as
// ReferenceLine names one on the value axis, and a non-finite value names none:
// it would enter the x domain and take every tick and mark to NaN with it. The
// reference host refuses it at decode, as WRONG_TYPE at the value slot, in the
// same arm as ReferenceLine. These tests hold this host to that until the corpus
// carries a reject vector for the case.

func referenceLineXChart(value string) string {
	return `{"id":"c1","kind":{"$type":"Chart","annotations":[{"$type":"ReferenceLineX","value":` + value +
		`}],"kind":"Scatter","source":{"$type":"Static","value":[]},"xField":"x","yFields":["y"]}}`
}

func TestReferenceLineXFiniteValueRoundTrips(t *testing.T) {
	src := referenceLineXChart("50")
	if got := reencodeChart(t, src); got != src {
		t.Errorf("a finite x reference line must round-trip:\n got %s\nwant %s", got, src)
	}
}

func TestReferenceLineXNonFiniteValueIsRefused(t *testing.T) {
	for _, sentinel := range []string{`"NaN"`, `"Infinity"`, `"-Infinity"`} {
		_, err := DecodeNode(referenceLineXChart(sentinel))
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Fatalf("value %s: expected a decode refusal, got %v", sentinel, err)
		}
		if de.Code != CodeWrongType || de.Path != "$.kind.annotations[0].value" {
			t.Errorf("value %s: got %s at %s, want WRONG_TYPE at $.kind.annotations[0].value", sentinel, de.Code, de.Path)
		}
	}
}
