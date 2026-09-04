# Authoring Fuaran trees in Go

`fuaran-go` is the **headless backend / orchestrator host** of the Fuaran UI wire
format. A Go service or an AI orchestrator authors a UI tree as typed data,
encodes it byte-identically to every other host, validates it before it leaves,
and emits complete server-side HTML. It is a library, not a runtime:
`render(tree, data) → bytes` is a pure function. There is no frontend codebase to
own and no UI session state to hold.

This page is the authoring reference — the structural model, how to build a tree,
and the encode / decode / validate / apply round trip. For the interactive
delivery modes see [Go server-driven and islands](SERVER-DRIVEN.md); for the
language-neutral contract the whole thing conforms to, see the wire-format
specification.

## The structural model, and why it is structural

Go has no sum types. The other typed hosts model each `$type`-discriminated wire
case as its own variant and lean on compile-time exhaustiveness; Go cannot, so
`fuaran-go` does not pretend to. Instead the decoded document is a small typed
tree over a **closed interface**:

```go
// wire/value.go
type Value interface{ isValue() }
```

`isValue` is unexported, so the case set is closed at the package boundary — no
consumer can add a `Value`. The cases are:

| Case | Wire shape |
|---|---|
| `wire.Str` | a JSON string |
| `wire.Int` | a number written as an integer literal — re-encodes as a plain decimal |
| `wire.Float` | a number written with a decimal point or exponent — re-encodes in the canonical shortest-round-trip layout |
| `wire.Bool` | a JSON boolean |
| `wire.Null` | a JSON null — legal only inside the sanctioned obj-erased seams |
| `wire.Arr` | an ordered array; source order is preserved |
| `wire.Obj` | an object: a `$type`-tagged DU case when `Tag` is set, a plain record when it is empty |
| `wire.Node` | the UI-node envelope |

**`Int` and `Float` are separate cases on purpose.** The canonical form lays the
two out differently, so collapsing them — the natural thing to do if you reach for
`encoding/json` and `float64` — loses the distinction and the document stops
re-encoding byte-identically. This is the single most common way a new Go
integration drifts from the corpus.

A `Node` carries its id, its tagged kind, and the validated optional sections:

```go
// wire/node.go
type Node struct {
	ID     string
	Kind   Obj
	Extras map[string]Value
}
```

`Extras` holds the validated optional envelope sections: `state`, `style`,
`accessibility` and `tooltip`. **These are siblings of `kind`, not fields inside
it** — the most common early mistake is to reach for `Kind.Fields["accessibility"]`,
where nothing will read it. The wire-omitted fields (motion, extra attributes)
have no representation here at all; a conformant host never reads or writes them.

The cost of the structural model is that a mistyped kind name or a missing slot is
not a compile error. The compensation is deliberate and it is in the repo: the
conformance corpus, plus a corpus-driven discriminator exhaustiveness guard in
`conformance/`, and the pre-emit validator described below. Use the validator; it
is the substitute for the compiler that the other hosts get for free.

## Building a tree

```go
import "github.com/fuaran-ui/fuaran-go/wire"

tree := wire.Node{
	ID: "root",
	Kind: wire.Obj{Tag: "Box", Fields: map[string]wire.Value{
		"children": wire.Arr{
			wire.Node{ID: "title", Kind: wire.Obj{Tag: "Heading", Fields: map[string]wire.Value{
				"level":   wire.Int(2),
				"text":    wire.Str("Channel performance"),
				"variant": wire.Str("Standard"),
			}}},
			wire.Node{ID: "count", Kind: wire.Obj{Tag: "Metric", Fields: map[string]wire.Value{
				"label": wire.Str("Count"),
				"value": wire.Obj{Tag: "Static", Fields: map[string]wire.Value{
					"value": wire.Int(0),
				}},
			}}},
		},
		"layout": wire.Obj{Tag: "Flex", Fields: map[string]wire.Value{
			"direction": wire.Str("Vertical"),
			"wrap":      wire.Bool(false),
		}},
		"role": wire.Str("Group"),
	}},
	Extras: map[string]wire.Value{
		"accessibility": wire.Obj{Fields: map[string]wire.Value{"role": wire.Str("main")}},
	},
}
```

Three things to read off that:

- **A bare string in a text slot is a `Literal`.** `"text": wire.Str("…")` is the
  canonical spelling; the `{"$type":"Literal","text":…}` envelope still decodes,
  but it is not what the encoder emits.
- **A binding slot takes a tagged object.** `Static` is the constant case; `State`,
  `Query`, `Filter`, `Selection`, `Format` and `Transform` are the others. A slot
  that wants a live value takes one of those, not a bare scalar.
- **Layout and role are closed vocabularies spelled as bare strings.** A value
  outside the vocabulary is a decode error, not a pass-through.

### There is no builder, and small helpers are the idiom

Composite literals are the whole authoring surface — there is no `NewMetric`, no
fluent chain. That gets verbose fast, so the idiom in this repo is a handful of
typed helper functions in your own package:

```go
func metric(id, label string, value float64) wire.Node {
	return wire.Node{
		ID: id,
		Kind: wire.Obj{Tag: "Metric", Fields: map[string]wire.Value{
			"label": wire.Str(label),
			"value": wire.Obj{Tag: "Static", Fields: map[string]wire.Value{
				"value": wire.Float(value),
			}},
		}},
	}
}
```

Note the `wire.Float` there: a monetary or measured quantity takes the canonical
shortest-round-trip number form, and writing `wire.Int` for a value that is
conceptually fractional changes the bytes. Only what you leave out is omitted —
setting nothing but `label` and `value` gets you the bare `{"$type":"Metric",…}`
form the encoder emits for a default-styled metric.

### Finding the vocabulary from Go

You do not have to read the specification to enumerate what a slot accepts. The
codec exports its own vocabularies:

```go
wire.KnownNodeKinds()          // every kind the decoder accepts, incl. decode-only aliases
wire.CanonicalNodeKinds()      // the kinds the encoder emits
wire.CanonicalFormFieldKinds() // the form-field control vocabulary
wire.KnownOpKinds()            // the tree-op vocabulary
wire.RequiredKindFields()      // kind -> the slots the wire requires
```

and the closed discriminator sets slot by slot — `wire.TextSourceCases()`,
`BindingCases()`, `ActionCases()`, `CellFormatCases()` — plus the bare-string
enums: `ToneVariants()`, `StyleWeights()`, `EmphasisLevels()`, `Orientations()`,
`BadgeVariants()`, `HeadingVariants()`, `IconSizes()`, `TrendPolarities()`.

Every one of these is derived from the decoder's own private tables, and the
validator builds its checks from the same two calls at init. So an answer you get
here cannot disagree with the gate — which is the closest this host comes to the
compile-time exhaustiveness the sum-type hosts enjoy.

## Encoding

```go
wireJSON, err := wire.EncodeNode(tree)
```

The output is canonical: keys sorted in Ordinal order (`$` first), the pinned
string escapes, and the canonical number layout. It is byte-identical to what
every other conformant host emits for the same tree — that is the contract, and
the corpus round-trip legs in `conformance/` are what hold it.

The governing law is `encode(decode(x)) == x` for every canonical document `x`.
If you are writing a transformation, decode → transform → encode and the bytes
you produce are canonical by construction.

## Decoding

```go
node, err := wire.DecodeNode(canonicalJSON)
```

A malformed input returns a `*wire.DecodeError` — a structured, recoverable value,
never a panic:

```go
type DecodeError struct {
	Code          DecodeErrorCode // INVALID_JSON | MISSING_FIELD | WRONG_TYPE |
	                              // UNKNOWN_DU_CASE | WRONG_NODE_KIND | EMPTY_NODE_ID |
	                              // LIMIT_EXCEEDED   (+ the out-of-band FOREIGN_PROFILE)
	Path          string          // "$"-rooted, e.g. "$.kind.text"
	Message       string
	ExpectedShape string          // enumerates the valid cases when a discriminator is at fault
}
```

Every conformant host surfaces these exact codes at these exact paths, which is
why the reject corpus is host-neutral: a fixture that reds this decoder reds all
of them, and for the same reason.

`LIMIT_EXCEEDED` deserves a note because getting it wrong sends an author to
repair the wrong thing. It means the document is **well formed and merely too
large to walk** — it is deliberately not `INVALID_JSON`, and treating the two
alike is a defect the specification calls out by name.

Two decode entry points sit beside `DecodeNode`: `wire.DecodeOp` for a tree-op
document, and the §15 versioning envelope, where `wire.Negotiate(profile)`
classifies an authored profile against this host's `wire.HostProfile`
(`core@1.0`) as `Current`, `Behind` or `Foreign`. A `Behind` document tolerates an
unknown kind as a verbatim-preserved payload that re-encodes identically; a
`Foreign` one is hard-refused rather than mis-decoded.

## Validating before you emit

The decoder rejects malformed *input*. The validator checks a tree **you
constructed** before it goes out — which is the failure mode that actually bites
an authoring host, because nothing decoded it on the way in:

```go
import "github.com/fuaran-ui/fuaran-go/validator"

for _, f := range validator.ValidateNode(tree) {
	log.Printf("%s %s at %s: %s", f.Severity, f.Code, f.Path, f.Message)
}
```

```go
type Finding struct {
	Code     string
	Path     string   // "$"-rooted, as with decode errors
	Message  string
	Severity Severity // "Error" (a shape defect — do not emit) | "Warning" (advisory)
}
```

It is default-deny by shape: empty and duplicate node ids, unrecognised kinds,
missing wire-required slots, out-of-domain bounded primitives. An empty result
means the tree is clean. The codes and paths agree case-for-case with the sibling
hosts' validators, so a finding here is the finding a reviewer on another stack
would see.

**Run it.** In a language with exhaustive matching the compiler would have caught
most of this; here the validator is where that check lives.

## Mutating a tree as data

Rebuilding a tree to change one value throws away the edit. The op algebra
expresses the edit itself:

```go
import "github.com/fuaran-ui/fuaran-go/ops"

op := wire.Obj{Tag: "UpdateProp", Fields: map[string]wire.Value{
	"target": wire.Str("count"),
	"path":   wire.Str("Value"),
	"value":  wire.Int(1),
}}

next, err := ops.Apply(op, tree)
```

`ops.Apply` is total: an op that does not apply returns a structured
`*ops.ApplyError`, never a panic, and the input tree is untouched. `ops.CanApply`
is the dry run, and `canApply ≡ apply succeeds` by construction — so a caller can
test first without a second implementation drifting from the first.

The 11-op algebra covers the structural verbs plus `UpdateProp` (including the
nested path grammar) and `ReplaceBinding`. On top of it, `ops` ships a
**placement algebra** — `PlaceOp`, `MoveOp`, `NudgeOp`, `ReorderOp`, `CanPlace`,
and the clone verbs `DuplicateOp` / `PasteOp` — which turn "put this node there"
into ops from the existing vocabulary. These are helpers, not new wire: what
comes out is an ordinary op any host can apply.

## What this host does not do

Stated plainly, because each is a decision rather than an omission:

- **It renders no chart.** A `Chart` is a semantic kind that must be lowered to a
  `Drawing` before it can paint, and this host takes the **require-pre-lowered**
  posture: a `Chart` reaching the renderer emits a marked client-hydration
  placeholder, never a silent empty region. Pre-lower upstream, or let a
  conformant client paint it.

  **A `Sparkline` is the deliberate exception, and it does paint here.** The two
  look alike and are not: a chart needs axes, ticks, a legend and scale
  negotiation, and the shared `render-fidelity.json` gives it `"class":
  "clientOnly"` — which is exactly what licenses a server placeholder. A
  sparkline is a bounded arithmetic map from a resolved series onto a fixed
  100 x 30 canvas, and its fidelity row reads `"class": "none"`: no client-only
  tier exists, so the server render IS the whole render. This host therefore
  lowers it through the same `Drawing` builder every other tier uses, and
  certifies the result byte-for-byte against the shared
  `wire-format-fixtures/sparkline-lowering/*` goldens. An unresolved or empty
  series keeps the `fuaran-sparkline-empty` em-dash — a readable, deterministic
  stand-in rather than a blank, and the one case the lowering deliberately
  cannot express (the goldens spell it as the JSON literal `null`).
- **It holds no UI session state on the static path.** Values are resolved at
  render time so a page is correct before any JS runs, but interactivity is a
  client's job — see [the server-driven page](SERVER-DRIVEN.md) for the two
  delivery modes that add it.
- **The decoder is behind the shared corpus on three fixtures.** A `Toggle`
  form-field kind, a `Now` binding, and a required `stateKey` on `Switch` are
  vocabulary the corpus carries and this decoder does not yet model. CI
  quarantines exactly those three by name so a *new* regression still reds the
  gate while the known drift stays visible — which does mean a plain
  `go test ./...` beside a current corpus checkout is red today. The quarantine
  list shrinks to empty as the decoder catches up; it is a record of outstanding
  work, not a licence to add to it.
- **There is no stability policy yet.** The host is pre-1.0 (`fuarango.Version`
  is `0.0.2-alpha`) and declares no `STABILITY.md`. Pin a commit if you need one.

## Verifying

```powershell
.\run.ps1              # gofmt check -> go vet -> go build -> go test
.\run.ps1 -SkipTests   # switches: -SkipFormat / -SkipBuild / -SkipTests
```

The runtime host is **standard library only** — no third-party modules, including
no `encoding/json` on the canonical path (it cannot produce the required number
layout). The conformance legs certify against the shared corpus and skip cleanly
when the repo is checked out on its own.
