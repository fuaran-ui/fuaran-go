package renderer

import (
	"math"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Drawing (Phase 525) — first-party inline SVG for the headless host. The Go
// port of the canonical F# `Fuaran.UI.Renderer.Core.DrawingSvg` builder (mirrored
// by the TS / Python hosts): static geometry lowered to inline `<svg>` on the
// server — same path `d`, coordinate/number form, XML escaping, open-shape
// `fill="none"` defaults, and `role="img"` + optional `<title>`/`<desc>` (a11y).
// The class vocabulary (`fuaran-drawing*`) is parity-locked to the reference
// renderer. A Drawing is a resolved artefact: geometry is static numbers; only
// DrawStyle carries Bindings, resolved through the host sources.

// drawNum is the SVG coordinate number form: whole values drop the decimal
// (`10`), else the shortest round-trip (`1.5`); non-finite → `0`. This is the
// SVG form (DrawingSvg.formatNum), distinct from the canonical-JSON float layout.
func drawNum(v wire.Value) string {
	f, ok := numericValue(v)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	if f == math.Floor(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func drawEscape(s string) string {
	// XML-escape (the SVG string rides the innerHTML seam). `&` first.
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

func drawPoints(points wire.Value) string {
	arr, ok := points.(wire.Arr)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, p := range arr {
		if pf, ok := p.(wire.Obj); ok {
			parts = append(parts, drawNum(pf.Fields["x"])+","+drawNum(pf.Fields["y"]))
		}
	}
	return strings.Join(parts, " ")
}

// drawPathD lowers the typed CurveCommand list to an SVG path `d` string.
func drawPathD(commands wire.Value) string {
	arr, ok := commands.(wire.Arr)
	if !ok {
		return ""
	}
	pt := func(cf map[string]wire.Value, key string) string {
		p, _ := cf[key].(wire.Obj)
		return drawNum(p.Fields["x"]) + " " + drawNum(p.Fields["y"])
	}
	parts := make([]string, 0, len(arr))
	for _, c := range arr {
		cmd, ok := c.(wire.Obj)
		if !ok {
			continue
		}
		cf := cmd.Fields
		switch cmd.Tag {
		case "MoveTo":
			parts = append(parts, "M"+pt(cf, "to"))
		case "LineTo":
			parts = append(parts, "L"+pt(cf, "to"))
		case "CubicTo":
			parts = append(parts, "C"+pt(cf, "control1")+" "+pt(cf, "control2")+" "+pt(cf, "to"))
		case "QuadraticTo":
			parts = append(parts, "Q"+pt(cf, "control")+" "+pt(cf, "to"))
		case "Close":
			parts = append(parts, "Z")
		}
	}
	return strings.Join(parts, " ")
}

// drawStrokeJoinAttrs emits round line joins + caps for a STROKED path shape
// (Polyline / Polygon / Curve). A RENDERER default, not a wire field: DrawStyle
// gains nothing and no fixture changes shape, so this is a per-host emitter
// obligation rather than a corpus event. SVG's initial stroke-linejoin is
// miter, which spikes at the acute vertices a data polyline routinely has — a
// visible artefact carrying no data.
//
// Emitted only when the shape actually strokes, so a fill-only polygon (a chart
// area band) keeps its minimal attribute set. Line is deliberately excluded: a
// round cap on the axis and gridline rules would overhang each end by half the
// stroke width, lengthening chrome that is positioned exactly.
func (r *renderer) drawStrokeJoinAttrs(style wire.Value) string {
	obj, _ := style.(wire.Obj)
	if v, ok := obj.Fields["stroke"]; ok {
		if resolved := resolveBinding(v, r.sources); resolved != nil {
			return ` stroke-linejoin="round" stroke-linecap="round"`
		}
	}
	return ""
}

// drawStyleAttrs lowers a DrawStyle to SVG presentation attributes in the fixed
// reference order: fill, opacity, stroke, stroke-width, then the text-only attrs
// (Label). Bindings resolve through the host sources; each attribute is emitted
// only when present. Polyline / Curve pass defaultFillNone (open shapes get
// fill="none" when unstyled).
func (r *renderer) drawStyleAttrs(style wire.Value, defaultFillNone bool) string {
	obj, _ := style.(wire.Obj)
	f := obj.Fields
	var b strings.Builder

	if v, ok := f["fill"]; ok {
		if resolved := resolveBinding(v, r.sources); resolved != nil {
			b.WriteString(` fill="` + drawEscape(displayString(resolved)) + `"`)
		}
	} else if defaultFillNone {
		b.WriteString(` fill="none"`)
	}
	if v, ok := f["opacity"]; ok {
		if resolved := resolveBinding(v, r.sources); resolved != nil {
			b.WriteString(` opacity="` + drawNum(resolved) + `"`)
		}
	}
	if v, ok := f["stroke"]; ok {
		if resolved := resolveBinding(v, r.sources); resolved != nil {
			b.WriteString(` stroke="` + drawEscape(displayString(resolved)) + `"`)
		}
	}
	if v, ok := f["strokeWidth"]; ok {
		if resolved := resolveBinding(v, r.sources); resolved != nil {
			b.WriteString(` stroke-width="` + drawNum(resolved) + `"`)
		}
	}
	// Text-only attrs (Label, Phase 528.1): bare enum / string / number.
	if v, ok := f["textAnchor"]; ok {
		anchor := map[string]string{"Start": "start", "Middle": "middle", "End": "end"}[strValue(v)]
		if anchor == "" {
			anchor = "start"
		}
		b.WriteString(` text-anchor="` + anchor + `"`)
	}
	if v, ok := f["fontFamily"]; ok {
		b.WriteString(` font-family="` + drawEscape(strValue(v)) + `"`)
	}
	if v, ok := f["fontSize"]; ok {
		b.WriteString(` font-size="` + drawNum(v) + `px"`)
	}
	if v, ok := f["emphasis"]; ok {
		weight := map[string]string{"Quiet": "300", "Normal": "400", "Loud": "700"}[strValue(v)]
		if weight == "" {
			weight = "400"
		}
		b.WriteString(` font-weight="` + weight + `"`)
	}
	// Phase 642 — keyed mark identity: a data-bearing shape's derivation-based
	// id rides into the emitted SVG so marks are addressable (object
	// constancy) — last in the fixed attribute order, matching every other host.
	if v, ok := f["markId"]; ok {
		b.WriteString(` data-fuaran-mark="` + drawEscape(strValue(v)) + `"`)
	}
	return b.String()
}

// drawTipChild renders DrawStyle.tip (Phase 883) as an SVG <title> CHILD of the
// shape's own element: the native browser tooltip and the element's accessible
// name, with no script, so this host's server-emitted page carries the readout
// too. <title> must be the FIRST child to be the accessible name, which is why
// every arm below emits it ahead of any other content.
//
// A tip is the one DrawStyle field honoured on EVERY shape rather than only on
// Label — the marks a reader hovers are bars, wedges and points, and a <title>
// is inert geometry-wise on all of them (unlike rotation, whose off-Label
// emission would MOVE GEOMETRY).
//
// The text is XML-escaped through the same drawEscape the label text and the
// drawing title/desc already use: this builder emits raw markup, so the escape
// is the whole defence, and a chart lowering feeds it UNTRUSTED series and
// category strings straight off the data feed.
func (r *renderer) drawTipChild(style wire.Value) string {
	sf, ok := style.(wire.Obj)
	if !ok {
		return ""
	}
	t, ok := sf.Fields["tip"]
	if !ok {
		return ""
	}
	return "<title>" + drawEscape(r.text(t)) + "</title>"
}

// drawClose is the tail of a shape element carrying no child content of its
// own: self-closing when untipped (byte-unchanged from every pre-883 drawing),
// an open/close pair wrapping the <title> when tipped.
func (r *renderer) drawClose(style wire.Value, element string) string {
	sf, ok := style.(wire.Obj)
	if !ok {
		return "/>"
	}
	if _, ok := sf.Fields["tip"]; !ok {
		return "/>"
	}
	return ">" + r.drawTipChild(style) + "</" + element + ">"
}

func (r *renderer) drawShape(sh wire.Value) string {
	shape, ok := sh.(wire.Obj)
	if !ok {
		return ""
	}
	f := shape.Fields
	style := f["style"]
	switch shape.Tag {
	case "Group":
		var inner strings.Builder
		if children, ok := f["children"].(wire.Arr); ok {
			for _, c := range children {
				inner.WriteString(r.drawShape(c))
			}
		}
		return `<g class="fuaran-drawing-group"` + r.drawStyleAttrs(style, false) + `>` +
			r.drawTipChild(style) + inner.String() + `</g>`
	case "Rectangle":
		rx := ""
		if cr, ok := f["cornerRadius"]; ok {
			rx = ` rx="` + drawNum(cr) + `"`
		}
		return `<rect class="fuaran-drawing-rect" x="` + drawNum(f["x"]) + `" y="` + drawNum(f["y"]) +
			`" width="` + drawNum(f["width"]) + `" height="` + drawNum(f["height"]) + `"` + rx +
			r.drawStyleAttrs(style, false) + r.drawClose(style, "rect")
	case "Line":
		return `<line class="fuaran-drawing-line" x1="` + drawNum(f["x1"]) + `" y1="` + drawNum(f["y1"]) +
			`" x2="` + drawNum(f["x2"]) + `" y2="` + drawNum(f["y2"]) + `"` +
			r.drawStyleAttrs(style, false) + r.drawClose(style, "line")
	case "Polyline":
		return `<polyline class="fuaran-drawing-polyline" points="` + drawPoints(f["points"]) + `"` +
			r.drawStyleAttrs(style, true) + r.drawStrokeJoinAttrs(style) + r.drawClose(style, "polyline")
	case "Polygon":
		return `<polygon class="fuaran-drawing-polygon" points="` + drawPoints(f["points"]) + `"` +
			r.drawStyleAttrs(style, false) + r.drawStrokeJoinAttrs(style) + r.drawClose(style, "polygon")
	case "Curve":
		return `<path class="fuaran-drawing-curve" d="` + drawPathD(f["commands"]) + `"` +
			r.drawStyleAttrs(style, true) + r.drawStrokeJoinAttrs(style) + r.drawClose(style, "path")
	case "Circle":
		return `<circle class="fuaran-drawing-circle" cx="` + drawNum(f["cx"]) + `" cy="` + drawNum(f["cy"]) +
			`" r="` + drawNum(f["r"]) + `"` + r.drawStyleAttrs(style, false) + r.drawClose(style, "circle")
	case "Ellipse":
		return `<ellipse class="fuaran-drawing-ellipse" cx="` + drawNum(f["cx"]) + `" cy="` + drawNum(f["cy"]) +
			`" rx="` + drawNum(f["rx"]) + `" ry="` + drawNum(f["ry"]) + `"` +
			r.drawStyleAttrs(style, false) + r.drawClose(style, "ellipse")
	case "Label":
		// rotation (Phase 877) — emitted here rather than in drawStyleAttrs
		// because the pivot is the label's own anchor point, which the style
		// value does not carry; drawStyleAttrs is shared by every shape and
		// stays position-free. Anchoring at (x, y) is what makes the rotation
		// compose with textAnchor. Deliberately never emitted off Label: an SVG
		// transform on a <rect> would MOVE GEOMETRY rather than be inert as the
		// other text-only fields are.
		rot := ""
		if sf, ok := style.(wire.Obj); ok {
			if v, ok := sf.Fields["rotation"]; ok {
				rot = ` transform="rotate(` + drawNum(v) + ` ` + drawNum(f["x"]) + ` ` + drawNum(f["y"]) + `)"`
			}
		}
		return `<text class="fuaran-drawing-label" x="` + drawNum(f["x"]) + `" y="` + drawNum(f["y"]) + `"` +
			rot + r.drawStyleAttrs(style, false) + `>` +
			// The tip precedes the visible run — <title> is the accessible name
			// only as the FIRST child.
			r.drawTipChild(style) + drawEscape(r.text(f["text"])) + `</text>`
	}
	return ""
}

// terminateTitle (Phase 921) ends a title with "." unless it already ends in
// sentence punctuation, so the composed accessible name reads as two sentences
// rather than one run-on.
func terminateTitle(t string) string {
	if t == "" {
		return t
	}
	switch t[len(t)-1] {
	case '.', '!', '?':
		return t
	default:
		return t + "."
	}
}

// rootAriaLabel (Phase 921) is the drawing root's ANNOUNCED accessible name, or
// "" when the root emits no aria-label.
//
// role="img" presents the drawing as ONE graphic and does not traverse into it,
// and <desc> is not uniformly mapped to the accessible description (Chromium has
// never exposed it) — so the value the markup has carried since the builder
// landed is one a reader cannot reach. aria-label is the accessible NAME, which
// every assistive technology announces unconditionally for a role="img" element.
//
// NOT aria-labelledby / aria-describedby: both reference elements BY ID, and this
// builder has no id to give — its whole input is the drawing's own fields,
// several drawings routinely share one document, and any minted id would have to
// be both unique per page and byte-identical across five hosts.
//
// Emitted ONLY when a description is present, so every drawing predating this
// phase is byte-identical.
func (r *renderer) rootAriaLabel(fields map[string]wire.Value) string {
	d, ok := fields["description"]
	if !ok {
		return ""
	}
	composed := r.text(d)
	if t, ok := fields["title"]; ok {
		if titleText := terminateTitle(r.text(t)); titleText != "" {
			composed = titleText + " " + composed
		}
	}
	return ` aria-label="` + drawEscape(composed) + `"`
}

// drawing renders a Drawing kind as inline SVG (role="img" + optional title/desc,
// plus the Phase 921 aria-label that gets the description ANNOUNCED).
func (r *renderer) drawing(fields map[string]wire.Value) string {
	return element("div", nil, r.drawingSVG(fields))
}

// drawingSVG builds the `<svg class="fuaran-drawing">` string itself, without
// the wrapper element. Split out at Phase 1099 so the `Sparkline` lowering can
// reuse the ONE builder while supplying its own hook wrapper: the reference
// stylesheet's `.fuaran-sparkline > .fuaran-drawing` rule is a DIRECT-child
// selector, so a sparkline that went through `drawing` above would gain a second
// div and lose its sizing. Same bytes, different container — which is the whole
// point of lowering through a shared builder rather than beside one.
func (r *renderer) drawingSVG(fields map[string]wire.Value) string {
	vb, _ := fields["viewBox"].(wire.Obj)
	viewBox := drawNum(vb.Fields["minX"]) + " " + drawNum(vb.Fields["minY"]) + " " +
		drawNum(vb.Fields["width"]) + " " + drawNum(vb.Fields["height"])

	title := ""
	if t, ok := fields["title"]; ok {
		title = "<title>" + drawEscape(r.text(t)) + "</title>"
	}
	desc := ""
	if d, ok := fields["description"]; ok {
		desc = "<desc>" + drawEscape(r.text(d)) + "</desc>"
	}
	var body strings.Builder
	if shapes, ok := fields["shapes"].(wire.Arr); ok {
		for _, s := range shapes {
			body.WriteString(r.drawShape(s))
		}
	}
	rootStyle := r.drawStyleAttrs(fields["style"], false)
	aria := r.rootAriaLabel(fields)
	return `<svg class="fuaran-drawing" role="img" viewBox="` + viewBox + `"` + aria + rootStyle + `>` +
		title + desc + body.String() + `</svg>`
}
