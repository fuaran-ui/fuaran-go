package main

import (
	"strings"
	"testing"
)

// referenceWire IS the canonical wire the whole Rosetta parity strip pins for the
// default holes (the same bytes every other host reproduces). A failure here
// means the Go host would show "diverges" on the live page — the regression the
// parity lock exists to catch. Keep it in lockstep with the reference pin in the
// other hosts when a canonical-format rev lands.
const referenceWire = `{"id":"rosetta-root","kind":{"$type":"Box","children":[{"id":"rosetta-strip",` +
	`"kind":{"$type":"Box","children":[{"id":"rosetta-m-a","kind":{"$type":"Metric",` +
	`"label":"Signups","value":{"$type":"Static","value":1280}}},{"id":"rosetta-m-b",` +
	`"kind":{"$type":"Metric","label":"Revenue","value":{"$type":"Static","value":42.5}}},` +
	`{"id":"rosetta-m-c","kind":{"$type":"Metric","label":"Churn %","value":` +
	`{"$type":"Static","value":12.4}}}],"layout":{"$type":"Flex","direction":"Horizontal",` +
	`"wrap":true},"role":"Group"}}],"heading":"Revenue snapshot","layout":` +
	`{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Dashboard"}}`

const defaultHoles = `{"labelA":"Signups","valueA":1280,"labelB":"Revenue","valueB":42.5,"labelC":"Churn %","valueC":12.4}`

func TestEncodesReferenceBytesForDefaultHoles(t *testing.T) {
	got, err := encodeFromHoles(defaultHoles)
	if err != nil {
		t.Fatalf("default holes must encode: %v", err)
	}
	if got != referenceWire {
		t.Errorf("canonical wire mismatch\n got: %s\nwant: %s", got, referenceWire)
	}
}

func TestReflectsEditedHoles(t *testing.T) {
	edited := `{"labelA":"Active users","valueA":9001,"labelB":"Revenue","valueB":42.5,"labelC":"Churn %","valueC":12.4}`
	got, err := encodeFromHoles(edited)
	if err != nil {
		t.Fatalf("edited holes must encode: %v", err)
	}
	if !strings.Contains(got, `"label":"Active users"`) || !strings.Contains(got, `"value":{"$type":"Static","value":9001}`) {
		t.Errorf("edit did not flow through: %s", got)
	}
}

func TestMalformedHolesError(t *testing.T) {
	if _, err := encodeFromHoles("not json"); err == nil {
		t.Error("malformed holes JSON must return an error")
	}
}
