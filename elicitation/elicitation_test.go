package elicitation

import (
	"errors"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func TestOutcomeRoundTrip(t *testing.T) {
	in := `{"$type":"Answered","answer":{"grade":"a","salary":52000},"elicitationId":"elc-full"}`
	o, err := DecodeOutcome(in)
	if err != nil {
		t.Fatalf("DecodeOutcome: %v", err)
	}
	if o.Kind != "Answered" || o.ElicitationID != "elc-full" {
		t.Errorf("decoded outcome wrong: %+v", o)
	}
	got, err := EncodeOutcome(o)
	if err != nil {
		t.Fatalf("EncodeOutcome: %v", err)
	}
	if got != in {
		t.Errorf("round trip diverged:\n got %s\nwant %s", got, in)
	}
}

func TestDeclinedRejectsSmuggledAnswer(t *testing.T) {
	// A Declined outcome cannot carry an answer — default-deny by shape.
	_, err := DecodeOutcome(`{"$type":"Declined","answer":{"x":1},"elicitationId":"e"}`)
	var derr *wire.DecodeError
	if !errors.As(err, &derr) || derr.Code != CodeUndeclaredField {
		t.Errorf("expected UNDECLARED_FIELD, got %v", err)
	}
}

func TestAnswerDocAcceptAndReject(t *testing.T) {
	contract := `"contract":{"fields":[{"name":"rating","nodeId":"n","required":true,"space":{"$type":"intRange","max":5,"min":1},"stateKey":"r"}]}`
	if err := DecodeAnswerDoc(`{"answer":{"rating":4},` + contract + `}`); err != nil {
		t.Errorf("expected acceptance, got %v", err)
	}
	// Out of range → ANSWER_OUT_OF_SPACE at $.answer.rating.
	err := DecodeAnswerDoc(`{"answer":{"rating":9},` + contract + `}`)
	var derr *wire.DecodeError
	if !errors.As(err, &derr) || derr.Code != CodeAnswerOutOfSpace || derr.Path != "$.answer.rating" {
		t.Errorf("expected ANSWER_OUT_OF_SPACE at $.answer.rating, got %v", err)
	}
	// A string where an integer is required → ANSWER_TYPE_MISMATCH.
	err = DecodeAnswerDoc(`{"answer":{"rating":"4"},` + contract + `}`)
	if !errors.As(err, &derr) || derr.Code != CodeAnswerTypeMismatch {
		t.Errorf("expected ANSWER_TYPE_MISMATCH, got %v", err)
	}
}
