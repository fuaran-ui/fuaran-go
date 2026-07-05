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
// Status: stage-0 bootstrap. The canonical number formatter (the canonical
// package) is the first shipped brick; the node/op codec, apply engine, and
// validator are roadmap work (the "floor" tier). Nothing here claims a working
// codec yet.
package fuarango

// Version is the pre-release version of the fuaran-go host.
const Version = "0.0.1-alpha"
