#!/usr/bin/env pwsh
# Final benchmark suite — reproducible artifacts under benchmarks/results/<sha>/
# Requires: docker compose stack up, k6 installed.

$ErrorActionPreference = 'Stop'
$Root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
if (-not (Test-Path (Join-Path $Root 'go.mod'))) {
    $Root = Split-Path $PSScriptRoot -Parent
}
Set-Location $Root

$Sha = (git rev-parse --short HEAD).Trim()
$Stamp = Get-Date -Format 'yyyy-MM-dd-HHmm'
$OutDir = Join-Path $PSScriptRoot "results/$Sha-$Stamp"
$RawDir = Join-Path $OutDir 'raw'
New-Item -ItemType Directory -Force -Path $RawDir | Out-Null

function Write-EnvFile {
    $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
    $os = Get-CimInstance Win32_OperatingSystem
    $ramGB = [math]::Round($os.TotalVisibleMemorySize / 1MB, 0)
    $redisVer = docker exec rate-redis redis-server --version 2>$null
    $goVer = go version 2>$null
    $k6Ver = (k6 version 2>$null | Select-Object -First 1)
    $content = @"
commit_sha=$Sha
timestamp=$Stamp
cpu=$($cpu.Name)
cores=$($cpu.NumberOfCores)
threads=$($cpu.NumberOfLogicalProcessors)
ram_gb=$ramGB
os=$($os.Caption) $($os.Version) $($os.OSArchitecture)
go=$goVer
docker=$(docker version --format '{{.Server.Version}}' 2>$null)
redis=$redisVer
k6=$k6Ver
topology=1x limiter :8080, 1x sidecar :9090, 1x redis, optional 2nd replica :8083/:9092
algorithm_default=sliding
capacity=10
window_sec=60
warmup=10s
measure_duration=60s
"@
    Set-Content -Path (Join-Path $OutDir 'environment.txt') -Value $content -Encoding UTF8
}

function Run-K6 {
    param(
        [string]$Name,
        [string]$Script,
        [hashtable]$Env = @{}
    )
    $summary = Join-Path $RawDir "$Name-summary.json"
    $stream = Join-Path $RawDir "$Name-stream.json"
    $envArgs = $Env.GetEnumerator() | ForEach-Object { '-e', "$($_.Key)=$($_.Value)" }
    Write-Host "`n=== $Name ===" -ForegroundColor Cyan
    $cmd = @('run') + $envArgs + @(
        $Script,
        '--summary-export', $summary,
        '--out', "json=$stream"
    )
    & k6 @cmd
    return $summary
}

Write-EnvFile

$scripts = Join-Path (Split-Path $PSScriptRoot -Parent) 'scripts'
$summaries = @()

# A/B sliding window direct limiter
foreach ($rps in @(100, 1000, 5000)) {
    $s = Run-K6 -Name "direct-sliding-$rps" -Script (Join-Path $scripts 'direct-limiter.js') -Env @{
        TARGET_RPS = $rps; ALGORITHM = 'sliding'; WARMUP = '10s'; DURATION = '60s'
    }
    $summaries += $s
}

# Token bucket — ephemeral limiter on :8085
Write-Host "`n=== Starting token-bucket limiter on :8085 ===" -ForegroundColor Yellow
docker rm -f limiter-tb-bench 2>$null | Out-Null
docker run -d --name limiter-tb-bench --network distributed-rate-limiter_rate-net -p 8085:8080 `
    -e PORT=8080 -e REDIS_ADDR=redis:6379 -e REDIS_PASSWORD=dev-redis-password `
    -e ALGORITHM=token -e CAPACITY=10 -e REFILL_RATE=10.0 `
    -e INTERNAL_API_KEY=dev-internal-key-change-in-prod -e OTEL_ENABLED=false `
    distributed-rate-limiter-limiter | Out-Null
Start-Sleep -Seconds 8

foreach ($rps in @(100, 1000, 5000)) {
    $s = Run-K6 -Name "direct-token-$rps" -Script (Join-Path $scripts 'direct-limiter.js') -Env @{
        TARGET_RPS = $rps; ALGORITHM = 'token'; LIMITER_URL = 'http://localhost:8085'; WARMUP = '10s'; DURATION = '60s'
    }
    $summaries += $s
}

# C hierarchical
foreach ($rps in @(100, 1000)) {
    $s = Run-K6 -Name "hierarchical-$rps" -Script (Join-Path $scripts 'hierarchical-limiter.js') -Env @{
        TARGET_RPS = $rps; WARMUP = '10s'; DURATION = '60s'
    }
    $summaries += $s
}

# D sidecar e2e
foreach ($rps in @(100, 1000, 5000)) {
    $s = Run-K6 -Name "sidecar-e2e-$rps" -Script (Join-Path $scripts 'sidecar-e2e.js') -Env @{
        TARGET_RPS = $rps; WARMUP = '10s'; DURATION = '60s'
    }
    $summaries += $s
}

# E denial cache + F singleflight + G multi-replica + H idempotency
$summaries += Run-K6 -Name 'denial-cache' -Script (Join-Path $scripts 'denial-cache.js') -Env @{}
$summaries += Run-K6 -Name 'singleflight' -Script (Join-Path $scripts 'singleflight.js') -Env @{}
$summaries += Run-K6 -Name 'multi-replica-500' -Script (Join-Path $scripts 'multi-replica-e2e.js') -Env @{
    TARGET_RPS = 500; DURATION = '60s'
}
$summaries += Run-K6 -Name 'idempotency-race' -Script (Join-Path $scripts 'idempotency-race.js') -Env @{}

# Commands log
@(
    "# Final benchmark commands — $Stamp",
    "git_sha=$Sha",
    "See run-final-benchmarks.ps1"
) | Set-Content -Path (Join-Path $OutDir 'commands.txt') -Encoding UTF8

python (Join-Path $PSScriptRoot 'scripts\parse-k6-summary.py') @summaries | Tee-Object -FilePath (Join-Path $OutDir 'summary-table.md')

Write-Host "`nResults: $OutDir" -ForegroundColor Green
