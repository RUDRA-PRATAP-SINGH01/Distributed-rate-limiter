#!/usr/bin/env pwsh
# Run full benchmark suite with resource metrics collection.
# Requires: docker compose up -d, k6 installed.

$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
Set-Location $Root

$MetricsScript = Join-Path $PSScriptRoot 'metrics\collect-metrics.ps1'
$MetricsDir = Join-Path $PSScriptRoot 'metrics\results'
New-Item -ItemType Directory -Force -Path $MetricsDir | Out-Null

function Run-Test {
    param(
        [string]$MetricsName,
        [string]$Script,
        [string]$OutFile,
        [hashtable]$Env = @{}
    )

    Write-Host "`n=== $MetricsName ===" -ForegroundColor Cyan

    $metricsJob = Start-Job -ScriptBlock {
        param($Script, $TestName)
        & $Script -TestName $TestName -DurationSec 65
    } -ArgumentList $MetricsScript, $MetricsName

    $envArgs = $Env.GetEnumerator() | ForEach-Object { "-e", "$($_.Key)=$($_.Value)" }
    k6 run @envArgs $Script --out "json=$OutFile"

    Wait-Job $metricsJob | Out-Null
    Receive-Job $metricsJob | Out-Null
    Remove-Job $metricsJob
}

# Throughput tests
$throughputDir = Join-Path $PSScriptRoot 'throughput\results'
New-Item -ItemType Directory -Force -Path $throughputDir | Out-Null
foreach ($rps in @(100, 1000, 5000, 10000)) {
    Run-Test `
        -MetricsName "throughput-$rps" `
        -Script (Join-Path $PSScriptRoot 'throughput\throughput-test.js') `
        -OutFile (Join-Path $throughputDir "$rps.json") `
        -Env @{ TARGET_RPS = $rps }
}

# Saturation sweep (1500–4000 RPS)
& (Join-Path $PSScriptRoot 'run-saturation.ps1')

# Hot-key
$hotkeyDir = Join-Path $PSScriptRoot 'hot-key\results'
New-Item -ItemType Directory -Force -Path $hotkeyDir | Out-Null
Run-Test `
    -MetricsName 'hot-key-5000' `
    -Script (Join-Path $PSScriptRoot 'hot-key\hot-key-test.js') `
    -OutFile (Join-Path $hotkeyDir '5000.json')

# Enforcement
$enforceDir = Join-Path $PSScriptRoot 'enforcement\results'
New-Item -ItemType Directory -Force -Path $enforceDir | Out-Null
Run-Test `
    -MetricsName 'enforcement-enforcement' `
    -Script (Join-Path $PSScriptRoot 'enforcement\enforcement-test.js') `
    -OutFile (Join-Path $enforceDir 'enforcement.json')

Write-Host "`n=== Generating report ===" -ForegroundColor Cyan
python (Join-Path $PSScriptRoot 'parse-results.py')
python (Join-Path $PSScriptRoot 'graphs\generate-graphs.py')
