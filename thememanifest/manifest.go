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

// InvariantKind is a declared invariant's payload: closed for the kinds this
// host enforces, with UnknownInvariant for a kind it does not know (Phase 2133).
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

// UnknownInvariant is an invariant whose kind this host does not know — a
// newer constraint, or one another host enforces (Phase 2133). It is KEPT, so it
// round-trips through Decode and Encode with its members intact, and Validate
// reports it as INVARIANT_KIND_UNKNOWN: declared, and enforced by nothing here.
// Kind is the wire discriminator as written; Members are the invariant's
// members other than `kind` and `weight` (the weight is on the Invariant), as
// parsed.
type UnknownInvariant struct {
	Kind    string
	Members map[string]any
}

func (ContrastFloor) isInvariantKind()    {}
func (UsageBudget) isInvariantKind()      {}
func (MotionVoice) isInvariantKind()      {}
func (UnknownInvariant) isInvariantKind() {}

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

// InvariantKindName is the stable discriminator string for an invariant — the
// wire kind, as written, for an UnknownInvariant.
func InvariantKindName(inv Invariant) string {
	switch k := inv.Kind.(type) {
	case ContrastFloor:
		return "ContrastFloor"
	case UsageBudget:
		return "UsageBudget"
	case MotionVoice:
		return "MotionVoice"
	case UnknownInvariant:
		return k.Kind
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

// ─── Validation — what the decoded values MEAN (Phase 2133) ──────────────────

// The shared validation defect codes — the table in the corpus's WIRE_FORMAT
// §13 (the style-observer family's manifest-validation tier), identical on
// every manifest host.
const (
	// A ContrastFloor's minRatio lies outside [1, 21] (an omitted one is 0).
	ContrastFloorOutOfRange = "CONTRAST_FLOOR_OUT_OF_RANGE"
	// A ContrastFloor's role is empty.
	ContrastFloorRoleEmpty = "CONTRAST_FLOOR_ROLE_EMPTY"
	// No role binding binds the floor's non-empty role.
	ContrastFloorRoleUnbound = "CONTRAST_FLOOR_ROLE_UNBOUND"
	// A UsageBudget's token names no declared token.
	UsageBudgetTokenUndeclared = "USAGE_BUDGET_TOKEN_UNDECLARED"
	// The invariant's kind is outside this host's vocabulary — kept, and
	// enforced by nothing.
	InvariantKindUnknown = "INVARIANT_KIND_UNKNOWN"
)

// ManifestDefect is one validation finding: which rule, which invariant (its
// position in the decoded Invariants), and the role, token or kind it names.
type ManifestDefect struct {
	Code    string
	Index   int
	Subject string
}

// The range WCAG's contrast formula produces: a floor below it passes every
// pair, one above it no pair can pass.
const (
	minContrastRatio = 1.0
	maxContrastRatio = 21.0
)

// binds reports whether any role binding binds role — a tone binding binds its
// tone's wire name, a named binding its name.
func binds(m ThemeManifest, role string) bool {
	for _, b := range m.Roles {
		switch r := b.Role.(type) {
		case ToneRole:
			if r.Tone == role {
				return true
			}
		case NamedRole:
			if r.Name == role {
				return true
			}
		}
	}
	return false
}

// Validate is the validation pass, run after the tolerant Decode (Phase 2133):
// every declared invariant that cannot mean what it says, as one shared code.
// Invariant by invariant in list order, and within one in the table's order.
// It never refuses and never repairs; a host decides what a defect costs it.
func Validate(m ThemeManifest) []ManifestDefect {
	out := []ManifestDefect{}
	for i, inv := range m.Invariants {
		switch k := inv.Kind.(type) {
		case ContrastFloor:
			if !(k.MinRatio >= minContrastRatio && k.MinRatio <= maxContrastRatio) {
				out = append(out, ManifestDefect{ContrastFloorOutOfRange, i, k.Role})
			}
			if k.Role == "" {
				out = append(out, ManifestDefect{ContrastFloorRoleEmpty, i, ""})
			} else if !binds(m, k.Role) {
				out = append(out, ManifestDefect{ContrastFloorRoleUnbound, i, k.Role})
			}
		case UsageBudget:
			if TryGetToken(k.Token, m) == nil {
				out = append(out, ManifestDefect{UsageBudgetTokenUndeclared, i, k.Token})
			}
		case UnknownInvariant:
			out = append(out, ManifestDefect{InvariantKindUnknown, i, k.Kind})
		}
	}
	return out
}
