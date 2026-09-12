#Requires -Version 7.0
# fuaran-go - run.ps1's own go-red proof (Phase 1673).
#
# WHY THIS EXISTS. `run.ps1` is this repository's gate, and for a time it could
# not fail: it ended `& $go test ./...` with no `$LASTEXITCODE` check and no
# `exit`, so `pwsh -File ./run.ps1` returned 0 against a red suite. That is the
# invocation CI and every automation uses, so the gate was green while the suite
# was not.
#
# It was fixed by asserting `$LASTEXITCODE` after every native stage - and
# nothing asserted that the fix HOLDS. A later edit that swapped a `throw` for a
# `Write-Error`, added a stage without its assertion, or ended the script after a
# native command would silently restore the defect, and no run of the ordinary
# gate could notice: a green gate looks identical whether it can go red or not.
# This script is what notices.
#
# IT PROVES THE RED DIRECTION, DELIBERATELY. The green direction ("a clean tree
# exits 0") is what every ordinary `run.ps1` run already demonstrates, so
# asserting it again buys little; the direction that is never exercised, and the
# only one a broken launcher gets wrong, is red. Pass -WithGreenProbe to assert
# both.
#
# EVERY PROBE IS MEASURED IN A CHILD PROCESS. Read in-session, `$LASTEXITCODE`
# after `& ./run.ps1` reports the last NATIVE command the script ran, not the
# script's own exit - so it can read 1 while `pwsh -File ./run.ps1` exits 0. Two
# honest observers reported opposite verdicts on exactly that difference. Only
# the child-process value is the gate, so only that is asserted here.
#
# Cost: the test-stage probe runs the whole suite once (the failure it injects
# reaches the gate as an unnamed failure, which also exercises the residue gate's
# regression direction end to end). Run it after editing run.ps1, not on every
# commit.
[CmdletBinding()]
param([switch]$WithGreenProbe)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# Inside the module, so `./...` reaches it; never a dot-prefixed name, which the
# go tool skips. Gitignored, and removed in the finally below.
$probeDir = Join-Path $PSScriptRoot "selftestprobe"
$probeFile = Join-Path $probeDir "probe_test.go"

$script:failures = @()

function Invoke-Launcher {
    # The process boundary. `pwsh -File` is what CI runs; its exit code is the
    # claim under test.
    param([string[]]$LauncherArgs)
    $out = & pwsh -NoProfile -File (Join-Path $PSScriptRoot "run.ps1") @LauncherArgs 2>&1
    return [pscustomobject]@{ Exit = $LASTEXITCODE; Output = ($out -join "`n") }
}

function Assert-Red {
    param([string]$Name, [string[]]$LauncherArgs)
    Write-Host "==> $Name" -ForegroundColor Cyan
    $r = Invoke-Launcher -LauncherArgs $LauncherArgs
    if ($r.Exit -eq 0) {
        $script:failures += "$Name : run.ps1 $($LauncherArgs -join ' ') returned 0 with the stage deliberately broken - this stage's exit code is NOT propagated."
        Write-Host "    FAILED (exit 0, expected non-zero)" -ForegroundColor Red
    }
    else {
        Write-Host "    ok (exit $($r.Exit))" -ForegroundColor Green
    }
}

function Write-Probe {
    param([string]$Content)
    New-Item -ItemType Directory -Force -Path $probeDir | Out-Null
    # LF, no BOM: the module is gofmt-governed and a BOM is not valid Go.
    [IO.File]::WriteAllText($probeFile, $Content.Replace("`r`n", "`n"), (New-Object Text.UTF8Encoding $false))
}

try {
    # 1. FORMAT STAGE. Valid Go, deliberately unformatted (leading spaces where
    #    gofmt requires a tab), so only `gofmt -l` objects.
    Write-Probe @"
package selftestprobe

func probe() int {
  return 0
}
"@
    Assert-Red -Name "format stage goes red at the process boundary" -LauncherArgs @("-SkipBuild", "-SkipTests")

    # 2. VET / BUILD STAGE. gofmt-clean but does not compile.
    Write-Probe @"
package selftestprobe

func probe() int {
	return "not an int"
}
"@
    Assert-Red -Name "vet/build stage goes red at the process boundary" -LauncherArgs @("-SkipFormat", "-SkipTests")

    # 3. TEST STAGE - the one the ruling was about. Compiles, is formatted, and
    #    fails. It reaches the gate as a failure no residue line names, so this
    #    also proves the regression direction end to end through the launcher.
    Write-Probe @"
package selftestprobe

import "testing"

func TestDeliberateFailure(t *testing.T) {
	t.Error("deliberate failure - run.tests.ps1 process-boundary probe")
}
"@
    Assert-Red -Name "test stage goes red at the process boundary" -LauncherArgs @("-SkipFormat", "-SkipBuild")
}
finally {
    if (Test-Path $probeDir) { Remove-Item -Recurse -Force $probeDir }
}

if ($WithGreenProbe) {
    Write-Host "==> clean tree exits 0 at the process boundary" -ForegroundColor Cyan
    $r = Invoke-Launcher -LauncherArgs @()
    if ($r.Exit -ne 0) {
        $script:failures += "green probe : run.ps1 returned $($r.Exit) on a clean tree. Either the tree is genuinely red (read the output) or the launcher fails a tree it should pass."
        Write-Host "    FAILED (exit $($r.Exit), expected 0)" -ForegroundColor Red
    }
    else {
        Write-Host "    ok (exit 0)" -ForegroundColor Green
    }
}

if ($script:failures.Count -gt 0) {
    Write-Host ""
    Write-Host "run.ps1 CANNOT GO RED for $($script:failures.Count) stage(s):" -ForegroundColor Red
    $script:failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    exit 1
}

Write-Host ""
Write-Host "run.tests.ps1: run.ps1 propagates every stage's failure to its caller." -ForegroundColor Green
exit 0
