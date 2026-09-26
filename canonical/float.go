// Package canonical implements the canonical-JSON encoding primitives the Fuaran
// UI wire format requires: number form, key sort, and string escaping. The
// number form is the make-or-break of any host (WIRE_FORMAT.md §5) — this is the
// first brick fuaran-go ships.
//
// The implementation is the Core twin in internal/core/canonical, kept behind an
// import boundary (see CONTRIBUTING.md, "The Core boundary"). This package
// forwards to it unchanged.
package canonical

import (
	core "github.com/fuaran-ui/fuaran-go/internal/core/canonical"
)

// FormatFiniteDouble renders a finite float64 in the canonical Fuaran wire form
// (WIRE_FORMAT.md §5). The caller must ensure f is finite — NaN and ±Inf are not
// representable on the wire and are rejected upstream.
func FormatFiniteDouble(f float64) string { return core.FormatFiniteDouble(f) }
