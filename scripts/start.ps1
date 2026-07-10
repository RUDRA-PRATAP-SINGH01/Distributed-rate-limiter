# Distributed Rate Limiter — one-command stack + dashboards (Windows)
# Usage (from repo root):
#   .\scripts\start.ps1
#   .\scripts\start.ps1 -NoBrowser
#   .\scripts\start.ps1 -NoTraffic

param(
    [switch]$NoBrowser,
    [switch]$NoTraffic,
    [switch]$Build
)

$ErrorActionPreference = "Continue"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

function Write-Banner([string]$Text, [string]$Color = "Cyan") {
    Write-Host ""
    Write-Host ("=" * 62) -ForegroundColor $Color
    Write-Host $Text -ForegroundColor $Color
    Write-Host ("=" * 62) -ForegroundColor $Color
}

function Wait-ForEndpoint {
    param(
        [string]$Url,
        [string]$Name,
        [int]$MaxAttempts = 45
    )
    Write-Host -NoNewline "Waiting for $Name..."
    for ($i = 1; $i -le $MaxAttempts; $i++) {
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) {
                Write-Host " OK" -ForegroundColor Green
                return $true
            }
        } catch {}
        Write-Host -NoNewline "."
        Start-Sleep -Seconds 2
    }
    Write-Host " FAILED" -ForegroundColor Red
    return $false
}

function Start-FallbackTraffic {
    Write-Host "k6 not found — starting PowerShell fallback traffic (~10 RPS)..." -ForegroundColor Yellow
    $jobScript = {
        while ($true) {
            1..10 | ForEach-Object {
                $uid = "bg_user_$([Math]::Floor((Get-Random) % 5) + 1)"
                try {
                    Invoke-WebRequest "http://localhost:9090/?user_id=$uid" -UseBasicParsing -TimeoutSec 2 | Out-Null
                } catch {}
            }
            # occasional idempotent + deny-friendly burst for richer Grafana/Jaeger
            try {
                $k = [guid]::NewGuid().ToString()
                Invoke-WebRequest "http://localhost:9090/api/orders" -Method POST `
                    -Headers @{ "Content-Type" = "application/json"; "X-User-ID" = "bg_idem"; "Idempotency-Key" = $k } `
                    -Body '{"amount":1}' -UseBasicParsing -TimeoutSec 3 | Out-Null
            } catch {}
            Start-Sleep -Seconds 1
        }
    }
    Start-Job -Name "drl-bg-traffic" -ScriptBlock $jobScript | Out-Null
    Write-Host "Background traffic job: drl-bg-traffic (Stop-Job -Name drl-bg-traffic to stop)" -ForegroundColor Green
}

function Open-Observability {
    $urls = @(
        "http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet?orgId=1&refresh=5s&from=now-15m&to=now",
        "http://localhost:9091/graph?g0.expr=sum(rate(rate_limiter_requests_total%5B1m%5D))%20by%20(allowed)&g0.tab=0&g0.range_input=15m",
        "http://localhost:16686/search?service=rate-sidecar&operation=sidecar.proxy&lookback=15m"
    )
    foreach ($u in $urls) {
        try { Start-Process $u } catch { Write-Host "Open manually: $u" }
    }
}

Write-Banner "Distributed Rate Limiter — Dashboard Quick Start"

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "Docker is required. Install Docker Desktop and retry." -ForegroundColor Red
    exit 1
}

Write-Host "Starting Docker Compose stack..." -ForegroundColor Yellow
if ($Build) {
    docker compose up -d --build
} else {
    docker compose up -d
}
if ($LASTEXITCODE -ne 0) {
    Write-Host "docker compose failed (exit $LASTEXITCODE)" -ForegroundColor Red
    exit $LASTEXITCODE
}

$ok = $true
$ok = (Wait-ForEndpoint "http://localhost:8080/health" "Limiter") -and $ok
$ok = (Wait-ForEndpoint "http://localhost:9090/health" "Sidecar") -and $ok
$ok = (Wait-ForEndpoint "http://localhost:3000/api/health" "Grafana") -and $ok
$ok = (Wait-ForEndpoint "http://localhost:9091/-/healthy" "Prometheus") -and $ok
$ok = (Wait-ForEndpoint "http://localhost:16686/" "Jaeger") -and $ok

Write-Host -NoNewline "Probe sidecar proxy..."
try {
    Invoke-WebRequest "http://localhost:9090/?user_id=startup_probe" -UseBasicParsing -TimeoutSec 5 | Out-Null
    Write-Host " OK" -ForegroundColor Green
} catch {
    $code = $null
    try { $code = [int]$_.Exception.Response.StatusCode } catch {}
    if ($code -in 200, 429) { Write-Host " OK ($code)" -ForegroundColor Green }
    else { Write-Host " WARN ($code)" -ForegroundColor Yellow }
}

if (-not $NoTraffic) {
    if (Get-Command k6 -ErrorAction SilentlyContinue) {
        Write-Host "Launching k6 background traffic (15 RPS)..." -ForegroundColor Yellow
        Start-Process -FilePath "k6" `
            -ArgumentList @("run", "benchmarks/demo/background-traffic.js") `
            -WindowStyle Hidden
        Write-Host "k6 started in a hidden window." -ForegroundColor Green
    } else {
        Start-FallbackTraffic
    }
    # Seed a few rich traces for Jaeger immediately
    1..5 | ForEach-Object {
        try { Invoke-WebRequest "http://localhost:9090/?user_id=demo_$_" -UseBasicParsing -TimeoutSec 2 | Out-Null } catch {}
    }
    $k = [guid]::NewGuid().ToString()
    try {
        Invoke-WebRequest "http://localhost:9090/api/orders" -Method POST `
            -Headers @{ "Content-Type" = "application/json"; "X-User-ID" = "demo"; "Idempotency-Key" = $k } `
            -Body '{"amount":100}' -UseBasicParsing -TimeoutSec 5 | Out-Null
        Invoke-WebRequest "http://localhost:9090/api/orders" -Method POST `
            -Headers @{ "Content-Type" = "application/json"; "X-User-ID" = "demo"; "Idempotency-Key" = $k } `
            -Body '{"amount":100}' -UseBasicParsing -TimeoutSec 5 | Out-Null
    } catch {}
}

if (-not $NoBrowser) {
    Write-Host "Opening Grafana, Prometheus, and Jaeger..." -ForegroundColor Yellow
    Open-Observability
}

Write-Banner "Stack is ready — open these URLs" "Green"
Write-Host @"
  Grafana fleet dashboard
    http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet
    (anonymous access enabled — no login)

  Prometheus (try: sum(rate(rate_limiter_requests_total[1m])) by (allowed))
    http://localhost:9091/

  Jaeger traces (Service=rate-sidecar, Operation=sidecar.proxy)
    http://localhost:16686/

  Sidecar / Limiter
    http://localhost:9090/   |   http://localhost:8080/health

Tips:
  - Grafana: set time range to Last 15 minutes, refresh 5s
  - Jaeger: open a trace → Trace Timeline for the span DAG (not System Architecture)
  - Re-open UIs later:  .\scripts\open-observability.ps1
  - Stop stack:         docker compose down
"@

if (-not $ok) {
    Write-Host "Some health checks failed — dashboards may still work after a short wait." -ForegroundColor Yellow
    exit 1
}
exit 0
