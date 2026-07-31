package wire

// Phase 750 — CellKindErased.TonedPill, the one cell kind that survives the wire.
//
// The corpus pins the canonical round-trip, the three §16 shorthands and the reject's
// code + path. This file pins what the corpus deliberately does not:
//
//   - the THIRD tone-map alias (`tones` — the fixture exercises `toneMap`, and a host
//     that wired only the one it was shown is non-conformant in a way no fixture would
//     catch);
//   - the didactic CONTENT of the refusal, not merely its code and path — that it names
//     the offending key and teaches the seven legal tones is the entire reason that
//     fixture is in the corpus;
//   - the omit rule at both branches;
//   - that a closure `Pill` is left alone, which is the other half of the coercion.

import (
	"errors"
	"strings"
	"testing"
)

// column wraps a cell kind in the smallest grid document that carries it.
func column(kind string) string {
	return `{"id":"g1","kind":{"$type":"DataGrid","columns":[{"field":"status","kind":` +
		kind + `,"label":"Status"}],"source":{"$type":"Static","value":[]}}}`
}

// normalisesTo asserts that a grid document carrying `given` as its cell kind
// round-trips to the same document carrying `want`. `column`'s surrounding keys are
// already in canonical order, so the expected bytes are just column(want).
func normalisesTo(t *testing.T, given, want string) {
	t.Helper()
	node, err := DecodeNode(column(given))
	if err != nil {
		t.Fatalf("DecodeNode(%s): %v", given, err)
	}
	got, err := EncodeNode(node)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	if got != column(want) {
		t.Errorf("cell kind %s did not normalise:\n got %s\nwant %s", given, got, column(want))
	}
}

func TestEveryToneMapAliasNormalisesToMap(t *testing.T) {
	for _, alias := range []string{"map", "toneMap", "tones"} {
		normalisesTo(t,
			`{"$type":"TonedPill","field":"status","`+alias+`":{"Delayed":"Warning"}}`,
			`{"$type":"TonedPill","field":"status","map":{"Delayed":"Warning"}}`)
	}
}

func TestCanonicalToneMapWinsOverAnAlias(t *testing.T) {
	normalisesTo(t,
		`{"$type":"TonedPill","field":"status","map":{"Delayed":"Warning"},"toneMap":{"Delayed":"Critical"}}`,
		`{"$type":"TonedPill","field":"status","map":{"Delayed":"Warning"}}`)
}

func TestPillTagCarryingAToneMapCoercesToTonedPill(t *testing.T) {
	normalisesTo(t,
		`{"$type":"Pill","field":"status","map":{"Delayed":"Warning"}}`,
		`{"$type":"TonedPill","field":"status","map":{"Delayed":"Warning"}}`)
}

func TestAClosurePillIsUntouched(t *testing.T) {
	// The coercion keys off the tone map, so an ordinary closure Pill — which can
	// never carry one — still decodes to the closure sentinels.
	normalisesTo(t,
		`{"$type":"Pill","labelFn":"<closure>","toneFn":"<closure>"}`,
		`{"$type":"Pill","labelFn":"<closure>","toneFn":"<closure>"}`)
}

func TestTonedPillDefaultOmitsAtIdentity(t *testing.T) {
	cases := []struct {
		name  string
		given string
		want  string
	}{
		{"explicit identity omits", `"default":"Default",`, ""},
		{"aliased identity normalises then omits", `"default":"Neutral",`, ""},
		{"a real default survives", `"default":"Subdued",`, `"default":"Subdued",`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			normalisesTo(t,
				`{"$type":"TonedPill",`+c.given+`"field":"s","map":{"a":"Info"}}`,
				`{"$type":"TonedPill",`+c.want+`"field":"s","map":{"a":"Info"}}`)
		})
	}
}

func TestToneAliasesApplyInsideTheMap(t *testing.T) {
	normalisesTo(t,
		`{"$type":"TonedPill","field":"s","map":{"a":"Danger","b":"Positive","c":"Neutral"}}`,
		`{"$type":"TonedPill","field":"s","map":{"a":"Critical","b":"Success","c":"Default"}}`)
}

func TestTonedPillRejects(t *testing.T) {
	cases := []struct {
		name string
		kind string
		code DecodeErrorCode
		path string
	}{
		{"unknown tone-map value", `{"$type":"TonedPill","field":"status","map":{"Delayed":"Urgent"}}`,
			CodeUnknownDUCase, "$.kind.columns[0].kind.map.Delayed"},
		{"non-string tone-map value", `{"$type":"TonedPill","field":"s","map":{"a":7}}`,
			CodeWrongType, "$.kind.columns[0].kind.map.a"},
		{"field missing", `{"$type":"TonedPill","map":{"a":"Info"}}`,
			CodeMissingField, "$.kind.columns[0].kind.field"},
		{"map missing", `{"$type":"TonedPill","field":"s"}`,
			CodeMissingField, "$.kind.columns[0].kind.map"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeNode(column(c.kind))
			var derr *DecodeError
			if !errors.As(err, &derr) {
				t.Fatalf("expected a *DecodeError, got %v", err)
			}
			if derr.Code != c.code {
				t.Errorf("code = %s, want %s", derr.Code, c.code)
			}
			if derr.Path != c.path {
				t.Errorf("path = %q, want %q", derr.Path, c.path)
			}
		})
	}
}

func TestUnknownToneMapValueIsRefusedDidactically(t *testing.T) {
	_, err := DecodeNode(column(`{"$type":"TonedPill","field":"status","map":{"Delayed":"Urgent"}}`))
	var derr *DecodeError
	if !errors.As(err, &derr) {
		t.Fatalf("expected a *DecodeError, got %v", err)
	}
	// The offending KEY and value, in the terms the author wrote them — "one of your
	// tones is wrong" is not actionable when the map has nine entries.
	for _, want := range []string{"Delayed", "Urgent"} {
		if !strings.Contains(derr.Message, want) {
			t.Errorf("message %q does not name %q", derr.Message, want)
		}
	}
	// All seven legal names, so the author can fix it from the message alone.
	for _, tone := range []string{"Default", "Subdued", "Brand", "Success", "Warning", "Critical", "Info"} {
		if !strings.Contains(derr.ExpectedShape, tone) {
			t.Errorf("expected-shape %q does not teach %q", derr.ExpectedShape, tone)
		}
	}
}
