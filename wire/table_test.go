package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

// TestHandArmsReadOnlyDeclaredRows — every hand arm is an arm for a kind
// idl.json declares, and every field it reads by name is a row of that kind's
// table. spec.field reads an undeclared key as optional rather than failing a
// decode, so this is what keeps that path empty: a hand arm left reading a row
// the IDL renamed or removed is red here, not silently tolerant.
func TestHandArmsReadOnlyDeclaredRows(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "decode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	arms := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "arm") {
			continue
		}
		if recv, ok := fn.Recv.List[0].Type.(*ast.Ident); !ok || recv.Name != "handArms" {
			continue
		}
		arms++
		tag := strings.TrimPrefix(fn.Name.Name, "arm")
		kind, ok := idl.KindByTag(tag)
		if !ok {
			t.Errorf("hand arm %s decodes a kind idl.json does not declare", fn.Name.Name)
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "s" {
				return true
			}
			switch sel.Sel.Name {
			case "field", "req", "opt", "optDrop", "sentinel", "take":
			default:
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			if _, ok := kind.Field(key); !ok {
				t.Errorf("%s reads %q, which is not a row of idl.json's %s", fn.Name.Name, key, tag)
			}
			return true
		})
	}
	if arms < 30 {
		t.Fatalf("found only %d hand arms — the guard is not looking at the arm set", arms)
	}
}

// TestEveryKindRefusesAMissingRequiredRow — every kind with a required row
// refuses an object carrying none of its fields with MISSING_FIELD, whether its
// arm is a hand arm or the table arm: a kind with no hand arm is held to its
// table exactly as one with a hand arm is.
func TestEveryKindRefusesAMissingRequiredRow(t *testing.T) {
	for _, k := range idl.Kinds {
		if len(k.RequiredFields()) == 0 {
			continue
		}
		err := func() (err error) {
			defer recoverDecode(&err)
			dispatchKindArm(handArms{}, newWalkState(), k.Tag, map[string]any{"$type": k.Tag}, "$.kind")
			return nil
		}()
		if derr, ok := err.(*DecodeError); !ok || derr.Code != CodeMissingField {
			t.Errorf("%s with no fields: got %v, want MISSING_FIELD", k.Tag, err)
		}
	}
}

// Phase 2297 absorbs 1926: the five kinds that had no typed decoder decode
// through their tables, so what is beneath them is typed, counted and refused.
func TestStructuralKindsDecodeTyped(t *testing.T) {
	n, err := DecodeNode(`{"id":"s","kind":{"$type":"SplitPanel","children":[{"id":"m","kind":{"$type":"Markdown","text":"x"}}],"weight":0.6}}`)
	if err != nil {
		t.Fatal(err)
	}
	children, ok := n.Kind.Fields["children"].(Arr)
	if !ok || len(children) != 1 {
		t.Fatalf("children = %#v", n.Kind.Fields["children"])
	}
	if _, ok := children[0].(Node); !ok {
		t.Errorf("a SplitPanel child decoded as %T, want Node", children[0])
	}
	eb, err := DecodeNode(`{"id":"e","kind":{"$type":"ErrorBoundary","child":{"id":"c","kind":{"$type":"Markdown","text":"x"}},"fallback":{"id":"f","kind":{"$type":"Markdown","text":"y"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, arm := range []string{"child", "fallback"} {
		if _, ok := eb.Kind.Fields[arm].(Node); !ok {
			t.Errorf("ErrorBoundary.%s decoded as %T, want Node", arm, eb.Kind.Fields[arm])
		}
	}
	ref, err := DecodeNode(`{"id":"r","kind":{"$type":"FragmentRef","args":{"content":{"$type":"SlotArg","tree":{"id":"t","kind":{"$type":"Markdown","text":"x"}}}},"name":"card"}}`)
	if err != nil {
		t.Fatal(err)
	}
	slot := ref.Kind.Fields["args"].(Obj).Fields["content"].(Obj)
	if _, ok := slot.Fields["tree"].(Node); !ok {
		t.Errorf("a FragmentRef slot argument decoded as %T, want Node", slot.Fields["tree"])
	}

	for _, c := range []struct {
		name, doc string
		code      DecodeErrorCode
		path      string
	}{
		{"bad child kind", `{"id":"s","kind":{"$type":"SplitPanel","children":[{"id":"m","kind":{"$type":"Nope"}}],"weight":0.6}}`, CodeWrongNodeKind, "$.kind.children[0].kind.$type"},
		{"missing weight", `{"id":"s","kind":{"$type":"SplitPanel","children":[]}}`, CodeMissingField, "$.kind.weight"},
		{"missing fallback", `{"id":"e","kind":{"$type":"ErrorBoundary","child":{"id":"c","kind":{"$type":"Markdown","text":"x"}}}}`, CodeMissingField, "$.kind.fallback"},
		{"missing name", `{"id":"r","kind":{"$type":"FragmentRef"}}`, CodeMissingField, "$.kind.name"},
		{"disclosure open", `{"id":"d","kind":{"$type":"Disclosure","children":[],"defaultOpen":true,"heading":"h","open":"yes"}}`, CodeWrongType, "$.kind.open"},
		{"unknown arg case", `{"id":"r","kind":{"$type":"FragmentRef","args":{"a":{"$type":"Date","value":1}},"name":"n"}}`, CodeUnknownDUCase, "$.kind.args.a.$type"},
	} {
		_, err := DecodeNode(c.doc)
		derr, ok := err.(*DecodeError)
		if !ok || derr.Code != c.code || !strings.HasPrefix(derr.Path, c.path) {
			t.Errorf("%s: got %v, want %s at %s", c.name, err, c.code, c.path)
		}
	}
}

// Nodes under a formerly structural kind count toward the section 21 total.
func TestStructuralKindChildrenCountTowardMaxNodes(t *testing.T) {
	leaf := `{"id":"m","kind":{"$type":"Markdown","text":""}}`
	half := MaxNodes/2 + 1
	panel := `{"id":"p","kind":{"$type":"SplitPanel","children":[` + strings.TrimSuffix(strings.Repeat(leaf+",", half), ",") + `],"weight":0.5}}`
	doc := `{"id":"b","kind":{"$type":"Box","children":[` + panel + `,` + panel + `],"layout":{"$type":"Auto"},"role":"Group"}}`
	_, err := DecodeNode(doc)
	derr, ok := err.(*DecodeError)
	if !ok || derr.Code != CodeLimitExceeded {
		t.Fatalf("%d nodes under two SplitPanels: got %v, want LIMIT_EXCEEDED", 2*half+3, err)
	}
}

// WIRE_FORMAT.md section 29.3 — several bad values in one open map are reported
// in Ordinal KEY order, whatever order the document wrote them in.
func TestOpenMapDefectsReportInOrdinalKeyOrder(t *testing.T) {
	doc := `{"id":"g","kind":{"$type":"DataGrid","columns":[{"field":"s","kind":{"$type":"TonedPill","field":"s","map":{"On time":"Fine","Delayed":"Late","Cancelled":"Gone"}},"label":"S"}],"source":{"$type":"Static","value":[]}}}`
	for i := 0; i < 20; i++ {
		_, err := DecodeNode(doc)
		derr, ok := err.(*DecodeError)
		if !ok || derr.Path != "$.kind.columns[0].kind.map.Cancelled" {
			t.Fatalf("run %d: got %v, want the Ordinal-first key Cancelled", i, err)
		}
	}
}

// matchesDefault compares canonical encodings, so it is exact on the payload:
// a Static carrying 0 is Tabs.activeIndex's identity and a Static carrying 1 is
// not.
func TestOmitAtDefaultComparesTheWholeValue(t *testing.T) {
	row, _ := idl.KindTabs.Field("activeIndex")
	zero := Obj{Tag: "Static", Fields: map[string]Value{"value": Int(0)}}
	one := Obj{Tag: "Static", Fields: map[string]Value{"value": Int(1)}}
	state := Obj{Tag: "State", Fields: map[string]Value{"key": Str("tab")}}
	if !matchesDefault(zero, row.Default) {
		t.Errorf("Static 0 is not recognised as %s", row.Default)
	}
	if matchesDefault(one, row.Default) || matchesDefault(state, row.Default) {
		t.Error("a non-identity binding matched the default")
	}
}
