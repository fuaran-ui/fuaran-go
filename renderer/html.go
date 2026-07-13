package renderer

import "strings"

// A tiny string-HTML builder + the escaping floor. The renderer emits HTML
// strings — no DOM, no template engine, stdlib only. This file is the single
// seam where a Go string becomes HTML, so the escaping floor lives here
// (mirroring the sibling hosts' escaping floors).
//
// Attribute VALUES are escaped here; attribute KEYS the caller passes are
// renderer-controlled (the class vocabulary, data-* markers, ARIA keys) —
// untrusted keys never reach this file (the URL seams are filtered upstream in
// sanitize.go). Attribute order is the callers' slice order, so output is
// deterministic and diffable.

type attr struct {
	name  string
	value string
}

// escapeText escapes a string for HTML text content.
func escapeText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

// escapeAttr escapes a string for a double-quoted HTML attribute value.
func escapeAttr(value string) string {
	value = escapeText(value)
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&#x27;")
	return value
}

func attrString(attrs []attr) string {
	var sb strings.Builder
	for _, a := range attrs {
		sb.WriteByte(' ')
		sb.WriteString(a.name)
		sb.WriteString(`="`)
		sb.WriteString(escapeAttr(a.value))
		sb.WriteByte('"')
	}
	return sb.String()
}

// element renders a non-void element with pre-escaped/pre-rendered inner HTML.
func element(tag string, attrs []attr, inner string) string {
	return "<" + tag + attrString(attrs) + ">" + inner + "</" + tag + ">"
}

// voidElement renders a void element (input / img / …) — self-closing.
func voidElement(tag string, attrs []attr) string {
	return "<" + tag + attrString(attrs) + " />"
}

// textElement renders an element whose only child is escaped text content.
func textElement(tag string, attrs []attr, text string) string {
	return element(tag, attrs, escapeText(text))
}
