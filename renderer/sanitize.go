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

// SanitizeURL returns the URL and true if its scheme is accepted, else
// ("", false) — default-deny.
func SanitizeURL(url string) (string, bool) {
	trimmed := strings.TrimSpace(url)
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
			i := strings.Index(lower, openTag)
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
	result = dangerousProtocolRe.ReplaceAllString(result, "about:blank")
	return result
}
