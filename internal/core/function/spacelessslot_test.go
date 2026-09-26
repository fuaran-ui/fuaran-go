// A slot hole with no declared space is invocable (Phase 1873, from
// fuaran-core#229).
//
// The reference writes a slot entry's space only when it disagrees with the
// slot's constraint, so an ordinary slotted capability travels with NO "space"
// on its slot entry, and the reference's decoder restores that space as the
// tree space constrained to the slot's kind. The declaration below is that
// ordinary shape: a slot constrained to "para" beside two value holes. The
// reference admits a well-formed tree of the constrained kind, refuses a tree
// of another kind as ArgOutOfSpace, and refuses anything that is no tree as
// UninvocableArg; an action hole, which has no space and no tree reading,
// stays uninvocable for every argument.
package function

import (
	"encoding/json"
	"strings"
	"testing"
)

const spacelessSlotDecl = `{"$type":"capability","determinism":"deterministic","id":"tpl-cap","placement":{"$type":"server"},"signature":{"effect":{"determinism":"deterministic","host":"pure"},"holes":[{"addr":"tpl/t","kind":"value","name":"title","required":true,"space":{"$type":"stringLen","max":20,"min":1}},{"addr":"tpl/c","kind":"value","name":"count","required":true,"space":{"$type":"intRange","max":10,"min":0}},{"addr":"tpl/s","kind":"slot","name":"body","required":true,"slotKind":"para"}],"name":"tpl"}}`

func decodeSpacelessSlotDecl(t *testing.T) Capability {
	t.Helper()
	// Numbers decode as json.Number, the form the codec reads (as the wire
	// decoder's bounded parse yields them).
	dec := json.NewDecoder(strings.NewReader(spacelessSlotDecl))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("the fixture is not JSON: %v", err)
	}
	c, err := DecodeDeclarationParsed(raw)
	if err != nil {
		t.Fatalf("a slotted declaration was refused: %v", err)
	}
	return c
}

func tplArgs(slot string) []InvokeArg {
	return []InvokeArg{{"tpl/t", "Hello"}, {"tpl/c", "5"}, {"tpl/s", slot}}
}

// The slot entry decodes spaceless and re-encodes to the same bytes: the tree
// space is applied when an argument is validated, never written into the
// declaration.
func TestSpacelessSlotStaysSpacelessOnTheWire(t *testing.T) {
	c := decodeSpacelessSlotDecl(t)
	if s := c.Signature.Holes[2].Space; s != nil {
		t.Fatalf("the slot entry decoded with a space %+v, want none", s)
	}
	got, err := EncodeDeclaration(c)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != spacelessSlotDecl {
		t.Errorf("round trip changed the bytes:\n got %s\nwant %s", got, spacelessSlotDecl)
	}
}

func TestSpacelessSlotIsInvocable(t *testing.T) {
	c := decodeSpacelessSlotDecl(t)
	cases := []struct {
		name string
		slot string
		want string // "" = accepted
	}{
		{"a tree of the constrained kind is admitted", `{"kind":"para","text":"hi"}`, ""},
		{"a tree of another kind is out of the space", `{"kind":"heading"}`, ErrArgOutOfSpace},
		{"a string scalar is no tree", "hi", ErrUninvocableArg},
		{"a number is no tree", "42", ErrUninvocableArg},
		{"an array is no tree", "[1,2]", ErrUninvocableArg},
		{"an object without a kind is no tree", `{"text":"no kind"}`, ErrUninvocableArg},
		{"an object with a non-string kind is no tree", `{"kind":7}`, ErrUninvocableArg},
		{"a malformed document is no tree", "{", ErrUninvocableArg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArgs(c, tplArgs(tc.slot))
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refused %s at %s, want accepted", err.Kind, err.Addr)
			case tc.want != "" && err == nil:
				t.Errorf("accepted, want %s", tc.want)
			case tc.want != "" && (err.Kind != tc.want || err.Addr != "tpl/s"):
				t.Errorf("refused %s at %s, want %s at tpl/s", err.Kind, err.Addr, tc.want)
			case tc.want == ErrArgOutOfSpace && err.Got != tc.slot:
				t.Errorf("argOutOfSpace carried %q, want the offending value %q", err.Got, tc.slot)
			}
		})
	}
}

// With no constraint the spaceless slot takes a tree of any kind, and still no
// scalar.
func TestSpacelessUnconstrainedSlotTakesAnyTree(t *testing.T) {
	c := NewCapability("any-cap", Signature{
		Name:   "any",
		Holes:  []SigEntry{{Addr: "s", Name: "s", Kind: "slot", Required: true}},
		Effect: EffectClass{Host: "pure", Determinism: "deterministic"},
	}, Placement{Kind: "server"})
	if err := ValidateArgs(c, []InvokeArg{{"s", `{"kind":"anything"}`}}); err != nil {
		t.Errorf("a tree of any kind was refused: %s", err.Kind)
	}
	if err := ValidateArgs(c, []InvokeArg{{"s", "anything"}}); err == nil || err.Kind != ErrUninvocableArg {
		t.Errorf("a scalar: got %v, want %s", err, ErrUninvocableArg)
	}
}

// An action hole has no space and no tree reading: it is refused for every
// argument, tree or scalar.
func TestSpacelessActionHoleStaysUninvocable(t *testing.T) {
	c := NewCapability("act-cap", Signature{
		Name:   "act",
		Holes:  []SigEntry{{Addr: "on", Name: "on", Kind: "action", Required: false}},
		Effect: EffectClass{Host: "pure", Determinism: "deterministic"},
	}, Placement{Kind: "server"})
	for _, v := range []string{`{"kind":"para"}`, "13"} {
		if err := ValidateArgs(c, []InvokeArg{{"on", v}}); err == nil || err.Kind != ErrUninvocableArg {
			t.Errorf("an action hole bound to %s: got %v, want %s", v, err, ErrUninvocableArg)
		}
	}
}
