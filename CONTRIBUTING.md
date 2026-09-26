# Contributing to fuaran-go

This repo is licensed **Apache-2.0** (see [`LICENSE`](LICENSE)). Contributions are welcome under
the same licence. The conventions below keep the tree green and the wire format stable.

## Contribution licensing — Developer Certificate of Origin

Every commit must be signed off under the [Developer Certificate of Origin 1.1](https://developercertificate.org/)
to certify you have the right to contribute the code under Apache-2.0. Add a `Signed-off-by:`
trailer to each commit:

```
git commit -s -m "feat: your change"
```

A pull request without DCO sign-off on every commit will not be merged.

## Per-commit hard requirements

1. **`pwsh ./run.ps1` is green** — the one-command gate: format check, build, and the full test
   suite in one pass.
2. **Formatting** — run `gofmt` (`go fmt ./...`) before every commit. Unformatted code is not mergeable.
3. **Conformance** — wire-surface changes must keep the bundled conformance corpus green (round-trip, reject, and lenient-accept families). The corpus is canonical upstream — corpus updates arrive as corpus-sync changes, never hand-edits to fixtures.

## The Core boundary

`internal/core/` holds this host's twins of the `Fuaran.Core` reference — the Core semantics
this host reimplements rather than owns:

- `internal/core/canonical` — the canonical-JSON primitives (number form, key order, string escaping);
- `internal/core/dataframe` — the Compute-layer model, the `Transform` evaluator, list-parameter
  substitution, and the decode half of the codec;
- `internal/core/function` — the signature-searchable function registry (`FindBySignature`,
  compose) and the capability model with its `InvocationKey` and declaration codec.

It mirrors `Fuaran.Core`, the public .NET reference these twins certify against (the shared law
vectors and wire corpus). The public `canonical`, `dataframe` and `function` packages forward every
exported name to it unchanged, so importers do not change. Two pieces stay in the public packages on
purpose, because they belong to this host rather than to Core: the dataframe encode half, which lowers
into this host's structural wire model (`wire.Value`, a closed sum that includes `wire.Node`), and the
bounded text parse (`wire.ParseBounded`, the §21 limits) in front of `DecodeSource`, `DecodePipeline`
and `DecodeDeclaration`.

The boundary is held by a test, not by convention: `internal/core/boundary_test.go` reads the real
import graph (`go list -deps -test`) and fails, naming the offending edge, if anything under
`internal/core` imports a package of this module outside `internal/core` or a package outside the
standard library. Keep Core semantics inside the boundary, and keep this host's domain packages
(`wire`, `renderer`, `ops`, …) out of it.

## Pull request flow

1. Branch from `main` with a descriptive name (`feat/<short-name>`, `fix/<short-name>`,
   `docs/<short-name>`).
2. Make focused, DCO-signed commits. Group related changes; do not bundle unrelated cleanups.
3. Run the per-commit hard requirements above.
4. Open a PR describing the change and its wire-format impact.
5. A maintainer reviews and merges.
