# Distributed Invariants

## Problem Statement

When N sidecars and M limiter replicas share one Redis primary, certain properties must hold fleet-wide regardless of process-local optimizations. This document states the **non-negotiable invariants**, how the code enforces them, and what evidence exists for each.

Violating any invariant here is a severity-1 correctness bug, not a performance tuning issue.

## Why the problem exists

Distributed rate limiting splits **stateless proxies** (sidecars) from **authoritative quota state** (Redis). Every optimization — denial cache, singleflight, override local cache — is a potential correctness leak if it allows more admissions than Redis would have granted.

Operators also confuse **429** (quota) with **503** (infrastructure). Mixing them causes wrong runbook actions during Redis outages.

## Design goals

| Invariant | Enforcement | Evidence |
|-----------|-------------|----------|
| **No quota over-admit** | Lua atomic refill+deduct; allowances never served from cache | SOURCE + TEST + RUNTIME |
| **429 ≠ infrastructure failure** | Limiter maps Redis errors → 503; quota deny → 429 | SOURCE + TEST |
| **Denial cache safe** | Only `Allowed=false` entries served from cache | SOURCE + TEST |
| **Fencing on idempotency complete** | `fence_token` checked inside `complete.lua` | SOURCE + TEST |
| **Audit drain before Redis close** | `Shutdown()` → `RedisCloseSafe()` → `Close(rdb)` | SOURCE + TEST |

## Alternative approaches considered

| Approach | Why rejected for invariants |
|----------|----------------------------|
| Fail-open on Redis down | Quota bypass during outage |
| Cache allowed responses | Stale allow → over-admit |
| Fence check in Go between claim/complete | Race window |
| Close Redis before audit workers stop | Torn writes / panics |

Fail-closed quota and process-local **deny-only** cache are deliberate product decisions.

## Final architecture

### 1. Quota: no over-admit

**Authority:** Redis Lua on the primary. Sidecar never invents allowance.

```
Client → Sidecar → Limiter → EVAL lua → {allowed, remaining}
```

- Token bucket: `rate:{id}` + `token_bucket.lua`
- Sliding window: `sw:{id}` + `sliding_window.lua`
- Hierarchical: four `rate:*` keys + `hierarchical.lua`

Sidecar `serveNormal` (`cmd/sidecar/main.go`):

- Cache hit + `!entry.Allowed` → serve **429** without limiter call.
- Cache hit + `entry.Allowed` → **ignored**; re-check limiter (`TestSidecar_AllowanceCache`).
- After limiter response → store entry (allow or deny); only denials benefit on replay.

**Concurrency tests:**

- `TestTokenBucket_Atomicity_30Goroutines`: cap=10, 30 goroutines → exactly 10 allowed (TEST-PROVEN).
- `TestConcurrency_RateChecking`: cap=50, 150 HTTP goroutines → exactly 50×200, 100×429 (TEST-PROVEN).
- Runtime: 60 concurrent, 2 sidecars, cap=10 → **10 allowed / 50 denied**, `ZCARD=10` (RUNTIME-PROVEN).

### 2. 429 is not circuit-breaker failure

**Limiter** (`cmd/limiter/main.go`):

| Condition | HTTP | Body |
|-----------|------|------|
| `Allow` returns `allowed=false` | **429** | `"Too many requests"` + `Retry-After` |
| Redis/Lua error | **503** | `"Rate limiter unavailable"` |
| Redis circuit open (limiter-side) | **503** | Circuit rejection before Lua |

**Sidecar** (`checkRateLimit`):

| Limiter response | Sidecar behavior |
|------------------|------------------|
| **429** | `limitResult{allowed: false}` → client **429** (not an error) |
| **503** or network error | `return limitResult{}, err` → client **503** (unless `FAIL_OPEN=true`) |
| Circuit open on central limiter | **503** `"central limiter circuit open"` |

Circuit breaker trips produce **503**, never **429**. Metrics: `circuit_breaker_rejections_total` vs `rate_limiter_requests_total{allowed="false"}`.

### 3. Denial cache invariant

Cache key: `userID` (flat) or `tenant|user|path` (hierarchical) — pipe separator prevents collision (`TestSidecar_CacheIsolation`).

Properties:

1. TTL-bound (`CACHE_TTL` env); sweeper removes expired entries (`StartCacheSweeper`).
2. Denial cache reduces Redis load; it **cannot** increase admissions.
3. Hammer benchmark: **618,175** requests, **618,074** cached 429s, p99 **7.11 ms** (BENCHMARK-PROVEN).

### 4. Fencing tokens (idempotency)

Claim assigns monotonic `fence_token` in Redis. Complete and Fail scripts verify:

```lua
-- complete.lua
if current_fence ~= fence_token then return {0} end
```

Go maps `{0}` → `ErrStaleFence`. Stale workers cannot overwrite a reclaimed lock. This is **not** at-most-once upstream execution — see [limitations.md](../limitations.md).

### 5. Audit Redis close ordering

Limiter shutdown sequence (`cmd/limiter/main.go`):

1. `srv.Shutdown` — stop accepting checks.
2. `auditStore.Shutdown(ctx)` — drain async queue.
3. `if auditStore.RedisCloseSafe()` → `redisclient.Close(rdb)`.
4. Else: log warning, **skip** Redis close.

`audit.Shutdown` (`internal/audit/shutdown.go`):

- Closes queue once (`shutdownBeginOnce`).
- `waitWorkers` blocks until workers exit or context deadline.
- `RedisCloseSafe()` true only when `state == stateStopped`.

`TestShutdown_RedisCloseOrdering` proves no Redis ops after close + post-shutdown `Record` drops without touching Redis (TEST-PROVEN).

## Tradeoffs

- **Fail-closed default:** Redis down → 503 storm, but no silent over-admit.
- **Denial cache staleness:** User may see 429 briefly after quota refills until TTL expires — under-admit acceptable, over-admit not.
- **Best-effort audit:** Queue full or post-shutdown → `audit_dropped_total`; not a quota invariant.
- **Idempotency replay skips quota:** `idempotent_replay=true` on hierarchical check returns synthetic allow — by design for cached responses, not fresh admissions.

## Failure modes

1. **FAIL_OPEN=true on sidecar:** Limiter errors forward to upstream — explicit escape hatch, breaks invariant.
2. **Denial cache + wrong key:** Mitigated by hierarchical pipe-separated cache keys.
3. **Override cache after generation GET failure:** Bounded staleness up to TTL — may temporarily under-enforce new lower caps.
4. **Audit shutdown timeout:** Redis close skipped; workers may still run — logged, not silent.
5. **Polluted runtime test:** 60-concurrent multi-sidecar run during outage recovery once showed 23 allowed — documented invalid in `final-benchmark-report.md` §10.

## Operational concerns

- Alert on **503 rate** separately from **429 rate**.
- `chaos/chaos_test.ps1`: Redis stop → sidecar **503** in ~1 s, not unlimited 200s.
- Rolling deploy: limiter drains HTTP before audit before Redis close.
- Never enable allowance caching without a design review — current code explicitly does not.

## Performance implications

Invariants are not free:

- Every **allow** path hits Redis (or limiter HTTP → Redis).
- Denial cache converts Redis-bound abuse into process-local **429** at ~17k RPS hammer rate.
- Singleflight collapses concurrent misses to one limiter RTT — safe because result is replayed to waiters identically.

## Lessons learned

I treat **429 vs 503** as part of the correctness contract, not UX polish. On-call runbooks depend on it.

The denial-cache invariant ("deny only") was the hardest to explain to reviewers who wanted to cache allows for speed. **Over-admit is worse than extra Redis RTTs.**

Audit shutdown ordering came from a real race in tests: closing Redis while workers still called `append.lua` produced flaky failures. `RedisCloseSafe()` is the guardrail.
