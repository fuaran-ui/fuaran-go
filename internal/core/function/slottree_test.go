// The explicit "slotTree" value space (Phase 1860, from fuaran-core#229).
//
// The reference's capability codec writes an explicit "slotTree" space only
// when a slot's space disagrees with its constraint (or a non-slot hole ranges
// over trees); an ordinary slotted signature stays byte-identical. This host
// must DECODE that encoding — not refuse it as an unknown space — re-encode it
// to the same bytes, and validate arguments against it as the reference does.
// The declaration below is the reference's own canonical encoding of the same
// capability; the other two hosts pin the same bytes.
package function

import (
	"encoding/json"
	"testing"
)

const slotTreeDecl = `{"$type":"capability","determinism":"random","id":"cap-tree","placement":{"$type":"server"},"signature":{"effect":{"determinism":"random","host":"readsHost"},"holes":[{"addr":"body","kind":"slot","name":"body","required":true,"slotKind":"Layout","space":{"$type":"slotTree"}},{"addr":"chart","kind":"value","name":"chart","required":false,"space":{"$type":"slotTree","slotKind":"Chart"}}],"name":"tree"}}`

func decodeSlotTreeDecl(t *testing.T) Capability {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(slotTreeDecl), &raw); err != nil {
		t.Fatalf("the fixture is not JSON: %v", err)
	}
	c, err := DecodeDeclarationParsed(raw)
	if err != nil {
		t.Fatalf("a declaration carrying an explicit slotTree space was refused: %v", err)
	}
	return c
}

func TestSlotTreeSpaceDecodes(t *testing.T) {
	c := decodeSlotTreeDecl(t)
	body, chart := c.Signature.Holes[0].Space, c.Signature.Holes[1].Space
	if body == nil || body.Kind != "slotTree" || body.SlotKind != "" {
		t.Errorf("body: got %+v, want an unconstrained slotTree space", body)
	}
	if chart == nil || chart.Kind != "slotTree" || chart.SlotKind != "Chart" {
		t.Errorf("chart: got %+v, want slotTree constrained to Chart", chart)
	}
}

func TestSlotTreeSpaceRoundTripsByteIdentically(t *testing.T) {
	got, err := EncodeDeclaration(decodeSlotTreeDecl(t))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != slotTreeDecl {
		t.Errorf("round trip changed the bytes:\n got %s\nwant %s", got, slotTreeDecl)
	}
}

func TestSlotTreeSpaceValidatesArguments(t *testing.T) {
	c := decodeSlotTreeDecl(t)
	cases := []struct {
		name string
		args []InvokeArg
		want string // "" = accepted
	}{
		{"any kind fills the unconstrained space", []InvokeArg{{"body", `{"kind":"Text"}`}}, ""},
		{"a scalar is no tree", []InvokeArg{{"body", "13"}}, ErrUninvocableArg},
		{"the constrained kind is accepted", []InvokeArg{{"body", `{"kind":"Text"}`}, {"chart", `{"kind":"Chart"}`}}, ""},
		{"another kind is out of the space", []InvokeArg{{"body", `{"kind":"Text"}`}, {"chart", `{"kind":"Text"}`}}, ErrArgOutOfSpace},
		{"a malformed document is no tree", []InvokeArg{{"body", `{"kind":`}}, ErrUninvocableArg},
		{"an object without a string kind is no tree", []InvokeArg{{"body", `{"kind":1}`}}, ErrUninvocableArg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArgs(c, tc.args)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refused %s at %s, want accepted", err.Kind, err.Addr)
			case tc.want != "" && err == nil:
				t.Errorf("accepted, want %s", tc.want)
			case tc.want != "" && err.Kind != tc.want:
				t.Errorf("refused %s, want %s", err.Kind, tc.want)
			}
		})
	}
}
