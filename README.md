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
  corpus), the URL/markdown sanitiser floor, the **ambient default-deny
  destination policy** (§14.1 — every emitted `href` / `src`, in a node or a
  markdown body, checked with no caller opt-in; see below), and **islands
  partial-hydration emission** (`RenderWithIslands`: per-island boundary
  wrappers + scoped hydrate payloads; zero islands ⇒ byte-identical to a plain
  render).
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

## Destination policy — ambient default-deny

A rendered URL is checked against a typed **destination policy** before it is
emitted (`WIRE_FORMAT.md` §14.1). The scheme floor answers "is this URL safe to
*have*"; the policy answers "is this destination one the composition
*declared*", and only the second closes exfiltration:
`https://collector.example/?s=<bound state>` passes every scheme check —
allowlisted scheme, well-formed host, no script anywhere — and an `<img src>`
pointed at it is contacted with **no user act at all**, because rendering *is*
the request.

**The policy is ambient, and the default is deny.** `RenderHTML` and
`RenderWithIslands` render under `DenyNonLocalEgress()`: a decoded tree may
point at its own origin and nowhere else, with no caller opt-in. A decoded tree
is untrusted input and an emission cannot declare its own egress (a policy an
emission can supply is one a hostile emission can widen), so absent a host's
declaration it gets none.

A host that needs a wider posture reaches for it **by name**:

```go
// Declares WHAT may be reached and FOR WHAT; default-deny for everything else.
policy := renderer.DenyNonLocalEgress().
    AllowOrigin(renderer.HostSuffix("cdn.example"), renderer.EgressMedia).
    AllowOrigin(renderer.ExactHost("docs.example"), renderer.EgressHyperlink)

html := renderer.RenderHTMLWithEgress(tree, sources, policy)
// islands: renderer.RenderWithIslandsAndEgress(tree, sources, islands, policy)
```

`renderer.PermissiveEgress()` permits every destination — correct for a
hand-authored tree where the author is the trust boundary, wrong for anything
that renders a decoded one. Naming it is deliberate: a grep for `permissive`
finds every host that widened its egress, in that host's own source.

A refused destination renders `about:blank#fuaran-egress-refused` plus a
`data-fuaran-egress-refused="<class>:<host>"` marker, so the refusal is visible
in the document rather than only in the logs. The marker value carries the class
and the host (or the scheme, or `local`) and **never the path or the query** —
the query string of a refused exfiltration attempt is the payload itself.

Four things about this host's adoption are declared rather than incidental:

- **No route class.** `EgressRoute` exists in the policy vocabulary, but this
  host is headless and static: it has no navigation runtime, emits no
  tree-declared route as a URL, and renders every action-bearing node inert. So
  there is no route call site to check here. The class stays in the vocabulary
  because a policy must be able to *speak* about a route it hands to a client.
- **No grid link column.** A bound `DataGrid` renders rich cell kinds
  (including `Link`) as their **text** projection — this host's inert server
  semantics — so no `href` is emitted from a grid cell and there is no
  egress call site there. If a grid cell ever emits a real `href`, it takes the
  `EgressHyperlink` class, like every other hyperlink.
- **A `download` link is still a hyperlink.** `EgressHyperlink` is the class
  even when `download` is set. The class names the **sink the browser reaches**,
  and flipping one boolean on a tree must not change which rule applies.
- **The two seams spell an UNSAFE url differently, on purpose.** At a node call
  site (`Link` href, `Image` src) a URL the *scheme floor* refuses renders the
  inert refusal URL plus an `unsafe-url` marker; inside a **markdown body** it
  keeps the bare `about:blank` it has always emitted. The markdown bytes are
  pinned by a cross-host corpus, and re-spelling them inside a change about
  egress would churn a shared contract — which is where a real divergence
  hides. The floor's own answer (`SanitizeURL` / `SanitizeURLOrBlank`) is
  unchanged either way.

One consequence worth stating because it surprises: a **protected email link**
(`protection: "email"`) needs a policy that permits non-network destinations.
`mailto:` is an egress channel — a body parameter carries arbitrary text off the
machine — and it has no host a rule can name, so `DenyNonLocalEgress()` refuses
it and the link renders as an ordinary refused anchor with **no address in the
output at all**. The narrowest widening is `DenyNonLocalEgress()` with
`AllowNonNetwork` set.

The **island hydrate payload is deliberately unfiltered**: it is the island
subtree's canonical wire JSON — the tree, not a rendering of it — and the client
that consumes it is a conformant host applying its own policy at its own
emission sites. Rewriting a destination inside the payload would hand the client
a tree that no longer round-trips.

Pinned by the corpus legs in `conformance/render_test.go`
(`TestMarkdownCorpus` certifies the seam; `TestMarkdownCorpusAmbient` and
`TestAmbientEgressAtTheNodeCallSites` certify that the ordinary entry points
*reach* it with no policy named) and the policy tests in
`renderer/egress_test.go`.

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

## Server-driven hand-off — what the driver hands a client, and what it never holds

The bound-grid section above ends on "sorting / paging / editing remain the
client's". That sentence is easy to read as a limitation of the *renderer*. It is
not: it is a line that runs through the whole host, and the server-driven driver
is where a reader would most reasonably expect it to have been quietly crossed.
So, stated for each interactive verb the declarative grid + form vocabulary adds.

**The driver's total state is the tree plus the host's closure.** `Session` holds
a `wire.Node` and a `Handler`; `Connection` adds a frame sequence number and a
bounded replay buffer, which are *transport* state — what to resend after a
reconnect — and carry no UI meaning. There is no page cursor, no sort cursor, no
per-field validity or dirty set, no focus, no scroll position, and no per-user
model. A frame is the applied `TreeOp` list itself; a conformant client
re-renders by applying those ops with the same apply engine every host ships.

**Sorting, paging and cell-edit commits do not reach this driver at all.** The
event-legitimacy table (`legitimateEvents`) has no `DataGrid` entry, so a sort
click, a page change or an edit commit addressed to a grid is refused as
`IllegitimateEvent` before any handler runs. That is default-deny working as
designed rather than a gap, and it is the reference driver's table too — neither
host routes grid interaction through the live channel. What the client is handed
instead is the **declaration**: `sortable` per column, the grid's sort and page
state keys, the declared read-only columns and the edit destination all ride the
wire as data, and the *resolved initial state* — the declared sort applied, page
one resolved — is already in the emitted bytes. A host that wants a server
round-trip for a sort drives the corresponding State key from a node that does
take an event, which is a composition the vocabulary already supports.

**Form events do reach it, and the driver decides nothing about them.** A `Form`
accepts `submit`, `change` and `input`; the driver checks the node exists and the
event is legitimate, then hands the event to the host's `Handler`, applies
whatever ops come back, and refuses the step if any of them does not apply. It
runs no field rule. A `rule` declared on a form field — `format`, `pattern`, the
length bounds, a cross-field `compare` — is decoded, re-encoded and (for the
slots a control can honour) validated pre-emit, but it is **not enforced by this
driver**: enforcement is the host's decision function. This is a real divergence
from the reference driver, which re-checks declared rules server-side
unconditionally, and it is recorded rather than closed — see "Known gaps" below.

**Known gaps, named so their absence is a decision rather than a claim.** The
form projection is a generic labelled `<input>`: this host emits neither the
control's `type` nor its `required` state nor its declared `rule` as HTML
constraint attributes, so adding rule attributes alone would put one constraint
on a control that models none of the others. And the driver has no server-side
re-check of field rules. Both are form-fidelity work, older and wider than the
rule vocabulary that made them visible.

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

## Retired wire vocabulary — the positional slot on `InsertChild` / `MoveNode`

`InsertChild` and `MoveNode` both **append**; `ReorderChildren` states order by naming
child ids. The integer `position` / `newPosition` these two ops once carried was removed
from the wire format, and this host **REFUSES** it: `WRONG_TYPE` at `$.position` /
`$.newPosition`, with a message naming `ReorderChildren`. Placing a node anywhere but
last is `Batch [InsertChild …, ReorderChildren …]`.

There was a migration window during which every host accepted and ignored the field so
the hosts could adopt independently. It is **closed**. How it closed is worth knowing,
because it is not the obvious thing: this decoder walks each op's schema table and never
looks at anything else, so *not reading* the ordinal **was** the tolerance — there was
never a read to delete. Closing the window therefore meant ADDING a refusal, not removing
an acceptance; a host that merely stopped mentioning the field would have gone on
accepting it forever, indistinguishable from one that had never adopted.

The refusal is **by name** and is the enumerated-near-miss narrowing of §2 rule 2: a
genuinely unknown key is still tolerated, because a slot a future profile may add must
stay addable. It is checked **before** the schema loop, so an op carrying both a retired
ordinal and another defect names the ordinal — identically ordered in every host, so
which defect surfaces first is deterministic. Certified by the corpus fixtures
`reject-op-insertchild-retired-position` / `reject-op-movenode-retired-newposition` and
pinned by `wire/retired_position_test.go`.

This host declares no stability policy yet (pre-1.0, `0.0.1-alpha`), so the change is
recorded here rather than in a `STABILITY.md` it does not have.

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
