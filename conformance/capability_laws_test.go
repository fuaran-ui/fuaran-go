// capability_laws_test.go — Phase 1482: this host runs the reference's own
// capability-law vectors.
//
// The shared corpus's `laws/` family carries the (input, expected) pairs the
// reference host's `capabilityLaws` conformance family DRAWS from a declared
// seed, each expectation computed by calling the pinned reference kit. Before
// this leg existed, this host's capability surface agreed with the reference
// only by having been written from the same description — a claim nothing could
// falsify. Running the reference's own sample here makes it falsifiable: a
// divergence names the vector id.
//
// Four cases, all four run:
//   - validateArgs        — accept, or a named refusal at a named address.
//   - invocationKey       — the replay key and the determinism tag (see the
//     named partial below).
//   - declarationRoundTrip— decode-then-encode returns the REFERENCE's bytes.
//   - registryEnumerate   — id-sorted enumeration, whatever the insertion order.
//
// ONE named partial, deliberately not silent. An `invocationKey` vector also
// carries `capturedValue`: the value a capture-replay seam must return
// byte-identically for that key. This host ships no capture/replay effect seam
// (its opstream package persists and replays TREE OPS, not effect captures), so
// there is nothing here to journal the value through — asserting it would mean
// comparing the number to itself. The count of unasserted members is reported
// in the test log rather than passing quietly, because a partial that nobody
// can see is indistinguishable from a complete one.
//
// The seed and vector count are read from the family manifest, never restated
// here: a count in prose drifts the first time the sample is regenerated, and a
// harness that pins its own copy of a number the corpus owns would then fail
// for the wrong reason.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/function"
)

type lawFamily struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	File       string `json:"file"`
	KitVersion string `json:"kitVersion"`
	Seed       int    `json:"seed"`
	Iterations int    `json:"iterations"`
	Vectors    int    `json:"vectors"`
}

type lawManifest struct {
	Version  int         `json:"version"`
	Families []lawFamily `json:"families"`
}

type capabilityVector struct {
	ID       string `json:"id"`
	Case     string `json:"case"`
	Expected struct {
		// validateArgs
		Verdict string `json:"verdict"`
		Error   string `json:"error"`
		Addr    string `json:"addr"`
		// invocationKey
		Key            string `json:"key"`
		DeterminismTag string `json:"determinismTag"`
		// declarationRoundTrip
		Declaration string `json:"declaration"`
		// registryEnumerate
		IDs []string `json:"ids"`
	} `json:"expected"`
	Input struct {
		Capability   string               `json:"capability"`
		Declaration  string               `json:"declaration"`
		Declarations []string             `json:"declarations"`
		Args         []function.InvokeArg `json:"args"`
	} `json:"input"`
}

type capabilityLawFile struct {
	Family     string             `json:"family"`
	KitVersion string             `json:"kitVersion"`
	Seed       int                `json:"seed"`
	Iterations int                `json:"iterations"`
	Vectors    []capabilityVector `json:"vectors"`
}

// loadCapabilityLaws locates the `laws/` family beside the wire corpus and
// returns its manifest record together with the vector file. Skips on a
// standalone checkout, matching every other corpus leg in this package.
func loadCapabilityLaws(t *testing.T) (lawFamily, capabilityLawFile) {
	t.Helper()
	corpus := findCorpus()
	if corpus == "" {
		t.Skip("wire-format-fixtures corpus not found alongside the repo; skipping (standalone checkout)")
	}
	manifestPath := filepath.Join(corpus, "laws", "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Skipf("law-vector family not present in this corpus checkout (%s); skipping", manifestPath)
	}
	var manifest lawManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("laws/manifest.json is not valid JSON: %v", err)
	}

	var family lawFamily
	for _, f := range manifest.Families {
		if f.ID == "capabilityLaws" {
			family = f
			break
		}
	}
	if family.ID == "" {
		t.Fatalf("laws/manifest.json enumerates no capabilityLaws family (families: %d)", len(manifest.Families))
	}
	if family.Kind != "law-vectors" {
		t.Fatalf("capabilityLaws family is kind %q, expected law-vectors", family.Kind)
	}

	vectorRaw, err := os.ReadFile(filepath.Join(corpus, "laws", family.File))
	if err != nil {
		t.Fatalf("capabilityLaws family names %q but it cannot be read: %v", family.File, err)
	}
	var file capabilityLawFile
	if err := json.Unmarshal(vectorRaw, &file); err != nil {
		t.Fatalf("%s is not valid JSON: %v", family.File, err)
	}

	// The manifest and the file each declare the sample. They are written by
	// one emitter, so a disagreement means the corpus was edited by hand —
	// which is worth failing on before any vector runs, since every expectation
	// below is only meaningful as a sample of the run the manifest describes.
	if file.Family != family.ID {
		t.Fatalf("%s declares family %q, manifest says %q", family.File, file.Family, family.ID)
	}
	if file.Seed != family.Seed || file.Iterations != family.Iterations {
		t.Fatalf("%s declares seed/iterations %d/%d, manifest says %d/%d",
			family.File, file.Seed, file.Iterations, family.Seed, family.Iterations)
	}
	if len(file.Vectors) != family.Vectors {
		t.Fatalf("%s carries %d vectors, manifest says %d", family.File, len(file.Vectors), family.Vectors)
	}
	return family, file
}

// mustDecodeDeclaration lifts a declaration string from a vector into this
// host's Capability through this host's own codec. A failure here is a real
// failure of the leg — the declaration was produced by the reference and this
// host claims to read it.
func mustDecodeDeclaration(t *testing.T, vectorID, declaration string) function.Capability {
	t.Helper()
	decoded, err := function.DecodeDeclaration(declaration)
	if err != nil {
		t.Fatalf("vector %s: this host could not decode the reference's capability declaration: %v", vectorID, err)
	}
	return decoded
}

func TestCapabilityLawVectors(t *testing.T) {
	family, file := loadCapabilityLaws(t)
	t.Logf("capabilityLaws: %d vectors, kit %s, seed %d over %d iterations",
		len(file.Vectors), family.KitVersion, file.Seed, file.Iterations)

	unassertedCapturedValues := 0

	for _, v := range file.Vectors {
		v := v
		t.Run(v.ID, func(t *testing.T) {
			switch v.Case {
			case "validateArgs":
				declared := mustDecodeDeclaration(t, v.ID, v.Input.Capability)
				refusal := function.ValidateArgs(declared, v.Input.Args)
				switch v.Expected.Verdict {
				case "accept":
					if refusal != nil {
						t.Fatalf("expected accept, this host refused: %s", refusal.Error())
					}
				case "reject":
					if refusal == nil {
						t.Fatalf("expected reject %s at %s, this host accepted", v.Expected.Error, v.Expected.Addr)
					}
					if refusal.Kind != v.Expected.Error {
						t.Fatalf("expected refusal %q, this host gave %q", v.Expected.Error, refusal.Kind)
					}
					if refusal.Addr != v.Expected.Addr {
						t.Fatalf("expected refusal at address %q, this host named %q", v.Expected.Addr, refusal.Addr)
					}
				default:
					t.Fatalf("unrecognised verdict %q — this harness does not know what to assert", v.Expected.Verdict)
				}

			case "invocationKey":
				declared := mustDecodeDeclaration(t, v.ID, v.Input.Capability)
				if got := function.InvocationKey(declared, v.Input.Args); got != v.Expected.Key {
					t.Fatalf("replay key: expected %q, this host computed %q", v.Expected.Key, got)
				}
				if got := function.DeterminismTag(declared); got != v.Expected.DeterminismTag {
					t.Fatalf("determinism tag: expected %q, this host computed %q", v.Expected.DeterminismTag, got)
				}
				// See the file header: no capture/replay effect seam in this
				// host, so `expected.capturedValue` has nothing to travel
				// through. Counted, not asserted.
				unassertedCapturedValues++

			case "declarationRoundTrip":
				declared := mustDecodeDeclaration(t, v.ID, v.Input.Declaration)
				got, err := function.EncodeDeclaration(declared)
				if err != nil {
					t.Fatalf("this host could not re-encode the declaration it decoded: %v", err)
				}
				if got != v.Expected.Declaration {
					t.Fatalf("round-trip is not byte-identical to the reference.\n reference: %s\n this host: %s",
						v.Expected.Declaration, got)
				}

			case "registryEnumerate":
				registry := function.NewCapabilityRegistry()
				for i, declaration := range v.Input.Declarations {
					declared := mustDecodeDeclaration(t, v.ID, declaration)
					if refusal := registry.Register(declared); refusal != nil {
						t.Fatalf("registering declaration %d refused: %s", i, refusal.Error())
					}
				}
				enumerated := registry.Enumerate()
				got := make([]string, len(enumerated))
				for i, c := range enumerated {
					got[i] = c.ID
				}
				if len(got) != len(v.Expected.IDs) {
					t.Fatalf("expected %d enumerated ids, this host gave %d (%v)", len(v.Expected.IDs), len(got), got)
				}
				for i := range got {
					if got[i] != v.Expected.IDs[i] {
						t.Fatalf("enumeration order: expected %v, this host gave %v", v.Expected.IDs, got)
					}
				}

			default:
				// Never a skip. A case this harness does not know is a case
				// this host is not certifying, and a green run must not be able
				// to mean that.
				t.Fatalf("vector case %q is not run by this host's harness — the corpus family has grown a case; "+
					"port it here rather than widening this switch to ignore it", v.Case)
			}
		})
	}

	if unassertedCapturedValues > 0 {
		t.Logf("NOT ASSERTED: expected.capturedValue on %d invocationKey vectors — this host ships no "+
			"capture/replay effect seam for a non-deterministic invocation to be journalled through. "+
			"The key and the determinism tag those captures would be keyed on ARE asserted above.",
			unassertedCapturedValues)
	}
}

// TestCapabilityLawFamilyIsFullyRun pins that every case the corpus family
// carries is one the harness above runs. The switch's default already fails a
// vector it does not know, but only if such a vector is REACHED — this states
// the coverage as a property of the family rather than as a consequence of
// having iterated it.
func TestCapabilityLawFamilyIsFullyRun(t *testing.T) {
	_, file := loadCapabilityLaws(t)
	run := map[string]bool{
		"validateArgs":         true,
		"invocationKey":        true,
		"declarationRoundTrip": true,
		"registryEnumerate":    true,
	}
	seen := map[string]int{}
	for _, v := range file.Vectors {
		seen[v.Case]++
		if !run[v.Case] {
			t.Errorf("vector %s carries case %q, which this host does not run", v.ID, v.Case)
		}
	}
	for c := range run {
		if seen[c] == 0 {
			t.Errorf("this harness claims to run case %q but the family carries no such vector", c)
		}
	}
	t.Logf("cases run: %v", seen)
}
