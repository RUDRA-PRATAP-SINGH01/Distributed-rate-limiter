# Scenario: Hot Key / Single User Spam (5000 RPS)
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Hot Key / Single User Spam (5000 RPS)" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Running hot-key script..." -ForegroundColor Yellow
k6 run benchmarks/hot-key/hot-key-test.js
