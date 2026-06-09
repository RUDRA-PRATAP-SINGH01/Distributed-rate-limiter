#!/usr/bin/env pwsh
# Auto-detect and write benchmark environment specs.

$OutFile = Join-Path $PSScriptRoot 'environment.md'

$dockerVer = (docker version --format '{{.Server.Version}}' 2>$null) ?? 'unknown'
$goVer = (go version 2>$null) ?? 'unknown'
$redisVer = (docker exec rate-redis redis-server --version 2>$null) ?? 'unknown (container not running)'
$k6Ver = (k6 version 2>$null | Select-Object -First 1) ?? 'unknown'

$cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
$os = Get-CimInstance Win32_OperatingSystem
$ramGB = [math]::Round($os.TotalVisibleMemorySize / 1MB, 0)

$content = @"
# Benchmark Environment

> Auto-generated on $(Get-Date -Format 'yyyy-MM-dd HH:mm'). Re-run ``collect-environment.ps1`` before each session.

| Component | Version / Spec |
|-----------|----------------|
| **CPU** | $($cpu.Name) ($($cpu.NumberOfCores) cores / $($cpu.NumberOfLogicalProcessors) threads) |
| **RAM** | ${ramGB} GB |
| **OS** | $($os.Caption) |
| **Docker** | $dockerVer |
| **Go** | $goVer |
| **Redis** | $redisVer |
| **k6** | $k6Ver |

## Stack Configuration

| Service | Port | Notes |
|---------|------|-------|
| Sidecar | 9090 | Benchmark entry point |
| Limiter | 8080 | Central rate limiter |
| Redis | 6379 | Quota state |
| Demo | 8081 | Upstream backend |

## Rate Limiter Settings (docker-compose)

- Algorithm: sliding window
- Per-user capacity: 10 req / 60s window
- Sidecar rate limit: 10 req/min per user

## Why Environment Matters

Benchmark numbers are only meaningful relative to hardware. Always report environment alongside results.
"@

Set-Content -Path $OutFile -Value $content -Encoding UTF8
Write-Host "Updated $OutFile"
