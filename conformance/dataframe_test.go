// Dataframe parity: certifies the Go Binding.Transform evaluator value-for-value
// against the reference evaluator's own answers — the corpus law family
// laws/dataframeParity (Phase 1938), emitted by the reference with a recorded
// producer commit in laws/manifest.json. (Until Phase 1938 this leg read the
// same cases from the Python host's tests, where they had hand-recorded answers
// and no producer.) Three legs per vector: the source table codec round-trips
// byte-exact, the pipeline codec round-trips byte-exact, and evaluating the
// pipeline over the source produces a result whose canonical encoding is
// byte-identical to the reference `expected.table` — or, for an `error`
// verdict, surfaces a typed evaluation error.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dataframe"
)

const dataframeParityFamily = "dataframeParity"

type lawFamilyRow struct {
	ID   string `json:"id"`
	File string `json:"file"`
}

type parityVector struct {
	ID    string `json:"id"`
	Input struct {
		Source   string `json:"source"`
		Pipeline string `json:"pipeline"`
	} `json:"input"`
	Expected struct {
		Verdict string `json:"verdict"`
		Table   string `json:"table"`
	} `json:"expected"`
}

// loadDataframeParity reads the family's vectors through laws/manifest.json.
func loadDataframeParity(t *testing.T) []parityVector {
	t.Helper()
	corpus, _ := loadCorpus(t)
	laws := filepath.Join(corpus, "laws")
	raw, err := os.ReadFile(filepath.Join(laws, "manifest.json"))
	if err != nil {
		t.Fatalf("reading laws/manifest.json: %v", err)
	}
	var manifest struct {
		Families []lawFamilyRow `json:"families"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing laws/manifest.json: %v", err)
	}
	var file string
	for _, f := range manifest.Families {
		if f.ID == dataframeParityFamily {
			file = f.File
		}
	}
	if file == "" {
		t.Fatalf("laws/manifest.json declares no %s family", dataframeParityFamily)
	}
	raw, err = os.ReadFile(filepath.Join(laws, file))
	if err != nil {
		t.Fatalf("reading laws/%s: %v", file, err)
	}
	var doc struct {
		Vectors []parityVector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing laws/%s: %v", file, err)
	}
	if len(doc.Vectors) == 0 {
		t.Fatalf("laws/%s carries no vectors", file)
	}
	return doc.Vectors
}

func TestDataframeParity(t *testing.T) {
	for _, v := range loadDataframeParity(t) {
		t.Run(v.ID, func(t *testing.T) {
			// Leg 1 — source codec round-trips byte-exact.
			src, cerr := dataframe.DecodeSource(v.Input.Source)
			if cerr != nil {
				t.Fatalf("decode source: %v", cerr)
			}
			reSrc, err := dataframe.EncodeSource(src)
			if err != nil {
				t.Fatalf("encode source: %v", err)
			}
			if reSrc != v.Input.Source {
				t.Fatalf("source round-trip mismatch\n got: %s\nwant: %s", reSrc, v.Input.Source)
			}

			// Leg 2 — pipeline codec round-trips byte-exact.
			pipeline, cerr := dataframe.DecodePipeline(v.Input.Pipeline)
			if cerr != nil {
				t.Fatalf("decode pipeline: %v", cerr)
			}
			rePipe, err := dataframe.EncodePipeline(pipeline)
			if err != nil {
				t.Fatalf("encode pipeline: %v", err)
			}
			if rePipe != v.Input.Pipeline {
				t.Fatalf("pipeline round-trip mismatch\n got: %s\nwant: %s", rePipe, v.Input.Pipeline)
			}

			// Leg 3 — evaluation is value-identical to the reference's answer.
			embedded, ok := src.(dataframe.Embedded)
			if !ok {
				t.Fatalf("parity source is not embedded")
			}
			result, evalErr := dataframe.EvalPipeline(pipeline, embedded.Table)
			if v.Expected.Verdict != "ok" {
				if evalErr == nil {
					t.Fatalf("the reference refused the pipeline; this evaluator returned a result")
				}
				return
			}
			if evalErr != nil {
				t.Fatalf("evaluate: %v", evalErr)
			}
			got, err := dataframe.EncodeSource(dataframe.Embedded{Table: result})
			if err != nil {
				t.Fatalf("encode result: %v", err)
			}
			if got != v.Expected.Table {
				t.Fatalf("evaluator mismatch\n got: %s\nwant: %s", got, v.Expected.Table)
			}
		})
	}
}
