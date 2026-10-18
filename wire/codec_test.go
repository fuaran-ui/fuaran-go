package wire

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire/idl"
)

func mustEncode(t *testing.T, v Value) string {
	t.Helper()
	s, err := encodeValue(v)
	if err != nil {
		t.Fatalf("encodeValue: %v", err)
	}
	return s
}

func TestCanonicalNumberLayouts(t *testing.T) {
	cases := []struct {
		v    Value
		want string
	}{
		{Int(42), "42"},
		{Int(-7), "-7"},
		{Int(0), "0"},
		{Float(1e21), "1E+21"},
		{Float(1e-7), "1E-07"},
		{Float(1234.5), "1234.5"},
		{Float(0.07), "0.07"},
	}
	for _, c := range cases {
		if got := mustEncode(t, c.v); got != c.want {
			t.Errorf("encode(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestCanonicalStringEscaping(t *testing.T) {
	// Quote and backslash escape; a control char becomes lower-case 4-digit
	// u-escape; '/' and non-ASCII pass through literally. The backslash and
	// the control char are built from runes so this source file never carries
	// a raw control byte (git would mark it binary) nor an escape sequence a
	// tooling layer could re-interpret.
	backslash := string(rune(92)) // one backslash character
	control := string(rune(1))    // U+0001
	input := `a"b` + backslash + "c" + control + "d/€"
	want := `"a` + backslash + `"b` + backslash + backslash + "c" + backslash + `u0001d/€"`
	if got := mustEncode(t, Str(input)); got != want {
		t.Errorf("escaped string = %q, want %q", got, want)
	}
}

func TestObjKeysSortedTypeFirst(t *testing.T) {
	obj := Obj{Tag: "Static", Fields: map[string]Value{"value": Int(1), "b": Bool(true), "a": Null{}}}
	got := mustEncode(t, obj)
	want := `{"$type":"Static","a":null,"b":true,"value":1}`
	if got != want {
		t.Errorf("encoded obj = %q, want %q", got, want)
	}
}

func TestSmallNodeRoundTrip(t *testing.T) {
	// 0.2.0 — the bare string IS the canonical Literal form; the verbose
	// envelope decodes (read-compat) and normalises to the bare string.
	in := `{"id":"m1","kind":{"$type":"Markdown","text":"héllo"}}`
	node, err := DecodeNode(in)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	out, err := EncodeNode(node)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	if out != in {
		t.Errorf("round trip diverged:\n got %s\nwant %s", out, in)
	}

	verbose := `{"id":"m1","kind":{"$type":"Markdown","text":{"$type":"Literal","text":"héllo"}}}`
	node, err = DecodeNode(verbose)
	if err != nil {
		t.Fatalf("DecodeNode (verbose Literal): %v", err)
	}
	out, err = EncodeNode(node)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	if out != in {
		t.Errorf("verbose Literal did not normalise:\n got %s\nwant %s", out, in)
	}
}

// Phase 867 — `Metric.trendPolarity`, and specifically the three claims that a
// corpus round-trip alone CANNOT make about it.
//
// This host's spec builder preserves unconsumed keys structurally, so
// `metric-inverted-polarity.json` round-tripped byte-for-byte here before a line
// of this phase was written — the fixture passed while the field was modelled
// nowhere. A green round-trip is therefore evidence about the ENCODER and says
// nothing about whether the slot is understood. What follows is the part the
// fixture cannot reach: that the vocabulary is CLOSED, that the default is
// dropped, and that a declared non-default survives as a typed value.
func TestMetricTrendPolarity(t *testing.T) {
	const declared = `{"id":"m","kind":{"$type":"Metric","label":"Avg wait","trend":{"$type":"Static","value":-0.0734},"trendPolarity":"LowerIsBetter","value":{"$type":"Static","value":80}}}`

	node, err := DecodeNode(declared)
	if err != nil {
		t.Fatalf("DecodeNode: %v", err)
	}
	out, err := EncodeNode(node)
	if err != nil {
		t.Fatalf("EncodeNode: %v", err)
	}
	if out != declared {
		t.Errorf("declared polarity did not round-trip:\n got %s\nwant %s", out, declared)
	}
	// The typed claim the round-trip cannot make: it is a decoded Str in the
	// slot, not an untyped passthrough of whatever bytes arrived.
	if got, ok := node.Kind.Fields["trendPolarity"].(Str); !ok || string(got) != "LowerIsBetter" {
		t.Errorf("trendPolarity = %#v, want Str(\"LowerIsBetter\")", node.Kind.Fields["trendPolarity"])
	}

	// §3.6.1 clause 4 — an absent slot IS HigherIsBetter, so an explicit default
	// normalises away rather than riding to the wire as a redundant byte.
	const explicitDefault = `{"id":"m","kind":{"$type":"Metric","label":"L","trendPolarity":"HigherIsBetter","value":{"$type":"Static","value":1}}}`
	const omitted = `{"id":"m","kind":{"$type":"Metric","label":"L","value":{"$type":"Static","value":1}}}`
	node, err = DecodeNode(explicitDefault)
	if err != nil {
		t.Fatalf("DecodeNode (explicit default): %v", err)
	}
	if out, err = EncodeNode(node); err != nil {
		t.Fatalf("EncodeNode: %v", err)
	} else if out != omitted {
		t.Errorf("explicit default did not normalise away:\n got %s\nwant %s", out, omitted)
	}

	// The vocabulary is CLOSED. `Neutral` is the reserved case and is refused
	// exactly like a name nobody has ever proposed — pinned by name, because the
	// day it is admitted this line is what says the admission was deliberate.
	for _, bad := range []string{"Neutral", "lowerisbetter", "Inverted"} {
		input := `{"id":"m","kind":{"$type":"Metric","label":"L","trendPolarity":"` + bad + `","value":{"$type":"Static","value":1}}}`
		_, err := DecodeNode(input)
		var derr *DecodeError
		if !errors.As(err, &derr) {
			t.Fatalf("trendPolarity %q: expected a *DecodeError, got %v", bad, err)
		}
		if derr.Code != CodeUnknownDUCase {
			t.Errorf("trendPolarity %q: code = %s, want %s", bad, derr.Code, CodeUnknownDUCase)
		}
		if want := "$.kind.trendPolarity"; !strings.HasPrefix(derr.Path, want) {
			t.Errorf("trendPolarity %q: path = %q, want prefix %q", bad, derr.Path, want)
		}
	}
}

func TestBatchOpRoundTrip(t *testing.T) {
	in := `{"$type":"Batch","ops":[{"$type":"RemoveNode","target":"m1"},{"$type":"UpdateProp","path":"Label","target":"m2","value":"New"}]}`
	op, err := DecodeOp(in)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	out, err := EncodeOp(op)
	if err != nil {
		t.Fatalf("EncodeOp: %v", err)
	}
	if out != in {
		t.Errorf("round trip diverged:\n got %s\nwant %s", out, in)
	}
}

func TestNodeRejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
		code  DecodeErrorCode
		path  string
	}{
		{"invalid json", "this is not json", CodeInvalidJSON, "$"},
		{"empty input", "", CodeInvalidJSON, "$"},
		{"trailing content", `{"id":"x","kind":{"$type":"Skeleton","rows":1}} trailing`, CodeInvalidJSON, "$"},
		{"missing id", `{"kind":{"$type":"Skeleton","rows":1}}`, CodeMissingField, "$.id"},
		{"empty id", `{"id":"","kind":{"$type":"Skeleton","rows":1}}`, CodeEmptyNodeID, "$.id"},
		{"id wrong type", `{"id":3,"kind":{"$type":"Skeleton","rows":1}}`, CodeWrongType, "$.id"},
		{"unknown kind", `{"id":"x","kind":{"$type":"Widget"}}`, CodeWrongNodeKind, "$.kind.$type"},
		{"rows wrong type", `{"id":"x","kind":{"$type":"Skeleton","rows":"3"}}`, CodeWrongType, "$.kind.rows"},
		{"unknown tone", `{"id":"x","kind":{"$type":"Skeleton","rows":1},"style":{"emphasis":"Normal","tone":"Magenta","weight":"Standard"}}`, CodeUnknownDUCase, "$.style.tone"},
		{"null custom prop", `{"id":"x","kind":{"$type":"Custom","componentId":"c","moduleId":"m","props":{"k":null}}}`, CodeWrongType, "$.kind.props.k"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeNode(c.input)
			var derr *DecodeError
			if !errors.As(err, &derr) {
				t.Fatalf("expected a *DecodeError, got %v", err)
			}
			if derr.Code != c.code {
				t.Errorf("code = %s, want %s", derr.Code, c.code)
			}
			if !strings.HasPrefix(derr.Path, c.path) {
				t.Errorf("path = %q, want prefix %q", derr.Path, c.path)
			}
		})
	}
}

func TestOpRejectsSentinelValue(t *testing.T) {
	_, err := DecodeOp(`{"$type":"UpdateProp","path":"Label","target":"m","value":"<closure>"}`)
	var derr *DecodeError
	if !errors.As(err, &derr) {
		t.Fatalf("expected a *DecodeError, got %v", err)
	}
	if derr.Code != CodeWrongType || derr.Path != "$.value" {
		t.Errorf("got %s at %s, want WRONG_TYPE at $.value", derr.Code, derr.Path)
	}
}

// Phase 2291 — PatchStyle's StyleDelta keeps every member it sets, a default
// token included: absence means "leave the field as it is", so a default must
// survive the round trip where a SemanticStyle would normalise it away.
func TestPatchStyleRoundTrip(t *testing.T) {
	for _, in := range []string{
		`{"$type":"PatchStyle","style":{"tone":"Success","voice":"Display"},"target":"metric-1"}`,
		`{"$type":"PatchStyle","style":{"tone":"Default"},"target":"metric-1"}`,
		`{"$type":"PatchStyle","style":{"direction":"auto","emphasis":"Normal","role":"None","tone":"Default","voice":"Default","weight":"Standard"},"target":"metric-1"}`,
		`{"$type":"Batch","ops":[{"$type":"PatchStyle","style":{"weight":"Spacious"},"target":"a"}]}`,
	} {
		op, err := DecodeOp(in)
		if err != nil {
			t.Fatalf("DecodeOp(%s): %v", in, err)
		}
		out, err := EncodeOp(op)
		if err != nil {
			t.Fatalf("EncodeOp: %v", err)
		}
		if out != in {
			t.Errorf("round trip diverged:\n got %s\nwant %s", out, in)
		}
	}
	// The same payload as an UpdateStyle still normalises the default away.
	op, err := DecodeOp(`{"$type":"UpdateStyle","style":{"tone":"Default"},"target":"metric-1"}`)
	if err != nil {
		t.Fatalf("DecodeOp: %v", err)
	}
	if out, _ := EncodeOp(op); out != `{"$type":"UpdateStyle","style":{},"target":"metric-1"}` {
		t.Errorf("UpdateStyle default normalisation changed: %s", out)
	}
}

func TestPatchStyleRejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
		code  DecodeErrorCode
		path  string
	}{
		{"empty delta", `{"$type":"PatchStyle","style":{},"target":"n1"}`, CodeWrongType, "$.style"},
		{"only unknown keys", `{"$type":"PatchStyle","style":{"colour":"red"},"target":"n1"}`, CodeWrongType, "$.style"},
		{"unknown token", `{"$type":"PatchStyle","style":{"tone":"Neon"},"target":"n1"}`, CodeUnknownDUCase, "$.style.tone"},
		{"non-string token", `{"$type":"PatchStyle","style":{"direction":true},"target":"n1"}`, CodeWrongType, "$.style.direction"},
		{"missing style", `{"$type":"PatchStyle","target":"n1"}`, CodeMissingField, "$.style"},
		{"missing target", `{"$type":"PatchStyle","style":{"tone":"Brand"}}`, CodeMissingField, "$.target"},
		{"style not an object", `{"$type":"PatchStyle","style":"Brand","target":"n1"}`, CodeWrongType, "$.style"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := DecodeOp(c.input)
			var derr *DecodeError
			if !errors.As(err, &derr) {
				t.Fatalf("expected a *DecodeError, got %v", err)
			}
			if derr.Code != c.code || derr.Path != c.path {
				t.Errorf("got %s at %s, want %s at %s", derr.Code, derr.Path, c.code, c.path)
			}
		})
	}
}

// TestHandDecodersReadOnlyDeclaredRows — the union-case and record twin of
// TestHandArmsReadOnlyDeclaredRows (Phase 2330). Every hand union or record
// decoder reads its fields through a spec bound to its row set, and every key it
// reads by name is a row of that set: of the record, or of the union case named
// by the switch clause the read sits in (of any case of the union where the
// read is outside a literal clause). spec.field and spec.has read an undeclared
// key as optional, so this is what keeps that path empty.
func TestHandDecodersReadOnlyDeclaredRows(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "decode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	specs, reads := 0, 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// The row set the function's spec is bound to.
		var record *idl.Record
		var union *idl.Union
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 4 {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || (name.Name != "newCaseSpec" && name.Name != "newRecordSpec") {
				return true
			}
			sel, ok := call.Args[3].(*ast.UnaryExpr)
			if !ok {
				return true
			}
			ref, ok := sel.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			specs++
			switch {
			case strings.HasPrefix(ref.Sel.Name, "Record"):
				record, _ = idl.RecordByName(strings.TrimPrefix(ref.Sel.Name, "Record"))
			case strings.HasPrefix(ref.Sel.Name, "Union"):
				union, _ = idl.UnionByName(strings.TrimPrefix(ref.Sel.Name, "Union"))
			}
			if record == nil && union == nil {
				t.Errorf("%s binds a spec to %s, which idl.json does not declare", fn.Name.Name, ref.Sel.Name)
			}
			return true
		})
		if record == nil && union == nil {
			continue
		}
		var clauses []*ast.CaseClause
		var visit func(n ast.Node) bool
		visit = func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				clauses = append(clauses, cc)
				for _, stmt := range cc.Body {
					ast.Inspect(stmt, visit)
				}
				clauses = clauses[:len(clauses)-1]
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "field" && sel.Sel.Name != "has") {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "s" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			reads++
			if record != nil {
				if _, ok := record.Field(key); !ok {
					t.Errorf("%s reads %q, which is not a row of idl.json's %s", fn.Name.Name, key, record.Name)
				}
				return true
			}
			var tags []string
			if len(clauses) > 0 {
				for _, e := range clauses[len(clauses)-1].List {
					if l, ok := e.(*ast.BasicLit); ok && l.Kind == token.STRING {
						tag, _ := strconv.Unquote(l.Value)
						if _, declared := union.Case(tag); declared {
							tags = append(tags, tag)
						}
					}
				}
			}
			if len(tags) == 0 {
				tags = union.CaseTags()
			}
			for _, tag := range tags {
				if c, _ := union.Case(tag); c != nil {
					if _, ok := c.Field(key); ok {
						return true
					}
				}
			}
			t.Errorf("%s reads %q, which is not a row of idl.json's %s %v", fn.Name.Name, key, union.Name, tags)
			return true
		}
		ast.Inspect(fn.Body, visit)
	}
	if specs < 30 || reads < 150 {
		t.Fatalf("found only %d row-bound specs and %d reads — the guard is not looking at the decoders", specs, reads)
	}
}

// TestNoHandStatedRequiredness — the acceptance of Phase 2330, held: no union or
// record decoder states a required field by hand. Required-ness is the table's
// answer (spec.field / spec.has, and absentLeniency for decode policy), so the
// only direct presence refusals left in decode.go are the two that are not a
// row's required-ness: Bound, a section-16 lenient spelling with no row, and
// Switch's `on` / `stateKey` pair, a relational rule over two optional rows.
// Before the phase decode.go held 140 such reads, 136 of them in 33 union and
// record decoders.
func TestNoHandStatedRequiredness(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "decode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch f := call.Fun.(type) {
			case *ast.Ident:
				if f.Name == "require" {
					found = append(found, fn.Name.Name+":require")
				}
			case *ast.SelectorExpr:
				if f.Sel.Name == "req" {
					found = append(found, fn.Name.Name+":req")
				}
			}
			return true
		})
	}
	want := []string{"decodeBindingTyped:require", "armSwitch:req"}
	if strings.Join(found, ",") != strings.Join(want, ",") {
		t.Errorf("hand-stated required reads in decode.go: got %v, want %v", found, want)
	}
}
