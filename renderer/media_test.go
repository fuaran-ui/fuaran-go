package renderer

import (
	"strings"
	"testing"
)

// The media-wave render obligations (WIRE_FORMAT.md §3.6.2–§3.6.6). Every
// assertion here is a NORMATIVE render obligation the bytes cannot carry — a
// host that got any of them wrong would still round-trip the corpus perfectly,
// which is exactly why they are pinned separately from the codec legs.

// §3.6.6 — `aria-label` ALWAYS, and `controls` unless the document switches it
// off. The label is mandatory on the wire and a transport has no decorative
// case, so unlike Image's `alt` there is no branch here.
func TestMediaAlwaysCarriesAccessibleName(t *testing.T) {
	for _, src := range []string{
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp4"}}}`,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio"},"label":"Studio walkthrough","src":{"$type":"Static","value":"/walkthrough.mp3"}}}`,
	} {
		html := RenderHTML(mustDecode(t, src), nil)
		if !strings.Contains(html, `aria-label="Studio walkthrough"`) {
			t.Errorf("media emitted no accessible name:\n%s", html)
		}
		if !strings.Contains(html, ` controls=""`) {
			t.Errorf("controls is omitted at TRUE on the wire, so its absence is the affirmative:\n%s", html)
		}
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

// §3.6.6 — `autoplay` NEVER WITHOUT `muted`, and no muted attribute where
// autoplay is absent. The pairing is not a default a caller overrides; it is
// what the declaration MEANS, which is why the wire carries no separate muted
// slot to get out of step with it.
func TestMediaAutoplayIsNeverEmittedWithoutMuted(t *testing.T) {
	on := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","controls":false,"kind":{"$type":"Video","autoplay":true},"label":"Ambient loop","loop":true,"src":{"$type":"Static","value":"/ambient.mp4"}}}`), nil)
	if !strings.Contains(on, ` autoplay=""`) || !strings.Contains(on, ` muted=""`) {
		t.Errorf("autoplay must be emitted together with muted:\n%s", on)
	}
	if !strings.Contains(on, ` loop=""`) {
		t.Errorf("declared loop was not emitted:\n%s", on)
	}
	if strings.Contains(on, ` controls=""`) {
		t.Errorf("controls:false must switch the transport off:\n%s", on)
	}

	off := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video"},"label":"Walkthrough","src":{"$type":"Static","value":"/w.mp4"}}}`), nil)
	if strings.Contains(off, ` muted=""`) {
		t.Errorf("muting a video the reader pressed play on is the same defect in the other direction:\n%s", off)
	}
	if strings.Contains(off, ` autoplay=""`) {
		t.Errorf("autoplay is not the default:\n%s", off)
	}
}

// §3.6.6 — `Audio` has NO autoplay pathway: in the type, on the wire, or in
// the emission. The case carries no slot to read, so a document that names one
// decodes to an audio surface that does not autoplay.
func TestMediaAudioHasNoAutoplayPathway(t *testing.T) {
	html := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Audio","autoplay":true},"label":"Commentary","src":{"$type":"Static","value":"/c.mp3"}}}`), nil)
	if strings.Contains(html, "autoplay") || strings.Contains(html, "muted") {
		t.Errorf("Audio must have no autoplay pathway at all:\n%s", html)
	}
}

// §3.6.6 — both URLs pass the egress floor, and they differ in what a REFUSAL
// means: an element must have a source, so `src` collapses and carries its
// marker, while a refused poster simply LEAVES. A <video> with no poster shows
// its first frame; a poster at the refusal URL is a broken image over the
// player.
func TestMediaPosterPassesTheFloorAndARefusedOneIsDropped(t *testing.T) {
	allowed := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video","poster":{"$type":"Static","value":"/poster.jpg"}},"label":"Walkthrough","src":{"$type":"Static","value":"/w.mp4"}}}`), nil)
	if !strings.Contains(allowed, `poster="/poster.jpg"`) {
		t.Errorf("a local poster was not emitted:\n%s", allowed)
	}

	refused := RenderHTML(mustDecode(t,
		`{"id":"m","kind":{"$type":"Media","kind":{"$type":"Video","poster":{"$type":"Static","value":"https://tracker.example/p.jpg"}},"label":"Walkthrough","src":{"$type":"Static","value":"/w.mp4"}}}`), nil)
	if strings.Contains(refused, "poster=") {
		t.Errorf("a refused poster must be DROPPED, not neutered:\n%s", refused)
	}
	if !strings.Contains(refused, `src="/w.mp4"`) {
		t.Errorf("dropping the poster must not disturb the source:\n%s", refused)
	}

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
// no wrapper at all (not an empty one).
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

// §3.6.4 — presentation order is the RENDERER's and is ASCENDING by width
// (the wire keeps the author's order); every candidate passes the same floor
// as the primary src, and a failing one is DROPPED rather than neutered.
func TestImageSrcSetIsSortedAscendingAndFloored(t *testing.T) {
	html := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-1600.jpg"},"width":1600},{"src":{"$type":"Static","value":"/harbour-800.jpg"},"width":800},{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400}],"variant":"Default"}}`), nil)
	if !strings.Contains(html, `srcset="/harbour-400.jpg 400w, /harbour-800.jpg 800w, /harbour-1600.jpg 1600w"`) {
		t.Errorf("candidates were not ordered ascending by width:\n%s", html)
	}
	if !strings.Contains(html, `sizes="100vw"`) {
		t.Errorf("the bounded sizes attribute was not emitted:\n%s", html)
	}

	dropped := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400},{"src":{"$type":"Static","value":"https://tracker.example/h-800.jpg"},"width":800}],"variant":"Default"}}`), nil)
	if !strings.Contains(dropped, `srcset="/harbour-400.jpg 400w"`) {
		t.Errorf("a candidate that fails the floor must be dropped, leaving the rest:\n%s", dropped)
	}
	if strings.Contains(dropped, EgressRefusalURL+" 800w") {
		t.Errorf("a failing candidate must be DROPPED, never emitted in neutered form:\n%s", dropped)
	}

	none := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"https://tracker.example/h-800.jpg"},"width":800}],"variant":"Default"}}`), nil)
	if strings.Contains(none, "srcset=") || strings.Contains(none, "sizes=") {
		t.Errorf("with every candidate refused, neither attribute is emitted:\n%s", none)
	}
}

// §3.6.5 — the rendered baseline is a REAL LINK to the resolved primary src,
// marked so an enhancement tier can find it; nothing crosses the dispatch
// gate; and a refused src emits NO anchor, because an affordance that cannot
// be honoured is worse than none.
func TestImageExpandableEmitsARealAnchor(t *testing.T) {
	html := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","expandable":true,"src":{"$type":"Static","value":"/harbour.jpg"},"variant":"Default"}}`), nil)
	if !strings.Contains(html, `<a class="fuaran-image-expand" href="/harbour.jpg" data-fuaran-expandable=""><img `) {
		t.Errorf("expandable did not emit a real, navigable anchor:\n%s", html)
	}
	if strings.Contains(html, "onclick") {
		t.Errorf("nothing crosses the dispatch gate:\n%s", html)
	}

	// The composition rule: <figure> wraps <a> wraps <img>, so the caption sits
	// OUTSIDE the link target, and the srcSet candidates stay on the <img>
	// while the anchor targets the full asset.
	composed := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","caption":"The harbour at dawn.","expandable":true,"src":{"$type":"Static","value":"/harbour.jpg"},"srcSet":[{"src":{"$type":"Static","value":"/harbour-400.jpg"},"width":400}],"variant":"Default"}}`), nil)
	if !strings.Contains(composed, `<figure class="fuaran-image-figure"><a class="fuaran-image-expand" href="/harbour.jpg" data-fuaran-expandable=""><img `) {
		t.Errorf("figure > a > img nesting was not emitted:\n%s", composed)
	}
	if !strings.Contains(composed, `srcset="/harbour-400.jpg 400w"`) {
		t.Errorf("the candidates are renditions of the THUMBNAIL and stay on the <img>:\n%s", composed)
	}
	if strings.Contains(composed, `href="/harbour-400.jpg"`) {
		t.Errorf("a candidate behind the link would show the reader a thumbnail:\n%s", composed)
	}

	refused := RenderHTML(mustDecode(t,
		`{"id":"i","kind":{"$type":"Image","alt":"Fishing boats","expandable":true,"src":{"$type":"Static","value":"javascript:alert(1)"},"variant":"Default"}}`), nil)
	if strings.Contains(refused, "fuaran-image-expand") || strings.Contains(refused, "data-fuaran-expandable") {
		t.Errorf("a refused src must emit NO anchor and no marker:\n%s", refused)
	}
	if !strings.Contains(refused, `<img `) {
		t.Errorf("the image still renders, carrying its refusal marker:\n%s", refused)
	}
}
