# Fencing Tokens — Engineering Journal

## Problem Statement

Distributed locks — including idempotency claims — suffer from a classic failure: **delayed processes**. Sidecar A claims a key, crashes or stalls, lock expires, sidecar B reclaims and completes. A wakes up and tries to complete with a stale view of ownership. Without fencing, A can overwrite B's cached response or mark complete while B already served the client.

I needed a **fence token**: an opaque owner identifier generated at claim time that must be presented on complete/fail. Only the current fence holder can mutate terminal state.

## Why the problem exists

Idempotency locks are not leases with automatic renewal in our design — they use `lock_until` timestamps in Redis (`claim.lua`). When `now_ms >= lock_until`, another claimant can enter `processing` state with a **new** `fence_token`.

The race window:

```
T0: A claims, fence=FA
T1: A slow, lock expires
T2: B claims, fence=FB, executes upstream, completes
T3: A resumes, tries Complete with FA
```

Without fencing, T3 corrupts idempotency. This is the "fencing token" pattern from Martin Kleppmann's distributed systems writing, applied to HTTP idempotency rather than shared storage writes.

## Design goals

1. **Generate on claim** — new UUID per successful claim in `RedisStore.Claim` (`store.go`).
2. **Store in Redis** — `fence_token` field set atomically in `claim.lua`.
3. **Verify on complete/fail** — `complete.lua` and `fail.lua` compare ARGV fence to `HGET fence_token`.
4. **Return to caller** — `ClaimResponse.FenceToken` passed through capturer to `CompleteRequest`.
5. **Clear failure mode** — Go returns `ErrStaleFence` when Lua returns `{0}`.

## Alternative approaches considered

| Approach | Issue |
|----------|-------|
| Monotonic Redis INCR fence | Works but exposes sequence; UUID is stateless |
| Compare-and-swap on version field | Same as fence, less explicit naming |
| etcd lease with keepalive | Extra dependency; overkill |
| No fencing, short lock TTL only | Reclaim + stale complete race remains |
| Upstream idempotency only | Doesn't protect sidecar response cache |

UUID fence in Redis HASH is minimal and debuggable.

## Final architecture

**Claim path** (`internal/idempotency/store.go`):

```go
fenceToken := uuid.New().String()
result, err := s.claimScript.Run(ctx, s.rdb, keys,
    requestHash, NowMillis(), LockTTL, CompletedTTL, fenceToken)
```

**New claim** (`claim.lua` lines 25–34): writes `fence_token` with `status=processing`.

**Reclaim after expiry** (lines 55–66): overwrites `fence_token` with new ARGV[5] — old holder invalidated.

**Complete path** (`complete.lua` lines 29–32):

```lua
local current_fence = redis.call('HGET', meta_key, 'fence_token')
if current_fence ~= fence_token then
  return {0}
end
```

**Fail path** — identical fence check in `fail.lua`.

Go maps `{0}` to `ErrStaleFence` in `Complete()` and `Fail()`.

**Client contract:** fence never crosses the HTTP boundary — it is an internal sidecar handoff from claim middleware to response capturer.

## Tradeoffs

- **UUID string compare in Lua** — negligible cost vs numeric monotonic counters.
- **No fence on read (replay)** — replay path returns cached response without fence — correct; terminal state is immutable until TTL.
- **Reclaim allows duplicate upstream** — fencing prevents corrupt cache, not duplicate execution — product must accept at-most-once **cache** not at-most-once **execution** after reclaim.
- **Failed complete silent to client** — `ErrStaleFence` should be logged; caller may have already sent response to client.

## Failure modes

1. **Stale complete after reclaim** — expected; log and drop — better than corrupt cache.
2. **Missing fence in capturer bug** — complete always fails; stuck `processing` until TTL — monitor in-progress keys.
3. **UUID collision** — astronomically unlikely; ignored.
4. **Manual Redis edit** — ops sets wrong fence — breaks complete; don't hand-edit idem keys.
5. **Fail without fence** — same as complete; stale fail attempts rejected.

## Operational concerns

- Alert on `ErrStaleFence` rate — indicates lock TTL too short vs upstream p99.
- Document reclaim behavior for support: user may see two charges if upstream is not idempotent — fence protects **our** cache, not their ledger.
- Admin purge (`admin.go`) should delete both meta and body keys.
- Correlate fence token in structured logs at claim + complete for postmortems.

## Performance implications

One extra HASH field per idempotency key — memory noise.

Two string comparisons in Lua per complete — unmeasurable vs network RTT.

Race benchmark (`benchmarks/idempotency/idempotency-race.js`) validates claim atomicity; add integration test where reclaim + stale complete is asserted to return `ErrStaleFence`.

## Lessons learned

Fencing is not optional for **any** expiring lock that protects shared mutable state. I almost shipped without it because "we have TTL" felt sufficient — TTL creates the stale writer problem, it doesn't solve it.

Checking fence in Go **after** reading status was my first bug. The check must be in the same atomic write as status transition — hence Lua.

Naming matters: `ErrStaleFence` communicates ops action (increase lock TTL) better than generic "conflict".

This pattern generalizes — if I add distributed cron or leader election on Redis, I'll use the same fence_token field pattern rather than inventing a new one.
