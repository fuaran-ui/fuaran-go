package wire

// Phase 1962 — a multi-select Select carries `values` and no `value`. The corpus
// pins the lenient drop and the single-select MISSING_FIELD; this file pins that
// the drop still DECODES the value first, so a malformed binding there refuses as
// any malformed binding does rather than vanishing unexamined.

import (
	"errors"
	"strings"
	"testing"
)

func multiSelect(value string) string {
	return `{"id":"s","kind":{"$type":"Select","label":"Tags","multiple":true,` +
		`"source":{"$type":"Static","value":[]},` + value +
		`"values":{"$type":"State","key":"tags"}}}`
}

func TestMultiSelectWellFormedValueIsDropped(t *testing.T) {
	want := multiSelect("")
	for _, value := range []string{
		`"value":{"$type":"Static"},`,
		`"value":{"$type":"Static","value":null},`,
		`"value":{"$type":"State","defaultValue":"Auth","key":"category-primary"},`,
	} {
		node, err := DecodeNode(multiSelect(value))
		if err != nil {
			t.Fatalf("DecodeNode(%s): %v", value, err)
		}
		got, err := EncodeNode(node)
		if err != nil {
			t.Fatalf("EncodeNode: %v", err)
		}
		if got != want {
			t.Errorf("value %s was not dropped:\n got %s\nwant %s", value, got, want)
		}
	}
}

func TestMultiSelectMalformedValueStillRefuses(t *testing.T) {
	_, err := DecodeNode(multiSelect(`"value":{"$type":"Nope"},`))
	var derr *DecodeError
	if !errors.As(err, &derr) {
		t.Fatalf("expected a *DecodeError, got %v", err)
	}
	if !strings.HasPrefix(derr.Path, "$.kind.value") {
		t.Errorf("path = %q, want it under $.kind.value", derr.Path)
	}
}

// Phase 2413 — `multiple` is omit-at-default (Phase 2300's one rule): an
// absent `multiple` stays absent, an explicit false re-encodes WITHOUT the
// member (lenient/lenient-2413-select-multiple-false.json in the corpus), and
// true re-encodes as written.
func TestSelectMultipleOmitsAtDefault(t *testing.T) {
	const src = `"source":{"$type":"Static","value":[{"label":"Red","value":"red"}]},`
	single := `{"id":"s","kind":{"$type":"Select","label":"Tags",` + src +
		`"value":{"$type":"Static","value":"red"}}}`
	multi := `{"id":"s","kind":{"$type":"Select","label":"Tags","multiple":true,` + src +
		`"values":{"$type":"State","key":"tags"}}}`
	for _, tc := range []struct{ name, wire, want string }{
		{"absent", single, single},
		{"false", `{"id":"s","kind":{"$type":"Select","label":"Tags","multiple":false,` + src +
			`"value":{"$type":"Static","value":"red"}}}`, single},
		{"true", multi, multi},
	} {
		node, err := DecodeNode(tc.wire)
		if err != nil {
			t.Fatalf("%s: DecodeNode: %v", tc.name, err)
		}
		got, err := EncodeNode(node)
		if err != nil {
			t.Fatalf("%s: EncodeNode: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: not the canonical bytes:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}
