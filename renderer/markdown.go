package renderer

import (
	"strconv"
	"strings"
)

// Deterministic GFM markdown → HTML renderer, stdlib only — a faithful port
// of the reference renderer, byte-identical to the sibling hosts and verified
// against the shared corpus at wire-format-fixtures/markdown/corpus.json.
//
// The three feature buckets: IN — CommonMark core + GFM tables /
// strikethrough / task lists / bare-URL autolinks. OUT — raw/inline HTML is
// escaped (no passthrough); math + Mermaid are client-only passes. DEFERRED —
// emoji / footnotes / heading anchors / the full named-entity table render
// escaped-literal until added.
//
// Cross-host parity primitives: explicit ASCII whitespace / digit / punct
// classes (never the host language's Unicode classifiers — their sets differ
// at the edges). Byte-wise iteration is safe: every branch character is ASCII
// and multi-byte UTF-8 bytes classify as "other" on every host.

func mdIsWS(c byte) bool {
	return c == ' ' || (c >= 9 && c <= 13)
}

func mdIsDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func mdIsASCIIPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

// mdEscape matches the reference escape set exactly: & < > " (and NOT ').
func mdEscape(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// ── Entity decoding (common subset; the rest is DEFERRED) ───────────────────

var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " ",
	"copy": "©", "reg": "®", "trade": "™", "hellip": "…", "mdash": "—", "ndash": "–",
	"lsquo": "‘", "rsquo": "’", "ldquo": "“", "rdquo": "”",
	"deg": "°", "plusmn": "±", "times": "×", "divide": "÷",
	"frac12": "½", "frac14": "¼", "frac34": "¾", "sup2": "²", "sup3": "³",
	"middot": "·", "bull": "•", "dagger": "†",
	"euro": "€", "pound": "£", "cent": "¢", "yen": "¥", "sect": "§", "para": "¶",
}

// tryDecodeEntity decodes an entity at text[i] ('&'), returning (chars, next, ok).
func tryDecodeEntity(text string, i int) (string, int, bool) {
	semi := strings.IndexByte(text[i:], ';')
	if semi < 0 {
		return "", 0, false
	}
	semi += i
	if semi == i+1 {
		return "", 0, false
	}
	body := text[i+1 : semi]
	if strings.HasPrefix(body, "#") {
		isHex := len(body) > 1 && (body[1] == 'x' || body[1] == 'X')
		digits := body[1:]
		if isHex {
			digits = body[2:]
		}
		if digits == "" {
			return "", 0, false
		}
		base := 10
		if isHex {
			base = 16
		}
		code, err := strconv.ParseInt(digits, base, 64)
		if err != nil || code < 0 {
			return "", 0, false
		}
		cp := rune(code)
		if code == 0 || code > 0x10FFFF {
			cp = 0xFFFD
		}
		return string(cp), semi + 1, true
	}
	if s, ok := namedEntities[body]; ok {
		return s, semi + 1, true
	}
	return "", 0, false
}

// ── Inline AST ──────────────────────────────────────────────────────────────

type inline struct {
	kind string // "text" | "raw" | "emph" | "strong" | "strike" | "soft" | "hard"
	text string
	kids []inline
}

type refDef struct {
	url      string
	title    string
	hasTitle bool
}

// normLabel trims, collapses internal whitespace, and lowercases (the
// CommonMark reference-label match).
func normLabel(s string) string {
	var sb strings.Builder
	inWS := false
	for _, r := range strings.TrimSpace(s) {
		if r < 128 && mdIsWS(byte(r)) {
			inWS = true
			continue
		}
		if inWS {
			sb.WriteByte(' ')
		}
		inWS = false
		sb.WriteString(strings.ToLower(string(r)))
	}
	return sb.String()
}

func scanCodeSpan(text string, i int) (inline, int, bool) {
	n := len(text)
	j := i
	for j < n && text[j] == '`' {
		j++
	}
	openLen := j - i
	k := j
	closeStart := -1
	for k < n && closeStart < 0 {
		if text[k] == '`' {
			m := k
			for m < n && text[m] == '`' {
				m++
			}
			if m-k == openLen {
				closeStart = k
			}
			k = m
		} else {
			k++
		}
	}
	if closeStart < 0 {
		return inline{}, 0, false
	}
	raw := text[j:closeStart]
	collapsed := strings.ReplaceAll(raw, "\r\n", " ")
	collapsed = strings.ReplaceAll(collapsed, "\n", " ")
	collapsed = strings.ReplaceAll(collapsed, "\r", " ")
	if len(collapsed) >= 2 && collapsed[0] == ' ' && collapsed[len(collapsed)-1] == ' ' && strings.TrimSpace(collapsed) != "" {
		collapsed = collapsed[1 : len(collapsed)-1]
	}
	return inline{kind: "raw", text: "<code>" + mdEscape(collapsed) + "</code>"}, closeStart + openLen, true
}

func isSchemeChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-'
}

func scanAutolink(text string, i int) (inline, int, bool) {
	close := strings.IndexByte(text[i:], '>')
	if close < 0 {
		return inline{}, 0, false
	}
	close += i
	body := text[i+1 : close]
	if body == "" || strings.ContainsAny(body, " <") {
		return inline{}, 0, false
	}
	colon := strings.IndexByte(body, ':')
	looksURI := colon >= 2 && colon <= 32 &&
		((body[0] >= 'a' && body[0] <= 'z') || (body[0] >= 'A' && body[0] <= 'Z'))
	if looksURI {
		for k := 0; k < colon; k++ {
			if !isSchemeChar(body[k]) {
				looksURI = false
				break
			}
		}
	}
	looksEmail := !looksURI && strings.Contains(body, "@") && !strings.Contains(body, ":") && strings.IndexByte(body, '@') > 0
	if looksURI {
		safe := SanitizeURLOrBlank(body)
		return inline{kind: "raw", text: `<a href="` + mdEscape(safe) + `">` + mdEscape(body) + "</a>"}, close + 1, true
	}
	if looksEmail {
		return inline{kind: "raw", text: `<a href="mailto:` + mdEscape(body) + `">` + mdEscape(body) + "</a>"}, close + 1, true
	}
	return inline{}, 0, false
}

// scanInlineDestination scans the "(url "title")" tail of an inline link.
func scanInlineDestination(text string, start int) (url, title string, hasTitle bool, next int, ok bool) {
	n := len(text)
	i := start
	for i < n && (text[i] == ' ' || text[i] == '\n' || text[i] == '\t') {
		i++
	}
	parseOK := true
	if i < n && text[i] == '<' {
		closeIdx := strings.IndexByte(text[i:], '>')
		if closeIdx < 0 {
			parseOK = false
		} else {
			url = text[i+1 : i+closeIdx]
			i += closeIdx + 1
		}
	} else {
		depth := 0
		var parts strings.Builder
		for go_ := true; go_ && i < n; {
			c := text[i]
			switch {
			case c == ' ' || c == '\t' || c == '\n':
				go_ = false
			case c == '(':
				depth++
				parts.WriteByte(c)
				i++
			case c == ')':
				if depth == 0 {
					go_ = false
				} else {
					depth--
					parts.WriteByte(c)
					i++
				}
			case c == '\\' && i+1 < n && mdIsASCIIPunct(text[i+1]):
				parts.WriteByte(text[i+1])
				i += 2
			default:
				parts.WriteByte(c)
				i++
			}
		}
		url = parts.String()
	}
	if parseOK {
		j := i
		for j < n && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n') {
			j++
		}
		if j < n && (text[j] == '"' || text[j] == '\'') {
			q := text[j]
			tClose := strings.IndexByte(text[j+1:], q)
			if tClose >= 0 {
				title = text[j+1 : j+1+tClose]
				hasTitle = true
				i = j + 1 + tClose + 1
				for i < n && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n') {
					i++
				}
			}
		}
	}
	if !parseOK {
		return "", "", false, 0, false
	}
	if i < n && text[i] == ')' {
		return url, title, hasTitle, i + 1, true
	}
	return "", "", false, 0, false
}

func matchBracket(text string, open0 int) int {
	n := len(text)
	i := open0 + 1
	depth := 1
	for i < n {
		c := text[i]
		switch {
		case c == '\\' && i+1 < n:
			i += 2
		case c == '`':
			if _, next, ok := scanCodeSpan(text, i); ok {
				i = next
			} else {
				i++
			}
		case c == '[':
			depth++
			i++
		case c == ']':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return -1
}

func renderInlines(nodes []inline) string {
	var sb strings.Builder
	for _, node := range nodes {
		switch node.kind {
		case "text":
			sb.WriteString(mdEscape(node.text))
		case "raw":
			sb.WriteString(node.text)
		case "emph":
			sb.WriteString("<em>" + renderInlines(node.kids) + "</em>")
		case "strong":
			sb.WriteString("<strong>" + renderInlines(node.kids) + "</strong>")
		case "strike":
			sb.WriteString("<del>" + renderInlines(node.kids) + "</del>")
		case "soft":
			sb.WriteString("\n")
		case "hard":
			sb.WriteString("<br />\n")
		}
	}
	return sb.String()
}

func plainText(nodes []inline) string {
	var sb strings.Builder
	for _, node := range nodes {
		switch node.kind {
		case "text":
			sb.WriteString(node.text)
		case "emph", "strong", "strike":
			sb.WriteString(plainText(node.kids))
		case "soft", "hard":
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

func scanBareAutolink(text string, i int) (inline, int, bool) {
	n := len(text)
	starts := func(p string) bool { return strings.HasPrefix(text[i:], p) }
	if !starts("https://") && !starts("http://") && !starts("www.") {
		return inline{}, 0, false
	}
	j := i
	for j < n && !mdIsWS(text[j]) && text[j] != '<' {
		j++
	}
	for j > i && strings.IndexByte(`.,;:!?)"'`, text[j-1]) >= 0 {
		j--
	}
	if j <= i+4 {
		return inline{}, 0, false
	}
	raw := text[i:j]
	href := raw
	if strings.HasPrefix(raw, "www.") {
		href = "http://" + raw
	}
	safe := SanitizeURLOrBlank(href)
	return inline{kind: "raw", text: `<a href="` + mdEscape(safe) + `">` + mdEscape(raw) + "</a>"}, j, true
}

// ── Tokenizer + emphasis resolution ─────────────────────────────────────────

type mdToken struct {
	isDelim  bool
	node     inline
	ch       byte
	count    int
	canOpen  bool
	canClose bool
	active   bool
}

func nodeToken(node inline) *mdToken { return &mdToken{node: node} }

func tokenize(refs map[string]refDef, text string) []*mdToken {
	var toks []*mdToken
	n := len(text)
	i := 0
	var pending strings.Builder

	flush := func() {
		if pending.Len() > 0 {
			toks = append(toks, nodeToken(inline{kind: "text", text: pending.String()}))
			pending.Reset()
		}
	}
	prevChar := func() byte {
		if i == 0 {
			return ' '
		}
		return text[i-1]
	}
	makeImage := func(labelText, url, title string, hasTitle bool) {
		flush()
		alt := plainText(parseInlines(refs, labelText))
		safe := SanitizeURLOrBlank(url)
		titleAttr := ""
		if hasTitle {
			titleAttr = ` title="` + mdEscape(title) + `"`
		}
		toks = append(toks, nodeToken(inline{kind: "raw",
			text: `<img src="` + mdEscape(safe) + `" alt="` + mdEscape(alt) + `"` + titleAttr + " />"}))
	}
	makeLink := func(labelText, url, title string, hasTitle bool) {
		flush()
		inner := renderInlines(parseInlines(refs, labelText))
		safe := SanitizeURLOrBlank(url)
		titleAttr := ""
		if hasTitle {
			titleAttr = ` title="` + mdEscape(title) + `"`
		}
		toks = append(toks, nodeToken(inline{kind: "raw",
			text: `<a href="` + mdEscape(safe) + `"` + titleAttr + ">" + inner + "</a>"}))
	}

	for i < n {
		c := text[i]
		switch {
		case c == '\\' && i+1 < n && text[i+1] == '\n':
			flush()
			toks = append(toks, nodeToken(inline{kind: "hard"}))
			i += 2
		case c == '\\' && i+1 < n && mdIsASCIIPunct(text[i+1]):
			pending.WriteByte(text[i+1])
			i += 2
		case c == '`':
			if node, next, ok := scanCodeSpan(text, i); ok {
				flush()
				toks = append(toks, nodeToken(node))
				i = next
			} else {
				pending.WriteByte(c)
				i++
			}
		case c == '&':
			if chars, next, ok := tryDecodeEntity(text, i); ok {
				pending.WriteString(chars)
				i = next
			} else {
				pending.WriteByte(c)
				i++
			}
		case c == '<':
			if node, next, ok := scanAutolink(text, i); ok {
				flush()
				toks = append(toks, nodeToken(node))
				i = next
			} else {
				pending.WriteByte(c)
				i++
			}
		case c == '!' && i+1 < n && text[i+1] == '[':
			closeIdx := matchBracket(text, i+1)
			if closeIdx == -1 {
				pending.WriteByte(c)
				i++
				break
			}
			labelText := text[i+2 : closeIdx]
			if closeIdx+1 < n && text[closeIdx+1] == '(' {
				if url, title, hasTitle, next, ok := scanInlineDestination(text, closeIdx+2); ok {
					makeImage(labelText, url, title, hasTitle)
					i = next
				} else {
					pending.WriteByte(c)
					i++
				}
				break
			}
			refLabel, consumedTo := labelText, closeIdx+1
			if closeIdx+1 < n && text[closeIdx+1] == '[' {
				if r2 := matchBracket(text, closeIdx+1); r2 > 0 {
					inner := text[closeIdx+2 : r2]
					if strings.TrimSpace(inner) != "" {
						refLabel = inner
					}
					consumedTo = r2 + 1
				}
			}
			if found, ok := refs[normLabel(refLabel)]; ok {
				makeImage(labelText, found.url, found.title, found.hasTitle)
				i = consumedTo
			} else {
				pending.WriteByte(c)
				i++
			}
		case c == '[':
			closeIdx := matchBracket(text, i)
			if closeIdx == -1 {
				pending.WriteByte(c)
				i++
				break
			}
			labelText := text[i+1 : closeIdx]
			if closeIdx+1 < n && text[closeIdx+1] == '(' {
				if url, title, hasTitle, next, ok := scanInlineDestination(text, closeIdx+2); ok {
					makeLink(labelText, url, title, hasTitle)
					i = next
				} else {
					pending.WriteByte(c)
					i++
				}
				break
			}
			refLabel, consumedTo := labelText, closeIdx+1
			if closeIdx+1 < n && text[closeIdx+1] == '[' {
				if r2 := matchBracket(text, closeIdx+1); r2 > 0 {
					inner := text[closeIdx+2 : r2]
					if strings.TrimSpace(inner) != "" {
						refLabel = inner
					}
					consumedTo = r2 + 1
				}
			}
			if found, ok := refs[normLabel(refLabel)]; ok {
				makeLink(labelText, found.url, found.title, found.hasTitle)
				i = consumedTo
			} else {
				pending.WriteByte(c)
				i++
			}
		case c == '*' || c == '_' || c == '~':
			j := i
			for j < n && text[j] == c {
				j++
			}
			runLen := j - i
			before := prevChar()
			after := byte(' ')
			if j < n {
				after = text[j]
			}
			beforeWS := mdIsWS(before)
			afterWS := mdIsWS(after)
			beforePunct := mdIsASCIIPunct(before)
			afterPunct := mdIsASCIIPunct(after)
			leftFlank := !afterWS && (!afterPunct || beforeWS || beforePunct)
			rightFlank := !beforeWS && (!beforePunct || afterWS || afterPunct)
			var canOpen, canClose bool
			if c == '_' {
				canOpen = leftFlank && (!rightFlank || beforePunct)
				canClose = rightFlank && (!leftFlank || afterPunct)
			} else {
				canOpen = leftFlank
				canClose = rightFlank
			}
			flush()
			if c == '~' && runLen != 2 {
				toks = append(toks, nodeToken(inline{kind: "text", text: strings.Repeat(string(c), runLen)}))
			} else {
				toks = append(toks, &mdToken{
					isDelim: true, ch: c, count: runLen,
					canOpen: canOpen, canClose: canClose, active: true,
				})
			}
			i = j
		case c == '\n':
			s := pending.String()
			trimmedEnd := strings.TrimRight(s, " ")
			hard := len(s)-len(trimmedEnd) >= 2
			pending.Reset()
			pending.WriteString(trimmedEnd)
			flush()
			if hard {
				toks = append(toks, nodeToken(inline{kind: "hard"}))
			} else {
				toks = append(toks, nodeToken(inline{kind: "soft"}))
			}
			i++
		case (c == 'h' || c == 'w') && (i == 0 || mdIsWS(prevChar()) || strings.IndexByte("(*_~", prevChar()) >= 0):
			if node, next, ok := scanBareAutolink(text, i); ok {
				flush()
				toks = append(toks, nodeToken(node))
				i = next
			} else {
				pending.WriteByte(c)
				i++
			}
		default:
			pending.WriteByte(c)
			i++
		}
	}
	flush()
	return toks
}

func processEmphasis(toks []*mdToken) []inline {
	closerIdx := 0
	for closerIdx < len(toks) {
		closer := toks[closerIdx]
		if !(closer.isDelim && closer.canClose && closer.active && closer.count > 0) {
			closerIdx++
			continue
		}
		found := -1
		for openerIdx := closerIdx - 1; openerIdx >= 0; openerIdx-- {
			o := toks[openerIdx]
			if !(o.isDelim && o.ch == closer.ch && o.canOpen && o.active && o.count > 0) {
				continue
			}
			sumOK := true
			if (o.canClose || closer.canOpen) && closer.ch != '~' {
				sumOK = (o.count+closer.count)%3 != 0 || (o.count%3 == 0 && closer.count%3 == 0)
			}
			if sumOK {
				found = openerIdx
				break
			}
		}
		if found < 0 {
			if !closer.canOpen {
				closer.active = false
				toks[closerIdx] = nodeToken(inline{kind: "text", text: strings.Repeat(string(closer.ch), closer.count)})
			}
			closerIdx++
			continue
		}
		opener := toks[found]
		useCount := 1
		if closer.ch == '~' || (opener.count >= 2 && closer.count >= 2) {
			useCount = 2
		}
		var inner []inline
		for k := found + 1; k < closerIdx; k++ {
			tk := toks[k]
			if !tk.isDelim {
				inner = append(inner, tk.node)
			} else if tk.count > 0 {
				inner = append(inner, inline{kind: "text", text: strings.Repeat(string(tk.ch), tk.count)})
			}
		}
		var wrapped inline
		switch {
		case closer.ch == '~':
			wrapped = inline{kind: "strike", kids: inner}
		case useCount == 2:
			wrapped = inline{kind: "strong", kids: inner}
		default:
			wrapped = inline{kind: "emph", kids: inner}
		}
		opener.count -= useCount
		closer.count -= useCount
		rebuilt := make([]*mdToken, 0, len(toks))
		rebuilt = append(rebuilt, toks[:found]...)
		if opener.count > 0 {
			rebuilt = append(rebuilt, nodeToken(inline{kind: "text", text: strings.Repeat(string(opener.ch), opener.count)}))
		}
		rebuilt = append(rebuilt, nodeToken(wrapped))
		if closer.count > 0 {
			rebuilt = append(rebuilt, nodeToken(inline{kind: "text", text: strings.Repeat(string(closer.ch), closer.count)}))
		}
		rebuilt = append(rebuilt, toks[closerIdx+1:]...)
		toks = rebuilt
		closerIdx = found
	}

	var result []inline
	for _, t := range toks {
		if !t.isDelim {
			result = append(result, t.node)
		} else if t.count > 0 {
			result = append(result, inline{kind: "text", text: strings.Repeat(string(t.ch), t.count)})
		}
	}
	return result
}

func parseInlines(refs map[string]refDef, text string) []inline {
	return processEmphasis(tokenize(refs, text))
}

func renderInlineText(refs map[string]refDef, text string) string {
	return renderInlines(parseInlines(refs, text))
}

// ── Block parsing ───────────────────────────────────────────────────────────

func leadingIndent(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ':
			n++
		case '\t':
			n += 4 - (n % 4)
		default:
			return n
		}
	}
	return n
}

func isBlankLine(s string) bool {
	return strings.TrimSpace(s) == ""
}

func isThematicBreak(line string) bool {
	t := strings.TrimSpace(line)
	t = strings.ReplaceAll(t, " ", "")
	t = strings.ReplaceAll(t, "\t", "")
	if len(t) < 3 {
		return false
	}
	return allBytes(t, '-') || allBytes(t, '*') || allBytes(t, '_')
}

func allBytes(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

func tryATXHeading(line string) (int, string, bool) {
	if leadingIndent(line) >= 4 {
		return 0, "", false
	}
	t := strings.TrimLeft(line, " ")
	lvl := 0
	for lvl < len(t) && lvl < 7 && t[lvl] == '#' {
		lvl++
	}
	if lvl == 0 || lvl > 6 {
		return 0, "", false
	}
	if lvl < len(t) && t[lvl] != ' ' && t[lvl] != '\t' {
		return 0, "", false
	}
	body := strings.TrimSpace(t[lvl:])
	stripped := strings.TrimRight(strings.TrimRight(body, "#"), " \t")
	final := stripped
	if body != "" && allBytes(body, '#') {
		final = ""
	}
	return lvl, final, true
}

func parseAlignRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "-") {
		return nil, false
	}
	body := strings.Trim(trimmed, "|")
	cells := strings.Split(body, "|")
	aligns := make([]string, 0, len(cells))
	for _, raw := range cells {
		core := strings.TrimSpace(raw)
		if core == "" {
			return nil, false
		}
		left := strings.HasPrefix(core, ":")
		right := strings.HasSuffix(core, ":")
		dashes := strings.Trim(core, ":")
		if dashes == "" || !allBytes(dashes, '-') {
			return nil, false
		}
		switch {
		case left && right:
			aligns = append(aligns, "center")
		case left:
			aligns = append(aligns, "left")
		case right:
			aligns = append(aligns, "right")
		default:
			aligns = append(aligns, "none")
		}
	}
	return aligns, true
}

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	body := t
	if strings.HasPrefix(body, "|") {
		body = body[1:]
	}
	if strings.HasSuffix(body, "|") && !strings.HasSuffix(body, `\|`) {
		body = body[:len(body)-1]
	}
	var cells []string
	var buf strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\\' && i+1 < len(body) && body[i+1] == '|':
			buf.WriteByte('|')
			i++
		case c == '|':
			cells = append(cells, strings.TrimSpace(buf.String()))
			buf.Reset()
		default:
			buf.WriteByte(c)
		}
	}
	cells = append(cells, strings.TrimSpace(buf.String()))
	return cells
}

func indexOfAny(s, chars string) int {
	return strings.IndexAny(s, chars)
}

func extractRefDefs(lines []string) (map[string]refDef, []string) {
	refs := make(map[string]refDef)
	var kept []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		handled := false
		if strings.HasPrefix(t, "[") && leadingIndent(line) < 4 {
			closeIdx := strings.IndexByte(t, ']')
			if closeIdx > 1 && closeIdx+1 < len(t) && t[closeIdx+1] == ':' {
				label := t[1:closeIdx]
				rest := strings.TrimSpace(t[closeIdx+2:])
				if rest != "" && !strings.Contains(label, "]") {
					spaceIdx := indexOfAny(rest, " \t")
					url, titlePart := rest, ""
					if spaceIdx >= 0 {
						url = rest[:spaceIdx]
						titlePart = strings.TrimSpace(rest[spaceIdx:])
					}
					urlClean := url
					if strings.HasPrefix(url, "<") && strings.HasSuffix(url, ">") {
						urlClean = url[1 : len(url)-1]
					}
					def := refDef{url: urlClean}
					if len(titlePart) >= 2 && (titlePart[0] == '"' || titlePart[0] == '\'') {
						q := titlePart[0]
						if tc := strings.IndexByte(titlePart[1:], q); tc >= 0 {
							def.title = titlePart[1 : 1+tc]
							def.hasTitle = true
						}
					}
					refs[normLabel(label)] = def
					handled = true
				}
			}
		}
		if !handled {
			kept = append(kept, line)
		}
	}
	return refs, kept
}

func isListMarker(s string) bool {
	if s == "" {
		return false
	}
	if (s[0] == '-' || s[0] == '*' || s[0] == '+') && len(s) >= 2 && (s[1] == ' ' || s[1] == '\t') {
		return true
	}
	k := 0
	for k < len(s) && k < 9 && mdIsDigit(s[k]) {
		k++
	}
	return k > 0 && k+1 < len(s) && (s[k] == '.' || s[k] == ')') && (s[k+1] == ' ' || s[k+1] == '\t')
}

type listItem struct {
	task   int // -1 = plain item; 0 = unchecked task; 1 = checked task
	blocks []mdBlock
}

type mdBlock struct {
	kind    string // "heading" "paragraph" "hr" "fenced" "indented" "blockquote" "bullet" "ordered" "table"
	level   int    // heading level / ordered-list start
	text    string // heading/paragraph text, fenced/indented content
	lang    string
	blocks  []mdBlock // blockquote body
	tight   bool
	items   []listItem
	headers []string
	aligns  []string
	rows    [][]string
}

func splitWSFirst(s string) string {
	if idx := indexOfAny(s, " \t"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func parseBlocks(lines []string) []mdBlock {
	var blocks []mdBlock
	n := len(lines)
	i := 0
	for i < n {
		line := lines[i]
		if isBlankLine(line) {
			i++
			continue
		}
		if _, _, atxOK := tryATXHeading(line); isThematicBreak(line) && !atxOK {
			blocks = append(blocks, mdBlock{kind: "hr"})
			i++
			continue
		}
		if lvl, text, ok := tryATXHeading(line); ok {
			blocks = append(blocks, mdBlock{kind: "heading", level: lvl, text: text})
			i++
			continue
		}
		indent := leadingIndent(line)
		trimmedStart := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(trimmedStart, "```") || strings.HasPrefix(trimmedStart, "~~~"):
			fence := "```"
			if strings.HasPrefix(trimmedStart, "~~~") {
				fence = "~~~"
			}
			info := strings.TrimSpace(trimmedStart[3:])
			lang := ""
			if info != "" {
				lang = splitWSFirst(info)
			}
			var content []string
			j := i + 1
			closed := false
			for j < n && !closed {
				ln := lines[j]
				if strings.HasPrefix(strings.TrimLeft(ln, " \t"), fence) &&
					strings.TrimRight(strings.TrimSpace(ln), string(fence[0])) == "" {
					closed = true
				} else {
					content = append(content, ln)
				}
				j++
			}
			blocks = append(blocks, mdBlock{kind: "fenced", lang: lang, text: strings.Join(content, "\n")})
			i = j
		case indent >= 4:
			var content []string
			j := i
			for j < n && (leadingIndent(lines[j]) >= 4 || isBlankLine(lines[j])) {
				ln := lines[j]
				if isBlankLine(ln) {
					content = append(content, "")
				} else {
					content = append(content, ln[min(4, len(ln)):])
				}
				j++
			}
			for len(content) > 0 && content[len(content)-1] == "" {
				content = content[:len(content)-1]
			}
			blocks = append(blocks, mdBlock{kind: "indented", text: strings.Join(content, "\n")})
			i = j
		case strings.HasPrefix(trimmedStart, ">"):
			var inner []string
			j := i
			for j < n && strings.HasPrefix(strings.TrimLeft(lines[j], " \t"), ">") {
				ln := strings.TrimLeft(lines[j], " \t")[1:]
				ln = strings.TrimPrefix(ln, " ")
				inner = append(inner, ln)
				j++
			}
			blocks = append(blocks, mdBlock{kind: "blockquote", blocks: parseBlocks(inner)})
			i = j
		case tableAhead(lines, i):
			headers := splitTableRow(line)
			aligns, _ := parseAlignRow(lines[i+1])
			var rows [][]string
			j := i + 2
			for j < n && !isBlankLine(lines[j]) && strings.Contains(lines[j], "|") {
				rows = append(rows, splitTableRow(lines[j]))
				j++
			}
			blocks = append(blocks, mdBlock{kind: "table", headers: headers, aligns: aligns, rows: rows})
			i = j
		case isListMarker(trimmedStart):
			listBlock, next := parseList(lines, i)
			blocks = append(blocks, listBlock)
			i = next
		default:
			var para []string
			j := i
			stop := false
			setext := 0
			for j < n && !stop {
				ln := lines[j]
				trimmed := strings.TrimSpace(ln)
				_, _, lnATX := tryATXHeading(ln)
				switch {
				case isBlankLine(ln):
					stop = true
				case j > i && leadingIndent(ln) < 4 && trimmed != "" &&
					(allBytes(trimmed, '=') || allBytes(trimmed, '-')):
					if trimmed[0] == '=' {
						setext = 1
					} else {
						setext = 2
					}
					stop = true
					j++
				case j > i && (isThematicBreak(ln) || lnATX ||
					strings.HasPrefix(strings.TrimLeft(ln, " "), ">") ||
					isListMarker(strings.TrimLeft(ln, " \t"))):
					stop = true
				default:
					para = append(para, strings.TrimLeft(ln, " \t"))
					j++
				}
			}
			text := strings.TrimRight(strings.Join(para, "\n"), " \t\n")
			if setext > 0 && text != "" {
				blocks = append(blocks, mdBlock{kind: "heading", level: setext, text: text})
			} else if text != "" {
				blocks = append(blocks, mdBlock{kind: "paragraph", text: text})
			}
			i = j
		}
	}
	return blocks
}

func tableAhead(lines []string, i int) bool {
	if !strings.Contains(lines[i], "|") || i+1 >= len(lines) {
		return false
	}
	aligns, ok := parseAlignRow(lines[i+1])
	return ok && len(aligns) == len(splitTableRow(lines[i]))
}

func parseList(lines []string, start int) (mdBlock, int) {
	n := len(lines)
	first := strings.TrimLeft(lines[start], " \t")
	ordered := mdIsDigit(first[0])
	startNum := 1
	if ordered {
		k := 0
		for k < len(first) && mdIsDigit(first[k]) {
			k++
		}
		if v, err := strconv.Atoi(first[:k]); err == nil {
			startNum = v
		}
	}
	markerWidth := func(s string) int {
		if !ordered {
			return 2
		}
		k := 0
		for k < len(s) && mdIsDigit(s[k]) {
			k++
		}
		return k + 2
	}

	var items []listItem
	i := start
	tight := true
	sawBlankBetween := false
	for go_ := true; go_ && i < n; {
		raw := lines[i]
		trimmed := strings.TrimLeft(raw, " \t")
		baseIndent := leadingIndent(raw)
		switch {
		case isBlankLine(raw):
			sawBlankBetween = true
			i++
		case isListMarker(trimmed) && (ordered == mdIsDigit(trimmed[0])) && baseIndent < 4:
			if sawBlankBetween && len(items) > 0 {
				tight = false
			}
			sawBlankBetween = false
			mw := markerWidth(trimmed)
			contentOffset := baseIndent + mw
			afterMarker := ""
			if len(trimmed) > mw {
				afterMarker = trimmed[mw:]
			}
			task := -1
			firstContent := afterMarker
			switch {
			case strings.HasPrefix(afterMarker, "[ ]"):
				task = 0
				firstContent = strings.TrimLeft(afterMarker[3:], " ")
			case strings.HasPrefix(afterMarker, "[x]") || strings.HasPrefix(afterMarker, "[X]"):
				task = 1
				firstContent = strings.TrimLeft(afterMarker[3:], " ")
			}
			itemLines := []string{firstContent}
			i++
			for inItem := true; inItem && i < n; {
				ln := lines[i]
				switch {
				case isBlankLine(ln):
					itemLines = append(itemLines, "")
					i++
				case leadingIndent(ln) >= contentOffset:
					itemLines = append(itemLines, ln[min(contentOffset, len(ln)):])
					i++
				case isListMarker(strings.TrimLeft(ln, " \t")) && leadingIndent(ln) < 4:
					inItem = false
				case leadingIndent(ln) > 0 && !isListMarker(strings.TrimLeft(ln, " \t")):
					itemLines = append(itemLines, strings.TrimSpace(ln))
					i++
				default:
					inItem = false
				}
			}
			for len(itemLines) > 0 && itemLines[len(itemLines)-1] == "" {
				itemLines = itemLines[:len(itemLines)-1]
				sawBlankBetween = true
			}
			for _, ln := range itemLines {
				if ln == "" {
					tight = false
					break
				}
			}
			items = append(items, listItem{task: task, blocks: parseBlocks(itemLines)})
		default:
			go_ = false
		}
	}

	if ordered {
		return mdBlock{kind: "ordered", level: startNum, tight: tight, items: items}, i
	}
	return mdBlock{kind: "bullet", tight: tight, items: items}, i
}

// ── Block rendering ─────────────────────────────────────────────────────────

func alignAttr(a string) string {
	switch a {
	case "left":
		return ` align="left"`
	case "center":
		return ` align="center"`
	case "right":
		return ` align="right"`
	}
	return ""
}

func renderBlocks(refs map[string]refDef, blocks []mdBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(renderBlock(refs, b))
	}
	return sb.String()
}

func renderBlock(refs map[string]refDef, b mdBlock) string {
	switch b.kind {
	case "hr":
		return "<hr />\n"
	case "heading":
		lvl := strconv.Itoa(b.level)
		return "<h" + lvl + ">" + renderInlineText(refs, b.text) + "</h" + lvl + ">\n"
	case "paragraph":
		return "<p>" + renderInlineText(refs, b.text) + "</p>\n"
	case "fenced":
		cls := ""
		if b.lang != "" {
			cls = ` class="language-` + mdEscape(b.lang) + `"`
		}
		return "<pre><code" + cls + ">" + mdEscape(b.text) + "\n</code></pre>\n"
	case "indented":
		return "<pre><code>" + mdEscape(b.text) + "\n</code></pre>\n"
	case "blockquote":
		return "<blockquote>\n" + renderBlocks(refs, b.blocks) + "</blockquote>\n"
	case "table":
		var sb strings.Builder
		sb.WriteString(`<table class="fuaran-table"><thead><tr>`)
		for idx, h := range b.headers {
			a := "none"
			if idx < len(b.aligns) {
				a = b.aligns[idx]
			}
			sb.WriteString(`<th class="fuaran-table-header"` + alignAttr(a) + ">" + renderInlineText(refs, h) + "</th>")
		}
		sb.WriteString("</tr></thead><tbody>")
		for _, row := range b.rows {
			sb.WriteString(`<tr class="fuaran-table-row">`)
			for idx := range b.headers {
				cell := ""
				if idx < len(row) {
					cell = row[idx]
				}
				a := "none"
				if idx < len(b.aligns) {
					a = b.aligns[idx]
				}
				sb.WriteString(`<td class="fuaran-table-cell"` + alignAttr(a) + ">" + renderInlineText(refs, cell) + "</td>")
			}
			sb.WriteString("</tr>")
		}
		sb.WriteString("</tbody></table>\n")
		return sb.String()
	case "bullet":
		return "<ul>\n" + renderItems(refs, b.tight, b.items) + "</ul>\n"
	case "ordered":
		startAttr := ""
		if b.level != 1 {
			startAttr = ` start="` + strconv.Itoa(b.level) + `"`
		}
		return "<ol" + startAttr + ">\n" + renderItems(refs, b.tight, b.items) + "</ol>\n"
	}
	return ""
}

func renderItems(refs map[string]refDef, tight bool, items []listItem) string {
	var sb strings.Builder
	for _, item := range items {
		checkbox := ""
		liClass := ""
		switch item.task {
		case 0:
			checkbox = `<input class="fuaran-task-checkbox" disabled="" type="checkbox" /> `
			liClass = ` class="fuaran-task-item"`
		case 1:
			checkbox = `<input class="fuaran-task-checkbox" checked="" disabled="" type="checkbox" /> `
			liClass = ` class="fuaran-task-item"`
		}
		if tight {
			var inner strings.Builder
			for _, blk := range item.blocks {
				if blk.kind == "paragraph" {
					inner.WriteString(renderInlineText(refs, blk.text))
				} else {
					inner.WriteString("\n" + renderBlock(refs, blk))
				}
			}
			sb.WriteString("<li" + liClass + ">" + checkbox + inner.String() + "</li>\n")
		} else {
			sb.WriteString("<li" + liClass + ">\n" + checkbox + renderBlocks(refs, item.blocks) + "</li>\n")
		}
	}
	return sb.String()
}

// MarkdownToHTML renders GFM markdown source to deterministic, cross-host
// HTML (the corpus at wire-format-fixtures/markdown/corpus.json pins the
// bytes). Escapes by construction — no raw-HTML passthrough; URLs via the
// sanitiser — and the result still passes through sanitizeMarkdownHTML as
// defence in depth.
func MarkdownToHTML(source string) string {
	if source == "" {
		return ""
	}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	rawLines := strings.Split(normalized, "\n")
	refs, lines := extractRefDefs(rawLines)
	blocks := parseBlocks(lines)
	return sanitizeMarkdownHTML(renderBlocks(refs, blocks))
}
