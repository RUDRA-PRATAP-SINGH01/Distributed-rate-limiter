# Why Fencing Tokens

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** Idempotency lease reclaim, stale owner prevention, `ErrStaleFence`

---

## Problem Statement

When a processing lease expires and a second worker reclaims an idempotency key, the first worker might still be alive. slow GC, long upstream call, or partition. Without fencing, the latecomer could overwrite a completed response or mark the wrong outcome. I needed a mechanism where only the **current lease holder** can call `Complete` or `Fail`.

## Why the problem exists

Idempotency leases are not locks with automatic cancellation of in-flight goroutines. `IDEMPOTENCY_LOCK_TTL_MS` only bounds how long Redis considers the key "in progress." After expiry, `claim.lua` issues a new `fence_token` to the reclaiming worker. The old worker still holds its token in memory. This is the classic stale primary problem from distributed storage, shrunk to idempotency key scope.

## Design goals

- Per-claim UUID token: Generated in `RedisStore.Claim()` via `uuid.New().String()`, stored in Redis hash field `fence_token`.
- Atomic verify-and-write: `complete.lua` and `fail.lua` compare `ARGV[fence_token]` to `HGET fence_token` before mutating status.
- Explicit Go error: Mismatch returns `{0}` from Lua → `idempotency.ErrStaleFence` in `Complete()` and `Fail()`.
- No silent corruption: Stale completes fail loudly instead of merging responses.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **Version numbers only** | Same idea; UUID fence is simpler to generate per claim. |
| **Delete key on lease expiry** | Loses in-progress detection; races on recreate. |
| **Distributed lock (Redlock) separate from idempotency** | Two systems to fail; Lua already atomic. |
| **Ignore stale completes** | Silent data loss. worse than `ErrStaleFence`. |
| **Infinite lease TTL** | Blocks legitimate client retries after worker death. |

## Final architecture

Sequence:

1. Claim wins: `claim.lua` sets `fence_token` + `lock_until = now + IDEMPOTENCY_LOCK_TTL_MS`.
2. Sidecar holds token: `ClaimResponse.FenceToken` passed to `forwardIdempotent` → `completeIdempotent` / `failIdempotent`.
3. Complete: `complete.lua` lines 29 to 32 reject if `current_fence ~= fence_token`.
4. Reclaim: Expired lock → new claim overwrites `fence_token`; old Complete returns `{0}` → `ErrStaleFence`.

```go
// internal/idempotency/errors.go
ErrStaleFence = errors.New("idempotency fence token mismatch. stale lease holder")
```

Sidecar paths: `completeIdempotent`, `failIdempotent` in `cmd/sidecar/main.go` always pass `claim.FenceToken` from the winning claim only.

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| UUID fence tokens | Unforgeable per claim | Extra field in Redis hash |
| Lua-enforced fence | Atomic with status transition | Stale worker gets hard error |
| Reclaim on lease expiry | Availability after crashes | Brief window of dual live workers |
| No automatic retry on ErrStaleFence | Prevents corrupting completed state | Ops must understand logs |

## Failure modes

- Slow worker completes after reclaim: `Complete` returns `ErrStaleFence`; new owner’s response should win if it completes successfully.
- Lost fence token in sidecar memory: Process restart mid-request loses token. complete fails; client retries get replay or new claim.
- Clock skew: `lock_until` uses `NowMillis()` on server and Redis ARGV; extreme skew affects lease timing only, not fence equality check.

## Operational concerns

- Correlate logs: `Idempotency claimed key` vs `ErrStaleFence`. indicates lease too short or upstream too slow.
- Test coverage: `internal/idempotency/store_test.go` asserts `ErrStaleFence` for stale owner.
- Diagram: `docs/diagrams/fencing-flow.md`.
- Do not disable fencing in Lua; it is the correctness backbone for reclaim.

## Performance implications

Fence check is a string compare inside existing Lua scripts. negligible overhead. The cost of fencing is operational: shorter `IDEMPOTENCY_LOCK_TTL_MS` increases reclaim frequency and stale-complete errors; longer TTL increases client wait on `ResultInProgress` (409).

## Lessons learned

I borrowed this directly from Martin Kleppmann’s fencing token write-up, applied at idempotency granularity. One subtle issue: **without fencing, lease reclaim looks like success**. tests passed until I injected artificial delay on the first worker. `ErrStaleFence` turned a silent corruption bug into a measurable failure. In production I size `IDEMPOTENCY_LOCK_TTL_MS` to upstream p99 × 2, not mean latency.

**References:** `internal/idempotency/lua/complete.lua`, `internal/idempotency/lua/claim.lua`, `internal/idempotency/store.go`, `docs/deep-dives/fencing-tokens.md` (when generated)
