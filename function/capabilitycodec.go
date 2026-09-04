// capabilitycodec.go — the canonical wire codec for a capability DECLARATION.
//
// The declaration is what travels: a capability's id, its signature (holes,
// value spaces, effect class), its determinism tag and its placement. The BODY
// never travels — a host resolves it locally by id, which is what keeps the
// wire form portable and a registry a trust boundary rather than a code channel.
//
// Canonical form, so two hosts emit the same bytes for the same declaration:
// object keys are Ordinal-sorted at every level (`$type`, U+0024, sorts before
// every lower-case data key, so a discriminated object always leads with it),
// strings use the canonical escape (only `"`, `\` and the C0 controls), and
// floats use the one pinned float layout this host already ships. Encoding is
// therefore a FUNCTION of the declaration, and DecodeDeclaration ∘
// EncodeDeclaration is the identity on bytes for any declaration this host
// accepts — the property the shared law vectors assert.
//
// Decoding is order-TOLERANT (it looks fields up by name) and total: a
// malformed or unrecognised declaration yields a named error, never a panic.
// One cross-check is deliberate rather than incidental: the wire `determinism`
// tag must agree with the signature's effect determinism. The tag is derivable
// from the signature, so ignoring it on decode would let a tampered or
// divergent-host payload key the replay seam under a determinism its signature
// does not declare, silently.
package function

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/fuaran-ui/fuaran-go/canonical"
)

// ─── canonical rendering ────────────────────────────────────────────────────
//
// A rendered member is a (key, already-rendered value) pair; jobj sorts by key
// before joining, which is the whole of the canonical key-order rule. Go string
// comparison is byte-wise, which is Ordinal.

type jmember struct {
	key string
	val string
}

func jobj(members ...jmember) string {
	sort.SliceStable(members, func(i, j int) bool { return members[i].key < members[j].key })
	var sb strings.Builder
	sb.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(canonical.EscapeString(m.key))
		sb.WriteByte(':')
		sb.WriteString(m.val)
	}
	sb.WriteByte('}')
	return sb.String()
}

func jarr(items ...string) string { return "[" + strings.Join(items, ",") + "]" }

func jstr(s string) string { return canonical.EscapeString(s) }

func jint(n int64) string { return strconv.FormatInt(n, 10) }

func jbool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func jfloat(f float64) string {
	// The declaration carries no non-finite bound: a range whose bound is NaN
	// or an infinity is not a space anything can be validated against, and the
	// decoder refuses one, so the encoder never has to spell a sentinel here —
	// which is exactly the precondition FormatFiniteDouble states. The negative
	// zero collapse is that function's, not restated here.
	return canonical.FormatFiniteDouble(f)
}

// ─── encode ─────────────────────────────────────────────────────────────────

func encodeSpaceDecl(s *Space) (string, error) {
	if s == nil {
		return "", errors.New("cannot encode a nil value-space")
	}
	switch s.Kind {
	case "intRange":
		return jobj(
			jmember{"$type", jstr("intRange")},
			jmember{"min", jint(int64(s.Min))},
			jmember{"max", jint(int64(s.Max))},
		), nil
	case "floatRange":
		return jobj(
			jmember{"$type", jstr("floatRange")},
			jmember{"min", jfloat(s.Min)},
			jmember{"max", jfloat(s.Max)},
		), nil
	case "stringLen":
		return jobj(
			jmember{"$type", jstr("stringLen")},
			jmember{"min", jint(int64(s.Min))},
			jmember{"max", jint(int64(s.Max))},
		), nil
	case "enum":
		values := make([]string, len(s.Choices))
		for i, c := range s.Choices {
			values[i] = jstr(c)
		}
		return jobj(
			jmember{"$type", jstr("enum")},
			jmember{"values", jarr(values...)},
		), nil
	case "anyString":
		return jobj(jmember{"$type", jstr("anyString")}), nil
	default:
		return "", errors.New("unknown value-space kind: " + s.Kind)
	}
}

func encodeEffect(e EffectClass) string {
	return jobj(
		jmember{"host", jstr(e.Host)},
		jmember{"determinism", jstr(e.Determinism)},
	)
}

func encodeSigEntry(e SigEntry) (string, error) {
	members := []jmember{
		{"addr", jstr(e.Addr)},
		{"name", jstr(e.Name)},
		{"kind", jstr(e.Kind)},
		{"required", jbool(e.Required)},
	}
	if e.Space != nil {
		rendered, err := encodeSpaceDecl(e.Space)
		if err != nil {
			return "", err
		}
		members = append(members, jmember{"space", rendered})
	}
	if e.Slot != "" {
		members = append(members, jmember{"slotKind", jstr(e.Slot)})
	}
	if e.ActionEffect != nil {
		members = append(members, jmember{"actionEffect", encodeEffect(*e.ActionEffect)})
	}
	return jobj(members...), nil
}

func encodeSignature(sg Signature) (string, error) {
	holes := make([]string, len(sg.Holes))
	for i, h := range sg.Holes {
		rendered, err := encodeSigEntry(h)
		if err != nil {
			return "", err
		}
		holes[i] = rendered
	}
	return jobj(
		jmember{"name", jstr(sg.Name)},
		jmember{"effect", encodeEffect(sg.Effect)},
		jmember{"holes", jarr(holes...)},
	), nil
}

func encodePlacement(p Placement) (string, error) {
	switch p.Kind {
	case PlacementBuildTime, PlacementServer, PlacementClientDeclarative, PlacementPrecomputed:
		return jobj(jmember{"$type", jstr(p.Kind)}), nil
	case PlacementClientIsland:
		switch p.Island {
		case IslandPyodide, IslandFable, IslandJS:
			return jobj(
				jmember{"$type", jstr(PlacementClientIsland)},
				jmember{"island", jstr(p.Island)},
			), nil
		default:
			return "", errors.New("unknown island kind: " + p.Island)
		}
	default:
		return "", errors.New("unknown placement: " + p.Kind)
	}
}

// EncodeDeclaration renders a capability declaration in canonical form.
func EncodeDeclaration(c Capability) (string, error) {
	signature, err := encodeSignature(c.Signature)
	if err != nil {
		return "", err
	}
	placement, err := encodePlacement(c.Placement)
	if err != nil {
		return "", err
	}
	return jobj(
		jmember{"$type", jstr("capability")},
		jmember{"id", jstr(c.ID)},
		jmember{"signature", signature},
		jmember{"determinism", jstr(c.Determinism)},
		jmember{"placement", placement},
	), nil
}

// ─── decode ─────────────────────────────────────────────────────────────────

func objField(obj map[string]any, name string) (map[string]any, error) {
	raw, ok := obj[name]
	if !ok {
		return nil, errors.New("missing field: " + name)
	}
	o, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("field is not an object: " + name)
	}
	return o, nil
}

func strField(obj map[string]any, name string) (string, error) {
	raw, ok := obj[name]
	if !ok {
		return "", errors.New("missing field: " + name)
	}
	s, ok := raw.(string)
	if !ok {
		return "", errors.New("field is not a string: " + name)
	}
	return s, nil
}

func numField(obj map[string]any, name string) (float64, error) {
	raw, ok := obj[name]
	if !ok {
		return 0, errors.New("missing field: " + name)
	}
	n, ok := raw.(json.Number)
	if !ok {
		return 0, errors.New("field is not a number: " + name)
	}
	f, err := n.Float64()
	if err != nil {
		return 0, errors.New("field is not a finite number: " + name)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, errors.New("field is not a finite number: " + name)
	}
	return f, nil
}

func decodeSpaceDecl(el map[string]any) (*Space, error) {
	tag, err := strField(el, "$type")
	if err != nil {
		return nil, err
	}
	switch tag {
	case "intRange", "floatRange", "stringLen":
		lo, err := numField(el, "min")
		if err != nil {
			return nil, err
		}
		hi, err := numField(el, "max")
		if err != nil {
			return nil, err
		}
		return &Space{Kind: tag, Min: lo, Max: hi}, nil
	case "enum":
		raw, ok := el["values"]
		if !ok {
			return nil, errors.New("missing field: values")
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, errors.New("field is not an array: values")
		}
		choices := make([]string, len(items))
		for i, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, errors.New("enum values must be strings")
			}
			choices[i] = s
		}
		return &Space{Kind: "enum", Choices: choices}, nil
	case "anyString":
		return &Space{Kind: "anyString"}, nil
	default:
		return nil, errors.New("unknown value-space kind: " + tag)
	}
}

func decodeEffect(el map[string]any) (EffectClass, error) {
	host, err := strField(el, "host")
	if err != nil {
		return EffectClass{}, err
	}
	switch host {
	case HostPure, HostReadsHost, HostWritesHost:
	default:
		return EffectClass{}, errors.New("unknown host effect: " + host)
	}
	det, err := strField(el, "determinism")
	if err != nil {
		return EffectClass{}, err
	}
	switch det {
	case DeterminismDeterministic, DeterminismClock, DeterminismRandom, DeterminismNetwork:
	default:
		return EffectClass{}, errors.New("unknown determinism: " + det)
	}
	return EffectClass{Host: host, Determinism: det}, nil
}

func decodeSigEntry(el map[string]any) (SigEntry, error) {
	addr, err := strField(el, "addr")
	if err != nil {
		return SigEntry{}, err
	}
	name, err := strField(el, "name")
	if err != nil {
		return SigEntry{}, err
	}
	kind, err := strField(el, "kind")
	if err != nil {
		return SigEntry{}, err
	}
	rawRequired, ok := el["required"]
	if !ok {
		return SigEntry{}, errors.New("missing field: required")
	}
	required, ok := rawRequired.(bool)
	if !ok {
		return SigEntry{}, errors.New("field is not a boolean: required")
	}

	entry := SigEntry{Addr: addr, Name: name, Kind: kind, Required: required}

	if raw, present := el["space"]; present {
		o, ok := raw.(map[string]any)
		if !ok {
			return SigEntry{}, errors.New("field is not an object: space")
		}
		space, err := decodeSpaceDecl(o)
		if err != nil {
			return SigEntry{}, err
		}
		entry.Space = space
	}
	if raw, present := el["slotKind"]; present {
		s, ok := raw.(string)
		if !ok {
			return SigEntry{}, errors.New("field is not a string: slotKind")
		}
		entry.Slot = s
	}
	if raw, present := el["actionEffect"]; present {
		o, ok := raw.(map[string]any)
		if !ok {
			return SigEntry{}, errors.New("field is not an object: actionEffect")
		}
		effect, err := decodeEffect(o)
		if err != nil {
			return SigEntry{}, err
		}
		entry.ActionEffect = &effect
	}
	return entry, nil
}

func decodeSignature(el map[string]any) (Signature, error) {
	name, err := strField(el, "name")
	if err != nil {
		return Signature{}, err
	}
	effectObj, err := objField(el, "effect")
	if err != nil {
		return Signature{}, err
	}
	effect, err := decodeEffect(effectObj)
	if err != nil {
		return Signature{}, err
	}
	rawHoles, ok := el["holes"]
	if !ok {
		return Signature{}, errors.New("missing field: holes")
	}
	items, ok := rawHoles.([]any)
	if !ok {
		return Signature{}, errors.New("field is not an array: holes")
	}
	holes := make([]SigEntry, len(items))
	for i, item := range items {
		o, ok := item.(map[string]any)
		if !ok {
			return Signature{}, errors.New("a signature hole is not an object")
		}
		entry, err := decodeSigEntry(o)
		if err != nil {
			return Signature{}, err
		}
		holes[i] = entry
	}
	return Signature{Name: name, Holes: holes, Effect: effect}, nil
}

func decodePlacement(el map[string]any) (Placement, error) {
	tag, err := strField(el, "$type")
	if err != nil {
		return Placement{}, err
	}
	switch tag {
	case PlacementBuildTime, PlacementServer, PlacementClientDeclarative, PlacementPrecomputed:
		return Placement{Kind: tag}, nil
	case PlacementClientIsland:
		island, err := strField(el, "island")
		if err != nil {
			return Placement{}, err
		}
		switch island {
		case IslandPyodide, IslandFable, IslandJS:
			return Placement{Kind: PlacementClientIsland, Island: island}, nil
		default:
			return Placement{}, errors.New("unknown island kind: " + island)
		}
	default:
		return Placement{}, errors.New("unknown placement: " + tag)
	}
}

// DecodeDeclaration parses a canonical capability declaration. Total: every
// malformed input yields a named error.
func DecodeDeclaration(s string) (Capability, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return Capability{}, errors.New("not JSON: " + err.Error())
	}
	el, ok := raw.(map[string]any)
	if !ok {
		return Capability{}, errors.New("a capability declaration must be an object")
	}

	id, err := strField(el, "id")
	if err != nil {
		return Capability{}, err
	}
	signatureObj, err := objField(el, "signature")
	if err != nil {
		return Capability{}, err
	}
	signature, err := decodeSignature(signatureObj)
	if err != nil {
		return Capability{}, err
	}

	// The wire tag is derivable from the signature, so a disagreement is a
	// tampered or divergent payload rather than a spelling difference — refuse
	// it by name instead of silently preferring the signature.
	expected := signature.Effect.Determinism
	wireTag, err := strField(el, "determinism")
	if err != nil {
		return Capability{}, err
	}
	if wireTag != expected {
		return Capability{}, errors.New(
			"capability determinism disagrees with signature effect: wire '" + wireTag +
				"' vs signature '" + expected + "'")
	}

	placementObj, err := objField(el, "placement")
	if err != nil {
		return Capability{}, err
	}
	placement, err := decodePlacement(placementObj)
	if err != nil {
		return Capability{}, err
	}

	return Capability{
		ID:          id,
		Signature:   signature,
		Determinism: expected,
		Placement:   placement,
	}, nil
}
