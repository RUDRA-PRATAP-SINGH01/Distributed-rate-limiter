param(
    [switch]$Full
)

# Distributed Rate Limiter - Benchmark Runner
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "      Distributed Rate Limiter - Benchmark Runner        " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# Ensure results directory exists
New-Item -ItemType Directory -Force -Path "benchmarks/throughput/results" | Out-Null

if ($Full) {
    Write-Host "Running FULL production benchmark suite..." -ForegroundColor Yellow
    & .\benchmarks\run-all.ps1
} else {
    Write-Host "Running QUICK benchmark validation (10 seconds, 100 RPS)..." -ForegroundColor Yellow
    k6 run -e TARGET_RPS=100 --duration 10s benchmarks/throughput/throughput-test.js --out json=benchmarks/throughput/results/100.json
    
    Write-Host "Compiling parser records..." -ForegroundColor Yellow
    python benchmarks/parse-results.py
    
    Write-Host "Regenerating Matplotlib performance graphs..." -ForegroundColor Yellow
    python benchmarks/graphs/generate-graphs.py
    
    Write-Host "==========================================================" -ForegroundColor Cyan
    Write-Host "Benchmark validation completed successfully!" -ForegroundColor Green
    Write-Host "Graphs generated in: benchmarks/graphs/" -ForegroundColor Green
    Write-Host "==========================================================" -ForegroundColor Cyan
}
