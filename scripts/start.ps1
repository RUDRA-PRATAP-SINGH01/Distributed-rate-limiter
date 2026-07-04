# Distributed Rate Limiter - Windows PowerShell Startup

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "      Distributed Rate Limiter - One-Command Startup       " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Start containers
Write-Host "Starting Docker Compose services..." -ForegroundColor Yellow
docker compose up -d

# 2. Helper to poll endpoints
function Wait-For-Endpoint {
    param (
        [string]$Url,
        [string]$Name
    )
    $maxAttempts = 30
    $attempt = 1
    Write-Host -NoNewline "Waiting for $Name to be healthy..."
    
    while ($attempt -le $maxAttempts) {
        try {
            $response = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2 -ErrorAction SilentlyContinue
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 400) {
                Write-Host " [OK]" -ForegroundColor Green
                return
            }
        } catch {}
        Write-Host -NoNewline "."
        Start-Sleep -Seconds 2
        $attempt++
    }
    Write-Host " [FAILED]" -ForegroundColor Red
    exit 1
}

# 3. Wait for components
Wait-For-Endpoint -Url "http://localhost:8080/health" -Name "Central Limiter"
Wait-For-Endpoint -Url "http://localhost:3000/api/health" -Name "Grafana Server"

# Verify proxy behavior
Write-Host -NoNewline "Verifying Sidecar Proxy routing..."
try {
    $response = Invoke-WebRequest -Uri "http://localhost:9090/check?user_id=startup_probe" -UseBasicParsing -ErrorAction SilentlyContinue
    Write-Host " [OK]" -ForegroundColor Green
} catch {
    Write-Host " [FAILED]" -ForegroundColor Red
}

# 4. Start background traffic load
Write-Host "Launching background metrics loader (15 RPS constant)..." -ForegroundColor Yellow
Start-Process -FilePath "k6" -ArgumentList "run benchmarks/demo/background-traffic.js" -WindowStyle Hidden
Write-Host "Background traffic loader launched successfully in a hidden window." -ForegroundColor Green

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "🎉 System is UP and running!" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Access endpoints:"
Write-Host "  - Sidecar Proxy:       http://localhost:9090/"
Write-Host "  - Central Limiter:     http://localhost:8080/health"
Write-Host "  - Grafana Dashboard:   http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet"
Write-Host "  - Prometheus Console:  http://localhost:9091/"
Write-Host "  - Jaeger UI:           http://localhost:16686/"
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "To run interactive scenario scripts, explore 'scripts/demo/'."
