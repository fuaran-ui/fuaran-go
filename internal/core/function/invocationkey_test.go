// The capability invocation key's canonical pre-image (Phase 1860, the
// cross-host half of fuaran-core#225).
//
// The key used to hash the addr-sorted `addr=value` pairs joined with no
// separator, so the accepted argument sets [a="1b=2"] and [a="1"; b="2"]
// shared one pre-image and one key. The reference now builds the pre-image as
// a canonical field sequence — two fields per binding (addr, value), each field
// escaped and terminated — which is injective over argument sets. Every
// expected key below is the reference's own value for the same input, and the
// other two hosts pin the same literals.
package function

import "testing"

func keyOf(id string, args ...InvokeArg) string {
	return InvocationKey(Capability{ID: id}, args)
}

func TestInvocationKeyFormerlyCollidingPairIsDistinct(t *testing.T) {
	one := keyOf("cap", InvokeArg{"a", "1b=2"})
	two := keyOf("cap", InvokeArg{"a", "1"}, InvokeArg{"b", "2"})
	if one == two {
		t.Fatalf("[a=\"1b=2\"] and [a=\"1\"; b=\"2\"] share the key %s", one)
	}
	if one != "cap#4ad0d41a" {
		t.Errorf("[a=\"1b=2\"]: got %s, want cap#4ad0d41a", one)
	}
	if two != "cap#53c281a5" {
		t.Errorf("[a=\"1\"; b=\"2\"]: got %s, want cap#53c281a5", two)
	}
}

func TestInvocationKeyMatchesTheReferenceByteForByte(t *testing.T) {
	cases := []struct {
		name string
		id   string
		args []InvokeArg
		want string
	}{
		// The published capability-laws vector capability-0-invocation-key.
		{"published vector", "cap-0", []InvokeArg{{"h0", "13"}}, "cap-0#70fcefc7"},
		// No arguments: the empty pre-image, so the key is the FNV-1a offset basis.
		{"no args", "cap", nil, "cap#811c9dc5"},
		// A value carrying the terminator, and one carrying the escape: both are
		// escaped, so neither can end its field early.
		{"escaped value", "cap", []InvokeArg{{"a", "x\u0001y"}, {"b", "\u0010"}}, "cap#3d801624"},
		// The same characters moved into an address.
		{"escaped addr", "cap", []InvokeArg{{"a", "x"}, {"\u0001y", "\u0010"}}, "cap#46a6fdda"},
		// Addresses sort by UTF-16 code unit, as the reference compares strings:
		// U+1F600 (surrogate D83D) sorts BEFORE U+E000, where a UTF-8 byte order
		// would put it after.
		{"utf-16 order", "cap", []InvokeArg{{"", "1"}, {"\U0001F600", "2"}}, "cap#e3651ae3"},
		// A repeated address keeps its argument order (a stable sort).
		{"stable repeat", "cap", []InvokeArg{{"a", "2"}, {"a", "1"}}, "cap#1d4e4ee6"},
		{"stable repeat, swapped", "cap", []InvokeArg{{"a", "1"}, {"a", "2"}}, "cap#c79fd87e"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := keyOf(c.id, c.args...); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestInvocationKeyIsStableUnderArgumentReordering(t *testing.T) {
	a := keyOf("cap", InvokeArg{"b", "2"}, InvokeArg{"a", "1"})
	b := keyOf("cap", InvokeArg{"a", "1"}, InvokeArg{"b", "2"})
	if a != b {
		t.Fatalf("reordered arguments keyed differently: %s vs %s", a, b)
	}
}
