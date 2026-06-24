# Failure Mode: Lease Expiration (Idempotency)

**Status:** Documented  
**Severity:** Medium (correctness edge case)  
**Components:** `claim.lua`, `IDEMPOTENCY_LOCK_TTL_MS`, fencing tokens, `ErrStaleFence`

---

## Problem Statement

An idempotency record in `processing` state holds a lease (`lock_until` field). If the worker crashes, hangs past TTL, or loses network, the lease must expire so another request can reclaim the key. Expiration without fencing would let two workers both believe they own the key. I paired lease reclaim with **fence token rotation**.

## Why the problem exists

`IDEMPOTENCY_LOCK_TTL_MS` (default 60000) is a safety bound, not a precise job timer. Upstream p99 latency + GC pauses can approach the lease. Clients retrying during `in_progress` see 409 until lease expires or work completes. When `now_ms >= lock_until`, `claim.lua` reissues processing state with a **new** `fence_token`.

## Design goals

- Automatic reclaim in Lua. no cron sweeper.
- `PEXPIRE` on meta key aligned with lock TTL during processing phase.
- Stale owner `Complete`/`Fail` rejected → `ErrStaleFence`.
- Client retries after expiry get fresh claim (code `1`) or replay if another worker finished.
- Tunable lease via `IDEMPOTENCY_LOCK_TTL_MS` without code change.

## Alternative approaches considered

| Alternative | Issue |
|-------------|-------|
| Infinite lease | Dead keys block clients until manual delete |
| Heartbeat extend in app | Extra Redis traffic; worker must remember |
| Delete key on timeout | Loses request_hash. cannot detect replay |
| No reclaim (fail forever) | Violates availability for crash recovery |

## Final architecture

`claim.lua` processing branch:

```lua
if status == 'processing' then
  local lock_until = tonumber(redis.call('HGET', meta_key, 'lock_until') or '0')
  if now_ms < lock_until then
    return {3, lock_until - now_ms}  -- in_progress, Retry-After
  end
  -- expired: reclaim
  redis.call('HSET', meta_key, 'status', 'processing', 'lock_until', now_ms + lock_ttl_ms, 'fence_token', fence_token)
  return {1, fence_token}
end
```

Go path:

- Winner gets new `FenceToken` in `ClaimResponse`.
- Old worker's `complete.lua` compares stale token → `{0}` → `ErrStaleFence`.
- Sidecar `completeIdempotent` / `failIdempotent` swallow or log error depending on path.

Completed phase uses `IDEMPOTENCY_COMPLETED_TTL_MS` (default 24h). separate from processing lease.

## Tradeoffs

| Shorter lease | Longer lease |
|---------------|--------------|
| Faster reclaim after crash | More duplicate upstream risk if reclaim before first completes |
| More `ErrStaleFence` under slow upstream | Longer 409 windows for clients |

## Failure modes

| Scenario | Outcome |
|----------|---------|
| Worker dies at 30s, lease 60s | 30s of 409, then reclaim |
| Worker slow 70s, lease 60s | Reclaim at 60s; risk dual upstream if first still running |
| Reclaim then first completes | `ErrStaleFence` on first. second response should persist |
| Client retries before expiry | 409 `ResultInProgress` with `Retry-After` ms |
| Redis TTL expires entire key | Rare if `PEXPIRE` misaligned. key gone, fresh claim |

## Operational concerns

**Sizing formula I use:** `IDEMPOTENCY_LOCK_TTL_MS ≥ p99(upstream latency) × 2 + 5000ms margin`.

**Signals:**

- `idempotency_claims_total{result="in_progress"}` sustained high → lease too short or upstream slow.
- Logs: `ErrStaleFence` rate → lease too short relative to processing time.
- Test: `internal/idempotency/store_test.go` stale fence case.

**Do not** set lease to hours. blocks legitimate key reuse and inflates Redis memory.

## Performance implications

Reclaim is one Lua path. no extra processes. Frequent reclaims under load mean **multiple upstream attempts** for one logical operation if fencing fails operationally. financial risk, not CPU. `Retry-After` on 409 reduces thundering herd but adds client wait time.

## Lessons learned

I set 60s lease copying Stripe docs without measuring my demo backend. p99 was 45s under load and reclaims spiked. Tuning TTL is **workload-specific**, not copy-paste. Fencing tokens turned the scary dual-worker window into a detectable `ErrStaleFence` instead of merged garbage responses. Lease expiration is a feature for crash recovery, a bug if sized wrong for latency.

**References:** `internal/idempotency/lua/claim.lua`, `docs/decisions/why-fencing-tokens.md`, `docs/failure-modes/duplicate-requests.md`
