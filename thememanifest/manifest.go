// Package thememanifest is the Go host of the declared theme-token contract —
// the machine-readable theme the AI reasons against and the computed-style
// observer (package styleobserver) verifies resolved style against. It is
// DTCG-compatible (a vanilla DTCG file decodes cleanly) extended with the two
// things DTCG lacks: a per-token role→tone binding (so Tone.Brand is known to
// resolve to the manifest's brand token) and an invariant block (contrast
// floors, colour-usage budgets, motion voice, each soft-weighted). Tones are
// their wire strings ("Default"…"Info"). stdlib-only; a sibling of the F#/TS/
// Python ThemeManifest tiers, built to the same shapes.
package thememanifest

// The canonical ToneVariant palette, as wire strings.
var tones = map[string]bool{
	"Default": true, "Subdued": true, "Brand": true, "Success": true,
	"Warning": true, "Critical": true, "Info": true,
}

// ToneToString is the identity — a tone is already its PascalCase wire string.
func ToneToString(tone string) string { return tone }

// ToneOfString validates a wire string as a tone, returning ("", false) for an
// unrecognised token.
func ToneOfString(s string) (string, bool) {
	if tones[s] {
		return s, true
	}
	return "", false
}

// ManifestMeta is the `meta` block.
type ManifestMeta struct {
	Name        string
	Version     string
	Description *string
}

// AnonymousMeta is the empty metadata.
var AnonymousMeta = ManifestMeta{}

// ManifestToken is one token — DTCG-compatible (Type/Value/Description round-trip
// the DTCG $type/$value/$description) plus the dual-field Role tag. Name is the
// dotted path ("color.brand.base").
type ManifestToken struct {
	Name        string
	Type        string
	Value       string
	Description *string
	Role        *string
}

// ManifestRole is a role binding target — a tone variant or a broader named role.
type ManifestRole interface{ isManifestRole() }

// ToneRole binds a role to one of the canonical tone variants.
type ToneRole struct{ Tone string }

// NamedRole binds a role to a broader named semantic role (body text, divider…).
type NamedRole struct{ Name string }

func (ToneRole) isManifestRole()  {}
func (NamedRole) isManifestRole() {}

func roleKey(r ManifestRole) string {
	switch x := r.(type) {
	case ToneRole:
		return "tone:" + x.Tone
	case NamedRole:
		return "named:" + x.Name
	}
	return "?"
}

// RoleBinding binds a role onto a manifest token by name.
type RoleBinding struct {
	Role      ManifestRole
	TokenName string
}

// MotionBudget is the motion-voice budget — the payload of MotionVoice.
type MotionBudget struct {
	MaxDurationMs int
	Easing        *string
}

// InvariantKind is a declared invariant's payload (closed).
type InvariantKind interface{ isInvariantKind() }

// ContrastFloor: a named role's resolved contrast must be at least MinRatio.
type ContrastFloor struct {
	Role     string
	MinRatio float64
}

// UsageBudget: a token's share of visible surface must stay within TargetPct ± TolerancePct.
type UsageBudget struct {
	Token        string
	TargetPct    float64
	TolerancePct float64
}

// MotionVoice: the theme's motion must stay within the declared MotionBudget.
type MotionVoice struct{ Budget MotionBudget }

func (ContrastFloor) isInvariantKind() {}
func (UsageBudget) isInvariantKind()   {}
func (MotionVoice) isInvariantKind()   {}

// DefaultWeight is the soft weight an invariant carries unless overridden.
const DefaultWeight = 1.0

// Invariant is one declared invariant + its soft weight.
type Invariant struct {
	Kind   InvariantKind
	Weight float64
}

// NewInvariant constructs an invariant with the default weight.
func NewInvariant(kind InvariantKind) Invariant { return Invariant{Kind: kind, Weight: DefaultWeight} }

// WeightedInvariant constructs an invariant with an explicit weight.
func WeightedInvariant(weight float64, kind InvariantKind) Invariant {
	return Invariant{Kind: kind, Weight: weight}
}

// InvariantKindName is the stable discriminator string for an invariant.
func InvariantKindName(inv Invariant) string {
	switch inv.Kind.(type) {
	case ContrastFloor:
		return "ContrastFloor"
	case UsageBudget:
		return "UsageBudget"
	case MotionVoice:
		return "MotionVoice"
	}
	return ""
}

// ThemeManifest is the declared theme contract: metadata + tokens + role bindings
// + invariants.
type ThemeManifest struct {
	Meta       ManifestMeta
	Tokens     []ManifestToken
	Roles      []RoleBinding
	Invariants []Invariant
}

// EmptyManifest is the empty contract.
var EmptyManifest = ThemeManifest{Meta: AnonymousMeta}

// TryGetToken looks up a token by its dotted name.
func TryGetToken(name string, m ThemeManifest) *ManifestToken {
	for i := range m.Tokens {
		if m.Tokens[i].Name == name {
			return &m.Tokens[i]
		}
	}
	return nil
}

// ResolveRole resolves a tone to its declared manifest token, or nil.
func ResolveRole(tone string, m ThemeManifest) *ManifestToken {
	for _, b := range m.Roles {
		if tr, ok := b.Role.(ToneRole); ok && tr.Tone == tone {
			return TryGetToken(b.TokenName, m)
		}
	}
	return nil
}

// ResolveNamedRole resolves a named (non-tone) role to its declared manifest token.
func ResolveNamedRole(role string, m ThemeManifest) *ManifestToken {
	for _, b := range m.Roles {
		if nr, ok := b.Role.(NamedRole); ok && nr.Name == role {
			return TryGetToken(b.TokenName, m)
		}
	}
	return nil
}

// PaletteColours is every colour value declared in the palette — the off-palette
// check's membership set.
func PaletteColours(m ThemeManifest) map[string]bool {
	out := make(map[string]bool)
	for _, t := range m.Tokens {
		if t.Type == "color" {
			out[t.Value] = true
		}
	}
	return out
}
