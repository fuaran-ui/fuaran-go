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

// ── FUARAN100 — the unhonourable rule slot (Phase 864 vocabulary, adopted 869) ─

// formWithRule builds a one-field Form carrying a live value binding (so
// FUARAN069 stays silent and the codes below are unambiguous) plus a rule.
func formWithRule(control string, rule map[string]wire.Value) wire.Node {
	return wire.Node{ID: "signup", Kind: wire.Obj{Tag: "Form", Fields: map[string]wire.Value{
		"submitLabel": wire.Str("Go"), "onSubmit": wire.Str("<closure>"),
		"fields": wire.Arr{wire.Obj{Fields: map[string]wire.Value{
			"id": wire.Str("f"), "label": wire.Str("F"), "required": wire.Bool(true),
			"kind": wire.Obj{Tag: control, Fields: map[string]wire.Value{
				"value": wire.Obj{Tag: "State", Fields: map[string]wire.Value{"key": wire.Str("f")}},
			}},
			"rule": wire.Obj{Fields: rule},
		}}},
	}}}
}

func TestUnhonourableRuleSlot(t *testing.T) {
	// A Text control honours every slot — the fixture's own shape, and the case
	// that makes the rule non-vacuous: if this fired, the rule would be reporting
	// its own table rather than a defect.
	for _, rule := range []map[string]wire.Value{
		{"format": wire.Str("email")},
		{"pattern": wire.Str("[A-Z]+")},
		{"minLength": wire.Int(3), "maxLength": wire.Int(24)},
	} {
		if got := codesOf(ValidateNode(formWithRule("Text", rule))); len(got) != 0 {
			t.Errorf("Text honours %v: got %v; want none", rule, got)
		}
	}

	// TextArea honours the length/pattern bounds but not `format` — the one
	// asymmetric row in the table, and the row a copy of it would get wrong.
	if got := codesOf(ValidateNode(formWithRule("TextArea", map[string]wire.Value{"pattern": wire.Str("x")}))); len(got) != 0 {
		t.Errorf("TextArea honours a pattern: got %v; want none", got)
	}
	findings := ValidateNode(formWithRule("TextArea", map[string]wire.Value{"format": wire.Str("email")}))
	if got := codesOf(findings); len(got) != 1 || got[0] != "FUARAN100" {
		t.Fatalf("format on a TextArea: got %v; want [FUARAN100]", got)
	}
	if findings[0].Severity != SeverityWarning {
		t.Errorf("severity = %v, want Warning — the projection is a host's", findings[0].Severity)
	}
	for _, want := range []string{"form 'signup'", "field 'f'", "rule.format", "TextArea", "dead intent"} {
		if !strings.Contains(findings[0].Message, want) {
			t.Errorf("message missing %q: %q", want, findings[0].Message)
		}
	}
	if want := "$.kind.fields[0].rule.format"; findings[0].Path != want {
		t.Errorf("path = %q, want %q", findings[0].Path, want)
	}

	// A Checkbox honours none of them. All three text bounds report, in
	// declaration order, so a field with several dead slots names each.
	findings = ValidateNode(formWithRule("Checkbox", map[string]wire.Value{
		"format": wire.Str("email"), "pattern": wire.Str("x"),
		"minLength": wire.Int(1), "maxLength": wire.Int(9),
	}))
	if got := len(findings); got != 4 {
		t.Fatalf("checkbox with four dead slots: got %d findings; want 4 (%v)", got, codesOf(findings))
	}
	for i, want := range []string{"format", "pattern", "minLength", "maxLength"} {
		if suffix := ".rule." + want; !strings.HasSuffix(findings[i].Path, suffix) {
			t.Errorf("finding %d path = %q, want suffix %q", i, findings[i].Path, suffix)
		}
	}

	// `compare` is deliberately absent from the honour table on BOTH hosts: it
	// compares the field's VALUE, which every control has. A Date field whose
	// only rule is a compare is clean — which is the corpus fixture's own
	// `hire-end-date`, so this pins the fixture staying finding-free.
	compareOnly := formWithRule("Date", map[string]wire.Value{
		"compare": wire.Obj{Fields: map[string]wire.Value{
			"op": wire.Str("GreaterThanOrEqual"),
			"against": wire.Obj{Tag: "State", Fields: map[string]wire.Value{
				"key": wire.Str("hire-start-date"),
			}},
		}},
	})
	if got := codesOf(ValidateNode(compareOnly)); len(got) != 0 {
		t.Errorf("a compare-only rule is honourable on every control: got %v; want none", got)
	}

	// An unrecognised control is left ALONE. This rule's claim is that a KNOWN
	// control cannot honour a slot; "I do not know this control" is a different
	// statement, and reporting it as this code would be a false one.
	if got := codesOf(ValidateNode(formWithRule("Hologram", map[string]wire.Value{"format": wire.Str("email")}))); len(got) != 0 {
		t.Errorf("unknown control must not raise FUARAN100: got %v", got)
	}
}
