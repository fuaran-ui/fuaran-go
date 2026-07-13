package validator

import (
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
	if len(findings) != 1 || findings[0].Code != "EMPTY_NODE_ID" || findings[0].Path != "$.id" {
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
		if f.Code == "DUPLICATE_NODE_ID" {
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
	want := map[string]bool{"UNGROUNDED_SWITCH_STATE_KEY": false, "DUPLICATE_SWITCH_MATCH": false}
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
	if len(got) != 1 || got[0] != "DUPLICATE_NODE_ID" {
		t.Errorf("expected [DUPLICATE_NODE_ID], got %v", got)
	}
}
