package styleobserver

import (
	"math"
	"strconv"

	tm "github.com/fuaran-ui/fuaran-go/thememanifest"
)

// Manifest-aware flag derivation — the render-time enforcement of a declared
// aesthetic-semantic budget, composing the resolved fills (the manifest-free
// observation) + a ThemeManifest. Deterministic; no vision model in the verify
// path. Custom-subtree policy: EXEMPT — every per-node check fires only for TONED
// nodes; untoned content (Custom / domain SVG) is exempt by construction.

func rgbString(c Rgba) string {
	return "rgb(" + itoa(int(math.RoundToEven(c.R))) + ", " + itoa(int(math.RoundToEven(c.G))) + ", " + itoa(int(math.RoundToEven(c.B))) + ")"
}

func itoa(i int) string { return strconv.Itoa(i) }

type paletteEntry struct {
	colour Rgba
	name   string
}

func paletteRgba(manifest tm.ThemeManifest) []paletteEntry {
	var out []paletteEntry
	for _, t := range manifest.Tokens {
		if t.Type != "color" {
			continue
		}
		if c, ok := TryParseHex(t.Value); ok {
			out = append(out, paletteEntry{colour: c, name: t.Name})
		}
	}
	return out
}

func resolveSlot(manifest tm.ThemeManifest, slot string) *tm.ManifestToken {
	if tone, ok := tm.ToneOfString(slot); ok {
		return tm.ResolveRole(tone, manifest)
	}
	return tm.ResolveNamedRole(slot, manifest)
}

// PerNodeFlags derives per-node manifest-aware flags for one observation. Empty
// for untoned nodes.
func PerNodeFlags(manifest tm.ThemeManifest, obs StyleObservation) []StyleFlag {
	if obs.EmittedTone == nil {
		return nil
	}
	slot := *obs.EmittedTone
	resolved := resolveSlot(manifest, slot)
	var out []StyleFlag

	if resolved == nil {
		out = append(out, TokenResolutionFailed{Slot: slot})
	} else {
		onPalette := false
		for _, p := range paletteRgba(manifest) {
			if SameRGB(p.colour, obs.EffectiveBackground) {
				onPalette = true
				break
			}
		}
		if !onPalette {
			out = append(out, OffPaletteColour{Value: rgbString(obs.EffectiveBackground)})
		}
	}

	for _, inv := range manifest.Invariants {
		if cf, ok := inv.Kind.(tm.ContrastFloor); ok && cf.Role == slot && obs.ContrastRatio < cf.MinRatio {
			out = append(out, ContrastBelowDeclaredFloor{Role: cf.Role, Ratio: obs.ContrastRatio, Floor: cf.MinRatio})
		}
	}
	return out
}

// NodeArea pairs an observation with its rendered area (px²) for the tree-level
// usage-budget check.
type NodeArea struct {
	Obs  StyleObservation
	Area float64
}

// VerifyUsageBudgets is the tree-level area-weighted usage-budget verification
// (the 60-30-10 enforcement). Empty when no area is available.
func VerifyUsageBudgets(manifest tm.ThemeManifest, nodes []NodeArea) []StyleFlag {
	totalArea := 0.0
	for _, n := range nodes {
		totalArea += n.Area
	}
	if totalArea <= 0.0 {
		return nil
	}
	palette := paletteRgba(manifest)
	areaByToken := map[string]float64{}
	for _, n := range nodes {
		for _, p := range palette {
			if SameRGB(p.colour, n.Obs.EffectiveBackground) {
				areaByToken[p.name] += n.Area
				break
			}
		}
	}
	var out []StyleFlag
	for _, inv := range manifest.Invariants {
		if ub, ok := inv.Kind.(tm.UsageBudget); ok {
			tokenArea := areaByToken[ub.Token]
			observedPct := 100.0 * tokenArea / totalArea
			if math.Abs(observedPct-ub.TargetPct) > ub.TolerancePct {
				out = append(out, UsageBudgetExceeded{Token: ub.Token, DeclaredPct: ub.TargetPct, ObservedPct: observedPct})
			}
		}
	}
	return out
}
