package wire

// The structural typed model for decoded wire documents.
//
// A decoded document is a small typed tree over the closed Value interface —
// the Go analogue of a sum type: one implementation per JSON-shape case, closed
// by the unexported isValue method so no case can be added outside this
// package. The int-vs-float wire distinction survives a round-trip because the
// two are distinct cases (WIRE_FORMAT.md rule 5 lays them out differently).
//
// The model is deliberately structural — a tagged Obj carries any "$type"
// case's fields — rather than one struct per NodeKind / Binding / Action case:
// what a conformant host must preserve is exactly the structure the canonical
// encoder needs to reproduce byte-identical output. Typed per-kind field
// validation lives in the decoder's kind schemas (decode.go); per-case struct
// enrichment is a follow-up, mirroring the other wire-format reference hosts.
// Go gives no compile-time exhaustiveness over the "$type" vocabularies, so
// the conformance corpus plus the corpus-driven exhaustiveness guard in the
// conformance package are the safety net (see CLAUDE.md).

// Value is one decoded wire value: a string, integer, float, boolean, null, an
// ordered array, a (possibly "$type"-tagged) object, or a Node envelope.
type Value interface{ isValue() }

// Str is a JSON string value.
type Str string

// Int is a JSON number that was written as an integer literal (no decimal
// point, no exponent). It re-encodes as a plain decimal (rule 5).
type Int int64

// Float is a JSON number that was written with a decimal point or exponent.
// It re-encodes in the canonical shortest-round-trip layout (rule 5).
type Float float64

// Bool is a JSON boolean.
type Bool bool

// Null is a JSON null. The wire model has no null in structured positions
// (rule 12); it survives only inside the sanctioned obj-erased seams (a
// Binding.Static payload, WIRE_FORMAT.md §5).
type Null struct{}

// Arr is an ordered array of wire values (rule 3: source order is preserved).
type Arr []Value

// Obj is a JSON object: a "$type"-discriminated DU case when Tag is non-empty
// (e.g. "Static", "Literal", "Metric"), or a plain record when Tag is "".
// Fields never contains the "$type" key when Tag is set; the encoder re-merges
// it under the canonical Ordinal key sort (where "$" sorts first).
type Obj struct {
	Tag    string
	Fields map[string]Value
}

func (Str) isValue()   {}
func (Int) isValue()   {}
func (Float) isValue() {}
func (Bool) isValue()  {}
func (Null) isValue()  {}
func (Arr) isValue()   {}
func (Obj) isValue()   {}
