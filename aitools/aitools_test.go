package aitools

import (
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func decode(t *testing.T, s string) wire.Node {
	t.Helper()
	n, err := wire.DecodeNode(s)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	return n
}

const stateMetric = `{"id":"m","kind":{"$type":"Metric","label":"Users",` +
	`"value":{"$type":"State","defaultValue":0,"key":"users"}}}`

func stackOf(children ...string) string {
	body := ""
	for i, c := range children {
		if i > 0 {
			body += ","
		}
		body += c
	}
	return `{"id":"root","kind":{"$type":"Box","children":[` + body +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
}

func md(id, text string) string {
	return `{"id":"` + id + `","kind":{"$type":"Markdown","text":"` + text + `"}}`
}

func TestBindingClassification(t *testing.T) {
	slots := BindingSlots(decode(t, stateMetric))
	found := false
	for _, s := range slots {
		if s.Slot == "value" {
			found = true
			if s.Source != "State" || s.Expression != "$state.users" {
				t.Errorf("value slot = %+v, want State/$state.users", s)
			}
		}
	}
	if !found {
		t.Errorf("no value binding slot found: %+v", slots)
	}
}

func TestIntrospectionStructure(t *testing.T) {
	tree := decode(t, stackOf(md("a", "A"), md("b", "B"), stateMetric))
	ti := InspectTree(tree)
	if ti.Kind != "Box" || len(ti.Children) != 3 {
		t.Fatalf("root introspection wrong: kind=%s children=%d", ti.Kind, len(ti.Children))
	}
	if got := ChildIDs(tree); len(got) != 3 || got[0] != "a" || got[2] != "m" {
		t.Errorf("child ids = %v, want [a b m]", got)
	}
	ns, ok := NodeState(tree, "m")
	if !ok || ns.Kind != "Metric" {
		t.Errorf("NodeState(m) = %+v, ok=%v", ns, ok)
	}
}

func TestSearchAndAssertions(t *testing.T) {
	tree := decode(t, stackOf(md("a", "A"), md("b", "B"), stateMetric))
	if got := FindByKind(tree, "Markdown"); len(got) != 2 {
		t.Errorf("FindByKind(Markdown) = %d, want 2", len(got))
	}
	if CountByKind(tree)["Metric"] != 1 {
		t.Errorf("CountByKind Metric = %d, want 1", CountByKind(tree)["Metric"])
	}
	if a := AssertKind(tree, "m", "Metric"); !a.OK {
		t.Errorf("AssertKind failed: %s", a.Reason)
	}
	if a := AssertKind(tree, "m", "Chart"); a.OK {
		t.Error("AssertKind(m, Chart) should fail")
	}
	if a := AssertBound(tree, "m", "State"); !a.OK {
		t.Errorf("AssertBound(m, State) failed: %s", a.Reason)
	}
	if a := AssertExists(tree, "ghost"); a.OK {
		t.Error("AssertExists(ghost) should fail")
	}
}

func TestDispatchGateDefaultDeny(t *testing.T) {
	// The zero/deny-all gate denies every action shape.
	deny := DenyAll()
	if d := deny.AuthorizeShape("Navigate"); d.Allowed {
		t.Error("deny-all permitted Navigate")
	}
	// Explicit permit.
	gate := Permitting("Navigate")
	if d := gate.AuthorizeShape("Navigate"); !d.Allowed {
		t.Errorf("Navigate not permitted: %s", d.Reason)
	}
	if d := gate.AuthorizeShape("WriteToClipboard"); d.Allowed {
		t.Error("a non-permitted effect fired by omission (default-deny broken)")
	}
	// Inert-permissive allows the structural shapes but no outward effect.
	inert := PermissiveInert()
	if !inert.AuthorizeShape("SetState").Allowed || inert.AuthorizeShape("Navigate").Allowed {
		t.Error("PermissiveInert gate wrong")
	}
	// An unknown shape is denied as unknown, not silently.
	if d := gate.AuthorizeShape("Bogus"); d.Allowed {
		t.Error("unknown shape permitted")
	}
	// Authorize over a decoded Action object reads its $type.
	action := wire.Obj{Tag: "Navigate", Fields: map[string]wire.Value{"route": wire.Str("/x")}}
	if !gate.Authorize(action).Allowed {
		t.Error("Authorize(Navigate action) denied")
	}
}
