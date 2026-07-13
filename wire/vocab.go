package wire

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
