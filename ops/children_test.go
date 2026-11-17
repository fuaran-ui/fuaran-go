package ops

import (
	"reflect"
	"sort"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// The one child enumeration (traverse.go): the slot list it reads from the
// generated tables, its canonical order per reach, the rebuild through every
// slot shape, and the walks that used to stop short of the envelope fallback.

func idsOf(nodes []wire.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}

func childNamed(t *testing.T, tree wire.Node, id string) wire.Node {
	t.Helper()
	n, ok := FindNode(tree, id)
	if !ok {
		t.Fatalf("node %q not found", id)
	}
	return n
}

func TestNodeSlotsReadTheGeneratedTables(t *testing.T) {
	cases := map[string][]idl.NodeSlot{
		"Box":           {{Field: "children", Shape: idl.SlotList}},
		"Modal":         {{Field: "children", Shape: idl.SlotList}},
		"ErrorBoundary": {{Field: "child", Shape: idl.SlotNode}, {Field: "fallback", Shape: idl.SlotNode}},
		"FragmentDecl":  {{Field: "body", Shape: idl.SlotNode}},
		"Switch":        {{Field: "cases", Shape: idl.SlotRecords, Inner: "child"}, {Field: "default", Shape: idl.SlotNode}},
		"FragmentRef":   {{Field: "args", Shape: idl.SlotArgs, Case: "SlotArg", Inner: "tree"}},
		"Mount":         {{Field: "inputs", Shape: idl.SlotArgs, Case: "SlotArg", Inner: "tree"}},
		"Markdown":      nil,
	}
	for tag, want := range cases {
		if got := idl.NodeSlots(tag); !reflect.DeepEqual(got, want) {
			t.Errorf("NodeSlots(%s) = %+v, want %+v", tag, got, want)
		}
	}
}

// The envelope positions are the only ones traverse.go states by hand; they
// must be exactly the node envelope's node-holding members in the IDL.
func TestEnvelopeSlotsAreTheIDLsEnvelopeNodeFields(t *testing.T) {
	var fromIDL []string
	for _, f := range idl.NodeFields {
		switch {
		case f.Optionality == idl.HostOnly:
		case f.Type.Kind == idl.TNode:
			fromIDL = append(fromIDL, f.Name)
		case f.Type.Kind == idl.TRecord:
			rec, ok := idl.RecordByName(f.Type.Name)
			if !ok {
				t.Fatalf("envelope record %q missing from the tables", f.Type.Name)
			}
			for _, rf := range rec.Fields {
				if rf.Optionality != idl.HostOnly && rf.Type.Kind == idl.TNode {
					fromIDL = append(fromIDL, f.Name+"."+rf.Name)
				}
			}
		}
	}
	var stated []string
	for _, e := range envelopeSlots {
		if e.inner == "" {
			stated = append(stated, e.member)
		} else {
			stated = append(stated, e.member+"."+e.inner)
		}
	}
	sort.Strings(fromIDL)
	sort.Strings(stated)
	if !reflect.DeepEqual(fromIDL, stated) {
		t.Errorf("envelope slots %v, IDL envelope node fields %v", stated, fromIDL)
	}
}

func TestChildrenCanonicalOrderPerReach(t *testing.T) {
	tree := reachTree(t)
	sw := childNamed(t, tree, "sw")
	cases := []struct {
		name  string
		node  wire.Node
		reach Reach
		want  []string
	}{
		{"root structural", tree, ReachStructural, []string{"a", "sw", "m"}},
		{"switch keyed", sw, ReachKeyed, []string{"l", "e", "c0", "d"}},
		{"switch kind-held", sw, ReachKindHeld, []string{"c0", "d"}},
		{"switch fragment scope", sw, ReachFragmentScope, []string{"l", "e", "c0", "d"}},
		{"switch structural", sw, ReachStructural, []string{}},
		{"switch state arms", sw, ReachOf(StateArm), []string{"l", "e"}},
		{"fallback keyed", childNamed(t, tree, "a"), ReachKeyed, []string{"fb"}},
		{"fallback fragment scope", childNamed(t, tree, "a"), ReachFragmentScope, []string{}},
		{"mount keyed", childNamed(t, tree, "m"), ReachKeyed, []string{"seed"}},
		{"mount kind-held", childNamed(t, tree, "m"), ReachKindHeld, []string{}},
	}
	for _, c := range cases {
		if got := idsOf(Children(c.node, c.reach)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Children = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSlotsNameTheirWirePaths(t *testing.T) {
	tree := reachTree(t)
	var got []string
	for _, n := range []wire.Node{tree, childNamed(t, tree, "a"), childNamed(t, tree, "sw"), childNamed(t, tree, "m")} {
		for _, s := range Slots(n, ReachKeyed) {
			got = append(got, n.ID+":"+joinPath(s.Path()))
		}
	}
	want := []string{
		"r:kind.children.0", "r:kind.children.1", "r:kind.children.2",
		"a:fallback",
		"sw:state.onLoading", "sw:state.onEmpty", "sw:kind.cases.0.child", "sw:kind.default",
		"m:kind.inputs.seed.tree",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("slot paths = %v, want %v", got, want)
	}
}

func joinPath(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += "."
		}
		out += s
	}
	return out
}

// MapChildren puts a replacement back in every slot shape (list, arm, record
// list, argument bag, state record, fallback) and keeps what sits beside it.
func TestMapChildrenRebuildsEverySlotShape(t *testing.T) {
	tree := reachTree(t)
	rename := func(c wire.Node) (wire.Node, bool) {
		return wire.Node{ID: c.ID + "2", Kind: c.Kind, Extras: c.Extras}, true
	}
	for _, id := range []string{"r", "a", "sw", "m"} {
		before := childNamed(t, tree, id)
		after, changed := MapChildren(before, ReachKeyed, rename)
		if !changed {
			t.Fatalf("%s: MapChildren reported no change", id)
		}
		var want []string
		for _, c := range Children(before, ReachKeyed) {
			want = append(want, c.ID+"2")
		}
		if got := idsOf(Children(after, ReachKeyed)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: children after = %v, want %v", id, got, want)
		}
		if mustEncode(t, before) == mustEncode(t, after) {
			t.Errorf("%s: the rebuild did not change the node", id)
		}
	}
	sw, _ := MapChildren(childNamed(t, tree, "sw"), ReachKeyed, rename)
	if got := mustEncode(t, sw); got != `{"id":"sw","kind":{"$type":"Switch","cases":[{"child":{"id":"c02","kind":{"$type":"Markdown","text":"c0"}},"match":"x"}],"default":{"id":"d2","kind":{"$type":"Markdown","text":"d"}},"stateKey":"k"},"state":{"onEmpty":{"id":"e2","kind":{"$type":"Markdown","text":"e"}},"onLoading":{"id":"l2","kind":{"$type":"Markdown","text":"l"}}}}` {
		t.Errorf("switch rebuild lost a sibling field:\n%s", got)
	}
	m, _ := MapChildren(childNamed(t, tree, "m"), ReachKeyed, rename)
	if got := mustEncode(t, m); got != `{"id":"m","kind":{"$type":"Mount","capabilities":[],"channel":{"direction":"OutOnly"},"inputs":{"seed":{"$type":"SlotArg","tree":{"id":"seed2","kind":{"$type":"Markdown","text":"s"}}},"title":{"$type":"Str","value":"T"}},"scopeId":"g"}}` {
		t.Errorf("mount rebuild lost a value input:\n%s", got)
	}
	// A reach outside the change leaves the node as it was, unchanged.
	same, changed := MapChildren(childNamed(t, tree, "sw"), ReachStructural, rename)
	if changed || mustEncode(t, same) != mustEncode(t, childNamed(t, tree, "sw")) {
		t.Error("a reach holding none of the node's positions must change nothing")
	}
}
