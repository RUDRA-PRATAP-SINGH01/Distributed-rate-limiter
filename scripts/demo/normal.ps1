# Scenario: Normal Traffic Flow (50 RPS)
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Normal Traffic Flow (50 RPS)" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Running throughput script..." -ForegroundColor Yellow
k6 run -e TARGET_RPS=50 benchmarks/throughput/throughput-test.js
