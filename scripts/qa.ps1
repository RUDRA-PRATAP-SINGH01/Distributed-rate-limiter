# Distributed Rate Limiter - test automation runner (Windows)
# Usage (from repo root):
#   .\scripts\qa.ps1 unit
#   .\scripts\qa.ps1 process-smoke
#   .\scripts\qa.ps1 smoke
#   .\scripts\qa.ps1 sanity
#   .\scripts\qa.ps1 sanity -Changed
#   .\scripts\qa.ps1 race
#   .\scripts\qa.ps1 integration
#   .\scripts\qa.ps1 coverage
#   .\scripts\qa.ps1 quality-gate
#   .\scripts\qa.ps1 exploratory

param(
    [Parameter(Position = 0)]
    [ValidateSet(
        "unit", "process-smoke", "smoke", "sanity", "race",
        "integration", "coverage", "quality-gate", "exploratory", "help"
    )]
    [string]$Mode = "help",
    [switch]$Changed
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Write-Banner([string]$Text) {
    Write-Host ""
    Write-Host ("=" * 62) -ForegroundColor Cyan
    Write-Host $Text -ForegroundColor Cyan
    Write-Host ("=" * 62) -ForegroundColor Cyan
}

function Invoke-Go {
    param([string[]]$GoArgs)
    Write-Host ("go " + ($GoArgs -join " ")) -ForegroundColor DarkGray
    & go @GoArgs
    if ($LASTEXITCODE -ne 0) {
        throw "go exited $LASTEXITCODE"
    }
}

function Get-ChangedGoPackages {
    $names = @()
    $names += @(git diff --name-only)
    $names += @(git diff --name-only --cached)
    $upstream = git diff --name-only "origin/main...HEAD" 2>&1
    if ($LASTEXITCODE -eq 0) {
        $names += @($upstream)
    }
    $pkgs = @()
    foreach ($f in ($names | Sort-Object -Unique)) {
        if ($f -notlike "*.go") { continue }
        $dir = Split-Path -Parent $f
        if (-not $dir) { continue }
        if ($dir -like "vendor*" -or $dir -like "testdata*") { continue }
        $pkg = "./" + ($dir -replace "\\", "/")
        if ($pkgs -notcontains $pkg) {
            $pkgs += $pkg
        }
    }
    return $pkgs
}

function Show-Help {
    Write-Host "Test automation framework - Distributed Rate Limiter"
    Write-Host ""
    Write-Host "  unit            Full unit + handler suite (go test ./...)"
    Write-Host "  process-smoke   Fast critical-path tests (no Docker)"
    Write-Host "  smoke           Live stack is-up checks (needs compose)"
    Write-Host "  sanity          Live happy-path checks after a change"
    Write-Host "  sanity -Changed Also run go test on packages touched in git"
    Write-Host "  race            Race detector"
    Write-Host "  integration     Lua packages against REDIS_TEST_ADDR"
    Write-Host "  coverage        coverage.out + func summary"
    Write-Host "  quality-gate    vet + process-smoke + unit (local merge bar)"
    Write-Host "  exploratory     Print session charters (manual)"
    Write-Host ""
    Write-Host "Docs: docs/testing/quality-management.md"
}

function Invoke-ProcessSmoke {
    Write-Banner "Process smoke (white-box critical path, no Docker)"
    Invoke-Go @(
        "test", "-count=1", "-timeout", "60s", "./cmd/limiter/",
        "-run", "TestHealthEndpoint_Healthy|TestCheckHandler_TokenBucket"
    )
    Invoke-Go @(
        "test", "-count=1", "-timeout", "60s", "./cmd/sidecar/",
        "-run", "TestSidecarHealth_LimiterAndRedisHealthy"
    )
}

function Invoke-LiveSmoke {
    Write-Banner "Deploy smoke (black-box, live stack)"
    $env:QA_REQUIRE_STACK = "1"
    Invoke-Go @("test", "-count=1", "-tags=smoke", "-timeout", "2m", "-v", "./tests/smoke/...")
}

function Invoke-LiveSanity {
    Write-Banner "Sanity (black-box happy path, live stack)"
    $env:QA_REQUIRE_STACK = "1"
    Invoke-Go @("test", "-count=1", "-tags=sanity", "-timeout", "2m", "-v", "./tests/sanity/...")
    if ($Changed) {
        $pkgs = @(Get-ChangedGoPackages)
        if ($pkgs.Count -eq 0) {
            Write-Host "No changed Go packages - live sanity only." -ForegroundColor Yellow
            return
        }
        Write-Banner ("Sanity on changed packages: " + ($pkgs -join " "))
        Invoke-Go (@("test", "-count=1", "-timeout", "5m") + $pkgs)
    }
}

switch ($Mode) {
    "help" { Show-Help }
    "unit" {
        Write-Banner "Unit + handler tests"
        Invoke-Go @("test", "-count=1", "./...")
    }
    "process-smoke" { Invoke-ProcessSmoke }
    "smoke" { Invoke-LiveSmoke }
    "sanity" { Invoke-LiveSanity }
    "race" {
        Write-Banner "Race detector"
        Invoke-Go @("test", "-count=1", "-race", "./...")
    }
    "integration" {
        Write-Banner "Redis integration (set REDIS_TEST_ADDR)"
        if (-not $env:REDIS_TEST_ADDR) {
            throw "REDIS_TEST_ADDR is required (example: 127.0.0.1:6379)"
        }
        Invoke-Go @(
            "test", "-count=1", "-p", "1", "-v",
            "./internal/limiter/...",
            "./internal/circuitbreaker/...",
            "./internal/idempotency/...",
            "./internal/audit/...",
            "./internal/routing/..."
        )
    }
    "coverage" {
        Write-Banner "Coverage"
        Invoke-Go @("test", "-count=1", "-coverprofile=coverage.out", "./...")
        Invoke-Go @("tool", "cover", "-func=coverage.out")
    }
    "quality-gate" {
        Write-Banner "Quality gate (local merge bar)"
        Invoke-Go @("vet", "./...")
        Invoke-ProcessSmoke
        Invoke-Go @("test", "-count=1", "./...")
        Write-Host ""
        Write-Host "Quality gate passed: vet + process-smoke + unit." -ForegroundColor Green
        Write-Host "CI also runs lint, vuln, race, redis-integration, chaos." -ForegroundColor DarkGray
    }
    "exploratory" {
        Write-Banner "Exploratory testing - session charters"
        Write-Host "Open and timebox one charter from:"
        Write-Host "  docs/testing/exploratory-charters.md"
        Write-Host ""
        Write-Host "Suggested next sessions:"
        Write-Host "  ET-1  Quota allow / deny / headers"
        Write-Host "  ET-2  Auth: no query user_id, missing key = 401"
        Write-Host "  ET-3  Redis down -> 503 fail-closed"
        Write-Host "  ET-4  Hierarchical tenant / user / endpoint"
        Write-Host "  ET-5  Idempotency replay vs first write"
        Write-Host "  ET-6  Routing failover + circuit"
        Write-Host "  ET-7  Admin override visible on next /check"
        Write-Host "  ET-8  Grafana / Jaeger /health after start.ps1"
    }
}
