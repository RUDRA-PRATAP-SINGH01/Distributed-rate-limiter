# Consistency Guarantees

## Problem Statement

Operators and integrators need a precise list of **what this system guarantees** under concurrency, failover, and partial failure — and equally important, **what it does not guarantee**.

This document is the contract boundary. For implementation detail, see [distributed-invariants.md](distributed-invariants.md) and [multi-replica-correctness.md](multi-replica-correctness.md).

## Why the problem exists

"Distributed rate limiter" sounds like it promises exactly-once, linearizable everything. It does not. Without explicit guarantees, teams assume idempotency prevents all duplicate side effects, or that 429 means Redis is down.

## Design goals

Separate guarantees into tiers:

1. **Strong (Redis-primary atomic):** Quota, idempotency claim, circuit state transitions.
2. **Bounded eventual (process-local):** Override cache, denial cache TTL.
3. **Best-effort:** Audit async delivery, trace completeness.
4. **Explicitly not guaranteed:** Exactly-once upstream, linear multi-replica throughput.

## Alternative approaches considered

Publishing only happy-path guarantees was rejected — the invalid 23-allowed runtime run and idempotency lease reclaim cases must be documented to prevent false incidents.

## Final architecture

### Guaranteed (strong)

| Property | Guarantee | Mechanism | Evidence |
|----------|-----------|-----------|----------|
| **Global quota cap** | At most `capacity` (or window `limit`) admissions per key per evaluation | Lua atomic scripts | TEST + RUNTIME |
| **No partial hierarchical deduct** | All 4 levels or none | `hierarchical.lua` phase 2 | TEST-PROVEN |
| **Multi-sidecar quota** | Fleet-wide cap with shared Redis | Same Lua keys | RUNTIME: 10/50/60 |
| **Idempotency claim winner** | Exactly one concurrent claim succeeds per key | `claim.lua` | TEST + RUNTIME: 1×200/39×409 |
| **Fence stale complete** | Old worker cannot complete after reclaim | `complete.lua` fence check | TEST-PROVEN |
| **CB half-open probe bound** | Fleet-wide ≤ `HalfOpenMaxProbes` concurrent probes | `allow.lua` | TEST + RUNTIME: 3/61 |
| **429 vs 503 semantics** | Quota deny ≠ infra failure | Handler status mapping | SOURCE + TEST |
| **SCRIPT SHA/binary alignment** | Deployed Go embeds match executed Lua | `//go:embed` | SOURCE-PROVEN |

### Bounded eventual (process-local)

| Property | Guarantee | Bound | Evidence |
|----------|-----------|-------|----------|
| **Override visibility** | Admin write visible on next successful `RefreshGeneration` per replica | Not instant cross-replica; not TTL-only | TEST-PROVEN |
| **Override staleness on Redis blip** | Old override may persist until TTL if generation GET fails | `OVERRIDE_CACHE_TTL_MS` (default 5 s) | TEST-PROVEN |
| **Denial cache** | Repeat 429 without Redis re-check | `CACHE_TTL` sidecar env | TEST + BENCHMARK |
| **Denial cache safety** | Cache never increases admissions | Deny-only serve path | TEST-PROVEN |

### Best-effort

| Property | Behavior | Evidence |
|----------|----------|----------|
| **Audit delivery** | Async queue; drops when full or after shutdown | `audit_dropped_total` | SOURCE + TEST |
| **Audit before Redis close** | Drain attempted; may timeout | `Shutdown` + `RedisCloseSafe` | TEST-PROVEN |
| **Tracing** | `/health`, `/metrics` skipped | `SkipHealthMetrics` | SOURCE-PROVEN |
| **Routing weights** | EMA/convergence, not instantaneous | `record_outcome.lua` | SOURCE-PROVEN |

### Not guaranteed

| Property | Why | Evidence |
|----------|-----|----------|
| **Exactly-once upstream execution** | Crash after upstream success before `Complete` → lease reclaim | SOURCE + limitations.md |
| **At-most-once side effects** | Fencing protects metadata, not upstream body execution | TEST-PROVEN |
| **Linear throughput scale-out** | Shared Redis primary serializes hot keys | RUNTIME + BENCHMARK |
| **Redis Cluster hierarchical atomicity** | 4 keys may cross slots | SOURCE-PROVEN |
| **Instant override on all replicas** | Requires per-replica generation refresh | TEST + RUNTIME |
| **Audit durability** | No external outbox/SIEM guarantee | SOURCE-PROVEN |
| **Sub-ms quota fairness** | Ms-level timestamps; floor for allow decision | TEST-PROVEN |
| **Allowance caching** | Not implemented — would break cap | TEST-PROVEN |

## Tradeoffs

- **Strong quota vs availability:** Fail-closed on Redis errors (503) — no "unlimited mode" by default.
- **Override cache vs Redis load:** Generation check every hierarchical read — one extra GET for correctness.
- **Denial cache vs freshness:** Cached 429 may outlast refill by TTL — protects Redis, may confuse users who expect immediate retry success.
- **Async audit vs latency:** Record returns before Redis append completes — quota path not blocked.

## Failure modes

1. **Sentinel failover:** Brief window where Redis commands fail → 503s; no over-admit (chaos tests).
2. **Replica lag:** Quota never read from replicas — writes go to primary only (SOURCE-PROVEN).
3. **Clock skew across sidecars:** Refill drift at second/ms boundaries — bounded, not catastrophic.
4. **FAIL_OPEN=true:** Breaks quota guarantee explicitly.
5. **Idempotent replay path:** `idempotent_replay=true` skips Redis quota — only for stored response replay.

## Operational concerns

Use this matrix in incidents:

| Symptom | Likely class | Action |
|---------|--------------|--------|
| 429 + `Retry-After` | Quota (expected) | Capacity/plan review |
| 503 + Redis errors | Infra | Redis/Sentinel runbook |
| 503 + `circuit_state: open` | CB | Upstream/limiter health |
| 409 idempotency | Duplicate in-flight | Client retry with same key |
| Missing audit event | Best-effort gap | Check `audit_dropped_total` |

Evidence refresh commands:

```bash
go test ./internal/limiter/... -run Atomicity
go test ./cmd/sidecar/... -run Singleflight
go test ./internal/override/... -run VisibleAcrossReplicas
```

## Performance implications

Strong guarantees have costs:

- Every admission: ≥1 Redis RTT (hierarchical: 1 RTT, 4× Lua work).
- Sustainable ~872 RPS e2e — not millions RPS per key.
- Denial cache and singleflight improve efficiency **without** weakening strong quota guarantees.

## Lessons learned

Writing "what isn't guaranteed" reduced support tickets more than adding features. The idempotency **not exactly-once** caveat is the most commonly misunderstood.

I tag evidence as SOURCE / TEST / RUNTIME / BENCHMARK in other docs so guarantees do not become folklore. If a property lacks TEST or RUNTIME, it stays out of the "guaranteed" table.

Consistency is **per-key Redis atomic**, not global linearizability across unrelated keys. That distinction matters for system design interviews and production expectations alike.
