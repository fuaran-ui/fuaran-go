package wire

import (
	"errors"
	"strings"
	"testing"
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

// Every kind with a typed decoder must be a recognised kind, and the
// special-cased handlers (Box, the legacy containers, Table) must not also
// carry a builder — a builder for an unroutable kind would be dead code. The
// validator's required-fields registry must likewise name only routable kinds.
func TestKindSchemasAreRegistered(t *testing.T) {
	for kind := range kindBuilders {
		if !knownKinds.has(kind) {
			t.Errorf("kindBuilders[%q] is not in knownKinds", kind)
		}
		if kind == "Box" || kind == "Table" || legacyContainerTags[kind] {
			t.Errorf("kindBuilders[%q] is shadowed by a dedicated handler", kind)
		}
	}
	for kind := range requiredKindFields {
		if !knownKinds.has(kind) {
			t.Errorf("requiredKindFields[%q] is not in knownKinds", kind)
		}
	}
	for op := range opSchemas {
		if !opCases.has(op) {
			t.Errorf("opSchemas[%q] is not in opCases", op)
		}
	}
	for _, op := range KnownOpKinds() {
		if _, ok := opSchemas[op]; !ok {
			t.Errorf("op case %q has no schema", op)
		}
	}
}
