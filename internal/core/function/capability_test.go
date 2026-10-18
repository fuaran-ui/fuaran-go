// The capability semantics raised to Core 0.39.0 in Phase 2001: the
// determinism factor set and its one label per set, an address bound once, the
// invariant argument readers and the canonical spelling the replay key hashes,
// the admission gate, and the reader that runs it. The shared law vectors cover
// the first; these pin the rest, whose refusals the vectors do not draw.
package function

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeDoc parses a declaration the way the bounded parser hands one over
// (numbers kept as json.Number) and decodes it.
func decodeDoc(s string) (Capability, error) {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var raw any
	if err := d.Decode(&raw); err != nil {
		return Capability{}, err
	}
	return DecodeDeclarationParsed(raw)
}

func TestDeterminismLabelIsABijectionWithTheEightSets(t *testing.T) {
	want := []string{
		"deterministic", "clock", "random", "clock+random",
		"network", "clock+network", "random+network", "clock+random+network",
	}
	for set := DeterminismSet(0); set < 8; set++ {
		tag := set.Tag()
		if tag != want[set] {
			t.Errorf("set %03b: tag %q, want %q", set, tag, want[set])
		}
		back, ok := ParseDeterminismTag(tag)
		if !ok || back != set {
			t.Errorf("%q reads back as %03b (ok %v), want %03b", tag, back, ok, set)
		}
	}
}

func TestDeterminismLabelHasOneSpelling(t *testing.T) {
	for _, tag := range []string{"random+clock", "clock+clock", "deterministic+clock", "", "clock+", "+clock", "time", "Clock"} {
		if set, ok := ParseDeterminismTag(tag); ok {
			t.Errorf("%q is not a canonical label but read as %03b", tag, set)
		}
	}
}

func TestDeterminismJoinIsUnionAndCoversIsSuperset(t *testing.T) {
	if got := FactorClock.Union(FactorRandom).Tag(); got != "clock+random" {
		t.Errorf("clock ∪ random = %q, want clock+random (the chain dropped the clock)", got)
	}
	if FactorNetwork.Covers(FactorClock) {
		t.Error("a network declaration covers a clock read: the declaration must name every factor")
	}
	if !(FactorClock | FactorNetwork).Covers(FactorClock) {
		t.Error("clock+network does not cover clock")
	}
}

func intCap(holes ...SigEntry) Capability {
	return NewCapability("cap", Signature{
		Name:   "cap",
		Holes:  holes,
		Effect: EffectClass{Host: HostPure, Determinism: (FactorClock | FactorRandom).Tag()},
	}, Placement{Kind: PlacementServer})
}

func valueHole(addr string, space Space) SigEntry {
	return SigEntry{Addr: addr, Name: addr, Kind: "value", Space: &space, Required: true}
}

func TestARepeatedAddressIsRefusedFirst(t *testing.T) {
	c := intCap(valueHole("a", Space{Kind: "intRange", Min: 0, Max: 9}))
	// The second binding is out of space too; the duplicate is named, before
	// any value is read.
	e := ValidateArgs(c, []InvokeArg{{"a", "1"}, {"a", "99"}})
	if e == nil || e.Kind != ErrDuplicateArg || e.Addr != "a" {
		t.Fatalf("got %v, want duplicateArg(a)", e)
	}
}

func TestTheArgumentReadersAreTheReferencesInvariantOnes(t *testing.T) {
	ints := intCap(valueHole("a", Space{Kind: "intRange", Min: -10, Max: 10}))
	floats := intCap(valueHole("a", Space{Kind: "floatRange", Min: -10, Max: 10}))
	for _, v := range []string{"5", "-5", "05", "-0"} {
		if e := ValidateArgs(ints, []InvokeArg{{"a", v}}); e != nil {
			t.Errorf("integer %q refused: %v", v, e)
		}
	}
	for _, v := range []string{" 5", "5 ", "+5", "5.0", "", "-", "−5"} {
		if e := ValidateArgs(ints, []InvokeArg{{"a", v}}); e == nil || e.Kind != ErrArgOutOfSpace {
			t.Errorf("integer %q: got %v, want argOutOfSpace", v, e)
		}
	}
	for _, v := range []string{"1.5", "1.50", "-2", "1e0", "1E+0"} {
		if e := ValidateArgs(floats, []InvokeArg{{"a", v}}); e != nil {
			t.Errorf("number %q refused: %v", v, e)
		}
	}
	for _, v := range []string{".5", "1.", " 1", "+1", "Infinity", "NaN", "1e999"} {
		if e := ValidateArgs(floats, []InvokeArg{{"a", v}}); e == nil || e.Kind != ErrArgOutOfSpace {
			t.Errorf("number %q: got %v, want argOutOfSpace", v, e)
		}
	}
}

func TestOneValueHasOneReplayKey(t *testing.T) {
	ints := intCap(valueHole("a", Space{Kind: "intRange", Min: 0, Max: 10}))
	if InvocationKey(ints, []InvokeArg{{"a", "05"}}) != InvocationKey(ints, []InvokeArg{{"a", "5"}}) {
		t.Error("05 and 5 in an integer hole key apart")
	}
	floats := intCap(valueHole("a", Space{Kind: "floatRange", Min: 0, Max: 10}))
	if InvocationKey(floats, []InvokeArg{{"a", "1.50"}}) != InvocationKey(floats, []InvokeArg{{"a", "1.5"}}) {
		t.Error("1.50 and 1.5 in a number hole key apart")
	}
	// Out of its space, a value keys by its own spelling.
	if InvocationKey(ints, []InvokeArg{{"a", "011"}}) == InvocationKey(ints, []InvokeArg{{"a", "11"}}) {
		t.Error("an out-of-space value was re-spelled")
	}
}

func TestTheAdmissionGate(t *testing.T) {
	repeat := func(s Space) SigEntry { return SigEntry{Addr: "r", Name: "r", Kind: "repeat", Space: &s} }
	cases := []struct {
		name  string
		holes []SigEntry
		kind  string
		fault string
		addr  string
	}{
		{"an unbounded repeat", []SigEntry{repeat(Space{Kind: "anyString"})}, ErrNonTotalCapability, "", ""},
		{"a repeat above the cap", []SigEntry{repeat(Space{Kind: "intRange", Min: 0, Max: maxRepeatCount + 1})}, ErrNonTotalCapability, "", ""},
		{"a negative repeat", []SigEntry{repeat(Space{Kind: "intRange", Min: -1, Max: 3})}, ErrNonTotalCapability, "", ""},
		{"an empty range", []SigEntry{valueHole("a", Space{Kind: "intRange", Min: 3, Max: 2})}, ErrIllFormedCapability, FaultEmptySpace, "a"},
		{"an empty enum", []SigEntry{valueHole("a", Space{Kind: "enum"})}, ErrIllFormedCapability, FaultEmptySpace, "a"},
		{"two holes at one address", []SigEntry{
			valueHole("a", Space{Kind: "anyString"}), valueHole("a", Space{Kind: "anyString"}),
		}, ErrIllFormedCapability, FaultDuplicateHoleAddr, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewCapabilityRegistry().Register(intCap(tc.holes...))
			if e == nil || e.Kind != tc.kind || e.Fault != tc.fault || (tc.addr != "" && e.Addr != tc.addr) {
				t.Fatalf("got %v, want %s %s at %q", e, tc.kind, tc.fault, tc.addr)
			}
		})
	}
	if e := NewCapabilityRegistry().Register(intCap(repeat(Space{Kind: "intRange", Min: 0, Max: 5}))); e != nil {
		t.Fatalf("a bounded repeat was refused: %v", e)
	}
}

func TestTheReaderRefusesWhatTheGateRefuses(t *testing.T) {
	const ok = `{"$type":"capability","determinism":"clock+random","id":"c","placement":{"$type":"server"},` +
		`"signature":{"effect":{"determinism":"clock+random","host":"pure"},"holes":[` +
		`{"addr":"a","kind":"value","name":"a","required":true,"space":{"$type":"intRange","max":9,"min":0}}],"name":"c"}}`
	if _, err := decodeDoc(ok); err != nil {
		t.Fatalf("a well-formed declaration was refused: %v", err)
	}
	refused := map[string]string{
		"no $type":                   strings.Replace(ok, `"$type":"capability",`, "", 1),
		"another $type":              strings.Replace(ok, `"$type":"capability"`, `"$type":"query"`, 1),
		"a non-canonical label":      strings.ReplaceAll(ok, "clock+random", "random+clock"),
		"an empty space":             strings.Replace(ok, `"max":9,"min":0`, `"max":0,"min":9`, 1),
		"a value hole with no space": strings.Replace(ok, `,"space":{"$type":"intRange","max":9,"min":0}`, "", 1),
		"an unknown hole kind":       strings.Replace(ok, `"kind":"value"`, `"kind":"widget"`, 1),
	}
	for name, doc := range refused {
		if _, err := decodeDoc(doc); err == nil {
			t.Errorf("%s: the reader admitted it", name)
		}
	}
}
