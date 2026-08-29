package renderer

import (
	"strings"
	"testing"
)

// The media-wave render contract (WIRE_FORMAT.md §3.6.2–§3.6.6). Every
// assertion here is a NORMATIVE claim the bytes cannot carry — a host that got
// any of them wrong would still round-trip the corpus perfectly, which is
// exactly why they are pinned separately from the codec legs.
//
// The subset of that contract the corpus artefact declares as CHECKABLE CLAIMS
// (render-fidelity.json's per-kind `obligations`, WIRE_FORMAT.md §13) has moved
// to render_obligations_test.go, where each is reached BY CLAIM ID from the
// artefact's own enumeration rather than by a test name. There is one copy of
// those assertions and it lives there: `alt` always emitted, the autoplay/muted
// pairing, audio's absent autoplay pathway, the dropped refused poster, the
// expand anchor and its refusal, the figure/anchor nesting, the ascending
// srcset. What stays HERE is the rest of the fallback prose — real, normative,
// and not (yet) declared as a claim, so nothing enumerates it for us.

// §3.6.6 — `controls` emits unless the document switches it off. Omitted is
// TRUE on the wire, so its absence in the JSON is the affirmative.
func TestMediaControlsDefaultOnAndSwitchableOff(t *testing.T) {
	for _, src := range []string{
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp4"}}}`,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio"},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp3"}}}`,
	} {
		html := RenderHTML(mustDecode(t, src), nil)
		if !strings.Contains(html, ` controls=""`) {
			t.Errorf("controls is omitted at TRUE on the wire, so its absence is the affirmative:\n%s", html)
		}
	}

	off := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","controls":false,"kind":{"$type":"Video"},"label":"Ambient loop","src":{"$type":"Static","value":"/ambient.mp4"}}}`), nil)
	if strings.Contains(off, ` controls=""`) {
		t.Errorf("controls:false must switch the transport off:\n%s", off)
	}
}

func TestMediaVariantSelectsTheElement(t *testing.T) {
	video := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Walkthrough","src":{"$type":"Static","value":"/w.mp4"}}}`), nil)
	if !strings.Contains(video, `<video class="fuaran-media fuaran-media-video" src="/w.mp4"`) {
		t.Errorf("Video did not emit a real <video>:\n%s", video)
	}
	audio := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio"},"label":"Commentary","src":{"$type":"Static","value":"/c.mp3"}}}`), nil)
	if !strings.Contains(audio, `<audio class="fuaran-media fuaran-media-audio" src="/c.mp3"`) {
		t.Errorf("Audio did not emit a real <audio>:\n%s", audio)
	}
}

// §3.6.6 — `loop` emits only when declared.
func TestMediaLoopEmitsOnlyWhenDeclared(t *testing.T) {
	on := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Ambient loop","loop":true,"src":{"$type":"Static","value":"/ambient.mp4"}}}`), nil)
	if !strings.Contains(on, ` loop=""`) {
		t.Errorf("declared loop was not emitted:\n%s", on)
	}

	off := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Walkthrough","src":{"$type":"Static","value":"/w.mp4"}}}`), nil)
	if strings.Contains(off, ` loop=""`) {
		t.Errorf("loop is not the default:\n%s", off)
	}
}

// §3.6.6 — the src and the poster differ in what a REFUSAL means: an element
// must have a source, so `src` COLLAPSES and carries its marker, while a
// refused poster simply leaves (that half is the `refused-source-dropped`
// obligation, checked in render_obligations_test.go).
func TestMediaRefusedSrcCollapsesAndCarriesItsMarker(t *testing.T) {
	refusedSrc := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Walkthrough","src":{"$type":"Static","value":"javascript:alert(1)"}}}`), nil)
	if !strings.Contains(refusedSrc, `src="`+EgressRefusalURL+`"`) ||
		!strings.Contains(refusedSrc, `data-fuaran-egress-refused="unsafe-url"`) {
		t.Errorf("a refused src must COLLAPSE and carry its marker:\n%s", refusedSrc)
	}
}

// §3.6.2 — the presentation tokens map to CLASSES and nothing else, and
// `Natural` / `Eager` emit nothing at all, so a pre-phase image is untouched.
func TestImagePresentationTokensMapToClassesOnly(t *testing.T) {
	html := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"The harbour at dawn","aspectRatio":"SixteenNine","fit":"Cover","loading":"Lazy","src":{"$type":"Static","value":"/hero.jpg"},"variant":"Default"}}`), nil)
	if !strings.Contains(html, `class="fuaran-image fuaran-image-fit-cover fuaran-image-aspect-sixteen-nine"`) {
		t.Errorf("presentation tokens did not map to their classes:\n%s", html)
	}
	if !strings.Contains(html, ` loading="lazy"`) {
		t.Errorf("Lazy is a positive declaration:\n%s", html)
	}
	if strings.Contains(html, "style=") {
		t.Errorf("no value from the tree may reach a style attribute:\n%s", html)
	}

	bare := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"User avatar","src":{"$type":"Static","value":"/avatar.png"},"variant":"Avatar"}}`), nil)
	if !strings.Contains(bare, `class="fuaran-image fuaran-image-avatar" src="/avatar.png"`) {
		t.Errorf("the pre-phase class attribute must be byte-identical to what it was:\n%s", bare)
	}
	if strings.Contains(bare, "loading=") {
		t.Errorf("Eager emits no attribute at all — the browser's own default stays in place:\n%s", bare)
	}
}

// §3.6.3 — present, the caption means <figure>/<figcaption>; absent, there is
// no wrapper at all (not an empty one). The EXPANDABLE composition — figure
// wraps anchor wraps img — is the `figure-caption-outside-link` obligation.
func TestImageCaptionEmitsTheFigureBinding(t *testing.T) {
	html := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","caption":"The harbour at dawn, 1908.","src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`), nil)
	if !strings.Contains(html, `<figure class="fuaran-image-figure"><img `) ||
		!strings.Contains(html, `<figcaption class="fuaran-image-figure-caption">The harbour at dawn, 1908.</figcaption></figure>`) {
		t.Errorf("caption did not emit the figure binding:\n%s", html)
	}

	bare := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`), nil)
	if strings.Contains(bare, "figure") {
		t.Errorf("absent, there is no wrapper at all:\n%s", bare)
	}
}

// §3.6.4 — the bounded `sizes` rides a non-empty srcset, and with EVERY
// candidate refused neither attribute is emitted (the ordering and the
// per-candidate drop are the `srcset-ascending-by-width` obligation).
func TestImageSrcSetSizesAndTotalRefusal(t *testing.T) {
	html := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400}],"variant":"Default"}}`), nil)
	if !strings.Contains(html, `sizes="100vw"`) {
		t.Errorf("the bounded sizes attribute was not emitted:\n%s", html)
	}

	none := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"https://tracker.example/h-800.jpg"},"width":800}],"variant":"Default"}}`), nil)
	if strings.Contains(none, "srcset=") || strings.Contains(none, "sizes=") {
		t.Errorf("with every candidate refused, neither attribute is emitted:\n%s", none)
	}
}

// §3.6.5 — the srcSet candidates are renditions of the THUMBNAIL and stay on
// the <img>, while the anchor targets the full asset (the anchor's own
// emission, its refusal, and the figure nesting are obligations).
func TestImageExpandableKeepsCandidatesOnTheImg(t *testing.T) {
	composed := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","caption":"The harbour at dawn.","expandable":true,"src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400}],"variant":"Default"}}`), nil)
	if !strings.Contains(composed, `srcset="/harbour-400.jpg 400w"`) {
		t.Errorf("the candidates are renditions of the THUMBNAIL and stay on the <img>:\n%s", composed)
	}
	if strings.Contains(composed, `href="/harbour-400.jpg"`) {
		t.Errorf("a candidate behind the link would show the reader a thumbnail:\n%s", composed)
	}
}
