# Benchmark Summary

See [environment.md](environment.md) for machine specs and [methodology.md](methodology.md) for test design.

## Key Finding

Based on existing throughput data (100–10,000 RPS target):

> System sustains **~1,000 actual RPS** with p99 < 100 ms and 0% errors.  
> Beyond **5,000 target RPS** (actual ~1,353), p99 rises to 3.5 s with 10% errors — latency grows exponentially.

Run `.\run-saturation.ps1` for finer-grained saturation data (1,500–4,000 RPS).

## Throughput Results

| Target RPS | Actual RPS | p99 | Error Rate | Notes |
|------------|------------|-----|------------|-------|
| 100 | 100 | 11 ms | 0% | Pass |
| 1,000 | 1,000 | 3.2 ms | 0% | Pass — max sustainable |
| 5,000 | 1,353 | 3.5 s | 10% | Saturated |
| 10,000 | 1,082 | 4.3 s | 15% | Collapsed |

## Resource Utilization

Run benchmarks with `run-all.ps1` to collect CPU/memory via `docker stats`.  
Example format once metrics are collected:

| Actual RPS | p99 | Limiter CPU | Sidecar CPU | Redis CPU | Memory |
|------------|-----|-------------|-------------|-----------|--------|
| 1,000 | 3 ms | — | — | — | — |
| 1,353 | 3.5 s | — | — | — | — |

## Other Tests

| Test | Actual RPS | Result | Notes |
|------|------------|--------|-------|
| Hot-key (5,000 target) | 4,940 | 99.9% rejected (429) | Correct — 10 users hammered |
| Enforcement (500/min) | 8 | 98% rejected (429) | Correct — ~10 allowed per window |

## Graphs

| Graph | Shows |
|-------|-------|
| `graphs/latency-vs-rps.png` | p50/p95/p99 vs **actual** throughput |
| `graphs/saturation-curve.png` | Target vs actual RPS (saturation knee) |
| `graphs/error-rate-vs-rps.png` | Failures vs actual throughput |
| `graphs/resource-utilization.png` | CPU vs actual throughput |
| `graphs/enforcement-allowed-vs-rejected.png` | Allowed vs rejected requests |

Regenerate after new test runs:

```powershell
python benchmarks/parse-results.py
python benchmarks/graphs/generate-graphs.py
```
