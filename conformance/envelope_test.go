package conformance

import (
	"errors"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// The §15 versioning-envelope leg: envelope-round-trip decodes + re-renders
// byte-identical (Current known payloads + Behind unknown kinds preserved
// verbatim); envelope-reject refuses a Foreign profile with FOREIGN_PROFILE at
// $.$profile.
func TestEnvelopeCorpus(t *testing.T) {
	corpus, m := loadCorpus(t)
	ranRT, ranReject := 0, 0
	for _, fx := range m.Fixtures {
		switch fx.Kind {
		case "envelope-round-trip":
			ranRT++
			t.Run(fx.ID, func(t *testing.T) {
				env, err := wire.DecodeEnvelope(readFixture(t, corpus, fx.InputFile))
				if err != nil {
					t.Fatalf("DecodeEnvelope: %v", err)
				}
				got, err := wire.EncodeEnvelope(env)
				if err != nil {
					t.Fatalf("EncodeEnvelope: %v", err)
				}
				if want := readFixture(t, corpus, fx.ExpectedFile); got != want {
					t.Errorf("not byte-identical: %s", firstDiff(got, want))
				}
			})
		case "envelope-reject":
			ranReject++
			t.Run(fx.ID, func(t *testing.T) {
				_, err := wire.DecodeEnvelope(readFixture(t, corpus, fx.InputFile))
				var derr *wire.DecodeError
				if !errors.As(err, &derr) {
					t.Fatalf("expected a *wire.DecodeError, got %v", err)
				}
				if string(derr.Code) != fx.ExpectedErrorCode {
					t.Errorf("code = %s, want %s", derr.Code, fx.ExpectedErrorCode)
				}
				wantPath := fx.ExpectedPath
				if wantPath == "" {
					wantPath = "$"
				}
				if len(derr.Path) < len(wantPath) || derr.Path[:len(wantPath)] != wantPath {
					t.Errorf("path = %q, want prefix %q", derr.Path, wantPath)
				}
			})
		}
	}
	if ranRT == 0 || ranReject == 0 {
		t.Fatalf("manifest declares no envelope fixtures (rt=%d reject=%d)", ranRT, ranReject)
	}
}
