// Package renderer is the headless server-HTML renderer over the decoded Node
// tree — a pure string-HTML walk that emits the reference fuaran-* class
// vocabulary so the byte-copied content/fuaran-reference.css styles the output
// exactly as it styles the sibling hosts' output.
//
// Server semantics mirror the reference SSR precedent: no runtime, no
// dispatch. Action-bearing nodes render inert (a Button is dead until a client
// hydrates it; a Link is a real crawlable <a href>). Static bindings resolve
// to their value; other bindings resolve from a host-supplied BindingSources
// map or fall back to the em-dash placeholder. Client-library visualisations
// (Chart / Map / DataGrid) render a deterministic placeholder, never a blank.
//
// The renderer emits the BODY FRAGMENT only — the host owns <html> / <head> /
// the <link> to ReferenceCSS.
package renderer

import (
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// emDash is the unresolved-binding placeholder (matches the reference SSR).
const emDash = "—"

// asNode coerces a child value to a node envelope. Typed-decoded layout
// children arrive as wire.Node; structurally-decoded layouts keep their
// children as raw tagged Objs — both render uniformly.
func asNode(value wire.Value) (wire.Node, bool) {
	switch t := value.(type) {
	case wire.Node:
		return t, true
	case wire.Obj:
		id, idOK := t.Fields["id"].(wire.Str)
		kind, kindOK := t.Fields["kind"].(wire.Obj)
		if !idOK || !kindOK {
			return wire.Node{}, false
		}
		extras := make(map[string]wire.Value)
		for _, key := range []string{"state", "style", "accessibility"} {
			if v, ok := t.Fields[key]; ok {
				extras[key] = v
			}
		}
		return wire.Node{ID: string(id), Kind: kind, Extras: extras}, true
	}
	return wire.Node{}, false
}

func childNodesOf(fields map[string]wire.Value) []wire.Node {
	arr, ok := fields["children"].(wire.Arr)
	if !ok {
		return nil
	}
	var out []wire.Node
	for _, item := range arr {
		if n, ok := asNode(item); ok {
			out = append(out, n)
		}
	}
	return out
}

// collectFragments walks the tree registering FragmentDecl bodies by name.
func collectFragments(node wire.Node, acc map[string]wire.Node) {
	kind := node.Kind
	if kind.Tag == "FragmentDecl" {
		if name, ok := kind.Fields["name"].(wire.Str); ok {
			if body, ok := asNode(kind.Fields["body"]); ok {
				acc[string(name)] = body
			}
		}
	}
	for _, child := range childNodesOf(kind.Fields) {
		collectFragments(child, acc)
	}
	if boundaryChild, ok := asNode(kind.Fields["child"]); ok {
		collectFragments(boundaryChild, acc)
	}
	if kind.Tag == "Switch" {
		if cases, ok := kind.Fields["cases"].(wire.Arr); ok {
			for _, item := range cases {
				if caseObj, ok := item.(wire.Obj); ok {
					if caseChild, ok := asNode(caseObj.Fields["child"]); ok {
						collectFragments(caseChild, acc)
					}
				}
			}
		}
		if def, ok := asNode(kind.Fields["default"]); ok {
			collectFragments(def, acc)
		}
	}
}

// renderer holds the per-render context: host binding sources, the fragment
// registry, and the island markers (empty on a plain render).
type renderer struct {
	sources   BindingSources
	fragments map[string]wire.Node
	islands   map[string]string // node id → island id
}

func (r *renderer) text(ts wire.Value) string {
	return renderText(ts, r.sources)
}

func (r *renderer) stateLoading(node wire.Node) (wire.Node, bool) {
	if state, ok := node.Extras["state"].(wire.Obj); ok {
		return asNode(state.Fields["onLoading"])
	}
	return wire.Node{}, false
}

// a11yAttrs projects the structural accessibility section best-effort.
func (r *renderer) a11yAttrs(node wire.Node) []attr {
	a11y, ok := node.Extras["accessibility"].(wire.Obj)
	if !ok {
		return nil
	}
	var out []attr
	if label, ok := a11y.Fields["label"].(wire.Str); ok && label != "" {
		out = append(out, attr{"aria-label", string(label)})
	}
	if labelledBy, ok := a11y.Fields["labelledBy"].(wire.Str); ok {
		out = append(out, attr{"aria-labelledby", string(labelledBy)})
	}
	if describedBy, ok := a11y.Fields["describedBy"].(wire.Str); ok {
		out = append(out, attr{"aria-describedby", string(describedBy)})
	}
	if role, ok := a11y.Fields["role"].(wire.Str); ok {
		out = append(out, attr{"role", strings.ToLower(string(role))})
	}
	if live, ok := a11y.Fields["liveRegion"].(wire.Str); ok {
		out = append(out, attr{"aria-live", strings.ToLower(string(live))})
	}
	return out
}

// renderNode emits the node wrapper <div> plus the kind body — and, when the
// node is marked as an island, the boundary wrapper around it (whose children
// are exactly this node's static HTML, so client hydration is mismatch-free).
func (r *renderer) renderNode(node wire.Node) string {
	attrs := []attr{
		{"id", node.ID},
		{"data-fuaran-node-id", node.ID},
		{"class", nodeClassName(node)},
	}
	attrs = append(attrs, r.a11yAttrs(node)...)
	rendered := element("div", attrs, r.renderKind(node))
	if islandID, ok := r.islands[node.ID]; ok {
		return element("div", []attr{
			{"class", "fuaran-island"},
			{"data-fuaran-island", islandID},
		}, rendered)
	}
	return rendered
}

func (r *renderer) renderKind(node wire.Node) string {
	fields := node.Kind.Fields
	switch node.Kind.Tag {
	case "Box":
		return r.box(node, fields)
	case "SplitPanel":
		return r.splitPanel(fields)
	case "Tabs":
		return r.tabs(node, fields)
	case "SummaryList":
		return r.summaryList(fields)
	case "Disclosure":
		return r.disclosure(fields)
	case "Stepper":
		return r.stepper(fields)
	case "Modal":
		return r.modal(fields)
	case "ScrollArea":
		return r.scrollArea(fields)
	case "Heading":
		return r.heading(fields)
	case "Markdown":
		return r.markdown(fields)
	case "Metric":
		return r.metric(node, fields)
	case "Fact":
		return r.fact(fields)
	case "Badge":
		return r.badge(fields)
	case "Callout":
		return r.callout(fields)
	case "Progress":
		return r.progress(node, fields)
	case "Skeleton":
		return r.skeleton(fields)
	case "Sparkline":
		return textElement("div", []attr{{"class", "fuaran-sparkline fuaran-sparkline-empty"}}, emDash)
	case "LabelValueRow":
		return r.labelValueRow(fields)
	case "Link":
		return r.link(fields)
	case "Image":
		return r.image(fields)
	case "List":
		return r.list(fields)
	case "Toast":
		return r.toast(fields)
	case "CodeBlock":
		return r.codeBlock(fields)
	case "Math":
		return r.math(fields)
	case "Drawing":
		return r.drawing(fields)
	case "Button":
		return r.button(fields)
	case "Select":
		return r.selectControl(fields)
	case "Form":
		return r.form(fields)
	case "Filters":
		return element("div", []attr{{"class", "fuaran-filters"}}, "")
	case "FileUpload":
		return r.fileUpload(fields)
	case "DataGrid":
		return r.dataGrid(fields)
	case "Chart":
		return r.chart(fields)
	case "Map":
		return r.mapVis(fields)
	case "ErrorBoundary":
		if child, ok := asNode(fields["child"]); ok {
			return r.renderNode(child)
		}
		return ""
	case "Switch":
		return r.switchKind(fields)
	case "FragmentDecl":
		return "" // zero-paint — the decl is a template, not visible output.
	case "FragmentRef":
		return r.fragmentRef(fields)
	case "Custom":
		return r.custom(fields)
	}
	// Recognised-but-unhandled kind: render any children so the subtree is
	// never silently dropped (the wrapper already carries the kind class).
	return r.childrenHTML(fields)
}

// ── Layouts ─────────────────────────────────────────────────────────────────

func (r *renderer) childrenHTML(fields map[string]wire.Value) string {
	var sb strings.Builder
	for _, c := range childNodesOf(fields) {
		sb.WriteString(r.renderNode(c))
	}
	return sb.String()
}

// box is the unified container: role + layout mode drive the emitted element
// + classes so each retired kind's HTML/a11y is byte-identical.
func (r *renderer) box(_ wire.Node, fields map[string]wire.Value) string {
	role, _ := fields["role"].(wire.Str)
	layout, _ := fields["layout"].(wire.Obj)

	switch {
	case role == "Card":
		header := ""
		if heading, ok := fields["heading"]; ok {
			header = textElement("header", []attr{{"class", "fuaran-card-heading"}}, r.text(heading))
		}
		body := element("div", []attr{{"class", "fuaran-card-body"}}, r.childrenHTML(fields))
		return element("section", []attr{{"class", "fuaran-layout-card"}}, header+body)
	case role == "Dashboard" || (role == "Group" && layout.Tag == "Auto"):
		return element("div", []attr{{"class", "fuaran-layout-dashboard"}}, r.childrenHTML(fields))
	case role == "Separator":
		return element("hr", []attr{{"class", "fuaran-layout-separator"}}, "")
	case role == "Group" && layout.Tag == "Grid":
		template := ""
		if t, ok := layout.Fields["templateColumns"].(wire.Str); ok {
			template = string(t)
		} else {
			cols := int64(1)
			if c, ok := layout.Fields["cols"].(wire.Int); ok {
				cols = int64(c)
			}
			template = "repeat(" + strconv.FormatInt(cols, 10) + ", 1fr)"
		}
		style := "grid-template-columns:" + template
		if gap, ok := layout.Fields["gap"].(wire.Int); ok {
			style += ";gap:" + strconv.FormatInt(int64(gap), 10) + "px"
		}
		return element("div", []attr{{"class", "fuaran-layout-grid"}, {"style", style}}, r.childrenHTML(fields))
	default:
		// Group + Flex (the default / fallthrough).
		dirClass := "fuaran-stack-vertical"
		if d, ok := layout.Fields["direction"].(wire.Str); ok && d == "Horizontal" {
			dirClass = "fuaran-stack-horizontal"
		}
		wrap := ""
		if w, ok := layout.Fields["wrap"].(wire.Bool); ok && bool(w) {
			wrap = " fuaran-stack-wrap"
		}
		attrs := []attr{{"class", "fuaran-layout-stack " + dirClass + wrap}}
		if gap, ok := layout.Fields["gap"].(wire.Int); ok {
			attrs = append(attrs, attr{"style", "gap:" + strconv.FormatInt(int64(gap), 10) + "px"})
		}
		return element("div", attrs, r.childrenHTML(fields))
	}
}

// flexWeight renders a pane weight with six decimal places (host parity).
func flexWeight(w float64) string {
	return strconv.FormatFloat(w, 'f', 6, 64)
}

func (r *renderer) splitPanel(fields map[string]wire.Value) string {
	leftW := 0.5
	if w, ok := numericValue(fields["weight"]); ok {
		leftW = max(0.0, min(1.0, w))
	}
	rightW := 1.0 - leftW
	children := childNodesOf(fields)
	var left, right string
	if len(children) > 0 {
		left = r.renderNode(children[0])
	}
	for _, c := range children[min(1, len(children)):] {
		right += r.renderNode(c)
	}
	leftHTML := element("div", []attr{
		{"class", "fuaran-split-pane fuaran-split-pane-left"},
		{"style", "flex:" + flexWeight(leftW) + " 1 0"},
	}, left)
	rightHTML := element("div", []attr{
		{"class", "fuaran-split-pane fuaran-split-pane-right"},
		{"style", "flex:" + flexWeight(rightW) + " 1 0"},
	}, right)
	return element("div", []attr{{"class", "fuaran-layout-split-panel"}}, leftHTML+rightHTML)
}

// tabLabel: a Box with role=Card and a heading names its tab.
func (r *renderer) tabLabel(child wire.Node) string {
	if child.Kind.Tag == "Box" {
		if role, ok := child.Kind.Fields["role"].(wire.Str); ok && role == "Card" {
			if heading, ok := child.Kind.Fields["heading"]; ok {
				return r.text(heading)
			}
		}
	}
	return child.ID
}

func (r *renderer) tabs(node wire.Node, fields map[string]wire.Value) string {
	children := childNodesOf(fields)
	vertical := false
	if o, ok := fields["orientation"].(wire.Str); ok && o == "Vertical" {
		vertical = true
	}
	orientationClass := "fuaran-tabs-horizontal"
	ariaOrientation := "horizontal"
	if vertical {
		orientationClass = "fuaran-tabs-vertical"
		ariaOrientation = "vertical"
	}
	activeIndex := 0
	if v, ok := resolveBinding(fields["activeIndex"], r.sources).(wire.Int); ok {
		activeIndex = int(v)
	}
	activeIndex = max(0, min(activeIndex, max(0, len(children)-1)))

	var tabs strings.Builder
	for i, child := range children {
		isActive := i == activeIndex
		cls := "fuaran-tab"
		selected := "false"
		if isActive {
			cls += " fuaran-tab-active"
			selected = "true"
		}
		tabs.WriteString(element("button", []attr{
			{"id", node.ID + "-tab-" + strconv.Itoa(i)},
			{"class", cls},
			{"role", "tab"},
			{"aria-selected", selected},
			{"aria-controls", node.ID + "-panel-" + strconv.Itoa(i)},
			{"data-tab-index", strconv.Itoa(i)},
		}, element("span", []attr{{"class", "fuaran-tab-label"}}, escapeText(r.tabLabel(child)))))
	}
	bar := element("div", []attr{
		{"class", "fuaran-tabs-bar"},
		{"role", "tablist"},
		{"aria-orientation", ariaOrientation},
	}, tabs.String())
	panel := ""
	if len(children) > 0 {
		panel = element("div", []attr{
			{"id", node.ID + "-panel-" + strconv.Itoa(activeIndex)},
			{"role", "tabpanel"},
			{"aria-labelledby", node.ID + "-tab-" + strconv.Itoa(activeIndex)},
			{"class", "fuaran-tabs-panel"},
		}, r.renderNode(children[activeIndex]))
	}
	panels := element("div", []attr{{"class", "fuaran-tabs-panels"}}, panel)
	return element("div", []attr{{"class", "fuaran-layout-tabs " + orientationClass}}, bar+panels)
}

func (r *renderer) summaryList(fields map[string]wire.Value) string {
	header := ""
	if heading, ok := fields["heading"]; ok {
		header = textElement("header", []attr{{"class", "fuaran-summary-list-heading"}}, r.text(heading))
	}
	body := element("div", []attr{{"class", "fuaran-summary-list-body"}}, r.childrenHTML(fields))
	return element("section", []attr{{"class", "fuaran-layout-summary-list"}}, header+body)
}

func (r *renderer) disclosure(fields map[string]wire.Value) string {
	isOpen := false
	if resolved, ok := resolveBinding(fields["open"], r.sources).(wire.Bool); ok {
		isOpen = bool(resolved)
	} else if d, ok := fields["defaultOpen"].(wire.Bool); ok && bool(d) {
		isOpen = true
	}
	attrs := []attr{{"class", "fuaran-layout-disclosure"}}
	if isOpen {
		attrs = append(attrs, attr{"open", ""})
	}
	summary := textElement("summary", []attr{{"class", "fuaran-disclosure-summary"}}, r.text(fields["heading"]))
	body := element("div", []attr{{"class", "fuaran-disclosure-body"}}, r.childrenHTML(fields))
	return element("details", attrs, summary+body)
}

func (r *renderer) stepper(fields map[string]wire.Value) string {
	children := childNodesOf(fields)
	activeIndex := 0
	if v, ok := resolveBinding(fields["activeStep"], r.sources).(wire.Int); ok {
		activeIndex = int(v)
	}
	var steps strings.Builder
	for i := range children {
		cls := "fuaran-stepper-step"
		if i == activeIndex {
			cls += " fuaran-stepper-step-active"
		}
		steps.WriteString(element("li", []attr{
			{"class", cls},
			{"data-step-index", strconv.Itoa(i)},
		}, escapeText(strconv.Itoa(i+1))))
	}
	numbers := element("ol", []attr{{"class", "fuaran-stepper-numbers"}}, steps.String())
	body := ""
	if activeIndex >= 0 && activeIndex < len(children) {
		body = r.renderNode(children[activeIndex])
	}
	bodyHTML := element("div", []attr{{"class", "fuaran-stepper-body"}}, body)
	return element("div", []attr{{"class", "fuaran-layout-stepper"}}, numbers+bodyHTML)
}

// modal: the overlay render-fidelity contract's server half — the overlay is
// ALWAYS emitted (no portal), positioned + z-indexed by CSS; closed = the
// hidden attribute. role="dialog" + aria-modal, byte-identical structure to
// the client renderer so hydration finds the DOM it expects.
func (r *renderer) modal(fields map[string]wire.Value) string {
	isOpen := false
	if v, ok := resolveBinding(fields["open"], r.sources).(wire.Bool); ok {
		isOpen = bool(v)
	}
	var parts strings.Builder
	if heading, ok := fields["heading"]; ok {
		parts.WriteString(textElement("h2", []attr{{"class", "fuaran-modal-heading"}}, r.text(heading)))
	}
	if d, ok := fields["dismissable"].(wire.Bool); ok && bool(d) {
		parts.WriteString(textElement("button", []attr{
			{"class", "fuaran-modal-dismiss"}, {"type", "button"}, {"aria-label", "Close"},
		}, "×"))
	}
	parts.WriteString(element("div", []attr{{"class", "fuaran-modal-body"}}, r.childrenHTML(fields)))
	dialog := element("div", []attr{
		{"class", "fuaran-modal-dialog"}, {"role", "dialog"}, {"aria-modal", "true"},
	}, parts.String())
	overlayAttrs := []attr{{"class", "fuaran-modal-overlay"}}
	if !isOpen {
		overlayAttrs = append(overlayAttrs, attr{"hidden", ""})
	}
	return element("div", overlayAttrs, dialog)
}

func (r *renderer) scrollArea(fields map[string]wire.Value) string {
	axis := "vertical"
	if o, ok := fields["orientation"].(wire.Str); ok {
		switch o {
		case "Horizontal":
			axis = "horizontal"
		case "Both":
			axis = "both"
		}
	}
	attrs := []attr{
		{"class", "fuaran-scrollarea fuaran-scrollarea-" + axis},
		{"tabindex", "0"},
	}
	var styleParts []string
	if h, ok := fields["maxHeight"].(wire.Int); ok {
		styleParts = append(styleParts, "max-height:"+strconv.FormatInt(int64(h), 10)+"px")
	}
	if w, ok := fields["maxWidth"].(wire.Int); ok {
		styleParts = append(styleParts, "max-width:"+strconv.FormatInt(int64(w), 10)+"px")
	}
	if len(styleParts) > 0 {
		attrs = append(attrs, attr{"style", strings.Join(styleParts, ";")})
	}
	return element("div", attrs, r.childrenHTML(fields))
}

// ── Displays ────────────────────────────────────────────────────────────────

func (r *renderer) heading(fields map[string]wire.Value) string {
	suffix := ""
	if v, ok := fields["variant"].(wire.Str); ok {
		switch v {
		case "Eyebrow":
			suffix = " fuaran-heading-eyebrow"
		case "Caption":
			suffix = " fuaran-heading-caption"
		case "Lead":
			suffix = " fuaran-heading-lead"
		}
	}
	level := 6
	if l, ok := fields["level"].(wire.Int); ok && l >= 1 && l <= 6 {
		level = int(l)
	}
	return textElement("h"+strconv.Itoa(level), []attr{{"class", "fuaran-heading" + suffix}}, r.text(fields["text"]))
}

func (r *renderer) markdown(fields map[string]wire.Value) string {
	return element("div", []attr{{"class", "fuaran-markdown"}}, MarkdownToHTML(r.text(fields["text"])))
}

func (r *renderer) metric(node wire.Node, fields map[string]wire.Value) string {
	value := resolveScalarNumber(fields["value"], r.sources)
	if value == nil {
		if loading, ok := r.stateLoading(node); ok {
			return r.renderNode(loading)
		}
	}
	tone := lowerEnum(fields["tone"], "Default")
	valueText := emDash
	if value != nil {
		valueText = formatNumber(fields["format"], value)
	}
	var parts strings.Builder
	parts.WriteString(textElement("div", []attr{{"class", "fuaran-metric-label"}}, r.text(fields["label"])))
	parts.WriteString(textElement("div", []attr{{"class", "fuaran-metric-value"}}, valueText))
	if subtext, ok := fields["subtext"]; ok {
		parts.WriteString(textElement("div", []attr{{"class", "fuaran-metric-subtext"}}, r.text(subtext)))
	}
	return element("div", []attr{{"class", "fuaran-metric fuaran-metric-" + tone}}, parts.String())
}

// fact — the labeled TEXT fact tile ("Patient: Alice Smith"). Mirrors the
// reference server renderer's markup: label + value (with optional icon span)
// + optional help, tone class suffix, emphasis modifier.
func (r *renderer) fact(fields map[string]wire.Value) string {
	tone := lowerEnum(fields["tone"], "Default")
	emphasis := ""
	if v, ok := fields["emphasis"].(wire.Bool); ok && bool(v) {
		emphasis = " fuaran-fact-emphasis"
	}
	var value strings.Builder
	if icon, ok := fields["icon"].(wire.Str); ok {
		value.WriteString(textElement("span", []attr{{"class", "fuaran-fact-icon"}}, string(icon)))
	}
	value.WriteString(textElement("span", nil, r.text(fields["value"])))
	var parts strings.Builder
	parts.WriteString(textElement("div", []attr{{"class", "fuaran-fact-label"}}, r.text(fields["label"])))
	parts.WriteString(element("div", []attr{{"class", "fuaran-fact-value"}}, value.String()))
	if help, ok := fields["help"]; ok {
		parts.WriteString(textElement("div", []attr{{"class", "fuaran-fact-help"}}, r.text(help)))
	}
	return element("div", []attr{{"class", "fuaran-fact fuaran-fact-" + tone + emphasis}}, parts.String())
}

func lowerEnum(v wire.Value, def string) string {
	if s, ok := v.(wire.Str); ok {
		return strings.ToLower(string(s))
	}
	return strings.ToLower(def)
}

func (r *renderer) badge(fields map[string]wire.Value) string {
	variant := lowerEnum(fields["variant"], "Neutral")
	return textElement("span", []attr{{"class", "fuaran-badge fuaran-badge-" + variant}}, r.text(fields["label"]))
}

func (r *renderer) callout(fields map[string]wire.Value) string {
	tone := lowerEnum(fields["tone"], "Default")
	var parts strings.Builder
	if heading, ok := fields["heading"]; ok {
		parts.WriteString(textElement("div", []attr{{"class", "fuaran-callout-heading"}}, r.text(heading)))
	}
	parts.WriteString(textElement("div", []attr{{"class", "fuaran-callout-body"}}, r.text(fields["body"])))
	return element("div", []attr{{"class", "fuaran-callout fuaran-callout-" + tone}}, parts.String())
}

func (r *renderer) progress(node wire.Node, fields map[string]wire.Value) string {
	resolved := resolveBinding(fields["fraction"], r.sources)
	if resolved == nil {
		if loading, ok := r.stateLoading(node); ok {
			return r.renderNode(loading)
		}
	}
	fraction := 0.0
	if f, ok := numericValue(resolved); ok {
		fraction = f
	}
	tone := lowerEnum(fields["tone"], "Default")
	indeterminate := ""
	if v, ok := fields["indeterminate"].(wire.Bool); ok && bool(v) {
		indeterminate = " fuaran-progress-indeterminate"
	}
	var parts strings.Builder
	if label, ok := fields["label"]; ok {
		parts.WriteString(textElement("div", []attr{{"class", "fuaran-progress-label"}}, r.text(label)))
	}
	fill := element("div", []attr{
		{"class", "fuaran-progress-fill"},
		{"style", "width:" + flexWeight(fraction*100.0) + "%"},
	}, "")
	parts.WriteString(element("div", []attr{{"class", "fuaran-progress-bar"}}, fill))
	return element("div", []attr{{"class", "fuaran-progress fuaran-progress-" + tone + indeterminate}}, parts.String())
}

func (r *renderer) skeleton(fields map[string]wire.Value) string {
	rows := 1
	if v, ok := fields["rows"].(wire.Int); ok && v > 0 {
		rows = int(v)
	}
	var body strings.Builder
	for range rows {
		body.WriteString(element("div", []attr{{"class", "fuaran-skeleton-row"}}, ""))
	}
	return element("div", []attr{{"class", "fuaran-skeleton"}}, body.String())
}

func (r *renderer) labelValueRow(fields map[string]wire.Value) string {
	emphasis := ""
	if v, ok := fields["emphasis"].(wire.Bool); ok && bool(v) {
		emphasis = " fuaran-label-value-row-emphasis"
	}
	value := resolveScalarNumber(fields["value"], r.sources)
	valueText := emDash
	if value != nil {
		valueText = formatNumber(fields["format"], value)
	}
	label := textElement("span", []attr{{"class", "fuaran-label-value-row-label"}}, r.text(fields["label"]))
	val := textElement("span", []attr{{"class", "fuaran-label-value-row-value"}}, valueText)
	return element("div", []attr{{"class", "fuaran-label-value-row" + emphasis}}, label+val)
}

func (r *renderer) link(fields map[string]wire.Value) string {
	href := ""
	if h, ok := resolveBinding(fields["href"], r.sources).(wire.Str); ok {
		href = string(h)
	}
	attrs := []attr{{"class", "fuaran-link"}, {"href", SanitizeURLOrBlank(href)}}
	if rel, ok := fields["rel"].(wire.Str); ok {
		attrs = append(attrs, attr{"rel", string(rel)})
	}
	if target, ok := fields["target"].(wire.Str); ok {
		attrs = append(attrs, attr{"target", string(target)})
	}
	if d, ok := fields["download"].(wire.Bool); ok && bool(d) {
		attrs = append(attrs, attr{"download", ""})
	}
	return textElement("a", attrs, r.text(fields["label"]))
}

func (r *renderer) image(fields map[string]wire.Value) string {
	src := ""
	if s, ok := resolveBinding(fields["src"], r.sources).(wire.Str); ok {
		src = string(s)
	}
	cls := "fuaran-image"
	if v, ok := fields["variant"].(wire.Str); ok {
		switch v {
		case "Avatar":
			cls = "fuaran-image fuaran-image-avatar"
		case "Rounded":
			cls = "fuaran-image fuaran-image-rounded"
		}
	}
	return voidElement("img", []attr{
		{"class", cls}, {"src", SanitizeURLOrBlank(src)}, {"alt", r.text(fields["alt"])},
	})
}

func (r *renderer) list(fields map[string]wire.Value) string {
	var items strings.Builder
	if raw, ok := fields["items"].(wire.Arr); ok {
		for _, item := range raw {
			items.WriteString(textElement("li", []attr{{"class", "fuaran-list-item"}}, r.text(item)))
		}
	}
	if ordered, ok := fields["ordered"].(wire.Bool); ok && bool(ordered) {
		return element("ol", []attr{{"class", "fuaran-list fuaran-list-ordered"}}, items.String())
	}
	return element("ul", []attr{{"class", "fuaran-list fuaran-list-unordered"}}, items.String())
}

// toast: the overlay render-fidelity contract's server half — always emitted;
// closed = the hidden attribute; role="status" + aria-live="polite".
func (r *renderer) toast(fields map[string]wire.Value) string {
	isOpen := false
	if v, ok := resolveBinding(fields["open"], r.sources).(wire.Bool); ok {
		isOpen = bool(v)
	}
	tone := lowerEnum(fields["tone"], "Default")
	var parts strings.Builder
	parts.WriteString(textElement("span", []attr{{"class", "fuaran-toast-message"}}, r.text(fields["message"])))
	if d, ok := fields["dismissable"].(wire.Bool); ok && bool(d) {
		parts.WriteString(textElement("button", []attr{
			{"class", "fuaran-toast-dismiss"}, {"type", "button"}, {"aria-label", "Dismiss"},
		}, "×"))
	}
	attrs := []attr{
		{"class", "fuaran-toast fuaran-toast-" + tone},
		{"role", "status"},
		{"aria-live", "polite"},
	}
	if !isOpen {
		attrs = append(attrs, attr{"hidden", ""})
	}
	return element("div", attrs, parts.String())
}

// codeBlock: deterministic <pre><code> (HTML-escaped, no markdown library);
// syntax highlighting is a client-only enhancement targeting .language-{x},
// outside the parity output.
func (r *renderer) codeBlock(fields map[string]wire.Value) string {
	language := strValue(fields["language"])
	containerClass := "fuaran-codeblock"
	if v, ok := fields["lineNumbers"].(wire.Bool); ok && bool(v) {
		containerClass = "fuaran-codeblock fuaran-codeblock-numbered"
	}
	attrs := []attr{{"class", containerClass}, {"data-language", language}}
	if highlight, ok := fields["highlightLines"].(wire.Arr); ok && len(highlight) > 0 {
		nums := make([]string, 0, len(highlight))
		for _, n := range highlight {
			if i, ok := n.(wire.Int); ok {
				nums = append(nums, strconv.FormatInt(int64(i), 10))
			}
		}
		attrs = append(attrs, attr{"data-highlight-lines", strings.Join(nums, ",")})
	}
	var parts strings.Builder
	if v, ok := fields["copyable"].(wire.Bool); ok && bool(v) {
		parts.WriteString(textElement("button", []attr{
			{"class", "fuaran-codeblock-copy"}, {"type", "button"}, {"aria-label", "Copy"},
		}, "Copy"))
	}
	codeEl := textElement("code", []attr{{"class", "fuaran-codeblock-code language-" + language}}, strValue(fields["code"]))
	parts.WriteString(element("pre", []attr{{"class", "fuaran-codeblock-pre"}}, codeEl))
	return element("div", attrs, parts.String())
}

// math: deterministic escaped-source fallback; KaTeX is a client-only
// enhancement targeting .fuaran-math-source, outside the parity output.
func (r *renderer) math(fields map[string]wire.Value) string {
	sourceSpan := textElement("span", []attr{{"class", "fuaran-math-source"}}, strValue(fields["source"]))
	if v, ok := fields["display"].(wire.Str); ok && v == "Inline" {
		return element("span", []attr{
			{"class", "fuaran-math fuaran-math-inline"}, {"data-math-display", "inline"},
		}, sourceSpan)
	}
	return element("div", []attr{
		{"class", "fuaran-math fuaran-math-block"}, {"data-math-display", "block"},
	}, sourceSpan)
}

// ── Inputs (inert — no dispatch server-side) ────────────────────────────────

func (r *renderer) button(fields map[string]wire.Value) string {
	variant := lowerEnum(fields["variant"], "Primary")
	attrs := []attr{{"class", "fuaran-button fuaran-button-" + variant}}
	if tooltip, ok := fields["tooltip"]; ok {
		attrs = append(attrs, attr{"title", r.text(tooltip)})
	}
	if v, ok := resolveBinding(fields["disabled"], r.sources).(wire.Bool); ok && bool(v) {
		attrs = append(attrs, attr{"disabled", ""})
	}
	return textElement("button", attrs, r.text(fields["label"]))
}

func (r *renderer) selectControl(fields map[string]wire.Value) string {
	label := element("span", []attr{{"class", "fuaran-select-label"}}, escapeText(r.text(fields["label"])))
	options := r.renderOptions(fields["source"], fields["placeholder"])
	selectAttrs := []attr{{"class", "fuaran-select-control"}}
	// A multi-select emits the multiple attribute; a controlled
	// <select multiple> rejects a scalar value, so none is emitted here.
	if v, ok := fields["multiple"].(wire.Bool); ok && bool(v) {
		selectAttrs = append(selectAttrs, attr{"multiple", ""})
	}
	if v, ok := resolveBinding(fields["disabled"], r.sources).(wire.Bool); ok && bool(v) {
		selectAttrs = append(selectAttrs, attr{"disabled", ""})
	}
	control := element("select", selectAttrs, options)
	return element("label", []attr{{"class", "fuaran-select"}}, label+control)
}

func (r *renderer) renderOptions(source, placeholder wire.Value) string {
	var items strings.Builder
	if placeholder != nil {
		items.WriteString(textElement("option", []attr{{"value", ""}}, r.text(placeholder)))
	}
	if resolved, ok := resolveBinding(source, r.sources).(wire.Arr); ok {
		for _, opt := range resolved {
			optObj, ok := opt.(wire.Obj)
			if !ok {
				continue
			}
			value := strValue(optObj.Fields["value"])
			labelText := value
			if label, ok := optObj.Fields["label"]; ok {
				labelText = r.text(label)
			}
			items.WriteString(textElement("option", []attr{{"value", value}}, labelText))
		}
	}
	return items.String()
}

func (r *renderer) form(fields map[string]wire.Value) string {
	var fieldHTML strings.Builder
	if raw, ok := fields["fields"].(wire.Arr); ok {
		for _, f := range raw {
			if fieldObj, ok := f.(wire.Obj); ok {
				fieldHTML.WriteString(r.formField(fieldObj))
			}
		}
	}
	submit := textElement("button", []attr{
		{"class", "fuaran-form-submit"}, {"type", "submit"},
	}, r.text(fields["submitLabel"]))
	return element("form", []attr{{"class", "fuaran-form"}}, fieldHTML.String()+submit)
}

func (r *renderer) formField(field wire.Obj) string {
	fieldID := strValue(field.Fields["id"])
	label := element("span", []attr{{"class", "fuaran-form-field-label"}}, escapeText(r.text(field.Fields["label"])))
	control := voidElement("input", []attr{
		{"class", "fuaran-form-field-control"}, {"data-fuaran-field", fieldID},
	})
	return element("label", []attr{{"class", "fuaran-form-field"}}, label+control)
}

func (r *renderer) fileUpload(fields map[string]wire.Value) string {
	label := element("span", []attr{{"class", "fuaran-file-upload-label"}}, escapeText(r.text(fields["label"])))
	control := voidElement("input", []attr{{"class", "fuaran-file-upload-control"}, {"type", "file"}})
	return element("label", []attr{{"class", "fuaran-file-upload"}}, label+control)
}

// ── Visualisations ──────────────────────────────────────────────────────────

func (r *renderer) staticTable(fields map[string]wire.Value) string {
	var headerCells strings.Builder
	if headers, ok := fields["headers"].(wire.Arr); ok {
		for _, h := range headers {
			headerCells.WriteString(textElement("th", []attr{{"class", "fuaran-table-header"}}, r.text(h)))
		}
	}
	var bodyRows strings.Builder
	if rows, ok := fields["rows"].(wire.Arr); ok {
		for _, row := range rows {
			rowArr, ok := row.(wire.Arr)
			if !ok {
				continue
			}
			var cells strings.Builder
			for _, c := range rowArr {
				cells.WriteString(textElement("td", []attr{{"class", "fuaran-table-cell"}}, r.text(c)))
			}
			bodyRows.WriteString(element("tr", []attr{{"class", "fuaran-table-row"}}, cells.String()))
		}
	}
	thead := element("thead", nil, element("tr", nil, headerCells.String()))
	tbody := element("tbody", nil, bodyRows.String())
	// Phase 801 — the declared sort intent as data attributes, so a
	// progressive-enhancement script honours it without re-parsing the wire.
	// Emitted ONLY when declared (an undeclared table's bytes are unchanged), and
	// in the same order as the F# / TS / Python server renderers so the
	// parity-locked markup stays parity-locked.
	attrs := []attr{{"class", "fuaran-table"}}
	if sortable, ok := fields["sortable"].(wire.Bool); ok {
		if bool(sortable) {
			attrs = append(attrs, attr{"data-fuaran-sortable", "true"})
		} else {
			attrs = append(attrs, attr{"data-fuaran-sortable", "false"})
		}
	}
	if ds, ok := fields["defaultSort"].(wire.Obj); ok {
		column, colOK := ds.Fields["column"].(wire.Int)
		direction, dirOK := ds.Fields["direction"].(wire.Str)
		if colOK && dirOK {
			attrs = append(attrs, attr{"data-fuaran-sort-column", strconv.FormatInt(int64(column), 10)})
			attrs = append(attrs, attr{"data-fuaran-sort-direction", string(direction)})
		}
	}
	return element("table", attrs, thead+tbody)
}

func seqLen(v wire.Value) int {
	if arr, ok := v.(wire.Arr); ok {
		return len(arr)
	}
	return 0
}

// dataGrid: a static read-only grid renders the semantic <table>; a
// data-bound grid renders a client-hydration placeholder.
func (r *renderer) dataGrid(fields map[string]wire.Value) string {
	if staticRows, ok := fields["staticRows"].(wire.Obj); ok {
		return r.staticTable(staticRows.Fields)
	}
	count := seqLen(resolveSource(fields["source"], r.sources))
	return textElement("div", []attr{
		{"class", "fuaran-grid fuaran-grid-ssr-placeholder"},
		{"data-fuaran-ssr-placeholder", "DataGrid"},
		{"data-fuaran-row-count", strconv.Itoa(count)},
	}, "[Grid: "+strconv.Itoa(count)+" rows "+emDash+" hydrates client-side]")
}

// chart is the go host's Chart arm. Chart-lowering posture (Phase 551):
// fuaran-go is REQUIRE-PRE-LOWERED. A raw Chart node reaching this headless SSR
// boundary is NOT lowered in-host to a Drawing; it renders a documented typed
// passthrough — a marked client-hydration placeholder (a fuaran-chart-ssr-placeholder
// div carrying data-fuaran-ssr-placeholder="Chart" + a data-fuaran-row-count and a
// visible "[Chart: N rows — hydrates client-side]" fallback), never a silent empty
// region. The contract: a go SSR consumer that wants a rendered chart pre-lowers the
// Chart to a Drawing (which this renderer DOES lower — see drawing()), or a conformant
// client renders the emitted wire. This is the cheap posture, justified by the host's
// headless-orchestrator role: go emits static output but paints no client-library
// visualisation in-host (a chart hydrates client-side), so in-host Chart→Drawing
// lowering earns no rendered pixel here (unlike the fuaran-rs WASM client, which
// lowers in-host so its browser renderer reaches chart parity). Demand-gated per the
// phase: revisit if a go SSR consumer needs in-host lowering. Pinned by
// TestChartRequiresPreLoweredPosture in render_test.go.
func (r *renderer) chart(fields map[string]wire.Value) string {
	count := seqLen(resolveSource(fields["source"], r.sources))
	titleHTML := ""
	if title, ok := fields["title"]; ok {
		titleHTML = textElement("div", []attr{{"class", "fuaran-chart-title"}}, r.text(title))
	}
	body := element("div", []attr{{"class", "fuaran-chart-placeholder"}},
		escapeText("[Chart: "+strconv.Itoa(count)+" rows "+emDash+" hydrates client-side]"))
	return element("div", []attr{
		{"class", "fuaran-chart fuaran-chart-ssr-placeholder"},
		{"data-fuaran-ssr-placeholder", "Chart"},
		{"data-fuaran-row-count", strconv.Itoa(count)},
	}, titleHTML+body)
}

func (r *renderer) mapVis(fields map[string]wire.Value) string {
	count := seqLen(resolveSource(fields["source"], r.sources))
	return textElement("div", []attr{
		{"class", "fuaran-map fuaran-map-ssr-placeholder"},
		{"data-fuaran-ssr-placeholder", "Map"},
		{"data-fuaran-marker-count", strconv.Itoa(count)},
	}, "[Map: "+strconv.Itoa(count)+" markers "+emDash+" hydrates client-side]")
}

// ── Structural ──────────────────────────────────────────────────────────────

// switchKind resolves the initial state value at stateKey from the host
// sources and renders the first case whose match equals its string form
// (first-match-wins), else the default. Server + client first render read the
// same initial state → hydration parity.
func (r *renderer) switchKind(fields map[string]wire.Value) string {
	valueStr := ""
	if key, ok := fields["stateKey"].(wire.Str); ok {
		if current, found := r.sources[string(key)]; found {
			valueStr = displayString(current)
		}
	}
	if cases, ok := fields["cases"].(wire.Arr); ok {
		for _, item := range cases {
			caseObj, ok := item.(wire.Obj)
			if !ok {
				continue
			}
			if match, ok := caseObj.Fields["match"].(wire.Str); ok && string(match) == valueStr {
				if child, ok := asNode(caseObj.Fields["child"]); ok {
					return r.renderNode(child)
				}
			}
		}
	}
	if def, ok := asNode(fields["default"]); ok {
		return r.renderNode(def)
	}
	return ""
}

func (r *renderer) fragmentRef(fields map[string]wire.Value) string {
	name, ok := fields["name"].(wire.Str)
	if !ok {
		return ""
	}
	if body, found := r.fragments[string(name)]; found {
		return r.renderNode(body)
	}
	return textElement("div", []attr{
		{"class", "fuaran-fragment-unresolved-placeholder"},
		{"data-fuaran-fragment-unresolved", string(name)},
	}, "[fuaran:fragment unresolved '"+string(name)+"']")
}

func (r *renderer) custom(fields map[string]wire.Value) string {
	moduleID := strValue(fields["moduleId"])
	componentID := strValue(fields["componentId"])
	return textElement("div", []attr{
		{"class", "fuaran-kind-custom-placeholder fuaran-custom-" + moduleID + "-" + componentID},
		{"data-fuaran-custom-module", moduleID},
		{"data-fuaran-custom-component", componentID},
	}, "[fuaran:custom "+moduleID+"."+componentID+"]")
}

// RenderHTML renders a decoded Node tree to a body-fragment HTML string.
// Sources is an optional host-supplied binding map (binding key → value) used
// to resolve non-Static bindings; the headless baseline works with nil,
// resolving Static bindings and placeholdering the rest.
func RenderHTML(node wire.Node, sources BindingSources) string {
	fragments := make(map[string]wire.Node)
	collectFragments(node, fragments)
	r := &renderer{sources: sources, fragments: fragments}
	return r.renderNode(node)
}
