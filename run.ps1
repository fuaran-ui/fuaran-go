#Requires -Version 7.0
# fuaran-go — Stage-0 entry point (workspace CLAUDE.md "Every new sibling ships a run.ps1").
# Full happy path: gofmt format-check -> go vet -> go build -> go test.
# Switches: -SkipFormat / -SkipBuild / -SkipTests for fast iteration.
[CmdletBinding()]
param(
    [switch]$SkipFormat,
    [switch]$SkipBuild,
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

function Assert-NativeSuccess {
    # $ErrorActionPreference governs CMDLET errors only: a native command that exits non-zero
    # sets $LASTEXITCODE and the script carries on regardless. Without this, `go test ./...`
    # could report FAIL packages while `pwsh ./run.ps1` still exited 0 — a gate that cannot go
    # red. Every native invocation below is followed by a call to this.
    #
    # MEASURE THIS AT THE PROCESS BOUNDARY, WHICH IS THE ONLY PLACE IT MEANS ANYTHING.
    # Read in-session, `$LASTEXITCODE` after `& ./run.ps1` reflects the last NATIVE command
    # this script ran, not the script's own exit — so it can show 1 while `pwsh -File
    # ./run.ps1` exits 0, and two honest observers can report opposite things. The gate is
    # what a CHILD process returns. `run.tests.ps1` asserts that property for every stage
    # below; run it after any edit to this file.
    param([string]$What)
    if ($LASTEXITCODE -ne 0) {
        throw "$What failed with exit code $LASTEXITCODE."
    }
}

function Resolve-Tool {
    param([string]$Name)
    $cmd = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $cmd) {
        throw "The Go toolchain is required but '$Name' was not found on PATH. Install Go (see go.mod for the pinned language floor) and re-run."
    }
    return $cmd.Source
}

$go = Resolve-Tool "go"

if (-not $SkipFormat) {
    Write-Host "==> gofmt -l (format check)" -ForegroundColor Cyan
    $gofmt = Resolve-Tool "gofmt"
    $bad = & $gofmt -l .
    Assert-NativeSuccess "gofmt -l ."
    if ($bad) {
        Write-Host "gofmt found unformatted files:" -ForegroundColor Red
        $bad | ForEach-Object { Write-Host "  $_" }
        throw "Run 'gofmt -w .' before committing (workspace formatting mandate — gofmt is the Go analogue of Fantomas)."
    }
}

if (-not $SkipBuild) {
    Write-Host "==> go vet" -ForegroundColor Cyan
    & $go vet ./...
    Assert-NativeSuccess "go vet ./..."
    # The `race`-constrained sources compile only under `go test -race`, which
    # needs cgo and a C toolchain a stock Windows dev box does not have — so
    # `go vet ./...` above never type-checks them and a syntax error in one
    # first surfaces in CI, on the leg it exists to prove. `-tags race` selects
    # them without the detector, so the check costs nothing and needs no
    # compiler. Verified 2026-09-14 (Phase 1750): `go list -tags race` names
    # `serverdriven/race_selftest_test.go`; the untagged list does not.
    Write-Host "==> go vet -tags race (the race-constrained sources)" -ForegroundColor Cyan
    & $go vet -tags race ./...
    Assert-NativeSuccess "go vet -tags race ./..."
    Write-Host "==> go build" -ForegroundColor Cyan
    & $go build ./...
    Assert-NativeSuccess "go build ./..."
}

if (-not $SkipTests) {
    # THE TEST STAGE IS THE RESIDUE GATE, NOT A BARE `go test ./...` (Phase 1673).
    #
    # `cmd/conformance-residue` runs the WHOLE suite with no exclusions and
    # compares the failure set against the named set in conformance/RESIDUE.txt,
    # going red in BOTH directions. CI's blocking step is that same command, so
    # this launcher and CI now block on one measurement and cannot drift apart.
    # Before this, a developer could read a green `run.ps1` while CI was red on
    # a stale cap — which is exactly what happened for six days from 2026-09-06
    # (see RESIDUE.txt).
    #
    # It also fixes a false green this stage carried on its own: the gate invokes
    # the suite with `-count=1`, and that is load-bearing rather than tidy. The
    # conformance corpus is a SIBLING CHECKOUT outside this module, so Go's test
    # cache cannot see it change; a bare `go test ./...` happily reports
    # `(cached)` for most packages against a corpus it has not read since the
    # last build. Observed while writing this: fourteen packages reported
    # `(cached)` against a corpus checkout minutes old.
    #
    # A bare raw run is still one command away (`go test ./...`) for iteration.
    Write-Host "==> go test (gated — full suite vs the named residue)" -ForegroundColor Cyan
    & $go run ./cmd/conformance-residue
    Assert-NativeSuccess "go run ./cmd/conformance-residue"
}

Write-Host "fuaran-go: run.ps1 complete." -ForegroundColor Green
