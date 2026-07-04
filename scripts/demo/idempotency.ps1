# Scenario: Stripe-Style Idempotency Race (100 concurrent VUs)
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Scenario: Stripe-Style Idempotency Race (100 concurrent VUs)" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "Running idempotency script..." -ForegroundColor Yellow
k6 run benchmarks/idempotency/idempotency-race.js
