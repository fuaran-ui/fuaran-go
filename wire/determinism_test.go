package wire

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/fuaran-ui/fuaran-go/canonical"
)

// WIRE_FORMAT.md §20 decode determinism and §21.6's string unit, asserted where
// the corpus cannot reach.
//
// The corpus pins each §20 row with a reject fixture and the conformance suite
// runs them. What it cannot pin is the §21.6 boundary — its vectors are
// deliberately host-local, a megabyte of padding committed to a shared
// repository to assert one integer comparison being a poor trade — nor the
// canonical key order for a document no fixture happens to carry, nor the
// corrected twin beside each refusal. A refusal-only suite passes on a decoder
// that refuses everything, so every case below is asserted from both sides.

func decodeOK(t *testing.T, doc string) {
	t.Helper()
	if _, err := DecodeNode(doc); err != nil {
		t.Fatalf("expected %.60q to decode, got %v", doc, err)
	}
}

func decodeRefuses(t *testing.T, doc string, want DecodeErrorCode) {
	t.Helper()
	_, err := DecodeNode(doc)
	if err == nil {
		t.Fatalf("expected %.60q to be refused, but it decoded", doc)
	}
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("expected a *DecodeError, got %T", err)
	}
	if de.Code != want {
		t.Fatalf("expected %s for %.60q, got %s at %s: %s", want, doc, de.Code, de.Path, de.Message)
	}
}

func markdownNode(text string) string {
	return `{"id":"markdown-1","kind":{"$type":"Markdown","text":"` + text + `"}}`
}

// ── §20.2 row 6 — unpaired surrogates, and the pair that must still decode ───

func TestUnpairedSurrogateEscapesAreRefused(t *testing.T) {
	cases := map[string]string{
		"lone high":  `\uD83D`,
		"lone low":   `\uDE00`,
		"split pair": `\uD83D x \uDE00`,
		// A high half followed by a NON-low \u escape: a check written as "a
		// high must be followed by another \u escape" passes this and leaves the
		// class open.
		"high then BMP escape": `\uD83DA`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			decodeRefuses(t, markdownNode(text), CodeInvalidJSON)
		})
	}
}

func TestRawSurrogateCodeUnitIsRefused(t *testing.T) {
	// The WTF-8 encoding of U+D800. Valid UTF-8 never produces it, but a Go
	// string is a byte sequence, so it would otherwise ride through untouched.
	decodeRefuses(t, markdownNode("x\xed\xa0\x80y"), CodeInvalidJSON)
}

func TestWellFormedSurrogatePairStillDecodes(t *testing.T) {
	// The corrected twin. Both halves, adjacent, denoting U+1F600.
	decodeOK(t, markdownNode(`Updated 😀 hourly.`))
}

func TestAstralCharacterLiteralStillDecodes(t *testing.T) {
	decodeOK(t, markdownNode("Updated \U0001F600 hourly."))
}

// ── §20.2 row 1 — a repeated member ─────────────────────────────────────────

func TestRepeatedMemberIsRefused(t *testing.T) {
	decodeRefuses(t, `{"id":"markdown-1","id":"smuggled","kind":{"$type":"Markdown","text":"x"}}`, CodeInvalidJSON)
}

func TestRepeatedMemberIsRefusedNestedAndInsideAnArray(t *testing.T) {
	// The row binds every object, not the root. A detector that tracked one
	// key set for the whole document, or reset it at the wrong boundary, passes
	// the root case and fails here.
	decodeRefuses(t,
		`{"id":"n","kind":{"$type":"Markdown","text":"x","text":"y"}}`,
		CodeInvalidJSON)
	decodeRefuses(t,
		`{"id":"n","kind":{"$type":"Box","role":"Group",`+
			`"layout":{"$type":"Flex","direction":"Vertical","wrap":false},`+
			`"children":[{"id":"c","id":"c2","kind":{"$type":"Markdown","text":"x"}}]}}`,
		CodeInvalidJSON)
}

func TestTheSameKeyInSiblingObjectsIsNotARepeat(t *testing.T) {
	// Non-vacuity for the detector: two objects each carrying "id" is the
	// ordinary shape of every tree, and a key set that was never scoped to its
	// own object would refuse it.
	decodeOK(t, `{"id":"n","kind":{"$type":"Box","role":"Group",`+
		`"layout":{"$type":"Flex","direction":"Vertical","wrap":false},`+
		`"children":[{"id":"a","kind":{"$type":"Markdown","text":"x"}},`+
		`{"id":"b","kind":{"$type":"Markdown","text":"y"}}]}}`)
}

// ── §7.1 — the integer-slot accept set ──────────────────────────────────────

func skeleton(rows string) string {
	return `{"id":"skel-1","kind":{"$type":"Skeleton","rows":` + rows + `}}`
}

func TestIntegerSlotAcceptsAnIntegralFloatSpelling(t *testing.T) {
	// `3.0` and `3` denote the same integer. Refusing the first is refusing a
	// document whose intent is unambiguous, for its spelling.
	node, err := DecodeNode(skeleton("3.0"))
	if err != nil {
		t.Fatalf("expected 3.0 to decode at an integer slot: %v", err)
	}
	got, encErr := EncodeNode(node)
	if encErr != nil {
		t.Fatal(encErr)
	}
	if !strings.Contains(got, `"rows":3`) || strings.Contains(got, `"rows":3.0`) {
		t.Fatalf("expected 3.0 to canonicalise to the integer spelling, got %s", got)
	}
}

func TestIntegerSlotRefusesAFraction(t *testing.T) {
	// Truncating to 2 discards a value at a slot the author chose to type as an
	// integer, which is the behaviour §7.1 retires rather than deprecates.
	decodeRefuses(t, skeleton("2.5"), CodeWrongType)
}

func TestIntegerSlotRefusesOutOfRange(t *testing.T) {
	for _, lit := range []string{"1e10", "3000000000", "-3000000000", "1e400", "9223372036854775808"} {
		t.Run(lit, func(t *testing.T) { decodeRefuses(t, skeleton(lit), CodeWrongType) })
	}
}

func TestIntegerSlotAcceptsTheRangeBoundaries(t *testing.T) {
	// The other side of the same bound.
	for _, lit := range []string{"-2147483648", "2147483647", "0"} {
		t.Run(lit, func(t *testing.T) { decodeOK(t, skeleton(lit)) })
	}
}

func TestIntegerSlotRefusesASentinelString(t *testing.T) {
	// §7 widens FLOAT slots to the three sentinels and nothing else. An integer
	// has no non-finite form, so this is the case that makes the two accept sets
	// distinguishable rather than merely stated.
	decodeRefuses(t, skeleton(`"NaN"`), CodeWrongType)
	decodeRefuses(t, skeleton("true"), CodeWrongType)
}

// ── §2 rule 5 — where integer identity stops ────────────────────────────────

func customProps(props string) string {
	return `{"id":"c","kind":{"$type":"Custom","componentId":"x","moduleId":"m","props":` + props + `}}`
}

func TestPayloadIntegerBeyondInt53DecodesAsADouble(t *testing.T) {
	node, err := DecodeNode(customProps(`{"beyond":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	got, encErr := EncodeNode(node)
	if encErr != nil {
		t.Fatal(encErr)
	}
	// 9007199254740993 has no double, so a conformant decoder accepts the
	// rounding to 9007199254740992 — the value every host that routes numbers
	// through a double already produced.
	if !strings.Contains(got, "9007199254740992") {
		t.Fatalf("expected the rounded value, got %s", got)
	}
}

func TestPayloadIntegerAtTheBoundaryKeepsItsIdentity(t *testing.T) {
	node, err := DecodeNode(customProps(`{"boundary":9007199254740991}`))
	if err != nil {
		t.Fatal(err)
	}
	got, encErr := EncodeNode(node)
	if encErr != nil {
		t.Fatal(encErr)
	}
	if !strings.Contains(got, `"boundary":9007199254740991`) {
		t.Fatalf("expected the boundary value to keep the integer layout, got %s", got)
	}
}

func TestEncoderNeverEmitsAnIntegerTokenBeyondInt53(t *testing.T) {
	// The value cannot be reached by decoding — a conformant decoder has already
	// widened it — but this host's Int carries an int64, so a value constructed
	// in-process can hold one. A conformant ENCODER must not emit it.
	//
	// 10^17, not PayloadIntMax+1: the two layouts COINCIDE just past the
	// boundary, because rule 5 uses fixed-point notation up to a base-10 exponent
	// of 16 and 2^53 has exponent 15. A test written at the boundary would
	// therefore assert nothing at all. The layouts first diverge at exponent 17,
	// where the float form is `1E+17`.
	const beyond = int64(100000000000000000)
	var sb strings.Builder
	appendInt(&sb, beyond)
	if got := sb.String(); got != "1E+17" {
		t.Fatalf("expected the float layout 1E+17 for %d, got %s", beyond, got)
	}

	// And the other side: a value inside the range keeps the integer layout.
	var inside strings.Builder
	appendInt(&inside, PayloadIntMax)
	if got := inside.String(); got != strconv.FormatInt(PayloadIntMax, 10) {
		t.Fatalf("expected the integer layout for %d, got %s", int64(PayloadIntMax), got)
	}
}

// ── §2 rule 2 — "Ordinal" is UTF-16 code-unit order ─────────────────────────

func TestKeyOrderIsUTF16AndNotCodePoint(t *testing.T) {
	// The discriminator: an astral key (a surrogate pair beginning in
	// U+D800–U+DBFF) sorts BELOW a U+E000 key under UTF-16 and ABOVE it under
	// code-point or UTF-8-byte order, which is what Go's sort.Strings gives.
	astral := "\U0001D11E"
	privateUse := ""
	if !canonical.LessUTF16(astral, privateUse) {
		t.Fatalf("expected the astral key to sort below the private-use key in UTF-16 order")
	}
	if astral < privateUse {
		t.Fatalf("the two orders no longer disagree, so this test no longer discriminates")
	}
	units := utf16.Encode([]rune(astral))
	if len(units) != 2 || units[0] < 0xD800 || units[0] > 0xDBFF {
		t.Fatalf("expected a surrogate pair for the astral key, got %v", units)
	}
}

// ── §21.6 — the string bound counts CODE POINTS ─────────────────────────────
//
// Four cases, and the astral pair is the one a byte-counting or UTF-16-counting
// host fails: a BMP string of `max` characters is inside every candidate unit,
// so a host that ports only the first two has not changed its unit and will not
// notice.

func TestBMPStringAtExactlyTheLimitDecodes(t *testing.T) {
	decodeOK(t, markdownNode(strings.Repeat("a", MaxStringLength)))
}

func TestBMPStringOneCodePointOverIsRefused(t *testing.T) {
	decodeRefuses(t, markdownNode(strings.Repeat("a", MaxStringLength+1)), CodeLimitExceeded)
}

func TestAstralStringAtExactlyTheLimitDecodes(t *testing.T) {
	// MaxStringLength astral characters: that many code points, twice as many
	// UTF-16 units, four times as many UTF-8 bytes. §21.2 rule 1 requires this
	// document to be ACCEPTED, and a byte-counting host refuses it.
	decodeOK(t, markdownNode(strings.Repeat("\U0001D11E", MaxStringLength)))
}

func TestAstralStringOneCodePointOverIsRefused(t *testing.T) {
	decodeRefuses(t, markdownNode(strings.Repeat("\U0001D11E", MaxStringLength+1)), CodeLimitExceeded)
}
