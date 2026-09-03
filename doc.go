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
// Status: the full headless host stack is shipped — the wire codec
// (byte-identical round-trip, six-code rejects, lenient-accept normalisation),
// the ops apply engine (the 11-op reducer with typed recoverable errors and a
// dry-run), the pre-emit validator (default-deny by shape), the renderer
// (server-HTML with the reference class vocabulary, the corpus-pinned
// deterministic markdown renderer, and islands partial-hydration emission),
// and the server-driven driver (a transport-neutral live channel — SSE + a
// stdlib WebSocket backend — carrying canonical TreeOp frames with per-frame
// Seq and reconnect-replay).
package fuarango

// Version is the pre-release version of the fuaran-go host.
//
// 0.0.2-alpha carries the Phase 1168 BREAKING change to the DAG record surface:
// dag.Record's bare UserID becomes the typed Actor, and pre-1144 DAG content
// addresses do not carry forward. Recorded in README.md — this host declares no
// STABILITY.md.
const Version = "0.0.2-alpha"
