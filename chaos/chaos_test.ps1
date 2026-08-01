<#
.SYNOPSIS
Local Windows demo of resilience contract R1 (Redis down → fail-closed).
Authoritative proof runs in CI: go test -tags=chaos ./chaos/...
#>

Write-Host ""
Write-Host "Chaos Demo (R1): Redis Failure and Fail-Closed" -ForegroundColor Cyan
Write-Host "Prefer CI: go test -tags=chaos ./chaos/..." -ForegroundColor DarkGray
Write-Host ""

$ErrorActionPreference = "Stop"
$ApiKey = if ($env:INTERNAL_API_KEY) { $env:INTERNAL_API_KEY } else { "dev-internal-key-change-in-prod" }
$Limiter = if ($env:CHAOS_LIMITER_URL) { $env:CHAOS_LIMITER_URL } else { "http://127.0.0.1:8080" }

function Invoke-Check([string]$User) {
    return curl.exe -s -i `
        -H "X-User-ID: $User" `
        -H "X-Internal-API-Key: $ApiKey" `
        -H "X-API-Key: $ApiKey" `
        "$Limiter/check"
}

function Test-Health {
    try {
        $r = Invoke-WebRequest -Uri "$Limiter/health" -UseBasicParsing -TimeoutSec 2
        return $r.StatusCode -eq 200
    } catch {
        return $false
    }
}

Write-Host "Pre-flight..." -ForegroundColor Magenta
$redisName = docker ps --filter "name=redis" --format "{{.Names}}" | Select-Object -First 1
if (-not $redisName) {
    Write-Host "FAIL: No redis container running. Start chaos stack:" -ForegroundColor Red
    Write-Host "  docker compose -f docker-compose.chaos.yml -p rate-chaos up -d --build"
    exit 1
}
if (-not (Test-Health)) {
    Write-Host "FAIL: Limiter not healthy at $Limiter/health" -ForegroundColor Red
    exit 1
}
Write-Host "OK: Redis ($redisName) and limiter are up." -ForegroundColor Green

$freshUser = "chaos_{0}" -f (Get-Date -Format "yyyyMMddHHmmssfff")
Write-Host ""
Write-Host "Step 1: Baseline check for $freshUser" -ForegroundColor Magenta
$first = Invoke-Check $freshUser
if ($first -match "200 OK") {
    Write-Host "OK: Got 200." -ForegroundColor Green
} else {
    Write-Host "FAIL:`n$first" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Step 2: Stopping Redis ($redisName)..." -ForegroundColor Magenta
docker stop $redisName | Out-Null
Start-Sleep -Seconds 1

Write-Host ""
Write-Host "Step 3: Check while Redis is down (expect 503)..." -ForegroundColor Magenta
$second = Invoke-Check $freshUser
if ($second -match "503") {
    Write-Host "OK: Got 503 — fail-closed." -ForegroundColor Green
} elseif ($second -match "200 OK") {
    Write-Host "FAIL: Got 200 — fail-open or bypass." -ForegroundColor Red
    exit 1
} else {
    Write-Host "FAIL: Unexpected:`n$second" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Step 4: Restarting Redis..." -ForegroundColor Magenta
docker start $redisName | Out-Null
$deadline = (Get-Date).AddSeconds(60)
while ((Get-Date) -lt $deadline) {
    if (Test-Health) { break }
    Start-Sleep -Seconds 2
}
if (-not (Test-Health)) {
    Write-Host "FAIL: Limiter did not recover." -ForegroundColor Red
    exit 1
}

$recoveryUser = "recover_{0}" -f (Get-Date -Format "yyyyMMddHHmmssfff")
Write-Host ""
Write-Host "Step 5: Recovery check for $recoveryUser" -ForegroundColor Magenta
$third = Invoke-Check $recoveryUser
if ($third -match "200 OK") {
    Write-Host "OK: Recovered." -ForegroundColor Green
} else {
    Write-Host "FAIL:`n$third" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "CHAOS DEMO PASSED (R1)" -ForegroundColor Green
