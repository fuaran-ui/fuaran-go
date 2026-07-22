// Command rosetta-wasm is a small `GOOS=js GOARCH=wasm` entry over the existing
// fuaran-go codec (Phase 656). It exposes one JS-callable function that builds
// the public Rosetta parity demo's exemplar tree from six scalar "holes" and
// returns its canonical wire bytes — the Go host doing its own independent
// canonical encode, exactly as the F#, TypeScript, Python, and Rust hosts do.
//
// Compiling the codec to wasm is a build leg, not a posture change: this program
// renders nothing and holds no state — it is `holes -> canonical bytes`, a pure
// function of the six scalars run through `wire.EncodeNode`. The codec is
// stdlib-only; the wasm entry adds only `syscall/js` (stdlib) and `encoding/json`
// (stdlib, for parsing the six-scalar holes envelope).
//
// The tree builder + encoder live here (platform-neutral, unit-tested by
// rosetta_test.go on the host toolchain); the thin `syscall/js` registration
// lives in main_wasm.go (built only under js/wasm), with main_stub.go carrying an
// empty main so `go build ./...` / `go test ./...` stay green on every platform.
package main

import (
	"encoding/json"

	"github.com/fuaran-ui/fuaran-go/wire"
)

// holes are the six typed edit-points that parameterise the exemplar — the only
// data that crosses the boundary, mirroring the TS `Holes` interface and the
// Python/Rust hosts. Every host receives exactly these six scalars and builds
// its own tree.
type holes struct {
	LabelA string  `json:"labelA"`
	ValueA float64 `json:"valueA"`
	LabelB string  `json:"labelB"`
	ValueB float64 `json:"valueB"`
	LabelC string  `json:"labelC"`
	ValueC float64 `json:"valueC"`
}

// metric builds one default-styled Metric node. Only label and value are
// non-default, so the canonical encoder emits the bare `{"$type":"Metric",…}`
// form (omit-when-default, WIRE_FORMAT.md §3.6). The value is a Float so it takes
// the canonical shortest-round-trip number form every host shares.
func metric(id, label string, value float64) wire.Node {
	return wire.Node{
		ID: id,
		Kind: wire.Obj{Tag: "Metric", Fields: map[string]wire.Value{
			"label": wire.Str(label),
			"value": wire.Obj{Tag: "Static", Fields: map[string]wire.Value{
				"value": wire.Float(value),
			}},
		}},
	}
}

// flexBox builds a flex Box node with the given axis / wrap / role / heading /
// children.
func flexBox(id, direction string, wrap bool, role string, heading string, children ...wire.Value) wire.Node {
	fields := map[string]wire.Value{
		"children": wire.Arr(children),
		"layout": wire.Obj{Tag: "Flex", Fields: map[string]wire.Value{
			"direction": wire.Str(direction),
			"wrap":      wire.Bool(wrap),
		}},
		"role": wire.Str(role),
	}
	if heading != "" {
		fields["heading"] = wire.Str(heading)
	}
	return wire.Node{ID: id, Kind: wire.Obj{Tag: "Box", Fields: fields}}
}

// exemplar builds the signature-bearing tree every Rosetta host reproduces: a
// dashboard Box (heading + a horizontal three-metric strip).
func exemplar(h holes) wire.Node {
	strip := flexBox(
		"rosetta-strip", "Horizontal", true, "Group", "",
		metric("rosetta-m-a", h.LabelA, h.ValueA),
		metric("rosetta-m-b", h.LabelB, h.ValueB),
		metric("rosetta-m-c", h.LabelC, h.ValueC),
	)
	return flexBox("rosetta-root", "Vertical", false, "Dashboard", "Revenue snapshot", strip)
}

// encodeFromHoles parses the six-scalar holes JSON, builds the exemplar tree, and
// returns its canonical wire bytes. A malformed holes envelope or an encode
// failure returns a non-nil error.
func encodeFromHoles(holesJSON string) (string, error) {
	var h holes
	if err := json.Unmarshal([]byte(holesJSON), &h); err != nil {
		return "", err
	}
	return wire.EncodeNode(exemplar(h))
}
