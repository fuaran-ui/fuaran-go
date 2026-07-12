# CLAUDE.md — fuaran-go (Go reference implementation)

This repo is the **Go host of the Fuaran UI wire format** — a **co-equal sibling
to the F# (`Fuaran.UI`), TypeScript (`@fuaran-ui/*`), and Python (`fuaran_py`)
tiers**. Its identity is a **headless host and driver**: the canonical-JSON codec,
a tree-op apply engine, a pre-emit validator, and server-side emission
(static-HTML + partial-hydration + server-driven), all conformant to the shared
wire format. What ships **today** is the codec floor — the canonical
codec (`DecodeNode` / `EncodeNode` / `DecodeOp` / `EncodeOp`), certified
byte-identical against the shared corpus round-trip + reject families; the
lenient-accept tier, apply engine, validator, and server emission are roadmap
work.

**Framing — load-bearing, do not regress.** The emission surface is the
**canonical JSON wire format, for every host**. The language tiers are
**human-developer authoring surfaces** that produce that JSON; Go's role is
primarily a **headless backend/orchestrator host** — a Go service reads, writes,
and *drives* wire trees, and a conformant client renders them. Go having no good
native way to produce dynamic UI is a reason this host is *valuable*, not a reason
to make it a lesser artefact than the other tiers.

This repo sits under the Fuaran-UI sub-estate at `../`, alongside the `fuaran`,
`fuaran-ts`, and `fuaran-py` tiers. Cross-repo conventions (port allocation, Sync
All, the formatting mandate, the language-baseline pinning, the OSS publication
boundary) live in the workspace `CLAUDE.md` (`../../../CLAUDE.md`) and the
Fuaran-UI sub-estate `CLAUDE.md` (`../CLAUDE.md`). Read those first.

## Posture

- **Apache 2.0 from day one** — same posture as `fuaran-ts` / `fuaran-py`, to make
  the reference-implementation claim unambiguous.
- **Sibling reference implementation, not a transpile.** `fuaran-go` is built to
  the language-neutral wire-format spec (`../fuaran/docs/WIRE_FORMAT.md`) + the
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
├── conformance/          # shared-corpus certification: round-trip + reject + exhaustiveness legs
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
(`../fuaran/docs/WIRE_FORMAT.md`) with the workspace-level
`../wire-format-fixtures/` corpus as the executable conformance suite. `fuaran-go`
is one conformant host: it must round-trip the corpus byte-for-byte and surface
the canonical reject code + path for every malformed fixture. The **forward-
coupling rule** (`WIRE_FORMAT.md` §11) means a new `NodeKind` / `Spec` / `TreeOp`
/ `Binding` / `Action` case must move every host in one change — `fuaran-go` is now
one of those hosts.

### Conformance coverage (codec floor)

The codec runs the corpus **round-trip + reject families green**: every
`node-round-trip` / `op-round-trip` fixture re-encodes byte-identically, and
every `reject` fixture fails with the canonical code + `$`-rooted path prefix
(fixture counts drift as the corpus grows — `../wire-format-fixtures/manifest.json`
is the authoritative enumeration). The decoder is the strict canonical tier
plus the legacy container decode-upgrades (`Stack` / `GridLayout` / `Dashboard`
/ `Card` → `Box`; `Table` → static `DataGrid`) the reject contract exercises;
typed field-level validation covers the common kinds, and
recognised-but-not-yet-typed kinds decode structurally (still byte-exact on
round-trip). The **exhaustiveness guard** (`conformance.TestDiscriminatorExhaustiveness`)
is the no-compile-time-totality mitigation: it walks the corpus and fails
naming any kind/op discriminator the decoder tables do not recognise. The
`lenient-accept`, envelope, and elicitation families land with their own
roadmap tiers (`conformance` locates `../wire-format-fixtures/` and skips when
absent).

## Interactivity — server-friendly delivery for a headless host

A Go host does not render UI itself; it *produces* and *drives* wire trees that a
conformant client renders. Two client tiers suit a Go server especially well and
keep the browser client generic (the Go program authors no client code):

- **Static + partial hydration** — Go emits a mostly-static HTML page; per
  interactive region it emits a boundary marker + a per-region hydrate payload
  (a wire subtree the codec produces); a small generic client hydrates only those
  regions. Interactions then run client-side.
- **Server-driven** — Go holds the tree + state, applies tree-ops in response to
  client events, and streams frame diffs over a transport-neutral channel to a
  thin generic client; interactions round-trip to Go. The server side is
  render-runtime-free.

Both are beyond the stage-0 floor (they need the codec + apply engine + a Go
server-HTML emitter / a channel transport) but are the headline reason a Go host
earns its place — see `README.md`.

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
