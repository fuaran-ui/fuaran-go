// This host's certification against the shared `style-observer/` corpus family
// (Phase 1752).
//
// The header of styleobserver_test.go beside this file used to say the pure-tier
// encode was "certified byte-for-byte against the sibling hosts in the
// styleobserver / thememanifest package tests (which mirror the fuaran-py
// reference vectors)". That was an honest description of an arrangement that
// could not hold: four hosts had each written the same literals into their own
// test files, and four suites that agree are not an oracle. A fifth host has
// nothing to certify against but the other four's tests, and a regression
// introduced in all four at once — by the port that created them — is invisible
// from every one of them.
//
// So the cases live in the corpus now, emitted by the reference host, and this
// reads them. The literals in styleobserver/observer_test.go and
// styleobserver/manifest_flags_test.go stay where they are: written by hand,
// they are the go-red partner of a family written by a generator.
//
// NOT CHECKED IS NOT PASSED. A tier outside this host's vocabulary is reported
// by name with the vector id, never skipped; and the vacuity guard refuses a run
// in which the family turned out to be empty, because "every vector matched" and
// "no vector was loaded" are the same green.
package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fuaran-ui/fuaran-go/styleobserver"
	tm "github.com/fuaran-ui/fuaran-go/thememanifest"
)

type soRgba struct {
	R float64 `json:"r"`
	G float64 `json:"g"`
	B float64 `json:"b"`
	A float64 `json:"a"`
}

type soOptions struct {
	ContrastAAThreshold       float64 `json:"contrastAaThreshold"`
	InvisibleTextThreshold    float64 `json:"invisibleTextThreshold"`
	AccentIndistinctThreshold float64 `json:"accentIndistinctThreshold"`
}

type soInput struct {
	Foreground       soRgba   `json:"foreground"`
	BackgroundLayers []soRgba `json:"backgroundLayers"`
	FontFamily       *string  `json:"fontFamily"`
	EmittedTone      *string  `json:"emittedTone"`
}

type soObservation struct {
	NodeID              string  `json:"nodeId"`
	Foreground          soRgba  `json:"foreground"`
	EffectiveBackground soRgba  `json:"effectiveBackground"`
	FontRole            string  `json:"fontRole"`
	EmittedTone         *string `json:"emittedTone"`
	ContrastRatio       float64 `json:"contrastRatio"`
}

type soNodeArea struct {
	Observation soObservation `json:"observation"`
	Area        float64       `json:"area"`
}

type soVector struct {
	ID                    string        `json:"id"`
	Description           string        `json:"description"`
	Tier                  string        `json:"tier"`
	Options               soOptions     `json:"options"`
	NodeID                string        `json:"nodeId"`
	Input                 soInput       `json:"input"`
	ExpectedFlags         []string      `json:"expectedFlags"`
	ExpectedObservation   string        `json:"expectedObservation"`
	Manifest              string        `json:"manifest"`
	Observation           soObservation `json:"observation"`
	ExpectedManifestFlags []string      `json:"expectedManifestFlags"`
	NodeAreas             []soNodeArea  `json:"nodeAreas"`
	ExpectedBudgetFlags   []string      `json:"expectedBudgetFlags"`
}

func (c soRgba) rgba() styleobserver.Rgba { return styleobserver.RGBA(c.R, c.G, c.B, c.A) }

func (o soOptions) options() styleobserver.StyleObserverOptions {
	return styleobserver.StyleObserverOptions{
		ContrastAAThreshold:       o.ContrastAAThreshold,
		InvisibleTextThreshold:    o.InvisibleTextThreshold,
		AccentIndistinctThreshold: o.AccentIndistinctThreshold,
	}
}

func (i soInput) input() styleobserver.StyleInput {
	layers := make([]styleobserver.Rgba, 0, len(i.BackgroundLayers))
	for _, l := range i.BackgroundLayers {
		layers = append(layers, l.rgba())
	}
	return styleobserver.StyleInput{
		Foreground:       i.Foreground.rgba(),
		BackgroundLayers: layers,
		FontFamily:       i.FontFamily,
		EmittedTone:      i.EmittedTone,
	}
}

// The manifest-aware tiers take an ALREADY-DERIVED observation, so its flag list
// is deliberately empty — those arms read only the tone, the effective
// background and the contrast ratio.
func (o soObservation) observation() styleobserver.StyleObservation {
	return styleobserver.StyleObservation{
		NodeID:              o.NodeID,
		Foreground:          o.Foreground.rgba(),
		EffectiveBackground: o.EffectiveBackground.rgba(),
		FontRole:            styleobserver.FontRole(o.FontRole),
		EmittedTone:         o.EmittedTone,
		ContrastRatio:       o.ContrastRatio,
	}
}

func encodedFlags(flags []styleobserver.StyleFlag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, styleobserver.EncodeStyleFlag(f))
	}
	return out
}

func styleObserverVectors(t *testing.T) (string, []soVector) {
	t.Helper()
	corpus, manifest := loadCorpus(t)

	var vectors []soVector
	for _, fx := range manifest.Fixtures {
		if fx.Kind != "style-observer" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(corpus, filepath.FromSlash(fx.InputFile)))
		if err != nil {
			t.Fatalf("%s: manifest.json lists %s, which could not be read: %v", fx.ID, fx.InputFile, err)
		}
		var v soVector
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", fx.ID, err)
		}
		vectors = append(vectors, v)
	}
	// The measurement must not be vacuous: a range over an empty slice reports
	// total success having compared nothing at all.
	if len(vectors) == 0 {
		t.Fatalf("manifest.json at %s lists no style-observer fixtures — this suite would have certified "+
			"nothing while reporting success", corpus)
	}
	return corpus, vectors
}

func TestStyleObserverCorpusFamily(t *testing.T) {
	_, vectors := styleObserverVectors(t)

	for _, v := range vectors {
		t.Run(v.ID, func(t *testing.T) {
			switch v.Tier {
			case "observation":
				opts := v.Options.options()
				in := v.Input.input()

				got := encodedFlags(styleobserver.DeriveStyleFlags(opts, in))
				if !eqStrings(got, v.ExpectedFlags) {
					t.Errorf("derived flags do not encode to the family's bytes:\n got %v\nwant %v", got, v.ExpectedFlags)
				}

				obs := styleobserver.EncodeStyleObservation(styleobserver.ToStyleObservation(opts, v.NodeID, in))
				if obs != v.ExpectedObservation {
					t.Errorf("observation bytes differ:\n got %s\nwant %s", obs, v.ExpectedObservation)
				}

			case "per-node-manifest":
				m, err := tm.Decode(v.Manifest)
				if err != nil {
					t.Fatalf("the vector's manifest does not decode in this host: %v", err)
				}
				got := encodedFlags(styleobserver.PerNodeFlags(m, v.Observation.observation()))
				if !eqStrings(got, v.ExpectedManifestFlags) {
					t.Errorf("manifest-aware per-node flags differ:\n got %v\nwant %v", got, v.ExpectedManifestFlags)
				}

			case "usage-budget":
				m, err := tm.Decode(v.Manifest)
				if err != nil {
					t.Fatalf("the vector's manifest does not decode in this host: %v", err)
				}
				nodes := make([]styleobserver.NodeArea, 0, len(v.NodeAreas))
				for _, na := range v.NodeAreas {
					nodes = append(nodes, styleobserver.NodeArea{Obs: na.Observation.observation(), Area: na.Area})
				}
				got := encodedFlags(styleobserver.VerifyUsageBudgets(m, nodes))
				if !eqStrings(got, v.ExpectedBudgetFlags) {
					t.Errorf("usage-budget flags differ:\n got %v\nwant %v", got, v.ExpectedBudgetFlags)
				}

			default:
				// Reported by name, never skipped: a tier this host does not
				// understand is a gap in the host, and silence about it is the
				// thing the family exists to remove.
				t.Fatalf("tier %q is outside this host's vocabulary", v.Tier)
			}
		})
	}
}

// TestStyleObserverCorpusGoesRed proves the comparison above can fail. A byte
// comparison falls silently into certifying nothing — an absent corpus, an empty
// enumeration, a skipped list — and every one of those looks like a pass.
func TestStyleObserverCorpusGoesRed(t *testing.T) {
	_, vectors := styleObserverVectors(t)

	var subject *soVector
	for i := range vectors {
		if vectors[i].Tier == "observation" {
			subject = &vectors[i]
			break
		}
	}
	if subject == nil {
		t.Fatal("no observation-tier vector to perturb — the probe measured nothing")
	}

	produced := styleobserver.EncodeStyleObservation(
		styleobserver.ToStyleObservation(subject.Options.options(), subject.NodeID, subject.Input.input()),
	)
	if produced != subject.ExpectedObservation {
		t.Fatalf("the unperturbed vector %s should still pass", subject.ID)
	}

	perturbed := subject.ExpectedObservation[:len(subject.ExpectedObservation)-2] + "X}"
	if perturbed == subject.ExpectedObservation {
		t.Fatal("the perturbation changed nothing, so this test proves nothing — the probe, not the subject, failed")
	}
	if produced == perturbed {
		t.Fatal("a flipped expectation byte compared equal to what this host produces")
	}
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
