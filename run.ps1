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
    Write-Host "==> go build" -ForegroundColor Cyan
    & $go build ./...
    Assert-NativeSuccess "go build ./..."
}

if (-not $SkipTests) {
    Write-Host "==> go test" -ForegroundColor Cyan
    & $go test ./...
    Assert-NativeSuccess "go test ./..."
}

Write-Host "fuaran-go: run.ps1 complete." -ForegroundColor Green
