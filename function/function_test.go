// Cross-host function-registry conformance (Phase 558).
//
// Loads the shared wire-format-fixtures/function-registry/goldens.json — the
// canonical registry + findBySignature (EXACT/SUBSUMES) queries + compose-path
// queries with expected results, derived from the SHIPPED Python reference (the
// twin of the F# Fuaran.Core.FunctionRegistry engine). This Go host must resolve
// every golden identically. The registry-shape pin is the 548-style attestation
// guard: a shape drift fails here with the entry named. Skips cleanly on a
// standalone checkout where the corpus is absent.
package function

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// findGoldens walks up from the working directory looking for the shared corpus
// (a sibling of the fuaran-go repo), returning the goldens path or "".
func findGoldens() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		g := filepath.Join(dir, "wire-format-fixtures", "function-registry", "goldens.json")
		if _, err := os.Stat(g); err == nil {
			return g
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

type goldens struct {
	Registry        []FunctionEntry `json:"registry"`
	RegistryShape   []string        `json:"registryShape"`
	FindBySignature []struct {
		Name  string `json:"name"`
		Mode  string `json:"mode"`
		Query struct {
			ResultType *string    `json:"resultType"`
			Available  []SigEntry `json:"available"`
		} `json:"query"`
		ExpectedIds []string `json:"expectedIds"`
	} `json:"findBySignature"`
	Compose []struct {
		Name     string        `json:"name"`
		Output   string        `json:"output"`
		Inputs   []SigEntry    `json:"inputs"`
		Expected ComposeResult `json:"expected"`
	} `json:"compose"`
}

func loadGoldens(t *testing.T) goldens {
	t.Helper()
	path := findGoldens()
	if path == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading goldens: %v", err)
	}
	var g goldens
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("parsing goldens: %v", err)
	}
	if len(g.Registry) == 0 {
		t.Fatal("goldens declare no registry")
	}
	return g
}

func buildRegistry(t *testing.T, g goldens) *Registry {
	t.Helper()
	reg := NewRegistry()
	for _, e := range g.Registry {
		if err := reg.Register(e); err != nil {
			t.Fatalf("register %q: %v", e.ID, err)
		}
	}
	return reg
}

// TestRegistryShapeMatchesGoldens is the Phase 558 registry-shape attestation
// pin: the go host's re-derived per-entry shape descriptors must equal the
// shared goldens. A cross-host shape drift fails here with the divergent entry
// visible in the diff.
func TestRegistryShapeMatchesGoldens(t *testing.T) {
	g := loadGoldens(t)
	reg := buildRegistry(t, g)
	got := reg.RegistrySignatureShape()
	if !reflect.DeepEqual(got, g.RegistryShape) {
		t.Errorf("registry shape drift:\n got  = %v\n want = %v", got, g.RegistryShape)
	}
}

func TestFindBySignatureMatchesGoldens(t *testing.T) {
	g := loadGoldens(t)
	reg := buildRegistry(t, g)
	for _, f := range g.FindBySignature {
		t.Run(f.Name, func(t *testing.T) {
			res := reg.FindBySignature(MatchMode(f.Mode), SignatureQuery{
				ResultType: f.Query.ResultType,
				Available:  f.Query.Available,
			})
			ids := make([]string, 0, len(res))
			for _, e := range res {
				ids = append(ids, e.ID)
			}
			if !reflect.DeepEqual(ids, f.ExpectedIds) {
				t.Errorf("%s [%s]: got %v, want %v", f.Name, f.Mode, ids, f.ExpectedIds)
			}
		})
	}
}

func TestComposeMatchesGoldens(t *testing.T) {
	g := loadGoldens(t)
	reg := buildRegistry(t, g)
	for _, c := range g.Compose {
		t.Run(c.Name, func(t *testing.T) {
			got := reg.Compose(c.Output, c.Inputs, Subsumes, 4)
			if !reflect.DeepEqual(got, c.Expected) {
				t.Errorf("%s: got %+v, want %+v", c.Name, got, c.Expected)
			}
		})
	}
}
