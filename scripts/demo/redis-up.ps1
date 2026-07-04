# Scenario: Redis Recovery Simulation
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Redis Recovery Simulation" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Starting Redis container..." -ForegroundColor Green
docker start rate-redis
Write-Host "Redis is now ONLINE. Circuit states should transition from OPEN -> HALF-OPEN -> CLOSED." -ForegroundColor Yellow
