package renderer

import (
	"regexp"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Class-name vocabulary — the parity contract with the reference renderers.
// The whole value of this renderer is that its output is visually consistent
// with the sibling hosts: it emits the identical fuaran-* class vocabulary, so
// the byte-copied content/fuaran-reference.css styles it unchanged. This file
// owns the projection from a decoded node's wire discriminator + style section
// to that vocabulary.

// kindClassMap projects the wire kind discriminator to the fuaran-kind-*
// class. Note the two deliberately-divergent grid names: layout GridLayout →
// fuaran-kind-grid-layout; visualisation DataGrid → fuaran-kind-grid. Box is
// NOT in this flat map — its hook derives from role + layout mode
// (boxKindClass) so the retired kinds' CSS hooks are unchanged.
var kindClassMap = map[string]string{
	// Layout
	"SplitPanel":  "fuaran-kind-split-panel",
	"Tabs":        "fuaran-kind-tabs",
	"Stepper":     "fuaran-kind-stepper",
	"SummaryList": "fuaran-kind-summary-list",
	"Disclosure":  "fuaran-kind-disclosure",
	"Modal":       "fuaran-kind-modal",
	"ScrollArea":  "fuaran-kind-scroll-area",
	// Display
	"Heading":       "fuaran-kind-heading",
	"LabelValueRow": "fuaran-kind-label-value-row",
	"Link":          "fuaran-kind-link",
	"Image":         "fuaran-kind-image",
	"List":          "fuaran-kind-list",
	"Toast":         "fuaran-kind-toast",
	"CodeBlock":     "fuaran-kind-code-block",
	"Math":          "fuaran-kind-math",
	"Markdown":      "fuaran-kind-markdown",
	"Metric":        "fuaran-kind-metric",
	"Badge":         "fuaran-kind-badge",
	"Sparkline":     "fuaran-kind-sparkline",
	"Callout":       "fuaran-kind-callout",
	"Progress":      "fuaran-kind-progress",
	"Skeleton":      "fuaran-kind-skeleton",
	"Icon":          "fuaran-kind-icon",
	// Input
	"Form":       "fuaran-kind-form",
	"Filters":    "fuaran-kind-filters",
	"Button":     "fuaran-kind-button",
	"FileUpload": "fuaran-kind-file-upload",
	"Select":     "fuaran-kind-select",
	// Visualisation
	"DataGrid": "fuaran-kind-grid",
	"Chart":    "fuaran-kind-chart",
	"Table":    "fuaran-kind-table",
	"Map":      "fuaran-kind-map",
	// Structural
	"ErrorBoundary": "fuaran-kind-error-boundary",
	"Switch":        "fuaran-kind-switch",
	"FragmentDecl":  "fuaran-kind-fragment-decl",
	"FragmentRef":   "fuaran-kind-fragment-ref",
}

var classFragmentRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sanitiseClassFragment replaces any char outside [a-zA-Z0-9_-] with '-'.
func sanitiseClassFragment(raw string) string {
	return classFragmentRe.ReplaceAllString(raw, "-")
}

// boxKindClass derives a Box's fuaran-kind-* hook from role + layout mode:
// Dashboard→dashboard, Card→card, Separator→divider, Group+Grid→grid-layout,
// Group+(Flex|Auto)→stack.
func boxKindClass(kind wire.Obj) string {
	role, _ := kind.Fields["role"].(wire.Str)
	layout, _ := kind.Fields["layout"].(wire.Obj)
	switch {
	case role == "Dashboard":
		return "fuaran-kind-dashboard"
	case role == "Card":
		return "fuaran-kind-card"
	case role == "Separator":
		return "fuaran-kind-divider"
	case role == "Group" && layout.Tag == "Grid":
		return "fuaran-kind-grid-layout"
	default:
		return "fuaran-kind-stack"
	}
}

// kindClass returns the fuaran-kind-* class for a decoded kind object.
func kindClass(kind wire.Obj) string {
	switch kind.Tag {
	case "Box":
		return boxKindClass(kind)
	case "Custom":
		moduleID := sanitiseClassFragment(strValue(kind.Fields["moduleId"]))
		componentID := sanitiseClassFragment(strValue(kind.Fields["componentId"]))
		return "fuaran-kind-custom fuaran-custom-" + moduleID + "-" + componentID
	}
	if cls, ok := kindClassMap[kind.Tag]; ok {
		return cls
	}
	// A recognised-but-unmapped kind still keys off the wire discriminator so
	// the vocabulary stays consistent end-to-end.
	return "fuaran-kind-" + sanitiseClassFragment(strings.ToLower(kind.Tag))
}

func strValue(v wire.Value) string {
	if s, ok := v.(wire.Str); ok {
		return string(s)
	}
	return ""
}

// styleClass projects a decoded style section (or the default) to the
// BEM-style class. Default: tone=Default, weight=Standard, emphasis=Normal —
// role None and voice Default contribute no fragment.
func styleClass(style wire.Obj) string {
	fragment := func(key, def string) string {
		if s, ok := style.Fields[key].(wire.Str); ok {
			return strings.ToLower(string(s))
		}
		return strings.ToLower(def)
	}
	parts := []string{
		"fuaran-node fuaran-tone-" + fragment("tone", "Default") +
			" fuaran-weight-" + fragment("weight", "Standard") +
			" fuaran-emphasis-" + fragment("emphasis", "Normal"),
	}
	if role, ok := style.Fields["role"].(wire.Str); ok && role != "None" {
		parts = append(parts, "fuaran-role-"+strings.ToLower(string(role)))
	}
	if voice, ok := style.Fields["voice"].(wire.Str); ok && voice != "Default" {
		parts = append(parts, "fuaran-voice-"+strings.ToLower(string(voice)))
	}
	return strings.Join(parts, " ")
}

// nodeClassName is the full wrapper className: kind class + style class.
func nodeClassName(node wire.Node) string {
	style, _ := node.Extras["style"].(wire.Obj)
	return kindClass(node.Kind) + " " + styleClass(style)
}

// trendSentiment computes a Metric trend's SENTIMENT class fragment and its
// non-colour glyph, per WIRE_FORMAT.md §3.6.1: `sentiment = sign(trend) ×
// polarity`, where HigherIsBetter is +1 and LowerIsBetter is −1.
//
// Two things this deliberately does NOT do, both of which a host reaches for and
// both of which the normative text refuses. It never negates the number: a
// −7.34% trend prints −7.34% under either declaration, because polarity changes
// how the number READS and never what it SAYS. And it never touches `tone` —
// tone colours the TILE and says how the reading STANDS, this says which way the
// quantity MOVED, and a renderer that inferred "improving ⇒ Success" would
// re-create in the render the exact conflation the wire slot exists to remove.
//
// The glyphs are U+25B2 BLACK UP-POINTING TRIANGLE, U+25BC BLACK DOWN-POINTING
// TRIANGLE and U+2192 RIGHTWARDS ARROW — named here so a mojibake in this file is
// a diff a reviewer can catch rather than a rendered byte nobody pinned. Colour
// alone fails WCAG 1.4.1, so the glyph is the sentiment's non-colour channel and
// carries the `aria-label`; it tracks the SENTIMENT rather than the number's
// direction, so under an inverted polarity the triangle deliberately disagrees
// with the sign — and that disagreement is the visible evidence the declaration
// was honoured.
//
// `polarity` is the decoded slot's bare-enum string, or "" for the omitted slot —
// which IS `HigherIsBetter` per clause 4, not a third state. The decoder has
// already refused everything outside the closed set, so the only two strings that
// can reach here are the two cases.
func trendSentiment(polarity string, trend float64) (sentiment, glyph string) {
	direction := 1.0
	if polarity == "LowerIsBetter" {
		direction = -1.0
	}
	switch product := trend * direction; {
	case product > 0:
		return "improving", "▲"
	case product < 0:
		return "regressing", "▼"
	default:
		return "unchanged", "→"
	}
}
