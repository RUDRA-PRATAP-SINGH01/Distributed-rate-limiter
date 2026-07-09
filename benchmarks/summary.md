# Benchmark Summary

> **Final run (2026-07-10, commit `a1de9ec`):** see [../docs/benchmarks/final-benchmark-report.md](../docs/benchmarks/final-benchmark-report.md) and `results/a1de9ec-final/`.

See [environment.md](environment.md) for machine specs and [methodology.md](methodology.md) for test design.

## Key Finding (final benchmark run)

On i9-14900HX / 32 GB / Docker Compose / Redis 7.4 / k6 1.7.1:

> System sustains **~872 actual RPS** end-to-end (sidecar, 1,000 target) with **p99 11 ms** and **0% non-429 errors**.  
> At **5,000 target RPS**, sliding-window paths saturate (actual **285–1,504 RPS**, p99 **382 ms–51 s**). Token bucket on a dedicated limiter reached **4,161 RPS** at p99 **148 ms** (unique users).

## Throughput Results (final run — sidecar e2e & direct limiter)

| Target RPS | Workload | Actual RPS | p99 | Error Rate | Verdict |
|------------|----------|------------|-----|------------|---------|
| 100 | Direct sliding | 100 | 3.9 ms | 0% | Healthy |
| 1,000 | Direct sliding | 871 | 8 ms | 0% | **Max sustainable** |
| 1,000 | Sidecar e2e | 872 | 11 ms | 0% | **Max sustainable** |
| 5,000 | Direct sliding | 285 | 51 s | 0% | Saturated |
| 5,000 | Sidecar e2e | 1,504 | 383 ms | 0% | Saturated |
| 5,000 | Direct token bucket | 4,161 | 148 ms | 0% | High throughput; p99 > 100 ms |

## Soak (15 min @ 300 RPS target)

| Actual RPS | p99 | Errors | Notes |
|------------|-----|--------|-------|
| 299 | 10 ms | 0% | Isolated max spike 1.3 s; no sustained drift |

## Correctness-oriented benchmarks

| Test | Result |
|------|--------|
| Denial cache hammer | p99 **7 ms** on 618k cached denials |
| Multi-sidecar quota | 60 concurrent, 2 sidecars → **10 allowed / 50 denied** |
| Idempotency burst | 40 parallel → **1×200, 39×409** |

## Legacy results

Older rows in `throughput/results/` (June 2026) used `throughput-test.js` via sidecar `/check`. Re-run with `benchmarks/scripts/` before citing in new documents.

## Graphs

Regenerate after new test runs:

```powershell
python benchmarks/parse-results.py
python benchmarks/graphs/generate-graphs.py
```
