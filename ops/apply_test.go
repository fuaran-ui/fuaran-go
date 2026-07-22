package ops

import (
	"errors"
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

func mustEncode(t *testing.T, n wire.Node) string {
	t.Helper()
	s, err := wire.EncodeNode(n)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	return s
}

func stackOf(id string, children ...string) string {
	body := ""
	for i, c := range children {
		if i > 0 {
			body += ","
		}
		body += c
	}
	return `{"id":"` + id + `","kind":{"$type":"Box","children":[` + body +
		`],"layout":{"$type":"Flex","direction":"Vertical","wrap":false},"role":"Group"}}`
}

func markdownOf(id, text string) string {
	return `{"id":"` + id + `","kind":{"$type":"Markdown","text":"` + text + `"}}`
}

func applyErrCode(t *testing.T, op wire.Obj, tree wire.Node) ApplyErrorCode {
	t.Helper()
	after, err := Apply(op, tree)
	var aerr *ApplyError
	if !errors.As(err, &aerr) {
		t.Fatalf("expected an *ApplyError, got %v", err)
	}
	// On any failure the input tree is returned untouched.
	if got, want := mustEncode(t, after), mustEncode(t, tree); got != want {
		t.Errorf("failed apply mutated the tree:\n got %s\nwant %s", got, want)
	}
	if CanApply(op, tree) {
		t.Errorf("CanApply = true for a failing op (law: canApply ≡ apply success)")
	}
	return aerr.Code
}

func TestErrorPaths(t *testing.T) {
	markdown := mustDecode(t, markdownOf("a", "x"))
	rootMD := mustDecode(t, markdownOf("root", "x"))
	stackA := mustDecode(t, stackOf("s", markdownOf("a", "x")))
	stackDup := mustDecode(t, stackOf("s", markdownOf("dup", "x")))
	stackAB := mustDecode(t, stackOf("s", markdownOf("a", "x"), markdownOf("b", "y")))
	heading := mustDecode(t, `{"id":"h","kind":{"$type":"Heading","level":2,"text":"Title","variant":"Standard"}}`)
	nested := mustDecode(t, stackOf("outer", stackOf("inner", markdownOf("leaf", "x"))))
	child := mustDecode(t, markdownOf("new", "y"))
	dupChild := mustDecode(t, markdownOf("dup", "y"))

	obj := func(tag string, fields map[string]wire.Value) wire.Obj {
		return wire.Obj{Tag: tag, Fields: fields}
	}

	cases := []struct {
		name string
		op   wire.Obj
		tree wire.Node
		want ApplyErrorCode
	}{
		{"node not found", obj("RemoveNode", map[string]wire.Value{"target": wire.Str("ghost")}), markdown, CodeNodeNotFound},
		{"remove root", obj("RemoveNode", map[string]wire.Value{"target": wire.Str("root")}), rootMD, CodeKindMismatch},
		{"insert into childless kind", obj("InsertChild", map[string]wire.Value{
			"child": child, "parentId": wire.Str("a"), "position": wire.Int(0),
		}), stackA, CodeChildlessKind},
		{"insert position out of range", obj("InsertChild", map[string]wire.Value{
			"child": child, "parentId": wire.Str("s"), "position": wire.Int(5),
		}), stackA, CodePositionOutOfRange},
		{"insert duplicate id", obj("InsertChild", map[string]wire.Value{
			"child": dupChild, "parentId": wire.Str("s"), "position": wire.Int(0),
		}), stackDup, CodeDuplicateNodeID},
		{"unknown field", obj("UpdateProp", map[string]wire.Value{
			"path": wire.Str("Nope"), "target": wire.Str("a"), "value": wire.Str("v"),
		}), markdown, CodeFieldNotFound},
		{"nested path without a nested surface", obj("UpdateProp", map[string]wire.Value{
			"path": wire.Str("Spec.Text"), "target": wire.Str("a"), "value": wire.Str("v"),
		}), markdown, CodePathNotSupportedYet},
		{"update-prop type mismatch", obj("UpdateProp", map[string]wire.Value{
			"path": wire.Str("Level"), "target": wire.Str("h"), "value": wire.Str("not-an-int"),
		}), heading, CodeKindMismatch},
		{"slot not found", obj("ReplaceBinding", map[string]wire.Value{
			"binding": wire.Obj{Tag: "Static", Fields: map[string]wire.Value{"value": wire.Int(1)}},
			"slot":    wire.Str("Source"), "target": wire.Str("a"),
		}), markdown, CodeSlotNotFound},
		{"reorder mismatch", obj("ReorderChildren", map[string]wire.Value{
			"newOrder": wire.Arr{wire.Str("a"), wire.Str("z")}, "parentId": wire.Str("s"),
		}), stackAB, CodeOrderingMismatch},
		{"move into own descendant", obj("MoveNode", map[string]wire.Value{
			"newParentId": wire.Str("inner"), "newPosition": wire.Int(0), "target": wire.Str("outer"),
		}), nested, CodeKindMismatch},
		{"list segment without index", obj("UpdateProp", map[string]wire.Value{
			"path": wire.Str("Columns.Label"), "target": wire.Str("g"), "value": wire.Str("X"),
		}), mustDecode(t, `{"id":"g","kind":{"$type":"DataGrid","columns":[],"editable":false,"source":{"$type":"Static","value":"<opaque>"}}}`), CodePathInvalid},
		{"closure leaf not addressable", obj("UpdateProp", map[string]wire.Value{
			"path": wire.Str("Columns[0].Kind"), "target": wire.Str("g"), "value": wire.Str("Text"),
		}), mustDecode(t, `{"id":"g","kind":{"$type":"DataGrid","columns":[{"format":{"$type":"None"},"kind":{"$type":"Text"},"label":"A","value":"<closure>","width":{"$type":"Auto"}}],"editable":false,"source":{"$type":"Static","value":"<opaque>"}}}`), CodePathNotSupportedYet},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := applyErrCode(t, c.op, c.tree); got != c.want {
				t.Errorf("code = %s, want %s", got, c.want)
			}
		})
	}
}

func TestBatchAbortsAllOrNothing(t *testing.T) {
	base := mustDecode(t, stackOf("s", markdownOf("a", "x")))
	good := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path": wire.Str("Text"), "target": wire.Str("a"), "value": wire.Str("changed"),
	}}
	bad := wire.Obj{Tag: "RemoveNode", Fields: map[string]wire.Value{"target": wire.Str("ghost")}}
	batch := wire.Obj{Tag: "Batch", Fields: map[string]wire.Value{"ops": wire.Arr{good, bad}}}

	after, err := Apply(batch, base)
	var aerr *ApplyError
	if !errors.As(err, &aerr) {
		t.Fatalf("expected an *ApplyError, got %v", err)
	}
	if aerr.Code != CodeBatchAborted || aerr.BatchIndex != 1 {
		t.Errorf("got %s at inner op %d, want BatchAborted at 1", aerr.Code, aerr.BatchIndex)
	}
	if got, want := mustEncode(t, after), mustEncode(t, base); got != want {
		t.Errorf("aborted batch left partial state:\n got %s\nwant %s", got, want)
	}
}

func TestNestedTabHeaderLabel(t *testing.T) {
	tabs := func(second string) string {
		return `{"id":"analysis-tabs","kind":{"$type":"Tabs","children":[` +
			markdownOf("tab-a", "A") + `,` + markdownOf("tab-b", "B") +
			`],"tabHeaders":[{"label":"Overview"},{"label":"` + second + `"}]}}`
	}
	op := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path":   wire.Str("TabHeaders[1].Label"),
		"target": wire.Str("analysis-tabs"),
		"value":  wire.Obj{Tag: "Literal", Fields: map[string]wire.Value{"text": wire.Str("Breakdown")}},
	}}
	after, err := Apply(op, mustDecode(t, tabs("Detail")))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, want := mustEncode(t, after), tabs("Breakdown"); got != want {
		t.Errorf("tab header not renamed:\n got %s\nwant %s", got, want)
	}
	if !CanApply(op, mustDecode(t, tabs("Detail"))) {
		t.Error("CanApply = false for a succeeding op")
	}
}

func TestNestedTabHeadersAbsentIsOutOfRange(t *testing.T) {
	base := mustDecode(t, `{"id":"bare-tabs","kind":{"$type":"Tabs","children":[`+markdownOf("only", "x")+`]}}`)
	op := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path":   wire.Str("TabHeaders[0].Label"),
		"target": wire.Str("bare-tabs"),
		"value":  wire.Obj{Tag: "Literal", Fields: map[string]wire.Value{"text": wire.Str("X")}},
	}}
	if got := applyErrCode(t, op, base); got != CodePositionOutOfRange {
		t.Errorf("code = %s, want PositionOutOfRange", got)
	}
}

// State-surface traversal: a node inside state.onLoading is addressable.
func TestApplyReachesStateSurfaces(t *testing.T) {
	base := mustDecode(t, `{"id":"m","kind":{"$type":"Markdown","text":"body"},"state":{"onLoading":{"id":"skel","kind":{"$type":"Skeleton","rows":3}}}}`)
	op := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
		"path": wire.Str("Rows"), "target": wire.Str("skel"), "value": wire.Int(5),
	}}
	after, err := Apply(op, base)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := `{"id":"m","kind":{"$type":"Markdown","text":"body"},"state":{"onLoading":{"id":"skel","kind":{"$type":"Skeleton","rows":5}}}}`
	if got := mustEncode(t, after); got != want {
		t.Errorf("state-surface apply diverged:\n got %s\nwant %s", got, want)
	}
}
