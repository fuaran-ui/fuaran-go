# Demo parity — capability matrix (`fuaran-go`)

"Demo parity" means: **every mechanic the Fuaran UI demos exercise can run in this host** — not the
authoring shell, but the underlying wire/substrate operation (decode, apply, render, verify a chain,
merge, evaluate a transform, introspect, contrast-audit, teleport, diff).

This is the Go host's own matrix, the sibling of `fuaran-rs/DEMO-PARITY.md`. It maps each capability
to the package that carries it and to the artefact that gates it.

**What a status means here — read this before reading a cell.**

| Mark | Meaning |
|---|---|
| ✅ | the package is shipped and carries its own test suite in this repo; where a shared-corpus leg or a cross-host oracle exists, the **Certified against** column names it |
| ◑ | shipped under a declared posture that is narrower than the reference host's, written up in `README.md` |
| ⛔ | deliberately absent — out of this host's charter, not a gap |

A ✅ is therefore a claim about **code plus a gate**, not a claim that some demo was walked through
by hand. It is what this file can honestly assert from inside the repo, and it is the claim the
`run.ps1` gate re-checks on every commit.

## Capability → status

| Capability | Status | Package | Certified against |
|---|---|---|---|
| `codec` (decode/encode wire trees + ops) | ✅ | `wire/` | shared-corpus node/op round-trips byte-identical, reject code + `$`-rooted path, lenient-accept normalisation; discriminator-exhaustiveness guard; generated hostile-input fuzz + native `testing.F` targets |
| `canonical-json` (number form, rule-6 escaping, Ordinal key sort) | ✅ | `canonical/` | the corpus divergence-zone float vectors + per-primitive unit suites |
| `apply` (tree-op apply engine + dry-run) | ✅ | `ops/` | apply-envelope leg over the corpus op fixtures + the `CanApply` ≡ apply-success law; placement algebra + clone verbs |
| `validator` (pre-emit structural defects) | ✅ | `validator/` | per-rule fire / stay-silent suites + the declared rule-coverage pin (`validator-coverage.json`, machine-checked against what the validator actually raises) |
| `render` (server-HTML + markdown + sanitiser + islands + ambient egress policy) | ✅ | `renderer/` | markdown corpus byte-pin, class-vocabulary + reference-CSS parity locks, sanitisation leg; the render-fidelity obligation roster (every declared claim has a checker, no exemptions), with `style.direction`'s five §3.1 rules also swept over a corpus tree of every roster kind; real controls for the `Rating` / `Color` / `Tokens` form fields, each pinned by tests that go red on the bare-input floor |
| `server-driven` (driver + transport-neutral channel + reconnect replay) | ✅ | `serverdriven/` | driver / channel / SSE / stdlib-WebSocket suites + the state-seeding leg |
| `opstream-hashchain` (SHA-256 chain, verify, replay, sink) | ✅ | `opstream/` | chain corpus golden, byte-identical |
| `dag-record` (DAG record wire form) | ✅ | `dag/` | `dag/` corpus fixtures, both actor cases |
| `merge-dag` (3-way tree merge) | ✅ | `merge/` | merge-conformance corpus (tree + outcome) |
| `tree-diff` (before→after op-script) | ✅ | `diff/` | diff leg over the corpus |
| `transform-eval` (`Binding.Transform` dataframe pipeline) | ✅ | `dataframe/` | dataframe + list-param legs; canonical round-trip |
| `function-capability` (declared capability surface + codec) | ✅ | `function/` | capability-laws leg |
| `elicitation` (§18 question-as-UI + typed answer contract) | ✅ | `elicitation/` | shared `elicitation/` corpus family |
| `versioning-envelope` (§15 profile / Unknown tolerance) | ✅ | `wire/envelope.go` | envelope leg — negotiation + degrade-and-preserve |
| `teleport` (§17 deflate + base64url + SHA-256 envelope) | ✅ | `teleport/` | byte-exact string round-trip + digest-tamper / version / oversize rejects — this host's own suite; it does not read the corpus's `teleport/` family |
| `style-observe` (resolved-style legibility flags + manifest fidelity + usage budgets) | ✅ | `styleobserver/` | style-observer corpus legs + ported sibling-host cases, byte-identical |
| `theme-manifest` (decode / project / merge / **encode**) | ✅ | `thememanifest/` | decode + projector + merge suites; **encode** byte-pinned to the `fuaran-rs` oracle — `decode∘encode` the identity on a decoded manifest, `encode` a fixpoint through the round trip |
| `ai-tools` (introspection / structural search / default-deny dispatch) | ✅ | `aitools/` | ai-tools leg over the corpus |
| `chart-lowering` (`Chart` → `Drawing`) | ◑ | `renderer/` | require-pre-lowered posture — a raw `Chart` is refused rather than lowered in-host; chart-lowering leg. The posture is contract, not a gap; see `README.md` |
| `client interactive render` | ⛔ | — | headless by charter: `render(tree, data) → bytes` is a pure function and this host authors no client code |

## Theme manifest — where the encode half came from

The theme-manifest row is the one whose **encode** half has a cross-host oracle rather than a corpus
family, so it is worth stating how it is held.

`fuaran-rs` was the first host to emit a theme manifest (Phase 1725). Having no sibling bytes to
assert against, it pinned hand-written canonical byte literals in its `tests/manifest.rs` as the
portable oracle — deliberately not a recording of its own output, since a byte pin whose recorder is
the code under test pins nothing. This host is the second emitter (Phase 1728): the literals in
`thememanifest/encode_test.go` are **copied verbatim** from that file, never re-derived here.

Two consequences follow, and both are load-bearing:

- A disagreement between these bytes and this host's output is a **real cross-host divergence**, not
  a fixture that needs refreshing.
- A change to one of those literals is a **wire-format change for every host**, and belongs in the
  shared specification before it belongs in any host's test.

The round trip is stated precisely in `Encode`'s own doc comment, including the four model states
the wire cannot carry (colliding token paths, a `$`-prefixed first segment, a tone outside the
canonical palette, and Go's nil `Role` / nil `Invariant.Kind`) — each reachable only by
hand-building a manifest, and each recorded rather than hidden.

## What this matrix does not claim

- It is not a demo-by-demo walk-through. It measures capability presence and the gate that holds it.
- A ⛔ is a charter line, not a backlog item. Live interactivity stays client-side by design.
- The corpus is the oracle for everything it covers. Where a row cites "own suite", nothing
  cross-host is being asserted — only that this host's behaviour is pinned against itself.

## Changelog

_2026-09-22 (Phase 1728) — file created. This host had no matrix of its own; the rows above are
derived from the packages present and the suites that gate them. The `theme-manifest` row lands
carrying its **encode** half: `thememanifest.Encode`, byte-pinned to the `fuaran-rs` oracle. Additive
throughout — no capability changed status, and no existing row was rewritten._

_2026-09-23 (Phase 1838) — the `render` row's **Certified against** cell now names the render
obligations it already carried: the render-fidelity roster gate, `style.direction` held on every
roster kind rather than on the two the obligation checkers build, and the `Rating` / `Color` /
`Tokens` controls (shipped in Phase 1677) with the floor-reddening tests that hold them. No status
changed; the row was under-describing its gate, not over-claiming it._
