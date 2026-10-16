package wire

import "github.com/fuaran-ui/fuaran-go/wire/idl"

// Exported copies of the closed wire vocabularies, for the downstream tiers
// (the apply engine's coercion surface, the validator, the renderer). Each
// returns a fresh sorted slice; the decoder's own tables stay private.

// TextSourceCases returns the TextSource discriminators.
func TextSourceCases() []string { return append([]string(nil), textSourceCases.names...) }

// BindingCases returns the Binding discriminators.
func BindingCases() []string { return append([]string(nil), bindingCases.names...) }

// CellFormatCases returns the CellFormat discriminators.
func CellFormatCases() []string { return append([]string(nil), cellFormatCases.names...) }

// ActionCases returns the Action discriminators.
func ActionCases() []string { return append([]string(nil), actionCases.names...) }

// ToneVariants returns the ToneVariant bare-enum vocabulary.
func ToneVariants() []string { return append([]string(nil), toneCases.names...) }

// StyleWeights returns the StyleWeight bare-enum vocabulary.
func StyleWeights() []string { return append([]string(nil), weightCases.names...) }

// EmphasisLevels returns the Emphasis bare-enum vocabulary.
func EmphasisLevels() []string { return append([]string(nil), emphasisCases.names...) }

// Orientations returns the Orientation bare-enum vocabulary.
func Orientations() []string { return append([]string(nil), orientationCases.names...) }

// BadgeVariants returns the BadgeVariant bare-enum vocabulary.
func BadgeVariants() []string { return append([]string(nil), badgeVariantCases.names...) }

// HeadingVariants returns the HeadingVariant bare-enum vocabulary.
func HeadingVariants() []string { return append([]string(nil), headingVariantCases.names...) }

// IconSizes returns the IconSize bare-enum vocabulary (Phase 821).
func IconSizes() []string { return append([]string(nil), iconSizeCases.names...) }

// TrendPolarities returns the TrendPolarity bare-enum vocabulary (Phase 867) —
// the Metric slot declaring which way the measured quantity IMPROVES. Two cases;
// `Neutral` is reserved and deliberately not among them.
func TrendPolarities() []string { return append([]string(nil), trendPolarityCases.names...) }

// RequiredKindFields returns, per kind discriminator, the wire fields the
// contract requires — the required rows of each kind's generated field table,
// which is the set the decoder enforces on wire input. The validator uses it to
// catch a CONSTRUCTED tree that would fail decode on any conformant host. Kinds
// with no required field are absent.
func RequiredKindFields() map[string][]string {
	out := make(map[string][]string, len(idl.Kinds))
	for _, k := range idl.Kinds {
		if required := k.RequiredFields(); len(required) > 0 {
			out[k.Tag] = required
		}
	}
	return out
}
