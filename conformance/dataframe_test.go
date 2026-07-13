// Dataframe parity: certifies the Go Binding.Transform evaluator value-for-value
// against the F# reference report (fuaran-py/tests/fixtures/dataframe_parity.json).
// Three legs per case: the source table codec round-trips byte-exact, the pipeline
// codec round-trips byte-exact, and evaluating the pipeline over the source
// produces a result whose canonical encoding is byte-identical to the reference
// `expected` (or, for an `ok:false` case, surfaces a typed evaluation error). The
// fixture is a sibling of the wire-format corpus; the leg skips on a standalone
// checkout.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/dataframe"
)

// findDataframeParity walks up from the working directory looking for the F#
// reference report under the fuaran-py sibling. Returns "" when absent.
func findDataframeParity() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, "fuaran-py", "tests", "fixtures", "dataframe_parity.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

type dataframeCase struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Source   string `json:"source"`
	Pipeline string `json:"pipeline"`
	Expected string `json:"expected"`
}

type dataframeReport struct {
	Cases []dataframeCase `json:"cases"`
}

func TestDataframeParity(t *testing.T) {
	path := findDataframeParity()
	if path == "" {
		t.Skip("dataframe_parity.json not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading parity report: %v", err)
	}
	var report dataframeReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("parsing parity report: %v", err)
	}
	if len(report.Cases) == 0 {
		t.Fatal("parity report has no cases")
	}

	for _, c := range report.Cases {
		t.Run(c.ID, func(t *testing.T) {
			// Leg 1 — source codec round-trips byte-exact.
			src, cerr := dataframe.DecodeSource(c.Source)
			if cerr != nil {
				t.Fatalf("decode source: %v", cerr)
			}
			reSrc, err := dataframe.EncodeSource(src)
			if err != nil {
				t.Fatalf("encode source: %v", err)
			}
			if reSrc != c.Source {
				t.Fatalf("source round-trip mismatch\n got: %s\nwant: %s", reSrc, c.Source)
			}

			// Leg 2 — pipeline codec round-trips byte-exact.
			pipeline, cerr := dataframe.DecodePipeline(c.Pipeline)
			if cerr != nil {
				t.Fatalf("decode pipeline: %v", cerr)
			}
			rePipe, err := dataframe.EncodePipeline(pipeline)
			if err != nil {
				t.Fatalf("encode pipeline: %v", err)
			}
			if rePipe != c.Pipeline {
				t.Fatalf("pipeline round-trip mismatch\n got: %s\nwant: %s", rePipe, c.Pipeline)
			}

			// Leg 3 — evaluation is value-identical to the reference report.
			embedded, ok := src.(dataframe.Embedded)
			if !ok {
				t.Fatalf("parity source is not embedded")
			}
			result, evalErr := dataframe.EvalPipeline(pipeline, embedded.Table)
			if !c.OK {
				if evalErr == nil {
					t.Fatalf("expected an evaluation error, got a result")
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
			if got != c.Expected {
				t.Fatalf("evaluator mismatch\n got: %s\nwant: %s", got, c.Expected)
			}
		})
	}
}
