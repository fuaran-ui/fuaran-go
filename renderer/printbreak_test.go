package renderer

import (
	"strings"
	"testing"
)

// WIRE_FORMAT.md §21 — the container's two paged-medium declarations,
// `keepTogether` and `breakBefore`.
//
// Before Phase 1653 this host decoded both booleans and emitted NEITHER. The
// byte-copied reference stylesheet already carried
// `.fuaran-break-inside-avoid` and `.fuaran-break-before-page` inside its
// `@media print` block, so the CSS shipped and nothing ever selected it: a
// document declaring that a totals block must not be halved printed halved,
// on a host that round-tripped the declaration perfectly.
//
// The corpus's `render-fidelity.json` roster does not declare the obligation,
// which is why no gate said so. Declaring it there moves five hosts in one
// change-set and is not this host's to do alone, so the emission is pinned here
// against the two corpus fixtures that carry the declarations — and against the
// Rust host's bytes for the same trees, which were compared directly when this
// was written and are what the substrings below were taken from.
func TestKeepTogetherEmitsTheBreakInsideAvoidClass(t *testing.T) {
	html := renderCorpusNode(t, "box-keep-together-1")
	if !strings.Contains(html, `<section class="fuaran-layout-card fuaran-break-inside-avoid">`) {
		t.Errorf("keepTogether must append its class to whichever element the box became:\n%s", html)
	}
	if strings.Contains(html, "fuaran-break-before-page") {
		t.Errorf("keepTogether must not emit the page-break class as well:\n%s", html)
	}
}

func TestBreakBeforeEmitsThePageBreakClass(t *testing.T) {
	html := renderCorpusNode(t, "box-break-before-1")
	if !strings.Contains(html, `<div class="fuaran-layout-stack fuaran-stack-vertical fuaran-break-before-page">`) {
		t.Errorf("breakBefore must append its class to the stack the box became:\n%s", html)
	}
	if strings.Contains(html, "fuaran-break-inside-avoid") {
		t.Errorf("breakBefore must not emit the keep-together class as well:\n%s", html)
	}
}

// Verify the probe: a host that appended both classes unconditionally, or
// neither, would need to fail something. An UNDECLARED container emits no
// paged-medium class at all — the flags are omitted-when-false on the wire, and
// "not stated" and "explicitly off" are the same state.
func TestAnUndeclaredContainerEmitsNoPagedClass(t *testing.T) {
	html := renderCorpusNode(t, "box-keep-together-1")
	// The three markdown children inside the card declare nothing, and neither
	// does the card's own body div.
	if strings.Contains(html, `<div class="fuaran-card-body fuaran`) {
		t.Errorf("the card body is not the declaring container and must carry no paged class:\n%s", html)
	}
	if n := strings.Count(html, "fuaran-break-"); n != 1 {
		t.Errorf("expected exactly one paged-medium class in this tree, got %d:\n%s", n, html)
	}
}
