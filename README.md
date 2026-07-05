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

## Status — stage-0 bootstrap

Shipped:

- **`canonical`** — the make-or-break canonical number formatter (the `.NET "R"`
  float layout the wire form requires), with the corpus divergence-zone vectors
  under test.
- **`conformance`** — the corpus harness wiring: it locates the shared
  `wire-format-fixtures` corpus and skips cleanly when the repo is checked out
  alone.
- **`wire`** — the codec surface and the six-code decode-error envelope (bodies
  stubbed).

The node/op codec, tree-op apply engine, validator, and server-HTML / hydration
emission are roadmap work (the "floor" tier and beyond). Nothing here claims a
working codec yet.

## Layout

```
fuaran-go/
├── go.mod
├── doc.go             # package doc + Version
├── canonical/         # canonical-JSON primitives — number form (shipped), key sort + escaping (floor)
│   └── float.go
├── wire/              # Node / TreeOp codec surface + DecodeError envelope (bodies = floor)
├── conformance/       # shared-corpus certification harness (smoke leg shipped)
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
