package validator

import (
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

func mustDecode(t *testing.T, canonicalJSON string) wire.Node {
	t.Helper()
	node, err := wire.DecodeNode(canonicalJSON)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	return node
}

func codes(findings []Finding) []string {
	out := make([]string, len(findings))
	for i, f := range findings {
		out[i] = f.Code
	}
	return out
}

func TestCleanTreeHasNoFindings(t *testing.T) {
	node := mustDecode(t, `{"id":"a","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"hi"}}}`)
	if findings := ValidateNode(node); len(findings) != 0 {
		t.Errorf("expected no findings, got %v", findings)
	}
}

func TestEmptyIDIsFlagged(t *testing.T) {
	node := wire.Node{ID: "", Kind: wire.Obj{Tag: "Markdown", Fields: map[string]wire.Value{
		"text": wire.Obj{Tag: "Literal", Fields: map[string]wire.Value{"text": wire.Str("x")}},
	}}}
	findings := ValidateNode(node)
	if len(findings) != 1 || findings[0].Code != "FUARAN-EMPTY-ID" || findings[0].Path != "$.id" {
		t.Errorf("expected [EMPTY_NODE_ID at $.id], got %v", findings)
	}
}

func TestDuplicateChildIDIsFlagged(t *testing.T) {
	md := func(text string) string {
		return `{"id":"dup","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"` + text + `"}}}`
	}
	root := mustDecode(t, `{"id":"root","kind":{"$type":"Box","children":[`+md("x")+`,`+md("y")+
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`)
	found := false
	for _, f := range ValidateNode(root) {
		if f.Code == "FUARAN-DUP-ID" {
			found = true
		}
	}
	if !found {
		t.Error("expected a DUPLICATE_NODE_ID finding")
	}
}

func TestUnknownKindIsFlagged(t *testing.T) {
	node := wire.Node{ID: "a", Kind: wire.Obj{Tag: "Sparkler", Fields: map[string]wire.Value{}}}
	findings := ValidateNode(node)
	if len(findings) != 1 || findings[0].Code != "UNKNOWN_NODE_KIND" || findings[0].Path != "$.kind.$type" {
		t.Errorf("expected [UNKNOWN_NODE_KIND at $.kind.$type], got %v", findings)
	}
}

func TestMissingRequiredFieldIsFlagged(t *testing.T) {
	// A constructed Markdown without its required text — would fail decode on
	// every conformant host; the validator catches it pre-emit.
	node := wire.Node{ID: "a", Kind: wire.Obj{Tag: "Markdown", Fields: map[string]wire.Value{}}}
	findings := ValidateNode(node)
	if len(findings) != 1 || findings[0].Code != "MISSING_REQUIRED_FIELD" || findings[0].Path != "$.kind.text" {
		t.Errorf("expected [MISSING_REQUIRED_FIELD at $.kind.text], got %v", findings)
	}
}

func TestSwitchDuplicateMatchAndEmptyStateKey(t *testing.T) {
	md := func(id string) string {
		return `{"id":"` + id + `","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"x"}}}`
	}
	tree := mustDecode(t, `{"id":"sw","kind":{"$type":"Switch","cases":[{"child":`+md("a")+
		`,"match":"one"},{"child":`+md("b")+`,"match":"one"}],"default":`+md("d")+`,"stateKey":""}}`)
	got := codes(ValidateNode(tree))
	want := map[string]bool{"FUARAN083": false, "FUARAN082": false}
	for _, c := range got {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for code, seen := range want {
		if !seen {
			t.Errorf("expected a %s finding, got %v", code, got)
		}
	}
}

func TestProgressFractionOutOfDomainIsAdvisory(t *testing.T) {
	tree := mustDecode(t, `{"id":"p","kind":{"$type":"Progress","fraction":{"$type":"Static","value":61},"indeterminate":false,"tone":"Default"}}`)
	findings := ValidateNode(tree)
	if len(findings) != 1 || findings[0].Code != "FUARAN050" || findings[0].Severity != SeverityWarning {
		t.Errorf("expected one advisory FUARAN050 finding, got %v", findings)
	}
	if findings[0].Path != "$.kind.fraction" {
		t.Errorf("path = %s, want $.kind.fraction", findings[0].Path)
	}

	clean := mustDecode(t, `{"id":"p","kind":{"$type":"Progress","fraction":{"$type":"Static","value":0.61},"indeterminate":false,"tone":"Default"}}`)
	if findings := ValidateNode(clean); len(findings) != 0 {
		t.Errorf("in-domain fraction flagged: %v", findings)
	}

	// A non-Static fraction carries no checkable literal — left alone.
	bound := mustDecode(t, `{"id":"p","kind":{"$type":"Progress","fraction":{"$type":"State","defaultValue":0,"key":"pct"},"indeterminate":false,"tone":"Default"}}`)
	if findings := ValidateNode(bound); len(findings) != 0 {
		t.Errorf("non-Static fraction flagged: %v", findings)
	}
}

func TestStateSurfaceChildrenAreWalked(t *testing.T) {
	// A duplicate id hiding in state.onLoading is a genuine defect — the apply
	// engine addresses those nodes, so the validator walks them too.
	tree := mustDecode(t, `{"id":"m","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"x"}},"state":{"onLoading":{"id":"m","kind":{"$type":"Skeleton","rows":1}}}}`)
	got := codes(ValidateNode(tree))
	if len(got) != 1 || got[0] != "FUARAN-DUP-ID" {
		t.Errorf("expected [DUPLICATE_NODE_ID], got %v", got)
	}
}

// ── FUARAN069 — the inert-control rule ───────────────────────────────────────
//
// An omitted handler is the DECLARATIVE shape, not a defect: the write-back
// default is supposed to carry the interaction. The defect is omitting the
// handler AND pointing the value at something unwritable. Both halves are pinned,
// because a rule that only ever fires is as useless as one that never does.

func staticBinding() wire.Obj {
	return wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": wire.Bool(false)}}
}

func stateBinding() wire.Obj {
	return wire.Obj{Tag: "State", Fields: map[string]wire.Value{"key": wire.Str("open")}}
}

func codesOf(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Code)
	}
	return out
}

func TestInertControlDisclosure(t *testing.T) {
	inertNode := wire.Node{ID: "d1", Kind: wire.Obj{Tag: "Disclosure", Fields: map[string]wire.Value{
		"heading": wire.Str("H"), "open": staticBinding(), "children": wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(inertNode)); len(got) != 1 || got[0] != "FUARAN069" {
		t.Errorf("inert disclosure: got %v; want [FUARAN069]", got)
	}

	live := wire.Node{ID: "d1", Kind: wire.Obj{Tag: "Disclosure", Fields: map[string]wire.Value{
		"heading": wire.Str("H"), "open": stateBinding(), "children": wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(live)); len(got) != 0 {
		t.Errorf("writable slot is live: got %v; want none", got)
	}

	handled := wire.Node{ID: "d1", Kind: wire.Obj{Tag: "Disclosure", Fields: map[string]wire.Value{
		"heading": wire.Str("H"), "open": staticBinding(),
		"onToggle": wire.Str("<closure>"), "children": wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(handled)); len(got) != 0 {
		t.Errorf("handler is live: got %v; want none", got)
	}
}

// Only a DISMISSABLE modal can be inert: one that cannot be dismissed by design
// is not inert, it is modal.
func TestInertControlModalOnlyWhenDismissable(t *testing.T) {
	dismissable := wire.Node{ID: "m1", Kind: wire.Obj{Tag: "Modal", Fields: map[string]wire.Value{
		"open": staticBinding(), "dismissable": wire.Bool(true), "children": wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(dismissable)); len(got) != 1 || got[0] != "FUARAN069" {
		t.Errorf("dismissable modal: got %v; want [FUARAN069]", got)
	}

	byDesign := wire.Node{ID: "m1", Kind: wire.Obj{Tag: "Modal", Fields: map[string]wire.Value{
		"open": staticBinding(), "dismissable": wire.Bool(false), "children": wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(byDesign)); len(got) != 0 {
		t.Errorf("non-dismissable modal: got %v; want none", got)
	}
}

// A defaulted filter is a read of a computed value, not a slot.
func TestFilterIsWritableOnlyWithoutADefault(t *testing.T) {
	writable := wire.Node{ID: "s1", Kind: wire.Obj{Tag: "Select", Fields: map[string]wire.Value{
		"label": wire.Str("L"), "source": staticBinding(),
		"value": wire.Obj{Tag: "Filter", Fields: map[string]wire.Value{"name": wire.Str("region")}},
	}}}
	if got := codesOf(ValidateNode(writable)); len(got) != 0 {
		t.Errorf("undefaulted filter is writable: got %v; want none", got)
	}

	defaulted := wire.Node{ID: "s1", Kind: wire.Obj{Tag: "Select", Fields: map[string]wire.Value{
		"label": wire.Str("L"), "source": staticBinding(),
		"value": wire.Obj{Tag: "Filter", Fields: map[string]wire.Value{
			"name": wire.Str("region"), "default": wire.Str("uk"),
		}},
	}}}
	if got := codesOf(ValidateNode(defaulted)); len(got) != 1 || got[0] != "FUARAN069" {
		t.Errorf("defaulted filter is a read: got %v; want [FUARAN069]", got)
	}
}

// The tag overlay is the second way a Tabs node can be live.
func TestInertControlTabsTagOverlay(t *testing.T) {
	inertNode := wire.Node{ID: "t1", Kind: wire.Obj{Tag: "Tabs", Fields: map[string]wire.Value{
		"activeIndex": staticBinding(), "children": wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(inertNode)); len(got) != 1 || got[0] != "FUARAN069" {
		t.Errorf("inert tabs: got %v; want [FUARAN069]", got)
	}

	viaTag := wire.Node{ID: "t1", Kind: wire.Obj{Tag: "Tabs", Fields: map[string]wire.Value{
		"activeIndex": staticBinding(),
		"tabTags":     wire.Arr{wire.Str("a"), wire.Str("b")},
		"activeTag":   wire.Obj{Tag: "State", Fields: map[string]wire.Value{"key": wire.Str("tab")}},
		"children":    wire.Arr{},
	}}}
	if got := codesOf(ValidateNode(viaTag)); len(got) != 0 {
		t.Errorf("tag overlay is live: got %v; want none", got)
	}
}

func TestInertFormFieldReportsItsOwnID(t *testing.T) {
	node := wire.Node{ID: "f1", Kind: wire.Obj{Tag: "Form", Fields: map[string]wire.Value{
		"submitLabel": wire.Str("Go"), "onSubmit": wire.Str("<closure>"),
		"fields": wire.Arr{wire.Obj{Fields: map[string]wire.Value{
			"id": wire.Str("email"), "label": wire.Str("Email"),
			"kind": wire.Obj{Tag: "Text", Fields: map[string]wire.Value{"value": staticBinding()}},
		}}},
	}}}
	findings := ValidateNode(node)
	if got := codesOf(findings); len(got) != 1 || got[0] != "FUARAN069" {
		t.Fatalf("inert form field: got %v; want [FUARAN069]", got)
	}
	if !strings.Contains(findings[0].Message, "FormField(email)") {
		t.Errorf("message should name the field: %q", findings[0].Message)
	}
}
