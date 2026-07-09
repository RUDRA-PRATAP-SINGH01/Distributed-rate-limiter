#!/usr/bin/env pwsh
# Targeted final benchmarks — writes to benchmarks/results/<sha>-<stamp>/
$ErrorActionPreference = 'Stop'
$Root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
Set-Location $Root

$Sha = (git rev-parse --short HEAD).Trim()
$Stamp = Get-Date -Format 'yyyy-MM-dd-HHmm'
$OutDir = Join-Path $PSScriptRoot "results/$Sha-$Stamp"
$Raw = Join-Path $OutDir 'raw'
New-Item -ItemType Directory -Force -Path $Raw | Out-Null

function Run-K6($Name, $Script, $Env) {
    $summary = Join-Path $Raw "$Name-summary.json"
    $stream = Join-Path $Raw "$Name-stream.json"
    $args = @('run') + ($Env.GetEnumerator() | ForEach-Object { '-e'; "$($_.Key)=$($_.Value)" }) + @(
        $Script, '--summary-export', $summary, '--out', "json=$stream"
    )
    Write-Host "=== $Name ===" -ForegroundColor Cyan
    & k6 @args | Out-Null
    return @{ name = $Name; summary = $summary; stream = $stream }
}

# environment.txt
$cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
$os = Get-CimInstance Win32_OperatingSystem
@(
"commit_sha=$Sha","timestamp=$Stamp","cpu=$($cpu.Name)","cores=$($cpu.NumberOfCores)",
"threads=$($cpu.NumberOfLogicalProcessors)","ram_gb=$([math]::Round($os.TotalVisibleMemorySize/1MB,0))",
"os=$($os.Caption) $($os.Version)","go=$(go version)","docker=$(docker version --format '{{.Server.Version}}')",
"redis=$(docker exec rate-redis redis-server --version 2>$null)","k6=$(k6 version 2>$null | Select-Object -First 1)",
"warmup=10s","measure=60s","capacity=10","window_sec=60"
) | Set-Content (Join-Path $OutDir 'environment.txt')

$scripts = Join-Path (Split-Path $PSScriptRoot -Parent) 'scripts'
$runs = @()

foreach ($rps in @(100,1000,5000)) {
    $runs += Run-K6 "direct-sliding-$rps" (Join-Path $scripts 'direct-limiter.js') @{ TARGET_RPS=$rps; ALGORITHM='sliding' }
}
foreach ($rps in @(100,1000,5000)) {
    $runs += Run-K6 "sidecar-e2e-$rps" (Join-Path $scripts 'sidecar-e2e.js') @{ TARGET_RPS=$rps }
}

docker rm -f limiter-tb-bench 2>$null | Out-Null
docker run -d --name limiter-tb-bench --network distributed-rate-limiter_rate-net -p 8085:8080 `
  -e PORT=8080 -e REDIS_ADDR=redis:6379 -e REDIS_PASSWORD=dev-redis-password `
  -e ALGORITHM=token -e CAPACITY=10 -e REFILL_RATE=10.0 `
  -e INTERNAL_API_KEY=dev-internal-key-change-in-prod -e OTEL_ENABLED=false distributed-rate-limiter-limiter | Out-Null
Start-Sleep 8
foreach ($rps in @(100,1000,5000)) {
    $runs += Run-K6 "direct-token-$rps" (Join-Path $scripts 'direct-limiter.js') @{ TARGET_RPS=$rps; LIMITER_URL='http://localhost:8085'; ALGORITHM='token' }
}
foreach ($rps in @(100,1000)) {
    $runs += Run-K6 "hierarchical-$rps" (Join-Path $scripts 'hierarchical-limiter.js') @{ TARGET_RPS=$rps }
}

$runs += Run-K6 'denial-cache' (Join-Path $scripts 'denial-cache.js') @{}
$runs += Run-K6 'singleflight' (Join-Path $scripts 'singleflight.js') @{}
$runs += Run-K6 'multi-replica-500' (Join-Path $scripts 'multi-replica-e2e.js') @{ TARGET_RPS=500 }
$runs += Run-K6 'idempotency-race' (Join-Path $scripts 'idempotency-race.js') @{}

$rows = @('| Workload | Target RPS | Actual RPS | p50 | p95 | p99 | max | 200 | 429 | errors |')
$rows += '|----------|------------|------------|-----|-----|-----|-----|-----|-----|--------|'
foreach ($r in $runs) {
    $parsed = python (Join-Path $scripts 'parse-k6-stream.py') $r.stream 70 2>$null
    if ($parsed -match 'total=(\d+) rps=([\d.]+) p50=([\d.]+) p95=([\d.]+) p99=([\d.]+).*max=([\d.]+) 200=(\d+) 429=(\d+) errors=(\d+)') {
        $rows += "| $($r.name) | - | $($Matches[2]) | $($Matches[3]) | $($Matches[4]) | $($Matches[5]) | $($Matches[6]) | $($Matches[7]) | $($Matches[8]) | $($Matches[9]) |"
    }
}
$rows | Set-Content (Join-Path $OutDir 'summary-table.md')
Write-Host "DONE $OutDir" -ForegroundColor Green
