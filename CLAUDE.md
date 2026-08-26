# CLAUDE.md — fuaran-go (Go reference implementation)

This repo is the **Go host of the Fuaran UI wire format** — a **co-equal sibling
to the F# (`Fuaran.UI`), TypeScript (`@fuaran-ui/*`), and Python (`fuaran_py`)
tiers**. Its identity is a **headless host and driver**: the canonical-JSON codec,
a tree-op apply engine, a pre-emit validator, and server-side emission
(static-HTML + partial-hydration + server-driven), all conformant to the shared
wire format. What ships **today** is the full stack: the canonical codec
(round-trip + reject + lenient-accept certified), the tree-op apply engine
(`ops.Apply` / `ops.CanApply`), the pre-emit validator, the server-HTML
renderer with islands partial-hydration emission (class-vocabulary +
reference-CSS + markdown corpus parity-locked), and the server-driven driver
(`serverdriven` — a transport-neutral live channel over SSE + a stdlib-only
WebSocket backend, carrying canonical TreeOp frames with per-frame `Seq` +
reconnect-replay).

**Framing — load-bearing, do not regress.** The emission surface is the
**canonical JSON wire format, for every host**. The language tiers are
**human-developer authoring surfaces** that produce that JSON; Go's role is
primarily a **headless backend/orchestrator host** — a Go service reads, writes,
and *drives* wire trees. Post-651 charter wording (operator decision 2026-07-22):
**go emits complete static output** — its static-HTML and islands emission
resolve compute (`Transform` bindings, `Selection.defaultValue`) at render time
(Phase 651), so a page's computed values are correct before any JS runs (and
genuinely no-JS surfaces — email digests, ops reports — are complete). A
data-bound `DataGrid` renders its **rows** on the same reasoning (the
completeness posture; the declared boundary — a closure-projected column, which
cannot survive the wire — is written up in the README). **Live
interactivity stays client-side** (islands hydration, or the server-driven
driver's thin client). The line that does NOT move: go stays a **library, not a
runtime** — `render(tree, data) → bytes` is a pure function; no UI session state,
no server-side interaction handling, no lifecycle. Go having no good native way
to produce dynamic UI is a reason this host is *valuable*, not a reason to make it
a lesser artefact than the other tiers.

This repo sits alongside the `fuaran`, `fuaran-ts`, and `fuaran-py` tiers as a co-equal
conformant host. Cross-repo development conventions (port allocation, formatting, language-baseline pinning) live at the maintainers' workspace level and are not shipped here.

## Posture

- **Apache 2.0 from day one** — same posture as `fuaran-ts` / `fuaran-py`, to make
  the reference-implementation claim unambiguous.
- **Sibling reference implementation, not a transpile.** `fuaran-go` is built to
  the language-neutral wire-format spec (`../fuaran-dotnet/docs/WIRE_FORMAT.md`) + the
  conformance corpus (`../wire-format-fixtures/`), not generated from any other
  tier. There is no Go transpile path (the F# host's Fable backend targets JS /
  Python, not Go) and none is wanted — the hard part (the canonical number form)
  is hand-written for every host regardless.
- **Wire-format conformance is the stability contract.** The codec must
  encode / decode byte-identically against the shared corpus and surface the
  canonical reject code + `$`-rooted path for every malformed fixture — certified
  the same way the F#, TypeScript, and Python hosts are.
- **Dependency-light.** The runtime host uses the Go standard library only.
  Third-party packages appear only as dev tooling if ever.

## Language baseline

Go **1.26+** (pinned in `go.mod` — the Go analogue of the workspace's F#-10 /
.NET-10 pinning; the `fuaran-ts` / `fuaran-py` siblings pin their own runtimes the
same way). Model the language's closed DUs (`NodeKind`, `Spec`, `TreeOp`,
`Binding`, `Action`, …) as an **interface + one struct per `$type` case** — the
TS/Python-proven shape for a language without sum types. Go gives **no
compile-time exhaustiveness** on those type-switches, so the conformance corpus
plus an exhaustiveness linter are the safety net (the one real regression vs the
F# host — a discipline cost, not a blocker).

## Layout

```
fuaran-go/
├── doc.go                # package doc + Version
├── canonical/            # canonical-JSON primitives — number form (float.go) + string escaping (escape.go)
├── wire/                 # Node / TreeOp codec: structural model + canonical encoder + decoders + DecodeError envelope
├── ops/                  # tree-op apply engine — Apply / CanApply, typed ApplyError, §3.4 nested paths
├── validator/            # pre-emit default-deny structural validator (shared codes + $-rooted paths)
├── renderer/             # server-HTML + GFM markdown + sanitiser + ambient egress policy + islands + reference-CSS byte-copy
├── serverdriven/         # server-driven driver + transport-neutral Channel (in-memory / SSE / stdlib WebSocket) + reconnect-replay
├── conformance/          # shared-corpus certification: all fixture legs + parity locks
├── go.mod
├── run.ps1               # Stage-0 entry point — gofmt check + go vet + go build + go test
├── LICENSE               # Apache 2.0 + Diametrical Ltd copyright
├── README.md
└── CLAUDE.md
```

## Build / verify pipeline

```powershell
.\run.ps1                 # gofmt -l check + go vet + go build ./... + go test ./...
.\run.ps1 -SkipTests      # switches: -SkipFormat / -SkipBuild / -SkipTests
```

Or drive the toolchain directly: `gofmt -l .`, `go vet ./...`, `go build ./...`,
`go test ./...`.

## Formatting mandate

The workspace formatting mandate (Fantomas for F#, Prettier for TS, ruff for
Python) maps here to **gofmt** — every commit is preceded by `gofmt -w` over the
changed files. The `run.ps1` gate is `gofmt -l .` (any listed file fails the gate).

## Wire format

The canonical wire format is owned by the F# `fuaran` tier
(`../fuaran-dotnet/docs/WIRE_FORMAT.md`) with the workspace-level
`../wire-format-fixtures/` corpus as the executable conformance suite. `fuaran-go`
is one conformant host: it must round-trip the corpus byte-for-byte and surface
the canonical reject code + path for every malformed fixture. The **forward-
coupling rule** (`WIRE_FORMAT.md` §11) means a new `NodeKind` / `Spec` / `TreeOp`
/ `Binding` / `Action` case must move every host in one change — `fuaran-go` is now
one of those hosts.

### Conformance coverage

The codec runs the corpus **round-trip + reject + lenient-accept families
green**: every `node-round-trip` / `op-round-trip` fixture re-encodes
byte-identically, every `reject` fixture fails with the canonical code +
`$`-rooted path prefix, and every `lenient-accept` shorthand (the verbose
`Literal` envelope — since 0.2.0 the bare string IS the canonical form — the
value/field-name aliases, the shape coercions, the omitted-when-default
explicit forms, legacy container tags, the pre-typed opaque/null Static
forms, the Transform frame/pipeline/expr shorthands) normalises to its
canonical bytes (fixture counts drift as the corpus grows —
`../wire-format-fixtures/manifest.json` is the authoritative enumeration). Typed field-level validation covers the common kinds;
recognised-but-not-yet-typed kinds decode structurally (still byte-exact on
round-trip). The **exhaustiveness guard** (`conformance.TestDiscriminatorExhaustiveness`)
is the no-compile-time-totality mitigation: it walks the corpus and fails
naming any kind/op discriminator the decoder tables do not recognise. Beyond
the wire families, the conformance package also runs the **apply-envelope**
leg (corpus op fixtures folded over base trees + the canApply ≡ apply-success
law), the **markdown corpus** (`markdown/corpus.json`, byte-pinned), and the
**renderer parity locks** (emitted class vocabulary ⊆ the reference renderer
source; the shipped reference CSS byte-equal to the canonical artefact — both
skip on a standalone checkout). The envelope and elicitation families land
with their own roadmap tiers (`conformance` locates `../wire-format-fixtures/`
and skips when absent).

### Decoder robustness fuzz (`wire/decoderfuzz_test.go`)

The refusal contract — decoding is TOTAL, so a malformed or hostile input yields a structured typed
error, never a panic and never a hang — asserted against **generated** hostile input rather than
against the curated reject corpus alone. A curated corpus is evidence about the author's
imagination.

**Two harnesses, and the pairing is the point.** `TestDecoderFuzz` drives a deterministic
(SplitMix64) stream over the five named input families, so the classes are covered reproducibly on
every `go test`. `FuzzDecodeNode` / `FuzzDecodeOp` are native `testing.F` targets over the same entry
points, **seeded from that same generated stream** — so a plain `go test` replays the seed corpus and
covers the same families, while `go test -fuzz=FuzzDecodeNode -fuzztime=10m ./wire` adds
coverage-guided mutation. That second half is why this leg is a native harness rather than a port of
another host's generator: the toolchain ships coverage guidance and a port would throw it away.

- **The bounded run IS the PR gate** — it landed in a package `go test ./...` already runs, so no
  workflow change was needed and the CI quarantine patterns do not touch it.
- **The long run + its machine-readable record:** `FUARAN_FUZZ_LONG=1 FUARAN_FUZZ_ITERATIONS=250000
  FUARAN_FUZZ_EVIDENCE=<file> go test ./wire -run TestDecoderFuzz -timeout 90m`.
- **The go-red self-test is permanent, and so is its inverse** — five mutants, one per invariant,
  each asserted to be PARTIAL.
- **The allocation invariant is measured, not proxied, where it bites.**
  `TestDecoderFuzzAllocationBudget` reads `runtime.MemStats` over the pathological family;
  `ReadMemStats` stops the world, so the main stream carries the cheap output-amplification proxy
  instead. Neither half is the whole invariant.
- **This host's hostile alphabet carries invalid UTF-8** — a WTF-8 lone surrogate, a bare
  continuation byte, `0xFF` — because a Go string is a byte sequence. Two of the sibling hosts
  cannot hold those inputs at all, so this is coverage only this leg has, not a difference to
  smooth away.
- **The `duplicate-key` mutator and the `NaN` / `Infinity` / `1e999` / `+1` tokens are generated
  deliberately, and nothing here asserts which answer is right.** Those are §20 "Decode
  determinism", landed PROPOSED and not yet ratified; crash-freedom on them is in scope, agreement
  is not.

`cmd/refusal-report` is the companion emitter: this host's refusal class for every reject fixture, as
JSON, for a cross-host runner that compares the hosts to each other rather than each to the corpus in
isolation.

## Destination policy — ambient, default-deny

Every URL this host emits is checked against a typed destination policy
(`WIRE_FORMAT.md` §14.1) before it is written: the `Link` href
(`EgressHyperlink`, including when `download` is set), the `Image` src
(`EgressMedia`), and every link / image inside a markdown body (via the
policy-taking `MarkdownToHTMLWithEgress`). The policy is a field on the
per-render context — never a package-level variable, which would be
non-reentrant under concurrent server renders.

**`RenderHTML` / `RenderWithIslands` default to `DenyNonLocalEgress()` — no
caller opt-in.** A wider posture is reached BY NAME through
`RenderHTMLWithEgress` / `RenderWithIslandsAndEgress`, so `grep -i permissive`
finds every widening in the host's own source. The one-call seam a new emission
site adopts is `policy.SanitizeURLForEgress(class, url)`: it returns the URL to
emit plus the refusal attribute pairs to splice, and replacing a
`SanitizeURLOrBlank` call with it IS the adoption.

**The declared divergences from the reference host — no route class, no grid
link column, the `download` class choice, the two seams' different spelling of
an unsafe URL, and the protected-email consequence — are written up in
`README.md` ("Destination policy — ambient default-deny").** Keep them there:
that file ships, and a divergence a reader cannot find is indistinguishable from
a defect.

## Interactivity — server-friendly delivery for a headless host

A Go host emits **complete static output** — the static-HTML and islands paths
resolve compute at render time (Phase 651), so computed values are correct before
any JS runs — and *drives* wire trees that a conformant client then makes live.
Live interactivity stays client-side; the Go program authors no client code and
holds no per-user UI state between calls (`render(tree, data) → bytes` is pure).
Two client tiers suit a Go server especially well and keep the browser client
generic:

- **Static + partial hydration** — Go emits a mostly-static HTML page whose
  values are already resolved (correct-before-hydration; hydration may re-resolve,
  never first-fill); per interactive region it emits a boundary marker + a
  per-region hydrate payload (a wire subtree the codec produces); a small generic
  client hydrates only those regions. Interactions then run client-side.
- **Server-driven** — Go holds the tree + state, applies tree-ops in response to
  client events, and streams frame diffs over a transport-neutral channel to a
  thin generic client; interactions round-trip to Go. The server side is
  render-runtime-free.

Both are shipped. **Server-driven frame contract — a Go-host divergence worth
knowing:** where the F# host lowers each step to browser DOM patches (it owns a
server-side HTML-fragment renderer keyed to `data-fuaran-node-id`), the Go
`serverdriven` frame carries the **canonical `TreeOp` list itself**
(`{"ops":[…],"seq":N}`), not lowered DOM patches. A conformant client
re-renders by applying those ops with the same apply engine every host ships —
so "the generic client already exists" holds and the Go host authors no client
code (true to the headless identity + the phase's dependency note). The
transport is behind the `Channel` seam (the `IFuaranLiveChannel` analogue); the
per-frame `Seq` + bounded replay buffer + `Resync(lastSeq)` are the
reconnect-replay contract, transport-agnostic across the in-memory / SSE /
stdlib-WebSocket backends. **stdlib-only WebSocket:** the RFC 6455 handshake
(SHA-1 + base64) and the text/close/ping/pong frame codec are hand-written in
`wsframe.go` — the no-third-party mandate rules out `gorilla`/`x/net/websocket`,
and the codec is small + unit-tested (incl. the RFC accept-key vector + the
client-mask unmask path).

## Cross-repo dependencies

No upstream dependency on any other sibling. At test time it reads the
workspace-relative corpus at `../wire-format-fixtures/` (skipped when absent, so
the repo is standalone-testable). It produces a Go module, not a NuGet pack — the
workspace `pack-all.ps1` treats it as a no-op.

## Public vocabulary discipline

`fuaran-go` is OSS-public (Apache 2.0). Per the workspace OSS publication
boundary, **shipped artefacts** (source, README, package metadata) reference only
"the Fuaran UI wire format" generically — never a private sibling / package name,
a commercial product name, or a strategic-command name. The specific banned list
lives in the workspace OSS publication boundary doc, not here. This `CLAUDE.md`
lives in the public repo, so it observes the same boundary — it names no private
sibling, package, product, or command.
