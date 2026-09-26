package canonical

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/canonical"
)

// EscapeString renders s as a canonical JSON string literal (WIRE_FORMAT.md §2
// rule 6): only `"`, `\`, and the control characters U+0000–U+001F are escaped
// (controls as lower-case four-digit \u00xx); everything else — including `/`
// and non-ASCII UTF-8 sequences — passes through literally. The result includes
// the surrounding quotes.
func EscapeString(s string) string { return core.EscapeString(s) }
