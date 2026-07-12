// Package fuarango is the Go host of the Fuaran UI wire format — a
// dependency-light, idiomatic-Go reference implementation of the canonical-JSON
// contract a Go service or AI orchestrator needs to read, write, and drive
// Fuaran UI trees.
//
// fuaran-go is a sibling reference implementation, not a transpile of any other
// host: it is built to the language-neutral wire-format specification
// (WIRE_FORMAT.md) and certified against the shared conformance corpus. See
// README.md and CLAUDE.md.
//
// Status: the codec floor is shipped. The wire package decodes and encodes
// Node and TreeOp documents byte-identically to the shared conformance corpus
// (every node/op round-trip fixture) and surfaces the six canonical decode
// error codes with $-rooted paths (every reject fixture). The lenient-accept
// normalisation tier, tree-op apply engine, validator, and server-side
// emission are roadmap work.
package fuarango

// Version is the pre-release version of the fuaran-go host.
const Version = "0.0.1-alpha"
