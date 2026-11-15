// The capability semantics raised to Core 0.39.0 in Phase 2001: the
// determinism factor set and its one label per set, an address bound once, the
// invariant argument readers and the canonical spelling the replay key hashes,
// the admission gate, and the reader that runs it. Raised to Core 0.43.0 in
// Phase 2740: the declared result, its admission and its reader, and the
// pipeline type check. The shared law vectors cover the first and the result's
// two drawn refusals; these pin the rest, whose refusals the vectors do not
// draw.
package function

import (
	"encoding/json"
	"math"
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
	}, Placement{Kind: PlacementServer}, ReturnsNothing())
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
		`"result":{"$type":"nothing"},"signature":{"effect":{"determinism":"clock+random","host":"pure"},"holes":[` +
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

func TestTheDeclaredResultIsHeldToTheGate(t *testing.T) {
	ok := intCap(valueHole("a", Space{Kind: "intRange", Min: 0, Max: 9}))
	cases := []struct {
		name   string
		result Returns
		fault  string
	}{
		{"an empty range", ReturnsValue(&Space{Kind: "intRange", Min: 3, Max: 2}), FaultEmptyResult},
		{"an empty enum", ReturnsValue(&Space{Kind: "enum"}), FaultEmptyResult},
		{"a negative string length", ReturnsValue(&Space{Kind: "stringLen", Min: 0, Max: -1}), FaultEmptyResult},
		{"a non-finite bound", ReturnsValue(&Space{Kind: "floatRange", Min: 0, Max: math.Inf(1)}), FaultNonFiniteResult},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ok
			c.Result = tc.result
			e := NewCapabilityRegistry().Register(c)
			if e == nil || e.Kind != ErrIllFormedCapability || e.Fault != tc.fault || e.Addr != "" {
				t.Fatalf("got %v, want %s %s naming no hole", e, ErrIllFormedCapability, tc.fault)
			}
		})
	}
	for _, r := range []Returns{ReturnsNothing(), ReturnsValue(&Space{Kind: "anyString"})} {
		c := ok
		c.Result = r
		if e := NewCapabilityRegistry().Register(c); e != nil {
			t.Fatalf("a well-formed result was refused: %v", e)
		}
	}
}

func TestTheResultReaderNamesItsCodeAndPath(t *testing.T) {
	const doc = `{"$type":"capability","determinism":"deterministic","id":"c","placement":{"$type":"server"},` +
		`RESULT"signature":{"effect":{"determinism":"deterministic","host":"pure"},"holes":[],"name":"c"}}`
	read := func(result string) error {
		_, err := decodeDoc(strings.Replace(doc, "RESULT", result, 1))
		return err
	}
	typed := map[string]struct{ result, code string }{
		"no result":          {"", "MissingField"},
		"a null result":      {`"result":null,`, "WrongKind"},
		"an empty result":    {`"result":{"$type":"enum","values":[]},`, "OutOfRange"},
		"an inverted result": {`"result":{"$type":"intRange","max":0,"min":1},`, "OutOfRange"},
	}
	for name, tc := range typed {
		de, isTyped := read(tc.result).(*DeclarationError)
		if !isTyped || de.Code != tc.code || strings.Join(de.Path, ".") != "result" {
			t.Errorf("%s: got %v, want %s at result", name, de, tc.code)
		}
	}
	if read(`"result":{"$type":"widget"},`) == nil {
		t.Error("an unknown result space was admitted")
	}
	// A declared result round-trips, nothing and value alike.
	for _, result := range []string{`"result":{"$type":"nothing"},`, `"result":{"$type":"intRange","max":9,"min":0},`} {
		want := strings.Replace(doc, "RESULT", result, 1)
		c, err := decodeDoc(want)
		if err != nil {
			t.Fatalf("%s refused: %v", result, err)
		}
		if got, err := EncodeDeclaration(c); err != nil || got != want {
			t.Errorf("round-trip:\n want %s\n  got %s (%v)", want, got, err)
		}
	}
}

func TestSpaceSubsumesIsTheReferenceLattice(t *testing.T) {
	ir := func(lo, hi float64) *Space { return &Space{Kind: "intRange", Min: lo, Max: hi} }
	fr := func(lo, hi float64) *Space { return &Space{Kind: "floatRange", Min: lo, Max: hi} }
	sl := func(lo, hi float64) *Space { return &Space{Kind: "stringLen", Min: lo, Max: hi} }
	en := func(xs ...string) *Space { return &Space{Kind: "enum", Choices: xs} }
	anyStr := &Space{Kind: "anyString"}
	tree := func(k string) *Space { return &Space{Kind: "slotTree", SlotKind: k} }
	cases := []struct {
		name                string
		required, available *Space
		want                bool
	}{
		{"int within int", ir(0, 10), ir(2, 3), true},
		{"int past int", ir(0, 10), ir(2, 11), false},
		{"int widens into float", fr(0, 10), ir(0, 10), true},
		{"float never narrows to int", ir(0, 10), fr(0, 1), false},
		{"enum within a string length", sl(1, 2), en("a", "bb"), true},
		{"enum past a string length", sl(1, 2), en("a", "ccc"), false},
		{"subset enum", en("a", "b"), en("b"), true},
		{"anyString is the scalar top", anyStr, ir(0, 999), true},
		{"anyString admits no tree", anyStr, tree(""), false},
		{"an unconstrained tree", tree(""), tree("Doc"), true},
		{"a constrained tree", tree("Doc"), tree("Para"), false},
		{"no scalar admits a tree", sl(0, 9), tree(""), false},
	}
	for _, tc := range cases {
		if got := SpaceSubsumes(tc.required, tc.available); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestThePipelineTypeCheckNamesEachRefusal(t *testing.T) {
	ints := &Space{Kind: "intRange", Min: 0, Max: 9}
	reg := NewCapabilityRegistry()
	for id, result := range map[string]Returns{"score": ReturnsValue(ints), "notify": ReturnsNothing()} {
		c := intCap(valueHole("x", *ints))
		c.ID, c.Result = id, result
		if e := reg.Register(c); e != nil {
			t.Fatalf("register %s: %v", id, e)
		}
	}
	lit := func(addr, v string) PipelineArg {
		return PipelineArg{Addr: addr, Source: ArgSource{Kind: ArgFromLiteral, Value: v}}
	}
	from := func(addr, node string) PipelineArg {
		return PipelineArg{Addr: addr, Source: ArgSource{Kind: ArgFromUpstream, Node: node}}
	}
	inv := func(id, capID string, out *Space, args ...PipelineArg) PipelineNode {
		return PipelineNode{Kind: NodeInvoke, ID: id, CapabilityID: capID, OutputType: out, Args: args}
	}
	src := func(id string, out *Space) PipelineNode {
		return PipelineNode{Kind: NodeSource, ID: id, DataRef: id, OutputType: out}
	}
	narrower := &Space{Kind: "intRange", Min: 0, Max: 5}
	anyStr := &Space{Kind: "anyString"}
	cases := []struct {
		name  string
		nodes []PipelineNode
		kind  string
	}{
		{"a well-typed chain", []PipelineNode{src("s", ints), inv("a", "score", ints, from("x", "s")),
			inv("b", "score", anyStr, from("x", "a"))}, ""},
		{"a repeated node id", []PipelineNode{src("s", ints), src("s", ints)}, ErrDuplicateNode},
		{"an unregistered capability", []PipelineNode{inv("a", "nope", ints)}, ErrPipelineNoSuchCapability},
		{"an output narrower than the result", []PipelineNode{inv("a", "score", narrower, lit("x", "1"))}, ErrPipelineOutputContradicts},
		{"a capability that answers nothing", []PipelineNode{inv("a", "notify", ints, lit("x", "1"))}, ErrPipelineOutputContradicts},
		{"a literal out of space", []PipelineNode{inv("a", "score", ints, lit("x", "10"))}, ErrPipelineArgRefused},
		{"an address bound twice", []PipelineNode{inv("a", "score", ints, lit("x", "1"), lit("x", "2"))}, ErrPipelineArgRefused},
		{"an unbound hole", []PipelineNode{inv("a", "score", ints)}, ErrPipelineArgRefused},
		{"an edge from nowhere", []PipelineNode{inv("a", "score", ints, from("x", "ghost"))}, ErrUnknownNode},
		{"a self edge", []PipelineNode{inv("a", "score", ints, from("x", "a"))}, ErrPipelineCycle},
		{"a closed cycle", []PipelineNode{inv("a", "score", ints, from("x", "b")), inv("b", "score", ints, from("x", "a"))}, ErrPipelineCycle},
		{"a forward edge", []PipelineNode{inv("a", "score", ints, from("x", "s")), src("s", ints)}, ErrPipelineForwardEdge},
		{"an ill-typed edge", []PipelineNode{src("s", anyStr), inv("a", "score", ints, from("x", "s"))}, ErrEdgeTypeMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := TypeCheckPipeline(reg, CapabilityPipeline{Nodes: tc.nodes})
			switch {
			case tc.kind == "" && e != nil:
				t.Fatalf("refused: %v", e)
			case tc.kind != "" && (e == nil || e.Kind != tc.kind):
				t.Fatalf("got %v, want %s", e, tc.kind)
			}
		})
	}
	// A cycle is named around its members, from the node the check reached.
	e := TypeCheckPipeline(reg, CapabilityPipeline{Nodes: []PipelineNode{
		inv("a", "score", ints, from("x", "b")), inv("b", "score", ints, from("x", "a"))}})
	if e == nil || strings.Join(e.Cycle, ",") != "a,b" {
		t.Errorf("cycle named %v, want [a b]", e)
	}
}
