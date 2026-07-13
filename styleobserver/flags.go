package styleobserver

import (
	"math"
	"strings"
)

// The StyleFlag vocabulary + the pure flag-derivation core. Two tiers: the first
// three flags are MANIFEST-FREE (derived from resolved colours + WCAG contrast);
// the last four are MANIFEST-AWARE (derived against a declared theme manifest, in
// manifest_flags.go). Both the in-memory observer and any future browser observer
// feed captured colours through DeriveStyleFlags, so identical inputs produce
// identical flags. The encode forms are byte-identical to the F#/TS/Python hosts.

// StyleFlag is one AI-facing legibility interpretation (closed).
type StyleFlag interface{ isStyleFlag() }

// ContrastBelowAA: composited fg/bg WCAG contrast below the AA floor but still
// faintly visible.
type ContrastBelowAA struct{ Ratio float64 }

// InvisibleText: contrast at/near 1.0 — text ≈ surface behind it (the severe subset).
type InvisibleText struct{ Ratio float64 }

// AccentIndistinct: a toned element's accent surface contrasts its container below
// the UI-component floor.
type AccentIndistinct struct{ Ratio float64 }

// TokenResolutionFailed: a tone/role the declared manifest has no token for.
type TokenResolutionFailed struct{ Slot string }

// OffPaletteColour: a toned element's resolved fill is not in the manifest palette.
type OffPaletteColour struct{ Value string }

// UsageBudgetExceeded: a token's surface-area share breached its declared budget.
type UsageBudgetExceeded struct {
	Token       string
	DeclaredPct float64
	ObservedPct float64
}

// ContrastBelowDeclaredFloor: a role's resolved contrast is below the manifest's
// declared per-role floor.
type ContrastBelowDeclaredFloor struct {
	Role  string
	Ratio float64
	Floor float64
}

func (ContrastBelowAA) isStyleFlag()            {}
func (InvisibleText) isStyleFlag()              {}
func (AccentIndistinct) isStyleFlag()           {}
func (TokenResolutionFailed) isStyleFlag()      {}
func (OffPaletteColour) isStyleFlag()           {}
func (UsageBudgetExceeded) isStyleFlag()        {}
func (ContrastBelowDeclaredFloor) isStyleFlag() {}

// FlagKind is the stable PascalCase discriminator wire string.
func FlagKind(f StyleFlag) string {
	switch f.(type) {
	case ContrastBelowAA:
		return "ContrastBelowAA"
	case InvisibleText:
		return "InvisibleText"
	case AccentIndistinct:
		return "AccentIndistinct"
	case TokenResolutionFailed:
		return "TokenResolutionFailed"
	case OffPaletteColour:
		return "OffPaletteColour"
	case UsageBudgetExceeded:
		return "UsageBudgetExceeded"
	case ContrastBelowDeclaredFloor:
		return "ContrastBelowDeclaredFloor"
	}
	return ""
}

func esc(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// EncodeStyleFlag encodes a flag as the AI-friendly tagged-object JSON.
func EncodeStyleFlag(f StyleFlag) string {
	switch x := f.(type) {
	case ContrastBelowAA:
		return `{"kind":"ContrastBelowAA","ratio":` + f2(x.Ratio) + `}`
	case InvisibleText:
		return `{"kind":"InvisibleText","ratio":` + f2(x.Ratio) + `}`
	case AccentIndistinct:
		return `{"kind":"AccentIndistinct","ratio":` + f2(x.Ratio) + `}`
	case TokenResolutionFailed:
		return `{"kind":"TokenResolutionFailed","slot":"` + esc(x.Slot) + `"}`
	case OffPaletteColour:
		return `{"kind":"OffPaletteColour","value":"` + esc(x.Value) + `"}`
	case UsageBudgetExceeded:
		return `{"kind":"UsageBudgetExceeded","token":"` + esc(x.Token) +
			`","declaredPct":` + f2(x.DeclaredPct) + `,"observedPct":` + f2(x.ObservedPct) + `}`
	case ContrastBelowDeclaredFloor:
		return `{"kind":"ContrastBelowDeclaredFloor","role":"` + esc(x.Role) +
			`","ratio":` + f2(x.Ratio) + `,"floor":` + f2(x.Floor) + `}`
	}
	return ""
}

// StyleObservation is one resolved-style snapshot for a single addressable node.
type StyleObservation struct {
	NodeID              string
	Foreground          Rgba
	EffectiveBackground Rgba
	FontRole            FontRole
	EmittedTone         *string
	ContrastRatio       float64
	Flags               []StyleFlag
}

// EncodeStyleObservation encodes an observation as JSON, byte-identical to the
// sibling hosts.
func EncodeStyleObservation(obs StyleObservation) string {
	tone := "null"
	if obs.EmittedTone != nil {
		tone = `"` + esc(*obs.EmittedTone) + `"`
	}
	flags := ""
	for i, f := range obs.Flags {
		if i > 0 {
			flags += ","
		}
		flags += EncodeStyleFlag(f)
	}
	return `{"nodeId":"` + esc(obs.NodeID) + `","foreground":` + EncodeRgba(obs.Foreground) +
		`,"effectiveBackground":` + EncodeRgba(obs.EffectiveBackground) +
		`,"fontRole":"` + string(obs.FontRole) + `","emittedTone":` + tone +
		`,"contrastRatio":` + f2(obs.ContrastRatio) + `,"flags":[` + flags + `]}`
}

// StyleObserverOptions is host-tunable policy; v1 defaults pin the standard WCAG floors.
type StyleObserverOptions struct {
	DebounceMs                int
	ContrastAAThreshold       float64
	InvisibleTextThreshold    float64
	AccentIndistinctThreshold float64
	EmitOnFlagChangeOnly      bool
}

// DefaultOptions is the v1 default policy.
var DefaultOptions = StyleObserverOptions{
	DebounceMs:                100,
	ContrastAAThreshold:       4.5,
	InvisibleTextThreshold:    1.1,
	AccentIndistinctThreshold: 3.0,
	EmitOnFlagChangeOnly:      true,
}

// StyleInput is the abstract evidence envelope the derivation operates on.
type StyleInput struct {
	Foreground       Rgba
	BackgroundLayers []Rgba
	FontFamily       *string
	EmittedTone      *string
}

// BaselineStyleInput is opaque-black text on the implicit white canvas.
func BaselineStyleInput() StyleInput { return StyleInput{Foreground: Black} }

// ── Compositing + WCAG contrast ────────────────────────────────────────────────

// Composite is the source-over composite of top (with its alpha) over bottom.
func Composite(top, bottom Rgba) Rgba {
	a := top.A + bottom.A*(1.0-top.A)
	if a <= 0.0 {
		return Transparent
	}
	blend := func(tc, bc float64) float64 {
		return (tc*top.A + bc*bottom.A*(1.0-top.A)) / a
	}
	return Rgba{R: blend(top.R, bottom.R), G: blend(top.G, bottom.G), B: blend(top.B, bottom.B), A: a}
}

// EffectiveBackground composites a background layer stack (element-first) down to
// the first opaque layer.
func EffectiveBackground(layers []Rgba) Rgba {
	var truncated []Rgba
	foundOpaque := false
	for _, layer := range layers {
		truncated = append(truncated, layer)
		if IsOpaque(layer) {
			foundOpaque = true
			break
		}
	}
	stack := truncated
	if !foundOpaque {
		stack = append(append([]Rgba{}, truncated...), White)
	}
	if len(stack) == 0 {
		return White
	}
	// base-first = reversed(stack)
	acc := stack[len(stack)-1]
	for i := len(stack) - 2; i >= 0; i-- {
		acc = Composite(stack[i], acc)
	}
	return acc
}

// RelativeLuminance is the WCAG relative luminance of an (assumed opaque) colour.
func RelativeLuminance(c Rgba) float64 {
	channel := func(v float64) float64 {
		s := v / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// ContrastRatio is the WCAG contrast ratio between two opaque colours (1.0…21.0).
func ContrastRatio(a, b Rgba) float64 {
	la := RelativeLuminance(a)
	lb := RelativeLuminance(b)
	lighter := math.Max(la, lb)
	darker := math.Min(la, lb)
	return (lighter + 0.05) / (darker + 0.05)
}

// ── Derived evidence ───────────────────────────────────────────────────────────

// ResolvedBackground is the opaque background the text sits on after compositing.
func ResolvedBackground(inp StyleInput) Rgba { return EffectiveBackground(inp.BackgroundLayers) }

// ResolvedForeground is the colour the text actually paints with.
func ResolvedForeground(inp StyleInput) Rgba {
	return Composite(inp.Foreground, ResolvedBackground(inp))
}

// Contrast is the WCAG contrast ratio between resolved foreground and effective
// background.
func Contrast(inp StyleInput) float64 {
	return ContrastRatio(ResolvedForeground(inp), ResolvedBackground(inp))
}

// FontRoleOf classifies the computed font-family string.
func FontRoleOf(inp StyleInput) FontRole {
	if inp.FontFamily == nil {
		return FontUnknown
	}
	f := strings.ToLower(*inp.FontFamily)
	switch {
	case strings.Contains(f, "mono"):
		return FontMonospace
	case strings.Contains(f, "sans"):
		return FontSansSerif
	case strings.Contains(f, "serif"):
		return FontSerif
	}
	return FontUnknown
}

// ── Per-flag predicates ────────────────────────────────────────────────────────

// InvisibleTextFlag: contrast at/below the invisible threshold.
func InvisibleTextFlag(invisibleThreshold float64, inp StyleInput) StyleFlag {
	c := Contrast(inp)
	if c < invisibleThreshold {
		return InvisibleText{Ratio: c}
	}
	return nil
}

// ContrastBelowAAFlag: contrast in [invisibleThreshold, aaThreshold).
func ContrastBelowAAFlag(invisibleThreshold, aaThreshold float64, inp StyleInput) StyleFlag {
	c := Contrast(inp)
	if invisibleThreshold <= c && c < aaThreshold {
		return ContrastBelowAA{Ratio: c}
	}
	return nil
}

// AccentIndistinctFlag: a toned element's tint barely contrasts the surface behind it.
func AccentIndistinctFlag(accentThreshold float64, inp StyleInput) StyleFlag {
	if inp.EmittedTone == nil || len(inp.BackgroundLayers) == 0 {
		return nil
	}
	own := inp.BackgroundLayers[0]
	if own.A <= 0.0 {
		return nil
	}
	accentSurface := ResolvedBackground(inp)
	ancestorSurface := EffectiveBackground(inp.BackgroundLayers[1:])
	c := ContrastRatio(accentSurface, ancestorSurface)
	if c < accentThreshold {
		return AccentIndistinct{Ratio: c}
	}
	return nil
}

// DeriveStyleFlags derives the manifest-free flag list for one input (deterministic order).
func DeriveStyleFlags(opts StyleObserverOptions, inp StyleInput) []StyleFlag {
	var out []StyleFlag
	if inv := InvisibleTextFlag(opts.InvisibleTextThreshold, inp); inv != nil {
		out = append(out, inv)
	}
	if aa := ContrastBelowAAFlag(opts.InvisibleTextThreshold, opts.ContrastAAThreshold, inp); aa != nil {
		out = append(out, aa)
	}
	if accent := AccentIndistinctFlag(opts.AccentIndistinctThreshold, inp); accent != nil {
		out = append(out, accent)
	}
	return out
}

// ToStyleObservation builds a fully-populated manifest-free observation.
func ToStyleObservation(opts StyleObserverOptions, nodeID string, inp StyleInput) StyleObservation {
	return StyleObservation{
		NodeID:              nodeID,
		Foreground:          ResolvedForeground(inp),
		EffectiveBackground: ResolvedBackground(inp),
		FontRole:            FontRoleOf(inp),
		EmittedTone:         inp.EmittedTone,
		ContrastRatio:       Contrast(inp),
		Flags:               DeriveStyleFlags(opts, inp),
	}
}

// FlagsEqual is order-sensitive flag-list equality (the derive order).
func FlagsEqual(a, b []StyleFlag) bool {
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
