package thememanifest

import (
	"regexp"
	"strconv"
	"strings"
)

func ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
func itoa(i int) string     { return strconv.Itoa(i) }

// Token-surface projectors — lower the adoption floor by projecting an app's
// existing token surface into a baseline ThemeManifest (tokens + inferable role
// bindings) the operator then enriches with invariants. Three source formats +
// a last-write-wins merge, a sibling of the F#/Python Project tiers.

var commentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// CssBlock is one selector block — its selector text + the --name → value
// custom-property declarations.
type CssBlock struct {
	Selector     string
	Declarations [][2]string
}

// ScanCssBlocks scans flat `selector { … }` blocks, keeping only custom-property
// (--) declarations.
func ScanCssBlocks(css string) []CssBlock {
	cleaned := commentRe.ReplaceAllString(css, "")
	var blocks []CssBlock
	for _, chunk := range strings.Split(cleaned, "}") {
		brace := strings.Index(chunk, "{")
		if brace < 0 {
			continue
		}
		selector := strings.TrimSpace(chunk[:brace])
		body := chunk[brace+1:]
		var decls [][2]string
		for _, decl := range strings.Split(body, ";") {
			colon := strings.Index(decl, ":")
			if colon < 0 {
				continue
			}
			name := strings.TrimSpace(decl[:colon])
			value := strings.TrimSpace(decl[colon+1:])
			if strings.HasPrefix(name, "--") {
				decls = append(decls, [2]string{name, value})
			}
		}
		if len(decls) > 0 {
			blocks = append(blocks, CssBlock{Selector: selector, Declarations: decls})
		}
	}
	return blocks
}

func inferType(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	for _, p := range []string{"#", "rgb", "hsl", "oklch", "oklab", "color("} {
		if strings.HasPrefix(v, p) {
			return "color"
		}
	}
	for _, s := range []string{"px", "rem", "em", "%"} {
		if strings.HasSuffix(v, s) {
			return "dimension"
		}
	}
	for _, s := range []string{"ms", "s"} {
		if strings.HasSuffix(v, s) {
			return "duration"
		}
	}
	return ""
}

func token(name, value string) ManifestToken {
	return ManifestToken{Name: name, Type: inferType(value), Value: value}
}

// dedupeTokens keeps the last write for each name, preserving first-appearance
// order of surviving names (matches the Python dict last-write-wins semantics).
func dedupeTokens(tokens []ManifestToken) []ManifestToken {
	idx := map[string]int{}
	var out []ManifestToken
	for _, t := range tokens {
		if i, ok := idx[t.Name]; ok {
			out[i] = t
			continue
		}
		idx[t.Name] = len(out)
		out = append(out, t)
	}
	return out
}

func cap1(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

// ProjectFromFuaranToneVars projects a --fuaran-tone-{tone}-{slot} set into
// tokens + Tone role bindings. Token names are tone.{tone}.{slot}; each tone
// whose bg slot is present gets a tone role binding to that token.
func ProjectFromFuaranToneVars(css string) ThemeManifest {
	var raw []ManifestToken
	for _, b := range ScanCssBlocks(css) {
		for _, d := range b.Declarations {
			stripped := strings.TrimLeft(d[0], "-")
			if !strings.HasPrefix(stripped, "fuaran-tone-") {
				continue
			}
			rest := stripped[len("fuaran-tone-"):]
			parts := strings.Split(rest, "-")
			if len(parts) != 2 {
				continue
			}
			tone, slot := parts[0], parts[1]
			if _, ok := ToneOfString(cap1(tone)); !ok {
				continue
			}
			raw = append(raw, token("tone."+strings.ToLower(tone)+"."+strings.ToLower(slot), d[1]))
		}
	}
	tokens := dedupeTokens(raw)
	var roles []RoleBinding
	for _, t := range tokens {
		nameParts := strings.Split(t.Name, ".")
		if len(nameParts) == 3 && nameParts[0] == "tone" && nameParts[2] == "bg" {
			if boundTone, ok := ToneOfString(cap1(nameParts[1])); ok {
				roles = append(roles, RoleBinding{Role: ToneRole{Tone: boundTone}, TokenName: t.Name})
			}
		}
	}
	return ThemeManifest{Meta: AnonymousMeta, Tokens: tokens, Roles: roles}
}

func isDarkSelector(selector string) bool {
	s := strings.ToLower(selector)
	return strings.Contains(s, "data-theme=dark") || strings.Contains(s, `data-theme="dark"`) || strings.Contains(s, ".dark")
}

// ProjectFromCssCustomProperties projects a generic :root block (+ optional dark
// block) into tokens; roles are left unbound. Dark tokens carry an @dark suffix.
func ProjectFromCssCustomProperties(css string) ThemeManifest {
	blocks := ScanCssBlocks(css)
	var all []ManifestToken
	for _, b := range blocks {
		if isDarkSelector(b.Selector) {
			continue
		}
		for _, d := range b.Declarations {
			all = append(all, token(strings.TrimLeft(d[0], "-"), d[1]))
		}
	}
	for _, b := range blocks {
		if !isDarkSelector(b.Selector) {
			continue
		}
		for _, d := range b.Declarations {
			all = append(all, token(strings.TrimLeft(d[0], "-")+"@dark", d[1]))
		}
	}
	return ThemeManifest{Meta: AnonymousMeta, Tokens: dedupeTokens(all)}
}

// ProjectFromDTCG projects a DTCG / tokens.json file into a manifest (values
// lossless; roles unmined).
func ProjectFromDTCG(jsonStr string) (ThemeManifest, error) {
	return Decode(jsonStr)
}

// Merge combines base + override with last-write-wins precedence (the CSS cascade).
func Merge(base, over ThemeManifest) ThemeManifest {
	overNames := map[string]bool{}
	for _, t := range over.Tokens {
		overNames[t.Name] = true
	}
	var tokens []ManifestToken
	for _, t := range base.Tokens {
		if !overNames[t.Name] {
			tokens = append(tokens, t)
		}
	}
	tokens = append(tokens, over.Tokens...)

	overRoles := map[string]bool{}
	for _, r := range over.Roles {
		overRoles[roleKey(r.Role)] = true
	}
	var roles []RoleBinding
	for _, r := range base.Roles {
		if !overRoles[roleKey(r.Role)] {
			roles = append(roles, r)
		}
	}
	roles = append(roles, over.Roles...)

	seen := map[string]bool{}
	var invariants []Invariant
	for _, inv := range append(append([]Invariant{}, over.Invariants...), base.Invariants...) {
		k := invariantKey(inv)
		if !seen[k] {
			seen[k] = true
			invariants = append(invariants, inv)
		}
	}

	meta := base.Meta
	if over.Meta != AnonymousMeta {
		meta = over.Meta
	}
	return ThemeManifest{Meta: meta, Tokens: tokens, Roles: roles, Invariants: invariants}
}

func invariantKey(inv Invariant) string {
	name := InvariantKindName(inv)
	switch k := inv.Kind.(type) {
	case ContrastFloor:
		return name + "|" + k.Role + "|" + ftoa(k.MinRatio) + "|" + ftoa(inv.Weight)
	case UsageBudget:
		return name + "|" + k.Token + "|" + ftoa(k.TargetPct) + "|" + ftoa(k.TolerancePct) + "|" + ftoa(inv.Weight)
	case MotionVoice:
		e := ""
		if k.Budget.Easing != nil {
			e = *k.Budget.Easing
		}
		return name + "|" + itoa(k.Budget.MaxDurationMs) + "|" + e + "|" + ftoa(inv.Weight)
	}
	return name
}
