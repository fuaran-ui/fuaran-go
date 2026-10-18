// The explicit "slotTree" value space (Phase 1860, from fuaran-core#229), at the
// Core release the reference pins (0.38.1).
//
// From Core 0.37.0 (fuaran-core Phase 409) a slot entry IS its constraint and
// holds no space, so the reference's canonical encoding writes an explicit
// "slotTree" space only for a non-slot hole ranging over trees. This host must
// DECODE that encoding — not refuse it as an unknown space — re-encode it to the
// same bytes, read the pre-0.37.0 form (a slot entry that also wrote a space)
// and re-encode it without the slot's space, and validate arguments as the
// reference does. Both declarations and every verdict below are the reference's
// own (fuaran-dotnet CoreFunctionLawTests, "an explicit slotTree space
// round-trips and validates").
package function

import (
	"encoding/json"
	"testing"
)

const slotTreeDecl = `{"$type":"capability","determinism":"random","id":"cap-tree","placement":{"$type":"server"},"signature":{"effect":{"determinism":"random","host":"readsHost"},"holes":[{"addr":"body","kind":"slot","name":"body","required":true,"slotKind":"Layout"},{"addr":"chart","kind":"value","name":"chart","required":false,"space":{"$type":"slotTree","slotKind":"Chart"}}],"name":"tree"}}`

// slotTreeDeclBefore037 is the same declaration as it was pinned before Core
// 0.37.0: the slot entry also wrote a slotTree space, which a slot hole no
// longer holds.
const slotTreeDeclBefore037 = `{"$type":"capability","determinism":"random","id":"cap-tree","placement":{"$type":"server"},"signature":{"effect":{"determinism":"random","host":"readsHost"},"holes":[{"addr":"body","kind":"slot","name":"body","required":true,"slotKind":"Layout","space":{"$type":"slotTree"}},{"addr":"chart","kind":"value","name":"chart","required":false,"space":{"$type":"slotTree","slotKind":"Chart"}}],"name":"tree"}}`

func decodeDecl(t *testing.T, text string) Capability {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("the fixture is not JSON: %v", err)
	}
	c, err := DecodeDeclarationParsed(raw)
	if err != nil {
		t.Fatalf("a declaration carrying an explicit slotTree space was refused: %v", err)
	}
	return c
}

func TestSlotTreeSpaceDecodes(t *testing.T) {
	c := decodeDecl(t, slotTreeDecl)
	body, chart := c.Signature.Holes[0], c.Signature.Holes[1].Space
	if body.Space != nil || body.Slot != "Layout" {
		t.Errorf("body: got space %+v slot %q, want no space and the Layout constraint", body.Space, body.Slot)
	}
	if chart == nil || chart.Kind != "slotTree" || chart.SlotKind != "Chart" {
		t.Errorf("chart: got %+v, want slotTree constrained to Chart", chart)
	}
}

func TestSlotTreeSpaceRoundTripsByteIdentically(t *testing.T) {
	got, err := EncodeDeclaration(decodeDecl(t, slotTreeDecl))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != slotTreeDecl {
		t.Errorf("round trip changed the bytes:\n got %s\nwant %s", got, slotTreeDecl)
	}
}

func TestSlotTreeBefore037ReadsAndReencodesWithoutTheSlotSpace(t *testing.T) {
	got, err := EncodeDeclaration(decodeDecl(t, slotTreeDeclBefore037))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != slotTreeDecl {
		t.Errorf("the pre-0.37.0 form re-encoded as:\n got %s\nwant %s", got, slotTreeDecl)
	}
}

func TestSlotTreeSpaceValidatesArguments(t *testing.T) {
	for _, decl := range []string{slotTreeDecl, slotTreeDeclBefore037} {
		c := decodeDecl(t, decl)
		cases := []struct {
			name string
			args []InvokeArg
			want string // "" = accepted
		}{
			{"the slot's kind fills the slot", []InvokeArg{{"body", `{"kind":"Layout"}`}}, ""},
			{"another kind is out of the slot", []InvokeArg{{"body", `{"kind":"Text"}`}}, ErrArgOutOfSpace},
			{"a scalar is no tree", []InvokeArg{{"body", "13"}}, ErrUninvocableArg},
			{"the constrained kind is accepted", []InvokeArg{{"body", `{"kind":"Layout"}`}, {"chart", `{"kind":"Chart"}`}}, ""},
			{"another kind is out of the space", []InvokeArg{{"body", `{"kind":"Layout"}`}, {"chart", `{"kind":"Text"}`}}, ErrArgOutOfSpace},
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
}
