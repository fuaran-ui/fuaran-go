# Server-driven UI and islands from Go

`fuaran-go` emits complete static output and never owns a frontend codebase. That
leaves a question this page answers: where does interactivity come from?

Two delivery modes, and they compose with the same tree you authored in
[Authoring Fuaran trees in Go](AUTHORING.md):

- **Static + islands** — emit a mostly-static page whose values are already
  resolved, and hydrate only the regions that need to be live. One render call.
- **Server-driven** — hold the tree and its state in Go, stream canonical
  `TreeOp` frames to a thin generic client, and round-trip interactions back.

In both, Go emits and drives; the browser renders. The Go program authors no
client code in either.

## The static baseline

Both modes build on the plain render, so it is worth being precise about what it
gives you:

```go
html := renderer.RenderHTML(tree, nil)   // or renderer.BindingSources{"users": wire.Int(42)}
```

`RenderHTML` returns a **body fragment, not a document** — you own the shell.
`renderer.ReferenceCSS()` hands back the reference stylesheet to embed or serve.
The second argument is a host-supplied binding map: `nil` resolves `Static`
bindings and placeholders the rest to an em-dash, and a declared `State` default
resolves ahead of the placeholder while a host-supplied source wins over both.

The destination policy is **ambient**: `RenderHTML` runs `DenyNonLocalEgress()`
with no caller opt-in, so a decoded tree may point at its own origin and nowhere
else. Widening is a named call:

```go
policy := renderer.DenyNonLocalEgress().
	AllowOrigin(renderer.HostSuffix("cdn.example"), renderer.EgressMedia).
	AllowOrigin(renderer.ExactHost("docs.example"), renderer.EgressHyperlink)

html := renderer.RenderHTMLWithEgress(tree, sources, policy)
```

The classes are `EgressHyperlink`, `EgressMedia`, `EgressRoute`,
`EgressDownload`, `EgressFileRead` and `EgressEmbed` — a rule declares *what may
be reached* and *for what*, and everything unlisted is denied.

## Islands: static by default, live where you say

A host marks individual subtrees as islands **per render**. The marker never
rides the wire — which subtree is live is a rendering decision, not a property of
the tree — so it is a parameter:

```go
import "github.com/fuaran-ui/fuaran-go/renderer"

html, err := renderer.RenderWithIslands(tree, nil, map[string]string{
	"chart-panel": "chart",   // node id -> island id
})
```

The map takes a **node id in the tree** to the **island id** the boundary and
payload carry. What comes out is the ordinary static render, with each island's
element wrapped in a `<div class="fuaran-island" data-fuaran-island="…">`
boundary, followed by one scoped `<script type="application/json">` per island
carrying only that subtree's canonical wire tree.

Three properties are worth knowing because they are what make islands safe to
reach for:

- **Zero islands is byte-identical to a plain render.** A nil or empty map
  returns exactly `RenderHTML(tree, sources)` — no wrappers, no scripts. Adding
  the call to a page that declares no islands changes nothing.
- **No hydration mismatch, by construction.** The boundary's children are exactly
  the island node's normal static render, which is what the client re-renders
  into the wrapper. There is nothing for a reconciler to disagree about.
- **The static and island surfaces resolve values identically.** Both run the
  same state-seeding pass. If they differed, one document would render two
  values depending only on whether a region happened to be marked — so they do
  not.

The embedded payload is the island subtree's **canonical wire JSON**, deliberately
unfiltered. It is the tree, not a rendering of it, and the client consuming it is
a conformant host applying its own policy at its own emission sites. Rewriting a
destination inside the payload would hand the client a tree that no longer round
trips.

### The destination policy does not widen when you mark an island

`RenderWithIslands` runs the ambient `DenyNonLocalEgress` policy, exactly as the
static path does. That is the point: a host must not widen its egress merely by
marking a region live. A wider posture is reached **by name**, through
`renderer.RenderWithIslandsAndEgress(tree, sources, islands, policy)` — an
explicit call, so it shows up in review.

## Server-driven: the tree lives in Go

The `serverdriven` package holds the tree and streams diffs. Its whole state is
one sentence long: **the tree, plus your handler's closure.**

```go
import "github.com/fuaran-ui/fuaran-go/serverdriven"

session := serverdriven.NewSession(tree, handler)
conn := serverdriven.NewConnection("c1", session, channel)
```

### The handler is your decision function

```go
type Handler func(tree wire.Node, ev Event) ([]wire.Obj, error)
```

Given the current tree and a structurally-validated event, it returns the
`TreeOp`s to apply — or an error to refuse the event. The host's model lives in
the closure, which is the natural Go shape:

```go
func counterHandler() serverdriven.Handler {
	count := 0
	return func(tree wire.Node, ev serverdriven.Event) ([]wire.Obj, error) {
		if ev.NodeID != "inc" || ev.Event != "click" {
			return nil, errors.New("only the inc button click is handled")
		}
		count++
		return []wire.Obj{{Tag: "UpdateProp", Fields: map[string]wire.Value{
			"target": wire.Str("count"),
			"path":   wire.Str("Value"),
			"value":  wire.Int(int64(count)),
		}}}, nil
	}
}
```

### What happens on each event

`Session.Step` runs four checks in order, and stops at the first that refuses.
None of them is optional and none can be turned off:

1. **Does the node exist in the current server tree?** No ⇒ `UnknownNode` — a
   stale or forged id.
2. **Does that node's kind accept this event?** No ⇒ `IllegitimateEvent`. The
   table is default-deny: a kind not listed accepts nothing. Today's listed kinds
   are `Button` (`click`), `Select` (`change`), `Form` (`submit` / `change` /
   `input`), `Filters` (`change` / `input` / `click`), `FileUpload` (`change` /
   `file-read`), `Tabs` and `Stepper` (`click` / `change`), and `Disclosure`
   (`click` / `change` / `toggle`).
3. **Does your handler accept it?** An error ⇒ `DispatchDenied`.
4. **Does every op it returned actually apply?** No ⇒ `DispatchDenied`, and the
   tree does not move at all — a partially-applying set advances nothing.

A refused step mutates nothing and pushes no frame:

```go
type Reject struct {
	Reason  RejectReason // UnknownNode | IllegitimateEvent | DispatchDenied
	NodeID  string
	Message string
}
```

Wire `serverdriven.WithOnReject(sink)` at construction to audit refusals — it is
the always-on hook, and a refusal that nobody logs is a refusal nobody can
diagnose.

### What a frame is

```go
type Frame struct {
	Seq int
	Ops []wire.Obj
}
```

Canonical `TreeOp`s, tagged with a per-connection sequence number. **Not DOM
patches** — that difference is what keeps this host render-runtime-free. A
conformant client re-renders by applying those ops with the same apply engine
every host ships, which is why the loop can be proved end-to-end in Go with no
browser in sight: a client holding its own copy of the tree and applying each
frame converges to the byte-identical server tree.

`EncodeFrameJSON` renders a frame as `{"ops":[…],"seq":N}` — itself canonical,
since `"ops"` sorts before `"seq"` under the Ordinal key order:

```json
{"ops":[{"$type":"UpdateProp","path":"Source","target":"count","value":1},{"$type":"RemoveNode","target":"x"}],"seq":7}
```

`EncodeSSE` wraps that body in one SSE event — `id: 7`, `event: patch`, the
single-line `data:` (canonical bytes carry no embedded newlines), and the
terminating blank line.

The client→server message is deliberately *not* canonical wire — it is a small
control message, so ordinary JSON is apt:

```json
{"connId":"c1","nodeId":"inc","event":"click","payload":"","lastSeq":4}
```

`DecodeEvent` parses it. The driver does not trust any of it: `nodeId` and
`event` go straight into the four checks above.

### Reconnects

Every frame carries `Seq`, and every inbound event carries the client's
`LastSeq`. A bounded per-connection buffer retains recent frames, so a transport
drop is recoverable:

```go
replayed, err := conn.Resync(lastSeq)   // re-pushes every retained frame newer than lastSeq
```

The buffer is bounded at `DefaultReplayBufferCapacity` (512 frames, overridable
with `WithReplayBufferCapacity`) and evicts oldest-first, so a client that never
reconnects cannot grow server memory without limit. A client returning from
behind the retained window gets the retained tail — state it did not see is gone,
which is a real limit rather than a rounding error, and the remedy is to re-seed
that client with a fresh tree.

Applying frames must be idempotent on the client: skip any frame whose `Seq` is
at or below the last one applied.

## Transports

**The library defines no HTTP routes.** There is no `/events`, no `/dispatch` —
the endpoints are yours to name and mount. What the package supplies is the
framing and the upgrade, and the driver never sees a transport type at all. It
sees only:

```go
type Channel interface {
	Push(frame Frame) error
	Receive(handler func(Event))
	Close() error
}
```

One channel is one live connection. A multiplexing backend owns the
`connId → Channel` registry above this seam. Three implementations ship:

**`InMemoryChannel`** — the reference channel. `Pushed()` returns every frame in
order and `Send(ev)` simulates an inbound event, so the whole loop is testable
headlessly.

**SSE** (`sse.go`) — `ServeSSE(w, r)` writes the streaming headers and returns
the channel plus the client's reconnect cursor read from `Last-Event-ID`, which
you hand straight to `Resync`. Keep the request goroutine alive for as long as the
connection should stream. SSE is one-directional, so the client→server path is a
companion POST endpoint you wire to `SSEChannel.Inbound`.

**WebSocket** (`ws.go`) — a hand-written RFC 6455 handshake and frame codec, no
third-party module. `ServeWebSocket(w, r)` upgrades and returns the channel; run
`Listen()` in its own goroutine for the inbound read loop (it answers pings with
pongs itself).

### The WebSocket origin policy is the one thing to read before shipping

The same-origin policy **does not cover WebSockets**. A browser will let a page on
any origin open a socket to your server, with the victim's cookies attached. So
`ServeWebSocket`'s zero-value policy is **same-origin only**, and widening it is a
deliberate act through `ServeWebSocketWithOptions`:

```go
ch, err := serverdriven.ServeWebSocketWithOptions(w, r, serverdriven.UpgradeOptions{
	AllowedOrigins: []string{"https://app.example.com"},
})
if errors.Is(err, serverdriven.ErrOriginNotAllowed) {
	http.Error(w, "forbidden", http.StatusForbidden)
	return
}
```

Same-origin is always allowed and needs no entry — the list is additive, so
naming a partner origin can never lock out the page you serve. Two values carry
teeth: `"*"` disables the check entirely, and `"null"` matches the origin any
sandboxed iframe can mint at will, so it is nearly as broad. Before adding
either, be sure the socket carries no ambient authority, or authenticates every
peer independently of the browser's credentials.

The origin check runs **before** the hijack, so a refused upgrade leaves the
`ResponseWriter` intact and you can still write a 403.

A missing `Origin` header is allowed by default, and that is deliberate rather
than an oversight: RFC 6455 requires browsers to send it, so its absence means
the peer is not a browser — a CLI, a service, a test, which is exactly what a
headless host exists to serve. Refusing them would buy nothing, since a
non-browser peer can send any origin it likes. Set `DenyMissingOrigin` when the
socket is only ever opened by page JavaScript.

## What the driver never holds

Worth stating explicitly, because a reader could reasonably assume otherwise.
There is no page cursor, no sort cursor, no per-field validity or dirty set, no
focus, no scroll position, and no per-user model. `Connection` adds a sequence
number and a replay buffer, which are *transport* state and carry no UI meaning.

**Grid interaction does not reach this driver at all.** `DataGrid` has no entry in
the legitimacy table, so a sort click, a page change or an edit commit addressed
to a grid is refused as `IllegitimateEvent` before any handler runs. That is
default-deny working, not a gap: what the client is handed instead is the
*declaration* — `sortable` per column, the sort and page state keys, the declared
read-only columns and the edit destination all ride the wire as data, with the
resolved initial state already in the emitted bytes. A host that wants a server
round trip for a sort drives the corresponding State key from a node that *does*
take an event.

**Form events reach the driver, and it decides nothing about them.** A `rule`
declared on a form field — `format`, `pattern`, length bounds, a cross-field
`compare` — is decoded, re-encoded, and validated pre-emit for the slots a control
can honour, but **this driver does not enforce it**. Enforcement is your handler's
decision. This is a real divergence from the reference driver, which re-checks
declared rules server-side unconditionally, and it is recorded rather than
closed. Two related gaps sit beside it: the form projection emits a generic
labelled `<input>` without the control's `type`, its `required` state, or its
declared rule as HTML constraint attributes. Both are form-fidelity work, older
and wider than the rule vocabulary that made them visible.

If your form needs enforcement, put it in the handler. Nothing below you will do
it for you.

## Verifying

```powershell
.\run.ps1              # gofmt check -> go vet -> go build -> go test
```

The `serverdriven` tests cover the loop end to end without a browser: dispatched
event → server apply → frame → client apply → byte-identical tree, including the
reconnect path with no double application of a frame already seen.
