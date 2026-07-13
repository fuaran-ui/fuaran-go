package renderer

import _ "embed"

// The reference stylesheet, byte-copied from the canonical F#-tier artefact
// (Fuaran.UI.Renderer/content/fuaran-reference.css). The class vocabulary
// this renderer emits is styled by it, so output is visually consistent
// across every wire-format host. Re-copy it in the same change-set as any
// canonical CSS change (the forward-coupling rule spans this host); the
// conformance reference-CSS parity test fails when the copy drifts and the
// canonical sibling is checked out alongside.
//
//go:embed content/fuaran-reference.css
var referenceCSS string

// ReferenceCSS returns the byte-copied canonical reference stylesheet. The
// host owns the document shell and serves/embeds this however it likes; the
// renderer emits the body fragment only.
func ReferenceCSS() string {
	return referenceCSS
}
