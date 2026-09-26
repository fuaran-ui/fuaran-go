package function

import (
	"errors"

	core "github.com/fuaran-ui/fuaran-go/internal/core/function"
	"github.com/fuaran-ui/fuaran-go/wire"
)

// DecodeDeclaration parses a canonical capability declaration. Total: every
// malformed input yields a named error.
func DecodeDeclaration(s string) (Capability, error) {
	// Parsed under the §21 limits. A declaration arrives from a registry, which
	// this codec's own header calls a trust boundary — so the bound belongs
	// here rather than in whatever happened to hand us the text. A breach is a
	// *wire.DecodeError with code LIMIT_EXCEEDED, distinct from "not JSON",
	// because a well-formed-but-too-large document is not malformed.
	raw, perr := wire.ParseBounded(s)
	if perr != nil {
		if de, ok := perr.(*wire.DecodeError); ok && de.Code == wire.CodeLimitExceeded {
			return Capability{}, de
		}
		return Capability{}, errors.New("not JSON: " + perr.Error())
	}
	return core.DecodeDeclarationParsed(raw)
}
