package canonical

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/canonical"
)

// SortKeys orders object keys as the canonical encoder requires: by UTF-16
// code-unit comparison (WIRE_FORMAT.md §2 rule 2).
func SortKeys(keys []string) { core.SortKeys(keys) }

// LessUTF16 reports whether a sorts before b in UTF-16 code-unit order.
func LessUTF16(a, b string) bool { return core.LessUTF16(a, b) }
