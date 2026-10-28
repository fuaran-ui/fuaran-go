// field_laws_test.go — Phase 2207: this host runs the schema field's law
// vectors (the corpus law family laws/fieldLaws, fuaran-core Phase 427;
// WIRE_FORMAT §3.6.26).
//
// Every vector of every case runs through this host's own implementation: a
// schema entry's canonical bytes from its members, the embedded source's decode
// and canonical re-encoding (the unit canonicalised, an empty `ext` dropped, an
// unknown member read past, and the refusals), the schema fingerprint, the
// schema delta with the schema it replays to, and the compatibility verdict.
// The schemas the last three cases read are decoded through this host's own
// schema-entry decoder. A `reject` vector requires a refusal, and its class is
// compared: the file's only class, MalformedShape, is this host's
// MALFORMED_SHAPE.
//
// The family is read through laws/manifest.json and held to its row; the set of
// case kinds is held to the five this leg runs, so a new kind fails loudly.
package conformance

import (
	"encoding/json"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dataframe"
)

const fieldLawsFamily = "fieldLaws"

type fieldLawVector struct {
	ID    string `json:"id"`
	Case  string `json:"case"`
	Input struct {
		Name        string            `json:"name"`
		Type        string            `json:"type"`
		Unit        *string           `json:"unit"`
		Label       *string           `json:"label"`
		Description *string           `json:"description"`
		Ext         map[string]string `json:"ext"`
		Document    string            `json:"document"`
		Schema      json.RawMessage   `json:"schema"`
		Old         json.RawMessage   `json:"old"`
		Target      json.RawMessage   `json:"target"`
		DependsOn   []string          `json:"dependsOn"`
	} `json:"input"`
	Expected struct {
		Verdict     string          `json:"verdict"`
		Error       string          `json:"error"`
		Canonical   string          `json:"canonical"`
		Fingerprint string          `json:"fingerprint"`
		Patched     json.RawMessage `json:"patched"`
		Compat      string          `json:"compat"`
	} `json:"expected"`
}

var fieldLawCases = map[string]func(*testing.T, fieldLawVector){
	"fieldEncode": runFieldEncode,
	"codecDecode": runFieldCodecDecode,
	"fingerprint": runFieldFingerprint,
	"delta":       runFieldDelta,
	"classify":    runFieldClassify,
}

func TestFieldLaws(t *testing.T) {
	row, raw := loadLawFamily(t, fieldLawsFamily)
	var doc struct {
		Family     string           `json:"family"`
		KitVersion string           `json:"kitVersion"`
		Vectors    []fieldLawVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing laws/%s: %v", row.File, err)
	}
	if doc.Family != row.ID {
		t.Fatalf("laws/%s declares family %q; the manifest row is %q", row.File, doc.Family, row.ID)
	}
	if doc.KitVersion != row.KitVersion {
		t.Fatalf("laws/%s is stamped kitVersion %q; the manifest row says %q", row.File, doc.KitVersion, row.KitVersion)
	}
	if len(doc.Vectors) != row.Vectors {
		t.Fatalf("laws/%s carries %d vectors; the manifest row says %d", row.File, len(doc.Vectors), row.Vectors)
	}
	seen := map[string]int{}
	for _, v := range doc.Vectors {
		run, ok := fieldLawCases[v.Case]
		if !ok {
			t.Fatalf("vector %s is case %q, which this leg does not know", v.ID, v.Case)
		}
		seen[v.Case]++
		v := v
		t.Run(v.ID, func(t *testing.T) {
			switch v.Expected.Verdict {
			case "accept", "reject":
			default:
				t.Fatalf("unknown verdict %q", v.Expected.Verdict)
			}
			run(t, v)
		})
	}
	for c := range fieldLawCases {
		if seen[c] == 0 {
			t.Fatalf("laws/%s carries no %q vectors; the leg's case set is stale", row.File, c)
		}
	}
	t.Logf("fieldLaws (kitVersion %s): ran %d vectors across %d cases", doc.KitVersion, len(doc.Vectors), len(seen))
}

// expectFieldRefusal asserts the vector's verdict against this host's answer,
// comparing a refusal's class; it reports whether the vector was a reject.
func expectFieldRefusal(t *testing.T, v fieldLawVector, err *dataframe.ColumnError) bool {
	t.Helper()
	if v.Expected.Verdict == "reject" {
		if err == nil {
			t.Fatalf("the reference refuses (%s); this host accepted", v.Expected.Error)
		}
		want, mapped := codecErrorCodes[v.Expected.Error]
		if !mapped {
			t.Fatalf("refusal class %q has no counterpart in this host's codes", v.Expected.Error)
		}
		if err.Code != want {
			t.Fatalf("refused with %v, want class %s", err, want)
		}
		return true
	}
	if err != nil {
		t.Fatalf("the reference accepts; this host refused: %v", err)
	}
	return false
}

// decodeLawSchema reads a vector's schema array through this host's decoder.
func decodeLawSchema(t *testing.T, what string, raw json.RawMessage) dataframe.Schema {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("the vector carries no %s schema", what)
	}
	s, err := dataframe.DecodeSchema(string(raw))
	if err != nil {
		t.Fatalf("decoding the %s schema: %v", what, err)
	}
	return s
}

func encodeLawSchema(t *testing.T, s dataframe.Schema) string {
	t.Helper()
	got, err := dataframe.EncodeSchema(s)
	if err != nil {
		t.Fatalf("encoding a schema: %v", err)
	}
	return got
}

func runFieldEncode(t *testing.T, v fieldLawVector) {
	in := v.Input
	entry := dataframe.SchemaEntry{Name: in.Name, Type: in.Type, Label: in.Label, Description: in.Description, Ext: in.Ext}
	if in.Unit != nil {
		u, r := dataframe.ParseUnit(*in.Unit)
		if r != nil {
			t.Fatalf("unit %q: %v", *in.Unit, r)
		}
		entry.Unit = &u
	}
	got, err := dataframe.EncodeSchemaEntry(entry)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != v.Expected.Canonical {
		t.Fatalf("encode\n got: %s\nwant: %s", got, v.Expected.Canonical)
	}
}

func runFieldCodecDecode(t *testing.T, v fieldLawVector) {
	src, err := dataframe.DecodeSource(v.Input.Document)
	if expectFieldRefusal(t, v, err) {
		return
	}
	got, encErr := dataframe.EncodeSource(src)
	if encErr != nil {
		t.Fatalf("re-encode: %v", encErr)
	}
	if got != v.Expected.Canonical {
		t.Fatalf("decode-then-encode\n got: %s\nwant: %s", got, v.Expected.Canonical)
	}
}

func runFieldFingerprint(t *testing.T, v fieldLawVector) {
	if got := dataframe.SchemaFingerprint(decodeLawSchema(t, "input", v.Input.Schema)); got != v.Expected.Fingerprint {
		t.Fatalf("fingerprint = %s, want %s", got, v.Expected.Fingerprint)
	}
}

func runFieldDelta(t *testing.T, v fieldLawVector) {
	old := decodeLawSchema(t, "old", v.Input.Old)
	target := decodeLawSchema(t, "target", v.Input.Target)
	d := dataframe.DiffSchema(old, target)
	got, err := dataframe.EncodeSchemaDelta(d)
	if err != nil {
		t.Fatalf("encoding the delta: %v", err)
	}
	if got != v.Expected.Canonical {
		t.Fatalf("delta\n got: %s\nwant: %s", got, v.Expected.Canonical)
	}
	patched, perr := dataframe.PatchSchema(old, d)
	if perr != nil {
		t.Fatalf("patch(old, diff(old, target)) refused: %v", perr)
	}
	want := encodeLawSchema(t, decodeLawSchema(t, "patched", v.Expected.Patched))
	if g := encodeLawSchema(t, patched); g != want {
		t.Fatalf("patched\n got: %s\nwant: %s", g, want)
	}
	if want != encodeLawSchema(t, target) {
		t.Fatalf("the vector's patched schema is not its target")
	}
}

func runFieldClassify(t *testing.T, v fieldLawVector) {
	d := dataframe.DiffSchema(decodeLawSchema(t, "old", v.Input.Old), decodeLawSchema(t, "target", v.Input.Target))
	if got := dataframe.ClassifySchema(v.Input.DependsOn, d); got.Verdict != v.Expected.Compat {
		t.Fatalf("classify = %s %v, want %s", got.Verdict, got.Reasons, v.Expected.Compat)
	}
}
