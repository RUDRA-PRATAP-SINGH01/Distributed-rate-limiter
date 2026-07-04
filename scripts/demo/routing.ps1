# Scenario: Dynamic Gateway Routing & Failovers
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Dynamic Gateway Routing & Failovers" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Running routing test script..." -ForegroundColor Yellow
k6 run benchmarks/routing/routing-test.js
