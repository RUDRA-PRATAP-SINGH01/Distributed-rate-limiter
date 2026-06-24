# Idempotency Benchmarks

## Problem Statement

Mutating API calls (POST `/api/orders`) can duplicate upstream execution through client retries and network duplicates. Double charge, double ship. I needed to prove that the sidecar idempotency layer guarantees **exactly one upstream execution** under 100-way contention, and that completed key replay serves at **~940+ RPS** without touching upstream. Numbers come from `benchmarks/idempotency/summary.md`. I anchor production narrative to those same figures.

## Why the problem exists

Idempotency is hard in distributed systems because:

- **Claim race**. 100 parallel POSTs with the same `Idempotency-Key`. Without atomic claim, all of them hit upstream.
- **In-progress window**. Losers need `409 Conflict` plus `X-Idempotency-Status: in_progress` until the winner completes.
- **Replay path**. Completed record lives in Redis. A retry storm must not touch upstream.
- **Redis dependency**. If claim Lua fails, fail-closed (503), not fail-open with unlimited duplicates.

## Design goals

1. **Single winner**. 100 VUs, 1 key, 1 upstream execution (verified via `/api/orders/count`).
2. **Fast replay**. p95 under 10 ms, 0% errors, 0 upstream calls during replay phase.
3. **Hash mismatch rejection**. Same key, different body, rejected (Go unit tests).
4. **Lease reclaim**. Expired processing lock is reclaimable (`TestExpiredLockReclaim`).
5. **Admin visibility**. `GET/DELETE /admin/idempotency/{scope}/{key}`.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| DB unique constraint only | Race before insert; no in-progress semantics |
| Client-side dedup | Untrusted; duplicates still arrive |
| Kafka exactly-once | Overkill for HTTP sidecar |
| Optimistic locking in Go | Redis still needed; Lua is simpler |
| Fail-open on Redis down | Rejected. Duplicates are worse than 503 |

Redis Lua claim plus complete pattern (`internal/idempotency/`) is the production path.

## Final architecture

**Sidecar env** (`docker-compose.yml`):

```
ENABLE_IDEMPOTENCY=true
REDIS_ADDR=redis:6379
IDEMPOTENCY_LOCK_TTL_MS=60000
IDEMPOTENCY_COMPLETED_TTL_MS=86400000
IDEMPOTENCY_FAIL_OPEN=false
```

**Race test**. `benchmarks/idempotency/idempotency-race.js`:

| Metric | Result |
|--------|--------|
| Total requests | 100 |
| Upstream executions | **1** |
| Claim latency p95 | **14.9 ms** |
| Claim latency avg | **9.2 ms** |
| 409 in-progress (k6 "failed") | **14%** |
| Checks passed | **100%** |

```bash
k6 run benchmarks/idempotency/idempotency-race.js
```

**Replay test**. `benchmarks/idempotency/idempotency-replay.js`:

| Metric | Result |
|--------|--------|
| Throughput | **~942 RPS** |
| p50 latency | **2.1 ms** |
| p95 latency | **5.7 ms** |
| Error rate | **0%** |
| Upstream calls during replay | **0** |

```bash
k6 run benchmarks/idempotency/idempotency-replay.js
```

**Go unit tests:**

```bash
go test ./internal/idempotency/... -v
```

| Test | Proves |
|------|--------|
| `TestClaimSingleWinnerUnderConcurrency` | 100 goroutines, 1 claim |
| `TestCompleteAndReplay` | Verbatim response replay |
| `TestHashMismatch` | Body mismatch rejected |
| `TestExpiredLockReclaim` | Stale lock reclaim |

**Admin:**

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" \
  http://localhost:8082/admin/idempotency/user/my-key
curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" \
  http://localhost:8082/admin/idempotency/user/my-key
```

## Tradeoffs

- **~2 to 10 ms claim overhead**. One Redis Lua RTT per mutating request (local Docker).
- **409 as k6 failure**. Race test shows 14% as "failed." Checks still pass at 100%. Parse scripts must know this.
- **24h completed TTL**. `IDEMPOTENCY_COMPLETED_TTL_MS=86400000`. Longer retries need a config change.
- **No quota burn on replay**. Replay skips the limiter path. Intentional for retry storms.
- **Redis SPOF for mutating paths**. `chaos_test.ps1` returns 503 when Redis is dead.

## Failure modes

1. **Duplicate upstream**. Claim Lua bug or fail-open. Race test catches it (count greater than 1).
2. **Stuck in_progress**. Winner crashes before complete. Lease TTL plus reclaim path.
3. **Hash mismatch false negative**. Canonical body hash drift. Unit test guards this.
4. **Replay stale response**. Completed TTL expired. Client gets a fresh execution.
5. **Concurrent complete**. Second complete rejected. Audit trail logs the decision.

## Operational concerns

- Run idempotency benchmarks after `docker compose up --build -d` full stack (Redis + Limiter + Sidecar + Demo).
- Verify upstream count endpoint: `GET http://localhost:8081/api/orders/count` (demo backend).
- Monitor idempotency metrics: claim latency, replay rate, in_progress 409 rate.
- Chaos: kill Redis during race test. Expect 503, not duplicate upstream on recovery without client retry discipline.
- Admin delete for stuck keys during an incident. Document key scope (`user`, `tenant`).

## Performance implications

**Overhead vs baseline** (`idempotency/summary.md`):

- Claim path: p95 **14.9 ms**, avg **9.2 ms** under 100-way contention
- Replay path: **~942 RPS**, p95 **5.7 ms**, avg replay **2.5 ms** end-to-end
- Stack sustainable throughput without idempotency contention: **~1,000 RPS** (`summary.md`)

Replay path is Redis-only. No limiter quota burn, no upstream. Suitable for client retry storms after transient 503.

Idempotency does **not** collapse the overall RPS knee. Hot-key and saturation limits still dominate at **5,000 target, 1,353 actual**.

## Lessons learned

First race test run: upstream count was 3. A subtle TOCTOU in the Lua claim script. After the fix, 100 VUs consistently produced **1 execution**.

I spent hours debugging k6 "failures" on 409 when checks passed at 100%. The methodology doc now has an explicit note about this.

Replay at **942 RPS** convinced me a retry storm will not kill upstream. That is the number I tell ops, not a micro-benchmark.

Fail-open=false is non-negotiable. The chaos test proved Redis down means 503, not silent duplicate risk.

Next: add idempotency race as an optional gate in `run-all.ps1`.
