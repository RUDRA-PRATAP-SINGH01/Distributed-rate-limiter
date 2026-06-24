# Idempotency

## Problem Statement

Payment and mutation APIs must survive client retries. Mobile networks drop; load balancers replay; users double-click. Without idempotency, the same `Idempotency-Key` can create duplicate charges, duplicate resource creation, or inconsistent state.

I needed a sidecar-level idempotency layer that:

1. Admits exactly **one** upstream execution per key+payload.
2. Replays the **cached HTTP response** for duplicates.
3. Handles **in-flight** collisions with `Retry-After`.
4. Detects **key reuse with different body** (hash mismatch).

## Why the problem exists

HTTP is not naturally idempotent for POST. Clients follow at-least-once retry semantics; servers must provide at-most-once execution **or** explicit replay. Distributed sidecars compound the problem: two instances can receive the same retry simultaneously.

Local in-memory dedup maps fail across replicas and restart. Database unique constraints help at the application layer but add latency and require schema coupling. Redis + Lua gives cross-sidecar coordination with millisecond latency. same infrastructure as rate limiting.

## Design goals

1. Atomic claim: `claim.lua` decides new / replay / in-progress / hash-mismatch in one `EVAL`.
2. Request fingerprint: `Fingerprint()` in `internal/idempotency/fingerprint.go` hashes method, path, sorted query, body.
3. Scoped keys: `BuildScope(tenant, user)` prevents cross-tenant key collision.
4. Response capture: `capturer.go` buffers upstream response for `Complete()`.
5. Bounded body size: `MaxBodyBytes` and inline vs external storage in `complete.lua`.
6. Traced: Spans `idempotency.claim`, `idempotency.complete`, `idempotency.fail` via `internal/telemetry`.

## Alternative approaches considered

| Approach | Why not |
|----------|---------|
| DB unique (key) + row lock | Slow; couples all services to same DB |
| Optimistic "insert if absent" in Go | Race under concurrent claims |
| Kafka exactly-once | Heavy; wrong tool for HTTP replay |
| Client-only idempotency | Cannot trust all clients |
| S3 response cache | Latency; no in-progress semantics |

Redis hash metadata at `idem:{scope}:{key}` plus optional `idem:body:{scope}:{key}` for large payloads won.

## Final architecture

```
POST with Idempotency-Key
        │
        ▼
ReadBody + Fingerprint (fingerprint.go)
        │
        ▼
RedisStore.Claim → claim.lua
        │
   ┌────┴────┬──────────┬─────────────┐
   │         │          │             │
claimed   replay   in_progress   hash_mismatch
   │         │          │             │
   ▼         ▼          ▼             ▼
proxy    return      409 +        422
upstream  cached     Retry-After
   │
   ▼
Complete / Fail → complete.lua / fail.lua (fence-checked)
```

**Claim states** (`claim.lua`):

- `{1, fence_token}`. new claim or reclaimed expired lock
- `{2, status, headers, body}`. replay completed/failed response
- `{3, retry_after_ms}`. another holder owns lock
- `{0}`. hash mismatch

**Storage layout:**

- Metadata HASH: `status`, `request_hash`, `fence_token`, `lock_until`, `http_status`, `resp_headers`, `body_ref`
- Large bodies: `body_ref=external` → STRING at `idem:body:...`

`internal/idempotency/store.go` generates fence token via `uuid.New()` on each successful claim. `Complete` passes token to `complete.lua` which rejects stale writers.

**Headers**. `internal/idempotency/headers.go` defines canonical header names and validation (`ValidateKey`).

**Admin**. `internal/idempotency/admin.go` allows ops to inspect/purge keys (tested in `admin_test.go`).

## Tradeoffs

- If handler exceeds `LockTTL`, another client can reclaim; fence prevents first writer from completing (`ErrStaleFence`) but duplicate upstream execution is possible. Tune `LockTTL` above p99 upstream latency.
- Failed responses cached: `fail.lua` stores error bodies with `status=failed`; retries replay failure unless TTL expires. correct for non-retryable errors, surprising for transient 503.
- Redis loss: In-flight claims disappear; clients may see duplicate upstream calls after failover. document RPO.
- Scope hashing: 16-byte truncated SHA256 scope is opaque; debugging requires knowing tenant/user inputs.

## Failure modes

1. Concurrent race: Benchmarked: `idempotency-race.js` → 1 upstream execution at 100 VUs. Lua holds.
2. Hash mismatch: Same key, different query param order normalized by `sortedQuery()`. intentional 422.
3. Stale fence on complete: Slow path after lock reclaim; metrics should alert on `ErrStaleFence` rate.
4. Body too large: `ErrBodyTooLarge` before Redis write.
5. Wrapped `ErrStoreUnavailable`; fail-closed, no passthrough without policy flag.

## Operational concerns

- Set `LockTTL`, `CompletedTTL`, `InlineThreshold` via `internal/idempotency` Config. document in runbooks.
- Monitor `idempotency_claim_total{result=...}` metrics from `metrics.RecordIdempotencyClaim`.
- Purge stuck `processing` keys via admin API after incidents.
- Replay benchmark: `idempotency-replay.js`. ~942 RPS, p95 5.7ms (see `benchmarks/idempotency/summary.md`).
- Ensure mutating methods only: `IsMutatingMethod()` gates POST/PUT/PATCH.

## Performance implications

Claim + complete = 2 Redis RTTs minimum per successful mutation. Replay = 1 RTT (claim returns body inline).

External body storage adds `SET` on complete but keeps metadata HASH small. important when responses are MB-scale (blocked by `MaxBodyBytes` anyway).

`metrics.RecordIdempotencyRedisDuration` separates idempotency Redis cost from limiter Redis cost in dashboards.

## Lessons learned

Fingerprinting must include **query string**, not just body. I got burned in tests where same key + different `?version=` must mismatch.

The in-progress path (`{3, retry_after_ms}`) is essential UX. returning 409 with `Retry-After` beats hanging or double-submitting.

Fence tokens belong in the same design doc as idempotency, not an afterthought. see `fencing-tokens.md`. Without `complete.lua` fence check, reclaim would corrupt cached responses.

First-person mistake: I initially completed in Go with `HSET` after reading status. two sidecars could both complete. Moving completion into Lua fixed it permanently.
