# Benchmarks

Reproducible load tests for the distributed rate limiter. Full final evidence: [docs/benchmarks/final-benchmark-report.md](../docs/benchmarks/final-benchmark-report.md).

## Quick start

```powershell
docker compose up -d
k6 run -e TARGET_RPS=1000 benchmarks/scripts/sidecar-e2e.js
```

## Layout

| Path | Purpose |
|------|---------|
| `scripts/` | k6 workloads (direct limiter, sidecar e2e, hierarchical, denial cache, soak) |
| `final/` | Final-phase orchestration (`run-targeted-benchmarks.ps1`) |
| `results/<sha>-final/` | Raw k6 exports + `environment.txt` + `commands.txt` |
| `throughput/` | Legacy throughput tests (superseded by `scripts/` for final run) |
| `methodology.md` | Sustainability criteria (p99 < 100 ms, non-429 errors < 1%) |
| `parse-results.py` | Legacy parser for `throughput/results/` |
| `scripts/parse-k6-stream.py` | Parser for final JSON stream exports |

## Sustainability rule

A load level is **sustainable** when **actual** RPS meets target within ~15%, **p99 < 100 ms**, and **non-429 error rate < 1%**. HTTP **429** is expected quota enforcement, not a failure.

## Final run headline (commit `a1de9ec`, i9-14900HX, Docker Compose)

| Workload | Target RPS | Actual RPS | p99 | Errors |
|----------|------------|------------|-----|--------|
| Sidecar e2e | 1,000 | **872** | **11 ms** | 0% |
| Direct sliding | 1,000 | **871** | **8 ms** | 0% |
| Direct token bucket | 5,000 | **4,161** | 148 ms | 0% |
| 15 min soak @ 300 RPS | 300 | **299** | **10 ms** | 0% |

Do not reuse numbers from older `summary.md` rows without re-running on your hardware.
