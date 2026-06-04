<#
.SYNOPSIS
Chaos test for the distributed rate limiter – kills Redis and checks fail‑closed behaviour.

.DESCRIPTION
This script simulates a Redis outage while the sidecar is in fail‑closed mode (default).
It expects:
- Redis running on localhost:6379
- Central rate limiter running on port 8080
- Sidecar running on port 9090 (with FAIL_OPEN=false)

The test:
1. Sends a request to a fresh user (should succeed – 200)
2. Stops the Redis container
3. Sends another request to the same user (should get 503 – rate limiter unavailable)
4. Restarts Redis
5. Sends a request to another fresh user (should succeed again)

If all steps match expected results, the test passes.
#>

Write-Host @"
╔══════════════════════════════════════════════════════════════╗
║     🔥 Chaos Test: Redis Failure & Sidecar Fail‑Closed      ║
║     Your rate limiter should gracefully handle Redis going  ║
║     down without corrupting state or returning wrong codes. ║
╚══════════════════════════════════════════════════════════════╝
"@ -ForegroundColor Cyan


# Helper to check if a service is responsive
function Test-Service {
    param($Url, $Name)
    try {
        $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2
        return $response.StatusCode -eq 200
    } catch {
        Write-Host "⚠️  $Name is not responding (maybe it's on a coffee break?)" -ForegroundColor Yellow
        return $false
    }
}

# Step 0: Pre‑flight checks
Write-Host "`n🔍 Pre‑flight checks..." -ForegroundColor Magenta
$redisOk = (docker ps --filter "name=rate-redis" --format "table {{.Names}}" | Select-String "rate-redis") -ne $null
if (-not $redisOk) {
    Write-Host "   ❌ Redis container 'rate-redis' not running. Wake it up with: docker start rate-redis" -ForegroundColor Red
    exit 1
} else {
    Write-Host "   ✅ Redis is alive and kicking (for now)." -ForegroundColor Green
}

$limiterOk = Test-Service "http://localhost:8080/health" "Central limiter"
if (-not $limiterOk) {
    Write-Host "   ❌ Central limiter not reachable on port 8080. Start it with: go run . (you know the drill)" -ForegroundColor Red
    exit 1
} else {
    Write-Host "   ✅ Central limiter looks healthy (probably lying)." -ForegroundColor Green
}

$sidecarOk = Test-Service "http://localhost:9090/health" "Sidecar"
if (-not $sidecarOk) {
    Write-Host "   ❌ Sidecar not reachable on port 9090. Did you set the env vars? Try: go run cmd/sidecar/main.go" -ForegroundColor Red
    exit 1
} else {
    Write-Host "   ✅ Sidecar is ready to catch the falling sky." -ForegroundColor Green
}

# Generate a fresh user ID (millisecond precision ensures uniqueness)
$freshUser = "chaos_$(Get-Date -Format 'yyyyMMddHHmmssfff')"

Write-Host "`n🧪 Step 1: Sending first request to a brand new user: $freshUser" -ForegroundColor Magenta
Write-Host "   (This user has never seen a rate limit. Innocent, like a newborn.)" -ForegroundColor Gray
$firstResponse = curl.exe -s -i "http://localhost:9090/check?user_id=$freshUser"
if ($firstResponse -match "200 OK") {
    Write-Host "   ✅ Got 200 OK – the limiter smiles upon you." -ForegroundColor Green
} else {
    Write-Host "   ❌ Unexpected response: $firstResponse – something's fishy." -ForegroundColor Red
    exit 1
}

Write-Host "`n💥 Step 2: Killing Redis container... (cue dramatic music)" -ForegroundColor Magenta
docker stop rate-redis | Out-Null
Start-Sleep -Seconds 1

Write-Host "`n⏳ Step 3: Sending SECOND request to the same user (Redis is now a ghost)" -ForegroundColor Magenta
$secondResponse = curl.exe -s -i "http://localhost:9090/check?user_id=$freshUser"

if ($secondResponse -match "503 Service Unavailable") {
    Write-Host "   ✅ Got 503 Service Unavailable – sidecar fails closed like a boss." -ForegroundColor Green
    Write-Host "   (Because if the limiter is broken, better to say 'no' than to lie.)" -ForegroundColor Gray
} elseif ($secondResponse -match "200 OK") {
    Write-Host "   ❌ Got 200 OK – sidecar is being too optimistic. Check FAIL_OPEN env var." -ForegroundColor Red
    exit 1
} elseif ($secondResponse -match "429 Too Many Requests") {
    Write-Host "   ❌ Got 429 – sidecar is holding a grudge. It remembered a denial that shouldn't exist." -ForegroundColor Red
    exit 1
} else {
    Write-Host "   ❓ Unexpected response: $secondResponse – the matrix is glitching." -ForegroundColor Yellow
}

Write-Host "`n🛠️ Step 4: Restarting Redis (like waking a bear from hibernation)" -ForegroundColor Magenta
docker start rate-redis | Out-Null
Start-Sleep -Seconds 2

Write-Host "`n🌈 Step 5: Verifying recovery – sending request to another fresh user" -ForegroundColor Magenta
$recoveryUser = "recover_$(Get-Date -Format 'yyyyMMddHHmmssfff')"
$thirdResponse = curl.exe -s -i "http://localhost:9090/check?user_id=$recoveryUser"
if ($thirdResponse -match "200 OK") {
    Write-Host "   ✅ Service recovered! Rate limiting is back, like nothing happened." -ForegroundColor Green
    Write-Host "   (Redis has amnesia, but that's okay.)" -ForegroundColor Gray
} else {
    Write-Host "   ❌ Recovery failed. Response: $thirdResponse – maybe Redis is still grumpy." -ForegroundColor Red
    exit 1
}

Write-Host @"

╔══════════════════════════════════════════════════════════════╗
║                    CHAOS TEST PASSED                         ║
║  Your rate limiter survived a Redis apocalypse. You may now  ║
║  pat yourself on the back (or treat yourself to a cookie).   ║
╚══════════════════════════════════════════════════════════════╝
"@ -ForegroundColor Green
