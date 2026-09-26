package canonical

import "strings"

// EscapeString renders s as a canonical JSON string literal (WIRE_FORMAT.md §2
// rule 6): only `"`, `\`, and the control characters U+0000–U+001F are escaped
// (controls as lower-case four-digit \u00xx); everything else — including `/`
// and non-ASCII UTF-8 sequences — passes through literally. The result includes
// the surrounding quotes.
//
// Byte-wise iteration is safe here: every byte of a multi-byte UTF-8 sequence
// is >= 0x80, so only ASCII bytes can match an escape case.
func EscapeString(s string) string {
	const hex = "0123456789abcdef"
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b == '"':
			sb.WriteString(`\"`)
		case b == '\\':
			sb.WriteString(`\\`)
		case b < 0x20:
			sb.WriteString(`\u00`)
			sb.WriteByte(hex[b>>4])
			sb.WriteByte(hex[b&0xF])
		default:
			sb.WriteByte(b)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
