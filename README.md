# fuaran-go

A **headless Go host of the Fuaran UI wire format** — a dependency-light,
idiomatic-Go reference implementation of the canonical-JSON contract a Go service
or AI orchestrator needs to read, write, and drive Fuaran UI trees.

`fuaran-go` is a **sibling reference implementation**, not a transpile of any
other host: it is built to the language-neutral wire-format specification
(`WIRE_FORMAT.md`) and certified against the shared conformance corpus.
Conformance to the spec is the contract; idiomatic Go is the deliverable.

## Why a Go host

Go is a first-class language for backends and, increasingly, AI agents — but it
has no idiomatic way to *produce* rich, dynamic UI without adopting a separate
JavaScript frontend. `fuaran-go` lets a Go service or orchestrator emit and
manipulate UI **as typed data**: the canonical wire tree. That tree is rendered
by any conformant client, so the interactivity lives server-side (or in a thin,
generic browser client) and the Go program never owns a frontend codebase.

Two server-friendly delivery modes fit a Go host especially well:

- **Static + partial hydration** — emit a mostly-static HTML page and hydrate
  only the interactive regions with a small, generic client bundle.
- **Server-driven** — hold the tree and its state in Go, stream tree-op diffs to
  a thin generic client, and let interactions round-trip to the server.

In both, Go stays runtime-free on rendering: it produces and drives; the browser
paints.

## Status — codec + apply + validator + renderer shipped

Shipped:

- **`canonical`** — the canonical-JSON primitives: the make-or-break number
  formatter (the `.NET "R"` float layout the wire form requires, corpus
  divergence-zone vectors under test) and the rule-6 string escaping.
- **`wire`** — the Node / TreeOp codec: `DecodeNode` / `EncodeNode` /
  `DecodeOp` / `EncodeOp` over a structural typed model with per-kind typed
  field schemas, a hand-written canonical encoder (Ordinal key sort, canonical
  number layout — never `encoding/json`), the six-code decode-error envelope
  with `$`-rooted paths, the §16 lenient AI-ingest shorthands, and the legacy
  container decode-upgrades.
- **`ops`** — the tree-op apply engine: `Apply(op, tree)` over the full 11-op
  algebra with typed, recoverable `ApplyError`s (never a panic), the §3.4
  nested `UpdateProp` path surface, and the `CanApply` dry-run
  (canApply ≡ apply success by construction).
- **`validator`** — the pre-emit, default-deny-by-shape structural validator:
  empty/duplicate ids, unrecognised kinds, missing wire-required slots, and
  the bounded-primitive advisory, with structured findings at `$`-rooted paths.
- **`renderer`** — the headless server-HTML renderer: a body-fragment HTML
  walk emitting the reference `fuaran-*` class vocabulary (parity-locked to
  the reference renderer, styled by the byte-copied reference CSS), the
  deterministic GFM markdown renderer (byte-pinned by the shared markdown
  corpus), the URL/markdown sanitiser floor, and **islands partial-hydration
  emission** (`RenderWithIslands`: per-island boundary wrappers + scoped
  hydrate payloads; zero islands ⇒ byte-identical to a plain render).
- **`conformance`** — the corpus certification legs: node/op round-trips
  byte-identical, rejects with the canonical code + path prefix,
  lenient-accept normalisation, apply-envelope conformance, the markdown
  corpus, class-vocabulary + reference-CSS parity, and the corpus-driven
  discriminator exhaustiveness guard. The harness skips cleanly when the repo
  is checked out alone.

The server-driven driver (the second interactivity tier) is roadmap work.

## Layout

```
fuaran-go/
├── go.mod
├── doc.go             # package doc + Version
├── canonical/         # canonical-JSON primitives — number form + string escaping
├── wire/              # Node / TreeOp codec + structural model + DecodeError envelope
├── ops/               # tree-op apply engine — Apply / CanApply + typed ApplyError
├── validator/         # pre-emit default-deny structural validator
├── renderer/          # server-HTML + markdown + sanitiser + islands emission + reference CSS
├── conformance/       # shared-corpus certification: all fixture legs + parity locks
├── run.ps1            # gofmt check -> go vet -> go build -> go test
├── LICENSE            # Apache-2.0
├── README.md
└── CLAUDE.md
```

## Build / verify

```powershell
.\run.ps1              # gofmt check -> go vet -> go build -> go test
.\run.ps1 -SkipTests   # fast-iteration switches: -SkipFormat / -SkipBuild / -SkipTests
```

Requires the Go toolchain (see `go.mod` for the pinned language floor). The
runtime host uses the **standard library only** — no third-party dependencies.

## Licence

Apache-2.0. See [`LICENSE`](LICENSE).
