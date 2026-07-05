package wire

import "errors"

// errNotImplemented marks the codec surface the stage-0 bootstrap declares but
// has not yet built. The node/op codec is the roadmap "floor" tier; see CLAUDE.md
// and the fuaran roadmap fuaran-go bootstrap phase.
var errNotImplemented = errors.New("fuaran-go: not implemented (stage-0 bootstrap; codec is roadmap floor work)")

// Node is the typed UI tree node. The stage-0 bootstrap carries only the stable
// identity field; the closed per-kind model lands with the codec.
//
// NodeKind (the closed $type discriminator) will be modelled as an interface plus
// one struct per case — the TS/Python-proven shape for a language without sum
// types. Exhaustiveness is not enforced by the compiler here; the conformance
// corpus and an exhaustiveness linter are the safety net (see CLAUDE.md).
type Node struct {
	ID string
	// Kind NodeKind — not yet defined.
}

// DecodeNode decodes canonical wire JSON into a Node. Stub — see errNotImplemented.
func DecodeNode(canonicalJSON string) (Node, error) {
	return Node{}, errNotImplemented
}

// EncodeNode re-encodes a Node to canonical wire JSON, byte-identical to the
// shared corpus. Stub — see errNotImplemented.
func EncodeNode(n Node) (string, error) {
	return "", errNotImplemented
}
