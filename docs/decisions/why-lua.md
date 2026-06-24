# Why Lua Scripts in Redis

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** Rate limiting, idempotency, circuit breakers, routing outcomes, audit append

---

## Problem Statement

Distributed rate limiting breaks the moment two goroutines on two machines both read "remaining=1" and both allow the request. I needed every increment, compare, and state transition to happen atomically in one server-side execution, without MULTI/EXEC races or application-level locks that fail across process crashes.

## Why the problem exists

Redis commands are single-key atomic, but quota checks are multi-field workflows: read tokens, subtract one, set TTL, return remaining. Idempotency claims must decide between replay, in-progress, hash-mismatch, and new-claim in one decision. Circuit breaker Allow/Record pairs must not double-count probes during concurrent failover traffic. Naive `GET` + `SET` from Go loses races under concurrent sidecars.

## Design goals

- Single round-trip atomicity: For hot paths.
- Embeddable scripts: Via `//go:embed` so binaries are self-contained (no runtime script fetch).
- Explicit return codes: From Lua (integers) parsed in Go. easier to test than parsing nested JSON.
- Consistent pattern: Across packages: `internal/limiter/lua/`, `internal/idempotency/lua/`, `internal/circuitbreaker/lua/`, `internal/routing/lua/`, `internal/audit/lua/`.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **WATCH/MULTI/EXEC** | Retry loops under contention; ugly error handling in Go. |
| **Redlock (distributed locks)** | Adds latency and famous edge cases; overkill when Redis single-threaded execution suffices. |
| **Application mutex + Redis** | Mutex does not cross hosts; useless for sidecar fleet. |
| **Compare-and-swap with version fields** | Possible but more round-trips and client complexity than one `EVAL`. |
| **Move logic to stored procedures in SQL** | Wrong store; I already committed to Redis for counters. |

## Final architecture

Each subsystem registers a `redis.Script` at init:

| Package | Script | Purpose |
|---------|--------|---------|
| `internal/limiter` | `token_bucket.lua`, `sliding_window.lua`, `hierarchical.lua` | Atomic allow/deny + remaining |
| `internal/idempotency` | `claim.lua`, `complete.lua`, `fail.lua` | Claim/replay/complete with fence check |
| `internal/circuitbreaker` | `allow.lua`, `record.lua` | State machine transitions |
| `internal/routing` | `record_outcome.lua` | EMA latency + health_score update |
| `internal/audit` | `append.lua` | Event write + ZSET index + trim |

Example: `claim.lua` returns `{1, fence_token}` for new claim, `{2, status, headers, body}` for replay, `{3, retry_after_ms}` for in-progress, `{0}` for hash mismatch. Go maps these in `idempotency.RedisStore.Claim()`.

Limiter selects algorithm via `ALGORITHM=token|sliding` and runs the corresponding script on every `/check` and `/check_hierarchical`.

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| Lua in Redis | True atomicity, one RTT | Scripts must be reviewed like migrations |
| Embedded `.lua` files | Reproducible deploys | Slightly larger binary |
| Integer return codes | Fast parsing | Documented contract per script |
| Server-side TTL logic | Fewer client bugs | Harder to debug than stepping through Go |

## Failure modes

- SCRIPT LOAD failures after Redis upgrade: Rare; go-redis re-sends script body on `EVAL`.
- Long-running scripts: Block Redis single thread; I kept scripts O(1) per key.
- Key mismatch in ARGV: Wrong fence token → `complete.lua` returns `{0}` → Go returns `ErrStaleFence`.
- Redis down: All Lua paths fail; circuit breaker records `OutcomeFailure` on `TargetRedis`.

## Operational concerns

- When changing a script, treat it as a **breaking schema change**. rolling deploys must tolerate old+new return codes briefly or flush state.
- Use `redis_failover_reconnects_total` and `*_redis_duration_seconds` histograms per subsystem to spot script latency regressions.
- Chaos tests (`chaos/chaos_test.ps1`) assume Lua-backed enforcement; disabling Redis invalidates test assumptions.

## Performance implications

Lua execution is in-process to Redis. typically sub-millisecond for my scripts. The dominant cost is network RTT, which is why hierarchical limits use one script for four buckets instead of four round-trips. Idempotency adds one Lua call before upstream work; routing adds one on every gateway outcome. Benchmarks under `benchmarks/` compare algorithms; token bucket Lua wins on steady-state throughput, sliding window on burst fairness.

## Lessons learned

The first version used separate `INCR` + `EXPIRE` calls and I watched limits overshoot by 15 to 20% under k6 load. Moving to Lua fixed enforcement accuracy immediately. The subtle lesson: **return codes are API contracts**. `ErrStaleFence` exists because `complete.lua` deliberately returns `{0}` instead of throwing Redis errors, letting Go distinguish stale owners from store outages. I would not ship another distributed counter without server-side atomicity.

**References:** `internal/limiter/lua/`, `internal/idempotency/lua/claim.lua`, `internal/circuitbreaker/lua/`, `benchmarks/enforcement/`
