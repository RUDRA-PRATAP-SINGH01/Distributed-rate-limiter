# Scenario: Redis Crash Simulation
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Redis Crash Simulation" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Stopping Redis container..." -ForegroundColor Red
docker stop rate-redis
Write-Host "Redis is now OFFLINE. Send traffic now to see 100% Error Rate and health indicators drop." -ForegroundColor Yellow
