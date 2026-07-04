# Scenario: Progressive System Saturation (500 RPS)
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Progressive System Saturation (500 RPS)" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Running saturation script..." -ForegroundColor Yellow
k6 run -e TARGET_RPS=500 benchmarks/saturation/saturation-test.js
