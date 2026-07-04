# Scenario: Infrastructure Chaos Engineering Simulation
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Infrastructure Chaos Engineering Simulation" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

Write-Host "1. Spawning payments routing traffic in background..." -ForegroundColor Yellow
$k6Job = Start-Process -FilePath "k6" -ArgumentList "run benchmarks/routing/routing-test.js" -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 3

Write-Host "2. Injecting fault: stopping gateway-b..." -ForegroundColor Red
docker stop gateway-b
Start-Sleep -Seconds 10

Write-Host "3. Injecting fault: stopping Redis database..." -ForegroundColor Red
docker stop rate-redis
Start-Sleep -Seconds 10

Write-Host "4. Recovering system: starting gateway-b and Redis..." -ForegroundColor Green
docker start gateway-b
docker start rate-redis

# Wait for k6 to finish
Write-Host "Waiting for traffic to settle..." -ForegroundColor Yellow
$k6Job.WaitForExit()

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "🎉 Chaos test completed successfully. Check Grafana for the error peaks!" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Cyan
