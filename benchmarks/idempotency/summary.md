# Idempotency Benchmark Summary

Tests run against Docker Compose stack (Redis + Limiter + Sidecar + Demo) on local machine.

## Race Test — 100 Concurrent Same Key

**Script:** `idempotency-race.js`  
**Scenario:** 100 VUs, 1 iteration each, identical `Idempotency-Key`

| Metric | Result |
|--------|--------|
| Total requests | 100 |
| Upstream executions | **1** (verified via `/api/orders/count`) |
| Claim latency p95 | **14.9 ms** |
| Claim latency avg | **9.2 ms** |
| 409 in-progress (k6 "failed") | **14%** |
| Checks passed | **100%** (201 or 409 + idempotency header) |

**Verdict:** Exactly one upstream execution under 100-way contention. Remaining requests received `409 Conflict` with `X-Idempotency-Status: in_progress` until the winner completed.

```bash
k6 run benchmarks/idempotency/idempotency-race.js
```

## Replay Test — Cached Response Throughput

**Script:** `idempotency-replay.js`  
**Scenario:** Seed one completed key, then 50 VUs replay for 30s

| Metric | Result |
|--------|--------|
| Throughput | **~942 RPS** |
| p50 latency | **2.1 ms** |
| p95 latency | **5.7 ms** |
| p99 latency | — |
| Error rate | **0%** |
| Upstream calls during replay | **0** |

**Verdict:** Replay path is Redis-only (no limiter quota burn, no upstream). Suitable for client retry storms.

```bash
k6 run benchmarks/idempotency/idempotency-replay.js
```

## Unit Tests (Go + miniredis)

| Test | Proves |
|------|--------|
| `TestClaimSingleWinnerUnderConcurrency` | 100 goroutines → 1 claim, 99 in_progress |
| `TestCompleteAndReplay` | Stored response replayed verbatim |
| `TestHashMismatch` | Same key + different body → rejected |
| `TestExpiredLockReclaim` | Expired processing lease → reclaim |
| `TestGetRecordAndDelete` | Admin read/delete paths |

```bash
go test ./internal/idempotency/... -v
```

## Overhead vs Baseline

Idempotency adds one Redis Lua round-trip per mutating request (~2–10 ms on local Docker). Replay requests skip upstream entirely and average **2.5 ms** end-to-end at ~940 RPS.
