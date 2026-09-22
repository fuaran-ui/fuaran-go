package thememanifest

import (
	"reflect"
	"sort"
	"testing"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// Encode round trip (Phase 1728), against the fuaran-rs oracle (Phase 1725).
//
// The byte literals below are COPIED VERBATIM from `fuaran-rs/tests/manifest.rs`
// at commit 78b2722 ("feat(theme): Phase 1725 - theme-manifest JSON encode +
// round-trip pins", read at repository HEAD 62b40d4bbe597160cc6245e192afbd703e6eb629).
// They are not re-derived from this host's own output — a byte pin whose
// recorder is the code under test pins nothing. Rust wrote them by hand as the
// portable oracle precisely because it was the FIRST host to emit a manifest;
// this host is the second, so a disagreement here is a real cross-host
// divergence and a change to one of these literals is a wire-format change for
// every host, not a fixture refresh.
//
// Premise check recorded with the code it justifies: at the commit this phase
// branched from, `thememanifest/` exposed Decode / OfJSON, the three projectors
// and Merge, and no encoder of any spelling — `grep -rn "func Encode"
// thememanifest/` matched nothing.

// The canonical bytes of sampleManifest() (manifest_test.go), which is the same
// model as the Rust `sample_manifest`. Note what is ABSENT — no `description`
// (nil), no `weight` on the invariant (it carries DefaultWeight), no
// `$description` or `$extensions` on any token.
const sampleBytes = `{"invariants":[{"kind":"ContrastFloor","minRatio":7,"role":"Brand"}],` +
	`"meta":{"name":"test","version":"1.0"},` +
	`"roles":[{"role":{"tone":"Brand"},"token":"color.brand.base"},` +
	`{"role":{"named":"body-text"},"token":"color.surface"}],` +
	`"tokens":{"color":{"brand":{"base":{"$type":"color","$value":"#3b5bdb"}},` +
	`"surface":{"$type":"color","$value":"#ffffff"}},` +
	`"space":{"md":{"$type":"dimension","$value":"16px"}}}}`

func TestEncodePinsTheCanonicalBytesOfAManifest(t *testing.T) {
	if got := Encode(sampleManifest()); got != sampleBytes {
		t.Fatalf("encode(sample):\n got %s\nwant %s", got, sampleBytes)
	}
	// Encode∘Decode is the identity ON CANONICAL BYTES — the half that says the
	// emitted shape is one the decoder reads back without normalising anything.
	m, err := Decode(sampleBytes)
	if err != nil {
		t.Fatalf("canonical bytes decode: %v", err)
	}
	if got := Encode(m); got != sampleBytes {
		t.Fatalf("encode(decode(bytes)):\n got %s\nwant %s", got, sampleBytes)
	}
}

func TestEncodePinsTheCanonicalBytesOfAProjectedManifest(t *testing.T) {
	// The same tone-vars source TestProjectors projects — a manifest the package
	// could derive and never hand back.
	m := ProjectFromFuaranToneVars(":root { --fuaran-tone-brand-bg: #3b5bdb; --fuaran-tone-brand-fg: #fff; }")
	want := `{"roles":[{"role":{"tone":"Brand"},"token":"tone.brand.bg"}],` +
		`"tokens":{"tone":{"brand":{"bg":{"$type":"color","$value":"#3b5bdb"},` +
		`"fg":{"$type":"color","$value":"#fff"}}}}}`
	if got := Encode(m); got != want {
		t.Fatalf("encode(projected):\n got %s\nwant %s", got, want)
	}
}

// decodableSources is every manifest source the decoder tests cover, plus one
// carrying each invariant arm with its full payload — the Rust oracle's set.
func decodableSources() []string {
	return []string{
		`{"meta":{"name":"acme","version":"2.1","description":"x"},
		  "tokens":{"color":{"brand":{"base":{"$type":"color","$value":"#3b5bdb","$description":"brand"}},
		                     "surface":{"$type":"color","$value":"#ffffff"}}},
		  "roles":[{"role":{"tone":"Brand"},"token":"color.brand.base"}],
		  "invariants":[{"kind":"ContrastFloor","role":"Brand","minRatio":7,"weight":2}]}`,
		`{"color":{"accent":{"$type":"color","$value":"#ff8800"}}}`,
		`{"color":{"brand":{"$type":"color","$value":"#3b5bdb","$extensions":{"fuaran":{"role":"accent"}}}}}`,
		`{"tokens":{"a":{"$value":"1"}},
		  "invariants":[{"kind":"UsageBudget","token":"a","targetPct":12.5,"tolerancePct":2,"weight":0.25},
		                {"kind":"MotionVoice","maxDurationMs":240,"easing":"ease-out"},
		                {"kind":"MotionVoice"}]}`,
	}
}

func decodeOrFail(t *testing.T, src string) ThemeManifest {
	t.Helper()
	m, err := Decode(src)
	if err != nil {
		t.Fatalf("fixture decodes: %v (source: %s)", err, src)
	}
	return m
}

func TestDecodeOfEncodeIsTheIdentityOnEveryDecodedManifest(t *testing.T) {
	// A manifest Decode produced already carries its tokens in the wire's own
	// order, so the round trip is an exact model identity — no normalisation.
	for _, src := range decodableSources() {
		m := decodeOrFail(t, src)
		again := decodeOrFail(t, Encode(m))
		if !reflect.DeepEqual(again, m) {
			t.Fatalf("decode(encode(m)) differs\n got %+v\nwant %+v\nsource: %s", again, m, src)
		}
		// The parsed-value seam is the same inverse one layer down.
		raw, err := wire.ParseCanonical(Encode(m))
		if err != nil {
			t.Fatalf("encode emits parseable JSON: %v", err)
		}
		if viaValue := OfJSON(jsonish(raw)); !reflect.DeepEqual(viaValue, m) {
			t.Fatalf("ofJSON of the emitted tree differs\n got %+v\nwant %+v\nsource: %s", viaValue, m, src)
		}
	}
}

// jsonish re-shapes a wire.ParseCanonical result into the plain map/slice form
// OfJSON walks. ParseCanonical keeps numbers as json.Number, which asNum already
// accepts, so only containers need converting.
func jsonish(raw any) any {
	switch t := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = jsonish(v)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			out[i] = jsonish(v)
		}
		return out
	}
	return raw
}

func tokenPairs(m ThemeManifest) [][2]string {
	out := make([][2]string, 0, len(m.Tokens))
	for _, t := range m.Tokens {
		out = append(out, [2]string{t.Name, t.Value})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func TestEncodeIsAFixpointThroughTheRoundTrip(t *testing.T) {
	// A projector or Merge result carries tokens in first-appearance order and
	// the wire's order is sorted, so the round trip there normalises rather than
	// preserving order. The total statement covering every manifest is that
	// Encode is a fixpoint through it.
	base := ProjectFromCssCustomProperties(":root { --b: 2px; --a: 1px; --c: #fff; }")
	over := ProjectFromCssCustomProperties(":root { --b: 9px; }")
	cases := []ThemeManifest{
		base,
		over,
		Merge(base, over),
		ProjectFromFuaranToneVars(":root { --fuaran-tone-critical-bg: #c92a2a; --fuaran-tone-brand-bg: #3b5bdb; }"),
		decodeOrFail(t, sampleBytes),
		sampleManifest(),
		EmptyManifest,
	}
	for _, m := range cases {
		once := Encode(m)
		twice := Encode(decodeOrFail(t, once))
		if twice != once {
			t.Fatalf("encode of decode of encode differs\n got %s\nwant %s\nmodel %+v", twice, once, m)
		}
		// What the normalisation may NOT do is lose a token: the set of
		// (name, value) pairs survives even where the order does not.
		before := tokenPairs(m)
		after := tokenPairs(decodeOrFail(t, once))
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("token set lost through the round trip\n got %v\nwant %v", after, before)
		}
	}
}

func TestEncodeEmitsCanonicalJSON(t *testing.T) {
	// The host-neutral half of the claim, and the one a sibling host can check
	// without agreeing with this host about anything else: the output is a
	// fixpoint of the shared canonical encoder. Unsorted keys, a non-canonical
	// number or a stray escape all go red here.
	for _, src := range decodableSources() {
		bytes := Encode(decodeOrFail(t, src))
		raw, err := wire.ParseCanonical(bytes)
		if err != nil {
			t.Fatalf("encode emits parseable JSON: %v (%s)", err, bytes)
		}
		again, err := wire.EncodeValue(wire.ValueFromParsed(raw))
		if err != nil {
			t.Fatalf("re-encode: %v", err)
		}
		if again != bytes {
			t.Fatalf("not canonical:\n got %s\nwant %s", again, bytes)
		}
	}
}

func TestMembersTheDecoderToleratesTheAbsenceOfAreOmittedAtTheirDefault(t *testing.T) {
	// The empty manifest is `tokens` and nothing else — and `tokens` is never
	// omitted even when empty, because a top-level `tokens` key is what selects
	// the wrapper shape in OfJSON. Dropping it would decode as vanilla DTCG and
	// silently discard meta, roles and invariants.
	if got := Encode(EmptyManifest); got != `{"tokens":{}}` {
		t.Fatalf("encode(empty): got %s", got)
	}

	// A default-weight invariant carries no `weight`; a doubled one does.
	with := func(weight float64) ThemeManifest {
		return ThemeManifest{
			Meta:       AnonymousMeta,
			Invariants: []Invariant{{Kind: MotionVoice{Budget: MotionBudget{}}, Weight: weight}},
		}
	}
	if got := Encode(with(DefaultWeight)); got != `{"invariants":[{"kind":"MotionVoice"}],"tokens":{}}` {
		t.Fatalf("encode(default weight): got %s", got)
	}
	if got := Encode(with(2.0)); got != `{"invariants":[{"kind":"MotionVoice","weight":2}],"tokens":{}}` {
		t.Fatalf("encode(weight 2): got %s", got)
	}

	// An absent `role` decodes to NamedRole{""}, so that one binding omits it.
	anonymous := ThemeManifest{
		Meta:  AnonymousMeta,
		Roles: []RoleBinding{{Role: NamedRole{Name: ""}, TokenName: "t"}},
	}
	if got := Encode(anonymous); got != `{"roles":[{"token":"t"}],"tokens":{}}` {
		t.Fatalf("encode(anonymous role): got %s", got)
	}
	if round := decodeOrFail(t, Encode(anonymous)); !reflect.DeepEqual(round, anonymous) {
		t.Fatalf("decode(encode(anonymous)): got %+v want %+v", round, anonymous)
	}
}

// The model states the wire cannot carry. Each is reachable only by
// hand-building a ThemeManifest — no decoder or projector in the package
// produces one — so these are recorded negative results rather than defects:
// widening any of them is a wire-format question for every host at once.

func bareToken(name, value string) ManifestToken {
	return ManifestToken{Name: name, Value: value}
}

func TestCollidingTokenPathsResolveLastWriteWins(t *testing.T) {
	// A DTCG path addresses a group or a token, never both. The later write wins
	// — the precedence dedupeTokens and Merge already apply — in BOTH directions,
	// which is the half an implementation gets wrong: descending past a leaf must
	// clear it, or the EARLIER token would win instead.
	deeperLast := ThemeManifest{
		Meta:   AnonymousMeta,
		Tokens: []ManifestToken{bareToken("a", "1"), bareToken("a.b", "2")},
	}
	if got := Encode(deeperLast); got != `{"tokens":{"a":{"b":{"$value":"2"}}}}` {
		t.Fatalf("encode(deeper last): got %s", got)
	}
	if got := decodeOrFail(t, Encode(deeperLast)).Tokens; !reflect.DeepEqual(got, []ManifestToken{bareToken("a.b", "2")}) {
		t.Fatalf("decode(encode(deeper last)).Tokens: got %+v", got)
	}

	shallowerLast := ThemeManifest{
		Meta:   AnonymousMeta,
		Tokens: []ManifestToken{bareToken("a.b", "2"), bareToken("a", "1")},
	}
	if got := Encode(shallowerLast); got != `{"tokens":{"a":{"$value":"1"}}}` {
		t.Fatalf("encode(shallower last): got %s", got)
	}
	if got := decodeOrFail(t, Encode(shallowerLast)).Tokens; !reflect.DeepEqual(got, []ManifestToken{bareToken("a", "1")}) {
		t.Fatalf("decode(encode(shallower last)).Tokens: got %+v", got)
	}
}

func TestADollarPrefixedFirstSegmentIsUnreachableToTheDecoder(t *testing.T) {
	// walkTokens skips `$`-prefixed keys as DTCG metadata, so such a token is
	// emitted and then not read back. Escaping it would mint wire vocabulary this
	// host may not mint alone.
	m := ThemeManifest{Meta: AnonymousMeta, Tokens: []ManifestToken{bareToken("$meta", "x")}}
	if got := Encode(m); got != `{"tokens":{"$meta":{"$value":"x"}}}` {
		t.Fatalf("encode($meta): got %s", got)
	}
	if got := decodeOrFail(t, Encode(m)).Tokens; len(got) != 0 {
		t.Fatalf("decode(encode($meta)).Tokens: got %+v", got)
	}
}

func TestAToneOutsideTheCanonicalPaletteDecodesBackAsANamedRole(t *testing.T) {
	// parseRole validates the tone, so an unrecognised one is a named role on the
	// way back in. A NamedRole holding a VALID tone string is unaffected — it
	// travels on the `named` member and returns as itself.
	bogus := ThemeManifest{
		Meta:  AnonymousMeta,
		Roles: []RoleBinding{{Role: ToneRole{Tone: "Bogus"}, TokenName: "t"}},
	}
	round := decodeOrFail(t, Encode(bogus))
	if len(round.Roles) != 1 || !reflect.DeepEqual(round.Roles[0].Role, ManifestRole(NamedRole{Name: "Bogus"})) {
		t.Fatalf("decode(encode(bogus tone)).Roles: got %+v", round.Roles)
	}

	namedBrand := ThemeManifest{
		Meta:  AnonymousMeta,
		Roles: []RoleBinding{{Role: NamedRole{Name: "Brand"}, TokenName: "t"}},
	}
	if got := decodeOrFail(t, Encode(namedBrand)); !reflect.DeepEqual(got, namedBrand) {
		t.Fatalf("decode(encode(named Brand)): got %+v want %+v", got, namedBrand)
	}
}

func TestANilRoleAndANilInvariantKindAreTheGoOnlyUncarriedStates(t *testing.T) {
	// Go's zero values have no analogue in the Rust host's closed enums, so this
	// pair is this host's own addition to the recorded negative results. A nil
	// Role means the anonymous binding and is omitted as one; a nil Kind has no
	// discriminator to emit, and the decoder drops an invariant whose `kind` it
	// does not recognise.
	nilRole := ThemeManifest{Meta: AnonymousMeta, Roles: []RoleBinding{{TokenName: "t"}}}
	if got := Encode(nilRole); got != `{"roles":[{"token":"t"}],"tokens":{}}` {
		t.Fatalf("encode(nil role): got %s", got)
	}
	if got := decodeOrFail(t, Encode(nilRole)).Roles[0].Role; !reflect.DeepEqual(got, ManifestRole(NamedRole{Name: ""})) {
		t.Fatalf("decode(encode(nil role)).Role: got %+v", got)
	}

	nilKind := ThemeManifest{Meta: AnonymousMeta, Invariants: []Invariant{{Weight: DefaultWeight}}}
	if got := Encode(nilKind); got != `{"invariants":[{"kind":""}],"tokens":{}}` {
		t.Fatalf("encode(nil kind): got %s", got)
	}
	if got := decodeOrFail(t, Encode(nilKind)).Invariants; len(got) != 0 {
		t.Fatalf("decode(encode(nil kind)).Invariants: got %+v", got)
	}
}
