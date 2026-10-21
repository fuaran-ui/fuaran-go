package conformance

// WIRE_FORMAT.md §5 "The two encode refusals" — the row-encode/ corpus family
// (Phase 1960). Each vector is a row feed in host-neutral cell descriptors and
// either its canonical bytes or its refusal. This host maps an int64
// descriptor onto a wire.Int cell and runs the feed through wire.EncodeRows,
// the check a DataGrid / Chart `source` takes on encode. The wire model holds
// no date value, so the dateTime vectors are unrepresentable here: they are
// named below with that reason, and the suite fails when it skips a vector it
// did not name, or names one the family does not have.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fuaran-ui/fuaran-go/internal/corpusroot"
	"github.com/fuaran-ui/fuaran-go/wire"
)

const noDateValue = "the wire model holds no date value, so a dateTime cell cannot be authored here"

var rowEncodeUnrepresentable = map[string]string{
	"row-encode-datetime-utc":                noDateValue,
	"row-encode-refuse-datetime-unspecified": noDateValue,
}

type rowEncodeVector struct {
	ID       string                         `json:"id"`
	Rows     []map[string]map[string]string `json:"rows"`
	Expected *string                        `json:"expected"`
	Refused  *struct {
		Code string `json:"code"`
		Row  int    `json:"row"`
		Key  string `json:"key"`
	} `json:"refused"`
}

func rowEncodeFeed(v rowEncodeVector) (wire.Arr, bool) {
	rows := wire.Arr{}
	for _, r := range v.Rows {
		fields := map[string]wire.Value{}
		for k, d := range r {
			text, ok := d["int64"]
			if !ok {
				return nil, false
			}
			n, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				panic(err)
			}
			fields[k] = wire.Int(n)
		}
		rows = append(rows, wire.Obj{Fields: fields})
	}
	return rows, true
}

func TestRowEncodeVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(corpusroot.ForTest(t), "row-encode", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Vectors []rowEncodeVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Vectors) < 6 {
		t.Fatalf("the family has %d vectors", len(m.Vectors))
	}
	seen := map[string]bool{}
	for _, v := range m.Vectors {
		seen[v.ID] = true
		feed, ok := rowEncodeFeed(v)
		_, named := rowEncodeUnrepresentable[v.ID]
		if ok == named {
			t.Errorf("%s: representable=%v but named unrepresentable=%v", v.ID, ok, named)
			continue
		}
		if !ok {
			continue
		}
		t.Run(v.ID, func(t *testing.T) {
			got, err := wire.EncodeRows(feed)
			switch {
			case v.Refused != nil:
				var refused *wire.RowCellRefusedError
				if !errors.As(err, &refused) {
					t.Fatalf("expected a refusal, got %q, %v", got, err)
				}
				if refused.Code != v.Refused.Code || refused.Row != v.Refused.Row || refused.Key != v.Refused.Key {
					t.Fatalf("refused %+v, want %+v", refused, *v.Refused)
				}
			case err != nil:
				t.Fatalf("expected bytes, refused: %v", err)
			case got != *v.Expected:
				t.Fatalf("got %s, want %s", got, *v.Expected)
			}
		})
	}
	for id := range rowEncodeUnrepresentable {
		if !seen[id] {
			t.Errorf("%s is named unrepresentable but the family has no such vector", id)
		}
	}
}

func TestARowFeedNodeRefusesThroughTheEncoder(t *testing.T) {
	node, err := wire.DecodeNode(`{"id":"g","kind":{"$type":"DataGrid","columns":[],"source":{"$type":"Static","value":[]}}}`)
	if err != nil {
		t.Fatal(err)
	}
	source := node.Kind.Fields["source"].(wire.Obj)
	source.Fields["value"] = wire.Arr{wire.Obj{Fields: map[string]wire.Value{"n": wire.Int(1<<53 + 1)}}}
	if _, err := wire.EncodeNode(node); err == nil || !strings.Contains(err.Error(), "row 0, cell 'n'") {
		t.Fatalf("expected the row refusal, got %v", err)
	}
	source.Fields["value"] = wire.Arr{wire.Obj{Fields: map[string]wire.Value{"n": wire.Int(1 << 53)}}}
	got, err := wire.EncodeNode(node)
	if err != nil || !strings.Contains(got, `"value":[{"n":9007199254740992}]`) {
		t.Fatalf("the bound encodes: %s, %v", got, err)
	}
}
