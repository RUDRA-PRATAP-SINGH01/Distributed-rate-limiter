#!/usr/bin/env pwsh
# Collect docker stats during a k6 benchmark run.
# Usage: .\collect-metrics.ps1 -TestName throughput-1000 -DurationSec 65

param(
    [Parameter(Mandatory = $true)]
    [string]$TestName,
    [int]$DurationSec = 65,
    [int]$IntervalSec = 5
)

$containers = @('rate-limiter', 'rate-sidecar', 'rate-redis', 'demo-backend')
$outDir = Join-Path $PSScriptRoot 'results'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null
$rawFile = Join-Path $outDir "$TestName-raw.jsonl"
$summaryFile = Join-Path $outDir "$TestName.json"

$samples = @()
$end = (Get-Date).AddSeconds($DurationSec)

Write-Host "Collecting docker stats for ${DurationSec}s (every ${IntervalSec}s)..."

while ((Get-Date) -lt $end) {
    $timestamp = (Get-Date).ToString('o')
    $stats = docker stats --no-stream --format '{{json .}}' @containers 2>$null
    if ($stats) {
        foreach ($line in ($stats -split "`n")) {
            if ($line.Trim()) {
                $obj = $line | ConvertFrom-Json
                $samples += [ordered]@{
                    timestamp = $timestamp
                    container = $obj.Name
                    cpu       = $obj.CPUPerc
                    mem       = $obj.MemUsage
                    mem_pct   = $obj.MemPerc
                }
            }
        }
    }
    Start-Sleep -Seconds $IntervalSec
}

$samples | ConvertTo-Json -Depth 5 | Set-Content $rawFile -Encoding UTF8

function Parse-Percent($s) { return [double]($s -replace '%', '') }
function Parse-MemMB($s) {
    if ($s -match '([\d.]+)\s*MiB') { return [double]$matches[1] }
    if ($s -match '([\d.]+)\s*GiB') { return [double]$matches[1] * 1024 }
    return 0
}

$byContainer = $samples | Group-Object container
$summary = [ordered]@{ test = $TestName; samples = $samples.Count }

foreach ($group in $byContainer) {
    $name = $group.Name
    $cpus = $group.Group | ForEach-Object { Parse-Percent $_.cpu }
    $mems = $group.Group | ForEach-Object { Parse-MemMB $_.mem }
    $key = switch -Regex ($name) {
        'limiter' { 'limiter'; break }
        'sidecar' { 'sidecar'; break }
        'redis'   { 'redis'; break }
        default   { $name; break }
    }
    $summary["${key}_cpu_avg"] = [math]::Round(($cpus | Measure-Object -Average).Average, 1)
    $summary["${key}_mem_avg_mb"] = [math]::Round(($mems | Measure-Object -Average).Average, 1)
}

$totalMem = @()
foreach ($ts in ($samples | Group-Object timestamp)) {
    $totalMem += ($ts.Group | ForEach-Object { Parse-MemMB $_.mem } | Measure-Object -Sum).Sum
}
$summary['total_mem_avg_mb'] = [math]::Round(($totalMem | Measure-Object -Average).Average, 1)

$summary | ConvertTo-Json -Depth 5 | Set-Content $summaryFile -Encoding UTF8
Write-Host "Saved metrics to $summaryFile"
