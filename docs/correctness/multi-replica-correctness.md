# Multi-Replica Correctness

## Problem Statement

Horizontal scale is worthless if effective quota multiplies by replica count. I must prove that **global caps hold** when traffic splits across multiple sidecars (and optionally multiple limiters) sharing one Redis primary.

This document covers runtime-proven multi-replica scenarios, the test harness, and known invalid runs.

## Why the problem exists

Clients load-balance across sidecar replicas (`:9090`, `:9092` with `docker-compose.scale.yml`). Each replica has:

- Its own `sync.Map` denial cache
- Its own `singleflight.Group`
- No shared memory

Only Redis Lua provides the fleet-wide admission count. Process-local optimizations must not weaken that.

## Design goals

| Scenario | Expected | Evidence |
|----------|----------|----------|
| 60 concurrent, 2 sidecars, 1 user, cap=10 | ≤10 allowed, rest 429 | RUNTIME-PROVEN |
| Override write on replica A visible on B | After `RefreshGeneration` | TEST-PROVEN |
| CB half-open, 64 concurrent /check | ≤3 admitted, rest 503 | RUNTIME-PROVEN |
| 40 parallel idempotent POSTs, 2 sidecars | 1×200, 39×409 | RUNTIME-PROVEN |

## Alternative approaches considered

| Approach | Issue |
|----------|-------|
| Sticky sessions to one sidecar | Uneven load; failover breaks stickiness |
| Per-replica quota counters | Limit × N replicas |
| Gossip sync between sidecars | Eventually consistent; minutes of drift |

Shared Redis + Lua is the correctness anchor.

## Final architecture

### Topology for proof runs

Base compose + scale overlay:

```bash
docker compose -f docker-compose.yml -f docker-compose.scale.yml --profile scale up --build
```

| Service | Port | Role |
|---------|------|------|
| sidecar (a) | 9090 | Primary proxy |
| sidecar-b | 9092 | Second replica |
| limiter | 8080 | Shared central limiter |
| limiter-b | 8083 | Optional second limiter (scale profile) |
| redis | 6379 | Single primary |

Default algorithm: **sliding window** (`ALGORITHM=sliding`, `CAPACITY=10`, `WINDOW_SEC=60`). Quota proof uses `ZCARD sw:{user}` = 10 after burst.

### Runtime test: 60 concurrent, 10/50

Documented in `docs/benchmarks/final-benchmark-report.md` §10:

| Parameter | Value |
|-----------|-------|
| Concurrent requests | **60** |
| Sidecars | **9090 + 9092** |
| Users | **1** (shared key) |
| Capacity | **10** |
| Result | **10 allowed, 50 denied** |
| Redis verification | **`ZCARD=10`** on `sw:{user}` |

**Evidence tag:** RUNTIME-PROVEN (clean stack, Phase 4B methodology).

**Invalid run (documented):** Same test during outage recovery → 23 allowed. Polluted environment — not a regression. Always verify Redis health before quota proofs.

### Override generation consistency

`internal/override/override.go`:

- Monotonic `config:generation` incremented on every `SetOverride` / `DeleteOverride` (pipeline with `HSET`/`DEL`).
- Each limiter replica calls `RefreshGeneration` before reading overrides on `/check_hierarchical`.
- Local `sync.Map` cache cleared when generation advances.

**Tests** (`override_test.go`):

- `TestOverrideSetVisibleAcrossReplicas` — write on store A, `RefreshGeneration` on B → visible (TEST-PROVEN).
- `TestOverrideDeleteVisibleAcrossReplicas` — delete propagates (TEST-PROVEN).
- `TestOverrideRedisInterruptionDoesNotPermanentStaleCache` — Redis blip during refresh → bounded TTL staleness (TEST-PROVEN).

**Runtime:** Admin capacity bump visible on next hierarchical check per replica — not after fixed 5 s wait (`OVERRIDE_CACHE_TTL_MS` bounds read amplification only).

### Circuit breaker: 3/61 at half-open

When central limiter circuit is **half-open**, probe budget is global in Redis (`cb:{target}` via `allow.lua`).

| Parameter | Value |
|-----------|-------|
| Concurrent requests | **64** |
| Phase | Seeded half-open on clean stack |
| `HalfOpenMaxProbes` | **3** (default) |
| Result | **3 admitted, 61×503** |
| Evidence | RUNTIME-PROVEN |

**Unit test mirror:** `TestHalfOpenConcurrentProbeBound` — 32 workers, `admitted ≤ HalfOpenMaxProbes`, `half_open_calls ≤ max` (TEST-PROVEN, `-race`).

CB rejections are **503**, not 429 — see [distributed-invariants.md](distributed-invariants.md).

### k6 multi-replica workload

`benchmarks/scripts/multi-replica-e2e.js`:

- Alternates sidecar A (`9090`) and B (`9092`).
- 10 shared user IDs → intentional quota contention.
- Default 500 RPS × 60 s — measures **sustained multi-replica path**, not the 60-concurrent burst proof.

Result snapshot (`a1de9ec`): actual **429 RPS**, p99 **7.28 ms**, 106×200 / 29,895×429 — correctness under load, not peak unique-user throughput.

## Tradeoffs

- **Two replicas proven, not N:** Linear scale throughput not guaranteed (`limitations.md`).
- **Single Redis master:** All replicas share one serialization point — correct but hot.
- **Process-local cache not shared:** Two replicas may each hit Redis once on simultaneous deny miss — safe, not over-admitting.
- **Flat `/check` ignores overrides:** Multi-replica override proofs apply to hierarchical path only.

## Failure modes

1. **Stale override after admin write:** Replica missed `RefreshGeneration` (Redis GET failure) → old cap until TTL.
2. **Denial cache on both replicas:** User sees 429 from cache on both — consistent deny, not over-admit.
3. **Split limiter replicas:** Both talk to same Redis — OK; different Redis would break all invariants.
4. **Running proofs during failover:** Invalid results (see 23-allowed polluted run).
5. **FAIL_OPEN sidecars:** Bypass quota on limiter errors — breaks multi-replica guarantee.

## Operational concerns

- Enable scale profile only for proof/staging unless second sidecar is required in prod.
- After admin override: verify with `GET config:generation` and hierarchical check from **each** replica.
- CB half-open storms: watch `circuit_breaker_state` and `circuit_breaker_rejections_total`.
- Proof checklist:
  1. `docker compose ps` — all healthy
  2. Redis `PING`
  3. Fire 60 concurrent to `:9090` and `:9092` with same `user_id`
  4. Assert ≤10×200, `ZCARD sw:{user} == 10`

## Performance implications

Multi-replica **correctness** does not imply multi-replica **throughput**:

| Workload | Actual RPS | Notes |
|----------|------------|-------|
| Sidecar e2e 1,000 target | **872** | Single sidecar path |
| Multi-replica 500 target | **429** | Shared users, high 429 rate by design |
| 60-concurrent burst | N/A | Latency-bound correctness test |

Adding sidecars reduces per-replica load but Redis remains the global bottleneck (~870 RPS knee).

## Lessons learned

The 60/10/50 test is my gold standard demo for "why Redis Lua matters." It is simple to explain and hard to fake without atomicity.

I document **invalid runs** (23 allowed during recovery) so future me does not chase ghosts. Runtime proofs require a **clean stack** — same discipline as benchmark methodology.

Override generation was the second multi-replica surprise: TTL-only cache would have left replicas divergent for seconds. Generation check on every hierarchical read fixed it without Pub/Sub.
