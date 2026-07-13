package conformance

import (
	"errors"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/elicitation"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// The §18 elicitation legs, driven off the manifest decoder ("elicitation" =
// the envelope codec, "elicitation-outcome" = the outcome codec,
// "elicitation-answer" = the answer-conformance doc):
//
//   - elicitation-round-trip → decode + re-encode byte-identical.
//   - elicitation-reject     → decode fails with expectedErrorCode at expectedPath.
//   - elicitation-answer-accept → answer validation passes.
//   - elicitation-answer-reject → answer validation fails with expectedErrorCode/path.

func elicitationRoundTrip(decoder, input string) (string, error) {
	if decoder == "elicitation-outcome" {
		o, err := elicitation.DecodeOutcome(input)
		if err != nil {
			return "", err
		}
		return elicitation.EncodeOutcome(o)
	}
	e, err := elicitation.DecodeElicitation(input)
	if err != nil {
		return "", err
	}
	return elicitation.EncodeElicitation(e)
}

func elicitationReject(decoder, input string) error {
	switch decoder {
	case "elicitation-outcome":
		_, err := elicitation.DecodeOutcome(input)
		return err
	case "elicitation-answer":
		return elicitation.DecodeAnswerDoc(input)
	default:
		_, err := elicitation.DecodeElicitation(input)
		return err
	}
}

func assertRejectCode(t *testing.T, err error, wantCode, wantPath string) {
	t.Helper()
	var derr *wire.DecodeError
	if !errors.As(err, &derr) {
		t.Fatalf("expected a *wire.DecodeError, got %v", err)
	}
	if string(derr.Code) != wantCode {
		t.Errorf("code = %s, want %s", derr.Code, wantCode)
	}
	if wantPath == "" {
		wantPath = "$"
	}
	if !strings.HasPrefix(derr.Path, wantPath) {
		t.Errorf("path = %q, want prefix %q", derr.Path, wantPath)
	}
}

func TestElicitationCorpus(t *testing.T) {
	corpus, m := loadCorpus(t)
	counts := map[string]int{}
	for _, fx := range m.Fixtures {
		fx := fx
		switch fx.Kind {
		case "elicitation-round-trip":
			counts[fx.Kind]++
			t.Run(fx.ID, func(t *testing.T) {
				got, err := elicitationRoundTrip(fx.Decoder, readFixture(t, corpus, fx.InputFile))
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if want := readFixture(t, corpus, fx.ExpectedFile); got != want {
					t.Errorf("not byte-identical: %s", firstDiff(got, want))
				}
			})
		case "elicitation-reject":
			counts[fx.Kind]++
			t.Run(fx.ID, func(t *testing.T) {
				assertRejectCode(t, elicitationReject(fx.Decoder, readFixture(t, corpus, fx.InputFile)),
					fx.ExpectedErrorCode, fx.ExpectedPath)
			})
		case "elicitation-answer-accept":
			counts[fx.Kind]++
			t.Run(fx.ID, func(t *testing.T) {
				if err := elicitation.DecodeAnswerDoc(readFixture(t, corpus, fx.InputFile)); err != nil {
					t.Errorf("expected acceptance, got %v", err)
				}
			})
		case "elicitation-answer-reject":
			counts[fx.Kind]++
			t.Run(fx.ID, func(t *testing.T) {
				assertRejectCode(t, elicitation.DecodeAnswerDoc(readFixture(t, corpus, fx.InputFile)),
					fx.ExpectedErrorCode, fx.ExpectedPath)
			})
		}
	}
	for _, kind := range []string{"elicitation-round-trip", "elicitation-reject", "elicitation-answer-accept", "elicitation-answer-reject"} {
		if counts[kind] == 0 {
			t.Fatalf("manifest declares no %s fixtures", kind)
		}
	}
}
