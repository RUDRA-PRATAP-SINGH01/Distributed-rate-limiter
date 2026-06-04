<#
.SYNOPSIS
Chaos test for the distributed rate limiter - kills Redis and checks fail-closed behaviour.
#>

Write-Host ""
Write-Host "Chaos Test: Redis Failure and Sidecar Fail-Closed" -ForegroundColor Cyan
Write-Host ""

function Test-Service {
    param($Url, $Name)
    try {
        $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2
        return $response.StatusCode -eq 200
    } catch {
        Write-Host "WARNING: $Name is not responding." -ForegroundColor Yellow
        return $false
    }
}

Write-Host "Pre-flight checks..." -ForegroundColor Magenta
$redisOk = (docker ps --filter "name=rate-redis" --format "{{.Names}}" 2>$null | Select-String "rate-redis") -ne $null
if (-not $redisOk) {
    Write-Host "FAIL: Redis container rate-redis not running. Start with: docker start rate-redis" -ForegroundColor Red
    exit 1
}
Write-Host "OK: Redis is running." -ForegroundColor Green

$limiterOk = Test-Service "http://localhost:8080/health" "Central limiter"
if (-not $limiterOk) {
    Write-Host "FAIL: Central limiter not reachable on port 8080. Start with: go run ." -ForegroundColor Red
    exit 1
}
Write-Host "OK: Central limiter is healthy." -ForegroundColor Green

$sidecarOk = Test-Service "http://localhost:9090/health" "Sidecar"
if (-not $sidecarOk) {
    Write-Host "FAIL: Sidecar not reachable on port 9090." -ForegroundColor Red
    exit 1
}
Write-Host "OK: Sidecar is healthy." -ForegroundColor Green

$freshUser = "chaos_{0}" -f (Get-Date -Format "yyyyMMddHHmmssfff")

Write-Host ""
Write-Host "Step 1: First request for new user: $freshUser" -ForegroundColor Magenta
$firstResponse = curl.exe -s -i "http://localhost:9090/check?user_id=$freshUser"
if ($firstResponse -match "200 OK") {
    Write-Host "OK: Got 200 OK." -ForegroundColor Green
} else {
    Write-Host "FAIL: Unexpected response:" -ForegroundColor Red
    Write-Host $firstResponse
    exit 1
}

Write-Host ""
Write-Host "Step 2: Stopping Redis container..." -ForegroundColor Magenta
docker stop rate-redis | Out-Null
Start-Sleep -Seconds 1

Write-Host ""
Write-Host "Step 3: Second request while Redis is down..." -ForegroundColor Magenta
$secondResponse = curl.exe -s -i "http://localhost:9090/check?user_id=$freshUser"

if ($secondResponse -match "503 Service Unavailable") {
    Write-Host "OK: Got 503 - sidecar fails closed." -ForegroundColor Green
} elseif ($secondResponse -match "200 OK") {
    Write-Host "FAIL: Got 200 - check FAIL_OPEN env var." -ForegroundColor Red
    exit 1
} elseif ($secondResponse -match "429 Too Many Requests") {
    Write-Host "FAIL: Got 429 - cached denial should not apply to fresh user." -ForegroundColor Red
    exit 1
} else {
    Write-Host "FAIL: Unexpected response:" -ForegroundColor Yellow
    Write-Host $secondResponse
    exit 1
}

Write-Host ""
Write-Host "Step 4: Restarting Redis..." -ForegroundColor Magenta
docker start rate-redis | Out-Null
Start-Sleep -Seconds 2

Write-Host ""
Write-Host "Step 5: Recovery check with new user..." -ForegroundColor Magenta
$recoveryUser = "recover_{0}" -f (Get-Date -Format "yyyyMMddHHmmssfff")
$thirdResponse = curl.exe -s -i "http://localhost:9090/check?user_id=$recoveryUser"
if ($thirdResponse -match "200 OK") {
    Write-Host "OK: Service recovered - got 200 OK." -ForegroundColor Green
} else {
    Write-Host "FAIL: Recovery failed. Response:" -ForegroundColor Red
    Write-Host $thirdResponse
    exit 1
}

Write-Host ""
Write-Host "CHAOS TEST PASSED" -ForegroundColor Green
Write-Host "Your rate limiter survived a Redis outage." -ForegroundColor Green
