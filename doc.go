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
// Status: the headless host tiers are shipped — the wire codec (byte-identical
// round-trip, six-code rejects, lenient-accept normalisation), the ops apply
// engine (the 11-op reducer with typed recoverable errors and a dry-run), the
// pre-emit validator (default-deny by shape), and the renderer (server-HTML
// with the reference class vocabulary, the corpus-pinned deterministic
// markdown renderer, and islands partial-hydration emission). The
// server-driven driver is roadmap work.
package fuarango

// Version is the pre-release version of the fuaran-go host.
const Version = "0.0.1-alpha"
