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

## Status — codec floor shipped

Shipped:

- **`canonical`** — the canonical-JSON primitives: the make-or-break number
  formatter (the `.NET "R"` float layout the wire form requires, corpus
  divergence-zone vectors under test) and the rule-6 string escaping.
- **`wire`** — the Node / TreeOp codec: `DecodeNode` / `EncodeNode` /
  `DecodeOp` / `EncodeOp` over a structural typed model with per-kind typed
  field schemas, a hand-written canonical encoder (Ordinal key sort, canonical
  number layout — never `encoding/json`), and the six-code decode-error
  envelope with `$`-rooted paths.
- **`conformance`** — the corpus certification legs: every node and TreeOp
  round-trip fixture re-encodes **byte-identically**, every reject fixture
  fails with the canonical code + path prefix, and a corpus-driven
  exhaustiveness guard names any discriminator the decoder does not recognise
  (the mitigation for Go's lack of compile-time totality over closed `$type`
  vocabularies). The harness skips cleanly when the repo is checked out alone.

The lenient-accept normalisation tier, tree-op apply engine, validator, and
server-HTML / hydration emission are roadmap work.

## Layout

```
fuaran-go/
├── go.mod
├── doc.go             # package doc + Version
├── canonical/         # canonical-JSON primitives — number form + string escaping
├── wire/              # Node / TreeOp codec + structural model + DecodeError envelope
├── conformance/       # shared-corpus certification: round-trip + reject + exhaustiveness legs
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
