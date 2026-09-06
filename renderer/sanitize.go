package renderer

import (
	"regexp"
	"strings"
)

// Render-time injection-safety floor — the Go port of the sibling hosts'
// sanitiser seam. The wire decoder is best-effort: a malicious emission can
// smuggle an onerror= handler inside markdown source or a javascript: href
// through the decode path. This file is the last line of defence before bytes
// reach a browser's HTML parser, mirroring the reference posture seam-for-seam
// so the hosts cannot drift on safety:
//
//  1. URL props — block javascript: / vbscript: / file: and any unknown
//     scheme; allow http / https / mailto / tel / ftp / sftp plus same-origin
//     relative paths.
//  2. Markdown raw HTML — strip dangerous element blocks, inline on*=
//     handlers, and script-scheme URLs from rendered markdown.
//
// The wire-omitted ExtraAttributes seam does not exist on a decoded tree, so
// no attribute-allowlist filter is needed here; the renderer's attribute keys
// are all renderer-controlled.

var (
	allowedURLSchemes  = map[string]bool{"http": true, "https": true, "mailto": true, "tel": true, "ftp": true, "sftp": true}
	rejectedURLSchemes = map[string]bool{"javascript": true, "vbscript": true, "file": true}
)

// extractScheme returns the lowercased scheme and true, or false for a
// relative / fragment URL. It looks for the first ':' before any '/' '?' '#';
// whitespace and control chars inside the scheme region are stripped first so
// obfuscated forms of "javascript" still classify as javascript.
func extractScheme(url string) (string, bool) {
	colonIdx := -1
	for i := 0; i < len(url); i++ {
		c := url[i]
		if c == ':' {
			colonIdx = i
			break
		}
		if c == '/' || c == '?' || c == '#' {
			return "", false
		}
	}
	if colonIdx < 0 {
		return "", false
	}
	var sb strings.Builder
	for _, r := range url[:colonIdx] {
		if r > 0x20 {
			sb.WriteRune(r)
		}
	}
	return strings.ToLower(strings.TrimSpace(sb.String())), true
}

// isProtocolRelative reports whether the URL is protocol-relative: "//host/path"
// and the forms browsers fold into it. WHATWG URL parsing treats '\' as '/' for
// special schemes, so `\\host`, `/\host` and `\/host` all resolve exactly as
// `//host` does.
//
// These carry no scheme, so the schemeless branch of SanitizeURL would otherwise
// admit them — but the browser resolves them against the CURRENT page's scheme
// and lands on an OFF-ORIGIN host, defeating the same-origin intent that makes a
// schemeless URL safe. On an href that is off-origin navigation; on an image src
// it is an off-origin request that leaks the Referer.
func isProtocolRelative(url string) bool {
	if len(url) < 2 {
		return false
	}
	sep := func(c byte) bool { return c == '/' || c == '\\' }
	return sep(url[0]) && sep(url[1])
}

// normalizeURLForFloor applies §19 rule 1 — the normalisation the WHATWG URL
// Standard's basic URL parser performs before it parses anything, ASCII-exact, in
// this order:
//
//  1. remove leading and trailing C0 control or space — ALL of U+0000–U+0020, not
//     merely the whitespace subset;
//  2. remove every U+0009 / U+000A / U+000D from anywhere in what remains.
//
// Deliberately NOT strings.TrimSpace. A native trim answers a different question
// in every language — Python's strip also removes U+001C–U+001F where Go, .NET, JS
// and Rust do not; JS alone keeps U+0085 NEL where the other four drop it — and all
// of them remove non-ASCII whitespace (U+00A0, U+2028, …) that the parser keeps.
// The floor's whole purpose is that a tree vetted on one host is safe on another,
// so the normalisation is defined by the parser that will actually consume the
// string, not by the host's standard library.
//
// Step 2 is those three code points ONLY: the parser removes U+000B and U+000C at
// the edges (step 1) and KEEPS them in the interior, so "/<VT>/host/x" is an
// ordinary same-origin path and must stay one.
//
// Byte-wise is correct here: every code point at or below U+0020 is a single byte
// in UTF-8 and every continuation byte is >= 0x80, so this can never split a
// multi-byte character.
func normalizeURLForFloor(url string) string {
	lo, hi := 0, len(url)
	for lo < hi && url[lo] <= 0x20 {
		lo++
	}
	for hi > lo && url[hi-1] <= 0x20 {
		hi--
	}
	var sb strings.Builder
	sb.Grow(hi - lo)
	for i := lo; i < hi; i++ {
		if c := url[i]; c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		sb.WriteByte(url[i])
	}
	return sb.String()
}

// SanitizeURL returns the URL and true if its scheme is accepted, else
// ("", false) — default-deny.
//
// The input is first normalised per §19 rule 1 (see normalizeURLForFloor), and that
// normalised form is also what is RETURNED on acceptance — so an accepted URL
// carrying an interior tab loses it, which is what the browser would have parsed
// anyway.
func SanitizeURL(url string) (string, bool) {
	trimmed := normalizeURLForFloor(url)
	if trimmed == "" {
		// Empty href/src — pass through (a same-page link).
		return trimmed, true
	}
	scheme, hasScheme := extractScheme(trimmed)
	if !hasScheme {
		if isProtocolRelative(trimmed) {
			// Off-origin despite carrying no scheme.
			return "", false
		}
		// No scheme → relative / fragment / same-origin. Allowed.
		return trimmed, true
	}
	if rejectedURLSchemes[scheme] {
		return "", false
	}
	if allowedURLSchemes[scheme] {
		return trimmed, true
	}
	// Unknown scheme — reject by default (conservative; adding one is additive).
	return "", false
}

// SanitizeURLOrBlank returns the URL if accepted, else the literal
// "about:blank" (keeps the link valid).
func SanitizeURLOrBlank(url string) string {
	if safe, ok := SanitizeURL(url); ok {
		return safe
	}
	return "about:blank"
}

// ── Markdown raw-HTML sanitization ──────────────────────────────────────────

// asciiLower lowercases ASCII A-Z only, and is therefore byte-length preserving
// — which strings.ToLower is NOT (U+0130 is two UTF-8 bytes and lowercases to a
// three-byte sequence). The sweep below searches a case-folded COPY and slices
// the resulting byte offsets out of the ORIGINAL, so the two must stay aligned;
// a Unicode-aware fold silently shifts the removal window and leaves a fragment
// of the element it meant to remove. Operating a byte at a time is safe on UTF-8
// because every continuation byte has its high bit set and so never matches A-Z.
// The tag / scheme / protocol vocabulary this file matches is ASCII, so an
// ASCII-only fold loses no matches.
func asciiLower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

var dangerousElements = []string{"script", "iframe", "object", "embed", "form", "link", "meta"}

var (
	eventHandlerRe      = regexp.MustCompile(`(?i)\son[a-zA-Z]+(\s*=\s*("[^"]*"|'[^']*'|[^\s>]*))?`)
	tagSpanRe           = regexp.MustCompile(`<[^>]*>`)
	dangerousProtocolRe = regexp.MustCompile(`(?i)(javascript|vbscript):`)
)

// stripEventHandlers removes on*= handlers, but only inside tag interiors.
// The tag-interior anchor is load-bearing: the whitespace-"on<letter>" pattern
// also matches ordinary English words ("one", "only", "once", …), so running
// it globally would delete those from body text. The render path constrains
// the input to the deterministic markdown emitter's output (raw HTML already
// escaped), so a real handler attribute only appears inside a tag the
// renderer emitted.
func stripEventHandlers(html string) string {
	return tagSpanRe.ReplaceAllStringFunc(html, func(tag string) string {
		return eventHandlerRe.ReplaceAllString(tag, "")
	})
}

// sanitizeMarkdownHTML strips dangerous element blocks, on*= handlers, and
// script-scheme URLs. Approximate (not a full HTML parser): the render path
// constrains the input to the deterministic markdown emitter's output, so a
// substring/regex sweep is sufficient defence in depth — the floor, not the
// ceiling.
func sanitizeMarkdownHTML(html string) string {
	if html == "" {
		return ""
	}
	result := html
	for _, tag := range dangerousElements {
		openTag := "<" + tag
		closeTag := "</" + tag + ">"
		for {
			// ASCII-only fold: the byte offsets below index into `result`, so the
			// searched copy must stay byte-aligned with it (see asciiLower).
			lower := asciiLower(result)
			i := indexOfElementOpen(lower, openTag)
			if i < 0 {
				break
			}
			if j := strings.Index(lower[i:], closeTag); j >= 0 {
				result = result[:i] + result[i+j+len(closeTag):]
			} else if end := strings.IndexByte(result[i:], '>'); end >= 0 {
				result = result[:i] + result[i+end+1:]
			} else {
				result = result[:i]
			}
		}
	}
	result = stripEventHandlers(result)
	result = stripDangerousProtocols(result)
	return result
}

// stripDangerousProtocols rewrites javascript: / vbscript: URLs to about:blank,
// but only inside tag interiors — the same discipline stripEventHandlers
// already keeps, and here for the same reason.
//
// Unanchored, this sweep rewrote VISIBLE PROSE. The markdown source
// "Never write `javascript:` in an href" renders to a <code> element whose TEXT
// is the literal token, and the substitution replaced it with about:blank — so a
// document explaining the hazard could not state it, and the reader was shown a
// sentence the author never wrote.
//
// A real javascript: URL can only do harm as the VALUE of an attribute — href,
// src, action, formaction, xlink:href, data, poster — and every one of those
// sits inside a <…> tag. Restricting the scan to tag interiors is therefore not
// a heuristic narrowing: it is the precise set of positions where the token is a
// URL rather than a word.
//
// Reusing tagSpanRe keeps this byte-for-byte with the handler sweep beside it,
// and inherits the same approximation: a > inside a quoted attribute value ends
// the span early, which SKIPS a rewrite — the direction of error that leaves
// prose intact.
func stripDangerousProtocols(html string) string {
	return tagSpanRe.ReplaceAllStringFunc(html, func(tag string) string {
		return dangerousProtocolRe.ReplaceAllString(tag, "about:blank")
	})
}

// isTagNameBoundary reports whether index marks the end of a tag NAME.
//
// An HTML tag name ends at whitespace, `/` or `>`, so a match on the bare
// prefix is a match on a DIFFERENT element: `<metadata>` is not `<meta>`, and
// `<linearGradient>` is not `<link>`. Both are real SVG elements the drawing
// builder emits, and with the bare prefix the first of them lost its opening
// tag to this sweep, leaving the provenance document's text loose in the
// figure.
//
// Requiring the boundary narrows only false positives: no spelling of a real
// `<meta>` element survives it, because the name has to be delimited for a
// parser to read it as that element in the first place. End of input counts as
// a boundary, so a truncated `...<script` is still stripped.
//
// Parity-locked with the F# Sanitize.sanitizeMarkdownHtml and the TypeScript
// renderer's sanitize.ts.
func isTagNameBoundary(s string, index int) bool {
	if index >= len(s) {
		return true
	}
	c := s[index]
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' || c == '>'
}

// indexOfElementOpen finds the first "<tag" whose name is DELIMITED, i.e. the
// first position where openTag names the element rather than merely prefixing a
// longer name. Returns -1 when there is none.
func indexOfElementOpen(s string, openTag string) int {
	from := 0
	for from <= len(s)-len(openTag) {
		i := strings.Index(s[from:], openTag)
		if i < 0 {
			return -1
		}
		i += from
		if isTagNameBoundary(s, i+len(openTag)) {
			return i
		}
		from = i + 1
	}
	return -1
}
