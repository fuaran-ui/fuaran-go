package thememanifest

import (
	"encoding/json"
	"sort"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// sortedKeys yields an object's keys in a deterministic order (Go maps are
// unordered, so the DTCG walk sorts to keep token output stable across runs;
// tokens are looked up by name, so their list order is not a wire contract).
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// JSON → ThemeManifest, a sibling of the F#/Python decoders. Two top-level
// shapes are accepted: a Fuaran manifest wrapper ({meta, tokens, roles,
// invariants}) — selected by a top-level "tokens" key — or a vanilla DTCG token
// tree at top level (decodes to tokens only, empty roles/invariants). stdlib
// json parser.

func asObj(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asStr(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func asNum(v any) *float64 {
	// A JSON bool decodes to a Go bool (not float64), so it is excluded here —
	// matching the Python _as_num guard.
	//
	// json.Number is accepted alongside float64 because the parse now runs
	// through wire.ParseBounded, which uses UseNumber to preserve the
	// int-vs-float distinction the canonical wire requires. A manifest number is
	// a ratio or a weight and is wanted as a float either way, so this converts
	// rather than branching further out; float64 stays accepted so a caller
	// handing us a value parsed some other way still works.
	switch n := v.(type) {
	case float64:
		return &n
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil
		}
		return &f
	}
	return nil
}

func strOr(v any, def string) string {
	if s := asStr(v); s != nil {
		return *s
	}
	return def
}

func numOr(v any, def float64) float64 {
	if f := asNum(v); f != nil {
		return *f
	}
	return def
}

// ── DTCG token-tree walk ──────────────────────────────────────────────────────

func walkTokens(prefix string, node any) []ManifestToken {
	obj, ok := asObj(node)
	if !ok {
		return nil
	}
	if _, hasValue := obj["$value"]; hasValue {
		var role *string
		if ext, ok := asObj(obj["$extensions"]); ok {
			if fuaran, ok := asObj(ext["fuaran"]); ok {
				role = asStr(fuaran["role"])
			}
		}
		return []ManifestToken{{
			Name:        prefix,
			Type:        strOr(obj["$type"], ""),
			Value:       strOr(obj["$value"], ""),
			Description: asStr(obj["$description"]),
			Role:        role,
		}}
	}
	var out []ManifestToken
	for _, k := range sortedKeys(obj) {
		if len(k) > 0 && k[0] == '$' {
			continue
		}
		next := k
		if prefix != "" {
			next = prefix + "." + k
		}
		out = append(out, walkTokens(next, obj[k])...)
	}
	return out
}

// ── roles + invariants ────────────────────────────────────────────────────────

func parseRole(j any) ManifestRole {
	obj, ok := asObj(j)
	if !ok {
		return NamedRole{Name: ""}
	}
	if tone := asStr(obj["tone"]); tone != nil {
		if validated, ok := ToneOfString(*tone); ok {
			return ToneRole{Tone: validated}
		}
		return NamedRole{Name: *tone}
	}
	return NamedRole{Name: strOr(obj["named"], "")}
}

func parseRoleBinding(j any) (RoleBinding, bool) {
	obj, ok := asObj(j)
	if !ok {
		return RoleBinding{}, false
	}
	token := asStr(obj["token"])
	if token == nil {
		return RoleBinding{}, false
	}
	role := ManifestRole(NamedRole{Name: ""})
	if r, ok := obj["role"]; ok {
		role = parseRole(r)
	}
	return RoleBinding{Role: role, TokenName: *token}, true
}

func parseInvariant(j any) (Invariant, bool) {
	obj, ok := asObj(j)
	if !ok {
		return Invariant{}, false
	}
	weight := numOr(obj["weight"], DefaultWeight)
	var inner InvariantKind
	switch strOr(obj["kind"], "") {
	case "ContrastFloor":
		inner = ContrastFloor{Role: strOr(obj["role"], ""), MinRatio: numOr(obj["minRatio"], 0.0)}
	case "UsageBudget":
		inner = UsageBudget{
			Token:        strOr(obj["token"], ""),
			TargetPct:    numOr(obj["targetPct"], 0.0),
			TolerancePct: numOr(obj["tolerancePct"], 0.0),
		}
	case "MotionVoice":
		inner = MotionVoice{Budget: MotionBudget{
			MaxDurationMs: int(numOr(obj["maxDurationMs"], 0.0)),
			Easing:        asStr(obj["easing"]),
		}}
	default:
		return Invariant{}, false
	}
	return Invariant{Kind: inner, Weight: weight}, true
}

func parseMeta(obj map[string]any) ManifestMeta {
	return ManifestMeta{
		Name:        strOr(obj["name"], ""),
		Version:     strOr(obj["version"], ""),
		Description: asStr(obj["description"]),
	}
}

func asArray(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

// OfJSON builds a manifest from a parsed JSON value.
func OfJSON(root any) ThemeManifest {
	obj, ok := asObj(root)
	if !ok {
		return EmptyManifest
	}
	if _, hasTokens := obj["tokens"]; hasTokens {
		meta := AnonymousMeta
		if m, ok := asObj(obj["meta"]); ok {
			meta = parseMeta(m)
		}
		var roles []RoleBinding
		for _, r := range asArray(obj["roles"]) {
			if b, ok := parseRoleBinding(r); ok {
				roles = append(roles, b)
			}
		}
		var invariants []Invariant
		for _, x := range asArray(obj["invariants"]) {
			if inv, ok := parseInvariant(x); ok {
				invariants = append(invariants, inv)
			}
		}
		return ThemeManifest{
			Meta:       meta,
			Tokens:     walkTokens("", obj["tokens"]),
			Roles:      roles,
			Invariants: invariants,
		}
	}
	return ThemeManifest{Meta: AnonymousMeta, Tokens: walkTokens("", root)}
}

// Decode decodes a manifest from JSON; returns an error on a parse failure or a
// §21 limit breach.
//
// The parse runs through wire.ParseBounded rather than encoding/json directly.
// A manifest is untrusted text — it is exactly the artefact a design system
// hands over — and this walk is recursive over the token tree, so nothing else
// bounded its depth, its string lengths or its object widths. A breach comes
// back as a *wire.DecodeError with code LIMIT_EXCEEDED rather than as a syntax
// error, which is the classification §21.2 rule 2 requires for a well-formed
// document that is merely too large.
func Decode(jsonStr string) (ThemeManifest, error) {
	root, err := wire.ParseBounded(jsonStr)
	if err != nil {
		return EmptyManifest, err
	}
	return OfJSON(root), nil
}
