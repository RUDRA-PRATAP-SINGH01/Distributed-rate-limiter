#!/usr/bin/env pwsh
# Saturation sweep — finer RPS steps to find max sustainable throughput.
# Requires: docker compose up -d, k6 installed.

$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
Set-Location $Root

$RpsLevels = @(1500, 2000, 2500, 3000, 3500, 4000)
$MetricsScript = Join-Path $PSScriptRoot 'metrics\collect-metrics.ps1'
$ResultsDir = Join-Path $PSScriptRoot 'saturation\results'
New-Item -ItemType Directory -Force -Path $ResultsDir | Out-Null

Write-Host "=== Saturation Sweep ===" -ForegroundColor Cyan
Write-Host "Finding max sustainable RPS (p99 < 100ms, errors < 1%)`n"

foreach ($rps in $RpsLevels) {
    Write-Host "Running saturation test at ${rps} RPS..." -ForegroundColor Yellow
    $testName = "saturation-$rps"
    $outFile = Join-Path $ResultsDir "$rps.json"

    $metricsJob = Start-Job -ScriptBlock {
        param($Script, $Name)
        & $Script -TestName $Name -DurationSec 65
    } -ArgumentList $MetricsScript, $testName

    k6 run -e "TARGET_RPS=$rps" `
        (Join-Path $PSScriptRoot 'saturation\saturation-test.js') `
        --out "json=$outFile"

    Wait-Job $metricsJob | Out-Null
    Receive-Job $metricsJob
    Remove-Job $metricsJob

    # Rename metrics file to match parser convention: saturation-{label}.json
    $metricsSrc = Join-Path $PSScriptRoot "metrics\results\$testName.json"
    $metricsDst = Join-Path $PSScriptRoot "metrics\results\saturation-$rps.json"
    if (Test-Path $metricsSrc) { Move-Item -Force $metricsSrc $metricsDst }

    Write-Host "Done: $rps RPS`n"
}

Write-Host "=== Parsing results ===" -ForegroundColor Cyan
python (Join-Path $PSScriptRoot 'parse-results.py')
python (Join-Path $PSScriptRoot 'graphs\generate-graphs.py')
