# fuaran-go

A **headless Go host of the Fuaran UI wire format** — a dependency-light,
idiomatic-Go reference implementation of the canonical-JSON contract a Go service
or AI orchestrator needs to read, write, and drive Fuaran UI trees.

`fuaran-go` is a **sibling reference implementation**, not a transpile of any
other host: it is built to the language-neutral wire-format specification
(`WIRE_FORMAT.md`) and certified against the shared conformance corpus.
Conformance to the spec is the contract; idiomatic Go is the deliverable.

## Get started

```sh
go get github.com/fuaran-ui/fuaran-go
```

Author a UI tree as typed data, then encode it byte-identically to every host:

```go
import "github.com/fuaran-ui/fuaran-go/wire"

tree := wire.Node{
	ID: "root",
	Kind: wire.Obj{Tag: "Box", Fields: map[string]wire.Value{
		"children": wire.Arr{
			wire.Node{ID: "title", Kind: wire.Obj{Tag: "Heading", Fields: map[string]wire.Value{
				"level": wire.Int(2), "text": wire.Str("Channel performance"), "variant": wire.Str("Standard"),
			}}},
		},
		"layout": wire.Obj{Tag: "Auto"},
		"role":   wire.Str("Dashboard"),
	}},
}

wireJSON, _ := wire.EncodeNode(tree)   // canonical wire JSON, byte-identical to every host
```

Render it server-side with `renderer.RenderHTML(tree, nil)`. Full walkthrough —
author → encode → render → playground: <https://fuaran-ui.io/get-started/go>.

## Why a Go host

Go is a first-class language for backends and, increasingly, AI agents — but it
has no idiomatic way to *produce* rich, dynamic UI without adopting a separate
JavaScript frontend. `fuaran-go` lets a Go service or orchestrator emit and
manipulate UI **as typed data**: the canonical wire tree. That tree is rendered
by any conformant client, and **go emits complete static output** — its
static-HTML and islands emission resolve compute (`Transform` bindings, a
preselected `Selection`'s default) at render time (Phase 651), so a page's
computed values are correct before any JS runs. **Live interactivity stays
client-side** (islands hydration, or a thin generic client for the server-driven
mode); the Go program never owns a frontend codebase.

Two server-friendly delivery modes fit a Go host especially well:

- **Static + partial hydration** — emit a mostly-static HTML page whose values
  are already resolved (correct-before-hydration), and hydrate only the
  interactive regions with a small, generic client bundle.
- **Server-driven** — hold the tree and its state in Go, stream tree-op diffs to
  a thin generic client, and let interactions round-trip to the server.

In both, Go emits complete static output and drives; live interactivity is the
client's. `render(tree, data) → bytes` stays a pure function — no UI session
state, no server-side interaction handling.

## Status — full headless host stack shipped

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
- **`serverdriven`** — the server-driven interactivity tier: a driver that
  holds the tree + state, validates each client event default-deny by shape,
  runs the host handler for the TreeOps, applies them (Phase-415 apply) to keep
  the tree authoritative, and pushes the applied ops as a canonical **TreeOp
  frame** over the transport-neutral `Channel` seam. Ships an in-memory
  reference channel, an SSE backend, and a **stdlib-only WebSocket backend**
  (hand-written RFC 6455 handshake + frame codec — no third-party module). Each
  frame carries a per-connection `Seq`; a bounded replay buffer re-pushes
  frames newer than the client's last-applied `Seq` on reconnect
  (`Resync`). A conformant client re-renders by applying the frame's TreeOps —
  the Go host authors no client code.
- **`conformance`** — the corpus certification legs: node/op round-trips
  byte-identical, rejects with the canonical code + path prefix,
  lenient-accept normalisation, apply-envelope conformance, the markdown
  corpus, class-vocabulary + reference-CSS parity, and the corpus-driven
  discriminator exhaustiveness guard. The harness skips cleanly when the repo
  is checked out alone.

Every roadmap tier for this host (codec → apply → validator → renderer →
server-driven driver) is now shipped.

## Bound-grid posture — completeness

A `DataGrid` bound to data renders its **rows**, server-side. The `source` is
resolved through the same render-time compute path every other bound slot uses
(a `Transform` pipeline is evaluated by the certified `dataframe` evaluator; a
`Selection` / `Filter` default resolves), and the resolved rows are emitted as
the reference grid's own `<table class="fuaran-grid">` markup — the same element
shape and class vocabulary a conformant client renders.

That last part is what makes it more than a nicety here. The islands emission's
mismatch-freedom property holds because an island boundary's static children are
byte-identical to what the client re-renders into the wrapper; a placeholder
where the client draws a table is markup the client must *replace*, not attach
to. And on a genuinely no-JS surface — an email digest, an ops report, a crawler
— a row count is all the reader ever gets, while the host had the rows in hand.

One boundary remains, and it is declared rather than incidental. A column
projects its cell either **declaratively**, by `field` (a row property name that
rides the wire), or through a **host closure** (`value`) — and a closure does not
survive serialisation; it decodes as an opaque sentinel. So:

| Bound grid | Rendered |
|---|---|
| at least one `field`-projected column, source resolves to rows | the rows, as a `fuaran-grid` table (closure-projected cells empty) |
| no `field`-projected column (including no columns at all) | the `[Grid: N rows — hydrates client-side]` placeholder, with `N` the *resolved* row count |
| source does not resolve to rows | the same placeholder |

Rich cell kinds (`TonedPill`, `Checkbox`, `Link`, `Progress`, …) render their
**text** projection — this host's inert server semantics for every interactive
node, not a special case for grids. The line stays where Phase 651 drew it:
`render(tree, data) → bytes` is a pure function, the rendered grid is inert
markup, and sorting / paging / editing remain the client's. Pinned by the
bound-grid tests in `renderer/transform_test.go`.

## Chart-lowering posture — require-pre-lowered

A resolved `Drawing` node renders as first-party inline SVG on every host, this
headless one included. A raw `Chart` node is a *semantic* wire kind that must be
*lowered* to a `Drawing` before it can paint. `fuaran-go` takes the
**require-pre-lowered** posture: it does **not** lower `Chart → Drawing` in-host.
A `Chart` reaching the SSR boundary renders a **documented typed passthrough** — a
marked client-hydration placeholder (`fuaran-chart-ssr-placeholder` carrying
`data-fuaran-ssr-placeholder="Chart"`, a `data-fuaran-row-count`, and a visible
`[Chart: N rows — hydrates client-side]` fallback), **never a silent empty region**.

A go SSR consumer that wants a *rendered* chart either pre-lowers the `Chart` to a
`Drawing` upstream (which this renderer then paints as inline SVG), or lets a
conformant client render the emitted wire. This is the cheap posture and it fits the
host's headless-orchestrator role: go emits static output but paints no client-library
visualisation in-host (a chart hydrates client-side), so in-host lowering
would earn no pixel here — unlike the `fuaran-rs` WASM client, which *does* lower
in-host so its browser renderer reaches chart parity. The posture is contract, not
accident: it is pinned by `TestChartRequiresPreLoweredPosture` (`renderer/render_test.go`).
Demand-gated — revisit only if a go SSR consumer needs in-host lowering.

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
├── serverdriven/      # server-driven driver + transport-neutral channel (SSE + stdlib WebSocket)
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
