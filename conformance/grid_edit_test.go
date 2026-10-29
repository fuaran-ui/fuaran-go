package conformance

// The corpus's grid-edit/ family (WIRE_FORMAT §3.2, "Edits as column ops —
// GridEdit"; enumerated by Phase 2500). This host decodes every vector's
// GridEdit with its own codec, applies the op, derives the row delta, inverts
// it and restores the table, and runs the three scripted histories through its
// own edit history. Every answer is compared after decoding, through this
// host's canonical encoders — the family pins behaviour, not file framing.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dataframe"
	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
)

func gridEditFamily(t *testing.T) map[string]any {
	t.Helper()
	dir := filepath.Join(corpusroot.ForTest(t), "grid-edit")
	manifest := gridEditJSON(t, filepath.Join(dir, "manifest.json"))
	if files, _ := manifest["files"].([]any); len(files) != 1 || files[0] != "grid-edit-vectors.json" {
		t.Fatalf("grid-edit/manifest.json names %v, not the vectors file", manifest["files"])
	}
	return gridEditJSON(t, filepath.Join(dir, "grid-edit-vectors.json"))
}

func gridEditJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return out
}

// compact is a parsed value written back with sorted members and the numbers as
// written — the canonical form of the corpus's ASCII payloads.
func compact(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func gridEditTable(t *testing.T, v any) dataframe.Table {
	t.Helper()
	src, err := dataframe.DecodeSource(compact(t, v))
	if err != nil {
		t.Fatalf("decoding a table: %v", err)
	}
	e, ok := src.(dataframe.Embedded)
	if !ok {
		t.Fatalf("not an embedded table: %v", v)
	}
	return e.Table
}

func gridEditTableText(t *testing.T, tbl dataframe.Table) string {
	t.Helper()
	s, err := dataframe.EncodeSource(dataframe.Embedded{Table: tbl})
	if err != nil {
		t.Fatalf("encoding a table: %v", err)
	}
	return s
}

func gridEditOpText(t *testing.T, op dataframe.ColumnOp) string {
	t.Helper()
	s, err := dataframe.EncodeColumnOp(op)
	if err != nil {
		t.Fatalf("encoding an op: %v", err)
	}
	return s
}

func gridEditDeltaText(t *testing.T, d dataframe.TableDelta) string {
	t.Helper()
	s, err := dataframe.EncodeTableDelta(d)
	if err != nil {
		t.Fatalf("encoding a delta: %v", err)
	}
	return s
}

// gridEditAnswer is what this host answers for one vector, in the vector's shape.
func gridEditAnswer(t *testing.T, table dataframe.Table, edit dataframe.GridEdit) map[string]any {
	t.Helper()
	after, refusal := dataframe.ApplyColumnOp(edit.Op, table)
	if refusal != nil {
		return map[string]any{"refused": refusal.Token}
	}
	out := map[string]any{"after": gridEditTableText(t, after)}
	if edit.RowKeyField == nil {
		out["delta"] = gridEditDeltaText(t, dataframe.TableDelta{FullRefresh: true})
		out["referencePath"] = "noRowIdentityDeclared"
	} else {
		out["delta"] = gridEditDeltaText(t, dataframe.GridEditDelta(*edit.RowKeyField, table, edit.Op))
	}
	if inverse, err := dataframe.InvertColumnOp(edit.Op, table); err == nil {
		undone, uerr := dataframe.ApplyColumnOp(inverse, after)
		if uerr != nil {
			t.Fatalf("the inverse refused: %v", uerr)
		}
		out["inverse"] = gridEditOpText(t, inverse)
		out["undone"] = gridEditTableText(t, undone)
	} else {
		out["notInvertible"] = true
	}
	return out
}

// gridEditExpected normalises a vector's expected answer through this host's codecs.
func gridEditExpected(t *testing.T, e map[string]any) map[string]any {
	t.Helper()
	out := map[string]any{}
	for k, v := range e {
		switch k {
		case "after", "undone":
			out[k] = gridEditTableText(t, gridEditTable(t, v))
		case "inverse":
			op, err := dataframe.DecodeColumnOpParsed(v)
			if err != nil {
				t.Fatalf("decoding an expected inverse: %v", err)
			}
			out[k] = gridEditOpText(t, op)
		case "delta":
			out[k] = compact(t, v)
		default:
			out[k] = v
		}
	}
	return out
}

func gridEditOf(t *testing.T, v any) dataframe.GridEdit {
	t.Helper()
	edit, err := dataframe.DecodeGridEditParsed(v)
	if err != nil {
		t.Fatalf("decoding a GridEdit: %v", err)
	}
	return edit
}

func TestGridEditVectors(t *testing.T) {
	family := gridEditFamily(t)
	vectors, _ := family["vectors"].([]any)
	if len(vectors) < 15 {
		t.Fatalf("a family certified by reading nothing is no family: %d vectors", len(vectors))
	}
	for _, raw := range vectors {
		v := raw.(map[string]any)
		id := v["id"].(string)
		input := v["input"].(map[string]any)
		edit := gridEditOf(t, input["edit"])
		got, err := dataframe.EncodeGridEdit(edit)
		if err != nil {
			t.Fatalf("%s: encoding the edit: %v", id, err)
		}
		if want := compact(t, input["edit"]); got != want {
			t.Errorf("%s: the edit re-encodes to\n %s\nnot\n %s", id, got, want)
		}
		answer := gridEditAnswer(t, gridEditTable(t, input["table"]), edit)
		want := gridEditExpected(t, v["expected"].(map[string]any))
		if !reflect.DeepEqual(answer, want) {
			t.Errorf("%s:\n got  %v\n want %v", id, answer, want)
		}
	}
}

func TestGridEditHistories(t *testing.T) {
	family := gridEditFamily(t)
	histories, _ := family["histories"].([]any)
	if len(histories) < 3 {
		t.Fatalf("the undo vectors are not read: %d histories", len(histories))
	}
	for _, raw := range histories {
		h := raw.(map[string]any)
		id := h["id"].(string)
		input := h["input"].(map[string]any)
		history := dataframe.NewGridEditHistory(gridEditTable(t, input["table"]), 100)
		var steps []any
		for _, g := range input["gestures"].([]any) {
			gesture := g.(map[string]any)
			var next dataframe.GridEditHistory
			var refusal *dataframe.EditRefusal
			switch {
			case gesture["edit"] != nil:
				next, refusal = history.Apply(gridEditOf(t, gesture["edit"]).Op)
			case gesture["undo"] != nil:
				next, refusal = history.Undo()
			default:
				next, refusal = history.Redo()
			}
			if refusal != nil {
				steps = append(steps, map[string]any{"refused": refusal.Token})
				continue
			}
			history = next
			steps = append(steps, map[string]any{
				"canRedo": history.CanRedo(), "canUndo": history.CanUndo(), "table": gridEditTableText(t, history.Table()),
			})
		}
		var want []any
		for _, s := range h["expected"].(map[string]any)["steps"].([]any) {
			step := s.(map[string]any)
			if _, refused := step["refused"]; !refused {
				step["table"] = gridEditTableText(t, gridEditTable(t, step["table"]))
			}
			want = append(want, step)
		}
		if !reflect.DeepEqual(steps, want) {
			t.Errorf("%s:\n got  %v\n want %v", id, steps, want)
		}
	}
}

func TestGridEditCodecRefuses(t *testing.T) {
	for _, text := range []string{
		`{"$type":"GridEdit","key":"k","op":{"$type":"removeColumn","name":"x"},"extra":1}`,
		`{"$type":"GridEdit","key":"","op":{"$type":"removeColumn","name":"x"}}`,
		`{"$type":"GridEdit","key":"k","op":{"$type":"removeColumn","name":"x"},"rowKeyField":""}`,
		`{"$type":"GridEdit","key":"k","op":{"$type":"removeColumn","name":"x"},"rowKeyField":3}`,
		`{"$type":"GridEdit","key":"k"}`,
		`{"$type":"GridEdit","key":"k","op":{"$type":"dropTable"}}`,
		`{"$type":"Edit","key":"k","op":{"$type":"removeColumn","name":"x"}}`,
		`[1]`,
		`{`,
	} {
		if _, err := dataframe.DecodeGridEdit(text); err == nil {
			t.Errorf("decoded %s", text)
		}
	}
	text := `{"$type":"GridEdit","key":"orders","op":{"$type":"removeColumn","name":"note"}}`
	edit, err := dataframe.DecodeGridEdit(text)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got, _ := dataframe.EncodeGridEdit(edit); got != text {
		t.Errorf("absence of rowKeyField: got %s", got)
	}
}
