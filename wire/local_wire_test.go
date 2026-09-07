package wire

import "testing"

// WIRE_FORMAT.md Section 3.3.3 — `Binding.Local`'s declarative half.
//
// The corpus already carries the round-trip and both reject vectors, and
// `conformance` runs them; what is pinned HERE is the pairing a manifest-driven
// sweep cannot express — each refusal beside the accepting neighbour that would
// otherwise let a refuse-everything decoder pass — plus the presence rule for
// `onCommit`, which is a re-encode property no reject fixture reaches.

const localDebounce = `{"id":"form-local-debounce","kind":{"$type":"Form","fields":[{"id":"email-input",` +
	`"kind":{"$type":"Text","onChange":"<closure>","value":{"$type":"Local","flushOn":{"$type":"OnDebounce",` +
	`"milliseconds":250},"format":"<closure>","initialFrom":{"$type":"Static","value":"draft@example.com"},` +
	`"onCommit":"<closure>","parse":"<closure>"}},"label":"Email","required":true}],"onSubmit":` +
	`{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`

const localDeclared = `{"id":"form-local-declared","kind":{"$type":"Form","fields":[{"id":"unit-price",` +
	`"kind":{"$type":"Number","value":{"$type":"Local","codec":{"$type":"Number","decimals":2},` +
	`"commitTo":"order.unitPrice","flushOn":{"$type":"OnBlur"},"format":"<closure>","initialFrom":` +
	`{"$type":"State","defaultValue":0,"key":"order.unitPrice"},"parse":"<closure>"}},"label":"Unit price",` +
	`"required":false}],"onSubmit":{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`

const localCurrencyCodec = `{"id":"f","kind":{"$type":"Form","fields":[{"id":"amount","kind":{"$type":"Number",` +
	`"value":{"$type":"Local","codec":{"$type":"Currency","isoCode":"GBP"},"commitTo":"order.amount",` +
	`"flushOn":{"$type":"OnBlur"},"format":"<closure>","initialFrom":{"$type":"State","defaultValue":0,` +
	`"key":"order.amount"},"parse":"<closure>"}},"label":"Amount","required":false}],"onSubmit":` +
	`{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`

const localBothDestinations = `{"id":"f","kind":{"$type":"Form","fields":[{"id":"email","kind":{"$type":"Text",` +
	`"value":{"$type":"Local","commitTo":"form.email","flushOn":{"$type":"OnBlur"},"format":"<closure>",` +
	`"initialFrom":{"$type":"Static","value":"a@b.c"},"onCommit":"<closure>","parse":"<closure>"}},` +
	`"label":"Email","required":false}],"onSubmit":{"$type":"Chain","ops":[]},"submitLabel":"Save"}}`

// A declared buffer round-trips both new members, and the closure sentinel is
// emitted only when the document wrote one. The second half is the interesting
// one: this arm used to emit `onCommit` unconditionally, which was invisible
// while every `Local` carried a closure and becomes a byte divergence the moment
// one declares `commitTo` instead.
func TestLocalDeclaredRoundTrip(t *testing.T) {
	for _, doc := range []string{localDeclared, localDebounce} {
		node, err := DecodeNode(doc)
		if err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		got, encErr := EncodeNode(node)
		if encErr != nil {
			t.Fatalf("encode failed: %v", encErr)
		}
		if got != doc {
			t.Fatalf("re-encode diverged:\n want %s\n  got %s", doc, got)
		}
	}
}

// A codec case with no total, locale-independent inverse is refused at the
// codec's own path — and a `Number` codec is NOT, which is what keeps the
// refusal about the case rather than about codecs.
func TestLocalCodecWithoutInverseRefused(t *testing.T) {
	_, err := DecodeNode(localCurrencyCodec)
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("a Currency codec has no parse; admitting it would format one way and read another (err = %v)", err)
	}
	if de.Code != CodeWrongType {
		t.Fatalf("code = %v, want %v", de.Code, CodeWrongType)
	}
	if de.Path != "$.kind.fields[0].kind.value.codec" {
		t.Fatalf("path = %q, want the codec slot", de.Path)
	}
	if _, e := DecodeNode(localDeclared); e != nil {
		t.Fatalf("the admitted codec was refused: %v", e)
	}
}

// Two commit destinations are refused at the declarative half's path — and
// either one ALONE decodes, so the refusal is the pair.
func TestLocalTwoCommitDestinationsRefused(t *testing.T) {
	_, err := DecodeNode(localBothDestinations)
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("two destinations from one document would commit to two places (err = %v)", err)
	}
	if de.Code != CodeWrongType {
		t.Fatalf("code = %v, want %v", de.Code, CodeWrongType)
	}
	if de.Path != "$.kind.fields[0].kind.value.commitTo" {
		t.Fatalf("path = %q, want the commitTo slot", de.Path)
	}
	if _, e := DecodeNode(localDebounce); e != nil {
		t.Fatalf("onCommit alone was refused: %v", e)
	}
	if _, e := DecodeNode(localDeclared); e != nil {
		t.Fatalf("commitTo alone was refused: %v", e)
	}
}
