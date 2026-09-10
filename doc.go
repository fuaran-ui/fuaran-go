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
// 0.0.6-alpha carries the Phase 1667 COMPILE-BREAKING change to the renderer's
// exported entry points: renderer.RenderHTML and renderer.RenderHTMLWithEgress
// answer (string, error) where they answered a bare string. The error is a
// binding-resolution error and rides ALONGSIDE the HTML rather than instead of
// it — the render is a pure function of the tree and always completes — and it
// is non-nil only where the document asked for something no decoded tree can
// answer: today exactly Binding.Computed, which WIRE_FORMAT.md §5 says resolves
// to an error naming its replacements and never to a value. Classify it with
// errors.Is against renderer.ErrDecodedComputed. renderer.RenderWithIslands and
// RenderWithIslandsAndEgress are UNCHANGED in shape — they already answered
// (string, error) — and now report the same class through it. An unevaluable
// pipeline (an unbound Transform param, an ambiguous non-1×1 scalar result) is
// deliberately NOT reported: that is the renderer unable to answer, it renders
// as the slot's empty state exactly as before, and keeping the error narrow is
// what makes it worth checking.
//
// It advances the version rather than riding 0.0.5-alpha because the class is
// higher: that entry describes a re-encode difference every caller still
// compiles against, and this one does not compile.
//
// 0.0.5-alpha carries the Phase 1585 WIRE-VISIBLE change to TabsSpec.activeIndex,
// the second half of the same phase: the member is omit-at-default at the
// identity Static 0, so this host stops emitting it for a tab strip opening on
// its first tab and drops it on decode when a document spells it out. Every
// pre-1585 document still decodes to the same tree — absence already meant
// Static 0 here — but it now re-encodes one member shorter, so a caller
// comparing its own stored bytes against a fresh encode of the same tab strip
// will see the difference. Any other binding is unaffected, including a Static
// carrying a different index and every State / Filter / Selection / Query form.
//
// 0.0.4-alpha carries the Phase 1585 WIRE-VISIBLE change to ChartSpec.stacked:
// the member is omit-at-default (false), so this host stops emitting it for a
// grouped chart and drops it on decode when a document spells it out. Every
// pre-1585 document still decodes to the same tree — absence already meant
// false here — but it now re-encodes one member shorter, so a caller comparing
// its own stored bytes against a fresh encode of the same chart will see the
// difference. Charts with stacked true are unaffected.
//
// 0.0.3-alpha is ADDITIVE over 0.0.2-alpha: the ChartSpec.annotations slot and
// its three wire refusals (a non-finite address, an unparseable event date, an
// unordered pair). A pre-1490 document decodes and re-encodes byte-for-byte as
// before, and the require-pre-lowered chart posture is unmoved.
//
// 0.0.2-alpha carries the Phase 1168 BREAKING change to the DAG record surface:
// dag.Record's bare UserID becomes the typed Actor, and pre-1144 DAG content
// addresses do not carry forward. Recorded in README.md — this host declares no
// STABILITY.md.
const Version = "0.0.6-alpha"
