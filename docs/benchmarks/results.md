# Benchmark Results

## Problem Statement

This document is my engineering journal for **aggregate benchmark results**. All numbers come from `benchmarks/summary.md`, `idempotency/summary.md`, `circuitbreaker/summary.md`, and `sentinel/summary.md`. Interviewers and on-call engineers need one page where throughput knee, idempotency proof, circuit breaker micro-benchmarks, and HA drill expectations live together. Without opening five folders.

## Why the problem exists

Benchmark results get fragmented:

- Throughput in `summary.md`
- Idempotency in a subdirectory
- Circuit breaker Go benches separate
- Sentinel qualitative expectations with no RPS table

Cherry-picked release notes (e.g., "942 RPS idempotency") without context (replay-only path) are misleading. A consolidated results doc keeps numbers with context.

## Design goals

1. **Single source tables**. Copy exact numbers from summary files.
2. **Target vs actual RPS**. Always show both.
3. **Pass/fail criteria**. p99 under 100 ms, errors under 1% for sustainable.
4. **Cross-reference scripts**. k6 paths, `run-all.ps1`, graphs.
5. **Environment anchor**. i9-14900HX, 32 GB, Docker 29.5.2, k6 v1.7.1.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Only graphs | No exact numbers for PR review |
| Spreadsheet | Not in repo; drifts from code |
| CI badge (RPS only) | Loses p99 and error context |
| README section | README already 1700 lines; split docs |
| Auto-generated only | Human narrative still valuable |

Hybrid: `parse-results.py` generates `summary.md`. This doc adds interpretation.

## Final architecture

**Environment** (`benchmarks/environment.md`):

| Component | Spec |
|-----------|------|
| CPU | Intel Core i9-14900HX (24c/32t) |
| RAM | 32 GB |
| OS | Windows 11 |
| Docker | 29.5.2 |
| Go | 1.25.0 module / 1.26.1 toolchain |
| Redis | 7.4.9 alpine |
| k6 | v1.7.1 |

**Stack ports:** Sidecar `:9090`, Limiter `:8080`, Admin `:8082`, Demo `:8081`, Redis `:6379`, Jaeger UI `:16686`.

**Regenerate:**

```powershell
.\benchmarks\run-all.ps1
python benchmarks/parse-results.py
python benchmarks/graphs/generate-graphs.py
```

**Graphs:** `latency-vs-rps.png`, `saturation-curve.png`, `error-rate-vs-rps.png`, `resource-utilization.png`, `enforcement-allowed-vs-rejected.png`.

## Tradeoffs

- **Laptop numbers**, not production SLA. Directional regression only.
- **Sliding window default**. Throughput table is `ALGORITHM=sliding` compose.
- **Missing CPU columns**. Until fresh `docker stats` collection.
- **Idempotency 942 RPS**. Replay path only, not full stack mutating load.
- **Circuit benches**. miniredis local, not full Docker RTT.

## Failure modes

1. **Misread 5,000 RPS row**. Actual **1,353**, not 5,000 sustained.
2. **429 as error**. Hot-key 99.9% 429 is correct enforcement.
3. **Stale summary**. Numbers below reflect the last committed `benchmarks/summary.md` run.
4. **Profile mismatch**. HA results are not the same as standalone throughput.
5. **k6 409 "failures"**. Idempotency race shows 14% failed metric but 100% checks passed.

## Operational concerns

- Compare PR benchmark diff against tables below.
- Attach `environment.md` when sharing externally.
- Admin APIs for post-benchmark inspection: `/admin/limits/`, `/admin/circuit`, `/admin/routing/gateways`, `/admin/idempotency/`, `/admin/audit` on `:8082`.
- Chaos and Sentinel drills are **not** included in `run-all.ps1`. Run manually pre-release.

## Performance implications

### Throughput (`benchmarks/summary.md`)

> System sustains **~1,000 actual RPS** with p99 under 100 ms and 0% errors.  
> Beyond **5,000 target RPS** (actual ~1,353), p99 rises to 3.5 s with 10% errors.

| Target RPS | Actual RPS | p99 | Error Rate | Notes |
|------------|------------|-----|------------|-------|
| 100 | 100 | 11 ms | 0% | Pass |
| 1,000 | 1,000 | 3.2 ms | 0% | **Max sustainable** |
| 5,000 | 1,353 | 3.5 s | 10% | Saturated |
| 10,000 | 1,082 | 4.3 s | 15% | Collapsed |

### Correctness tests

| Test | Actual RPS | Result |
|------|------------|--------|
| Hot-key (5,000 target) | 4,940 | 99.9% rejected (429) |
| Enforcement (500/min) | 8 | 98% rejected (~10 allowed) |

### Idempotency (`idempotency/summary.md`)

| Test | Key metrics |
|------|-------------|
| Race (100 VUs, 1 key) | **1** upstream execution, p95 **14.9 ms**, avg **9.2 ms**, 100% checks |
| Replay (50 VUs, 30s) | **~942 RPS**, p50 **2.1 ms**, p95 **5.7 ms**, **0%** errors, **0** upstream |

### Circuit breaker (`circuitbreaker/summary.md`)

| Benchmark | ops/sec | ns/op | bytes/op |
|-----------|---------|-------|----------|
| BenchmarkCircuitAllow | ~8k | ~120µs | ~400B |
| BenchmarkCircuitRecord | ~4k | ~250µs | ~800B |
| BenchmarkCircuitAllowRecordParallel | ~6k | ~180µs | ~600B |

### Sentinel HA (`sentinel/summary.md`)

| Phase | Expected |
|-------|----------|
| Failover detection | ~5s (`down-after-milliseconds`) |
| Promotion + reconnect | 5 to 30s total window |
| Post-recovery | Old master becomes replica |

## Lessons learned

**1,000 actual RPS** is the honest ceiling on my laptop stack. Better than marketing "10k ready."

Idempotency **942 RPS replay** and throughput **1,000 RPS** are different paths. Both are impressive. Different claims.

Hot-key **4,940 actual RPS** with **99.9% 429** proved Lua enforcement scales even when "load" is high. Latency is a separate matter.

I re-run `collect-environment.ps1` every quarter. Citing old numbers after a hardware upgrade is embarrassing.

Next: auto-sync hook from `parse-results.py` so tables in `results.md` do not drift.
