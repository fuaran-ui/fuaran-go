// Package idl is this host's projection of the wire vocabulary declared in the
// conformance corpus's idl.json (WIRE_FORMAT.md section 13): every kind, field,
// union, enum, record, op and omit-at-default value, as Go data.
//
// # The ruling this package implements (Phase 2297)
//
// The vocabulary is declared ONCE, in the corpus's idl.json, and the structural
// layer of every host is a projection of it. For this host the projection is
// produced by a host-owned generator over the PUBLISHED idl.json — not by a
// target added to another host's toolchain, and not by sharing codec code with
// any other host. The hosts stay siblings: the IDL is language-neutral data, and
// reading it with a small generator of this host's own is not a transpile.
//
// What is generated (idl_gen.go here, and ../kindarms_gen.go in package wire):
// the kind, enum, union, record and op rows with their field tables
// (optionality, omit-at-default values in canonical wire form, child-slot and
// binding-slot types), and the per-kind decoder arm set with its dispatch.
//
// What stays hand-written: decode policy (leniency, aliases, shape coercions),
// refusal messages, relational and limit checks, behaviour-bearing encoding,
// rendering, and everything the IDL marks hostSurface (host-language
// declarations that carry nothing observable on the wire).
//
// # Placement and the change-set
//
// The generator lives in ./internal/gen and is run by `go generate ./wire/idl`
// (the directive below). It reads idl.json through internal/corpusroot — the
// one locator every corpus leg in this module uses, so FUARAN_WIRE_FIXTURES
// points it at an in-flight corpus exactly as it points the conformance suite.
// The generated file records the SHA-256 of the idl.json bytes it was produced
// from (SourceSHA256).
//
// Two tests keep the projection honest (drift_test.go): one regenerates from
// the corpus and compares byte-for-byte with the committed output, the other
// compares the recorded hash with the corpus's. Both are red the moment
// idl.json moves and this host has not been regenerated, and both skip on a
// standalone checkout with no corpus. A vocabulary change is therefore one
// regeneration here, plus whatever behaviour the change genuinely adds.
//
//go:generate go run ./internal/gen
package idl

// Optionality is a field's optionality class (idl.json `optionality.$type`).
type Optionality uint8

const (
	// Required: the field is always present on the wire.
	Required Optionality = iota + 1
	// Optional: the field may be absent; absence is its own meaning.
	Optional
	// OmitDefault: absent on the wire exactly when it equals Field.Default.
	OmitDefault
	// HostOnly: a host-language slot that never reaches the wire.
	HostOnly
)

func (o Optionality) String() string {
	switch o {
	case Required:
		return "required"
	case Optional:
		return "optional"
	case OmitDefault:
		return "omitDefault"
	case HostOnly:
		return "hostOnly"
	}
	return "unknown"
}

// TypeKind is a field type's shape (idl.json `type.$type`).
type TypeKind uint8

const (
	TStr TypeKind = iota + 1
	TBool
	TInt
	TFloat
	TJSON
	TNode
	TKind
	TOp
	TList
	TMap
	TEnum
	TUnion
	TRecord
	TFn
	THosted
	TVar
)

// Type is one field type. Name is set for enum, union, record and var types;
// Args carries a parameterised union's type arguments (Binding<bool>); Elem is
// a list's or a map's element type, or the wire type a hosted value travels as;
// Wire is a token carrier — a fn's closure sentinel, or "json" for a hosted
// value carried as structured JSON.
type Type struct {
	Kind TypeKind
	Name string
	Args []Type
	Elem *Type
	Wire string
}

// Field is one row of a field table. Default is the canonical wire JSON of the
// omit-at-default value, set only when Optionality is OmitDefault.
type Field struct {
	Name        string
	Optionality Optionality
	Default     string
	Type        Type
}

// Kind is one NodeKind row.
type Kind struct {
	Tag      string
	Category string
	Fields   []Field
}

// Enum is one bare-string enum. HostCases are host-language spellings of the
// same cases where they differ from the wire tokens; they never reach the wire.
type Enum struct {
	Name      string
	Cases     []string
	HostCases []string
}

// Case is one union case. InProcessOnly marks a case that cannot survive the
// wire (its payload is a host closure).
type Case struct {
	Tag           string
	Fields        []Field
	InProcessOnly bool
}

// Union is one "$type"-discriminated union. TransparentCase names the case
// whose canonical encoding is its bare payload (TextSource's Literal).
type Union struct {
	Name            string
	Params          []string
	TransparentCase string
	Cases           []Case
}

// Record is one record shape.
type Record struct {
	Name   string
	Fields []Field
}

// Op is one TreeOp row.
type Op struct {
	Tag    string
	Fields []Field
}
