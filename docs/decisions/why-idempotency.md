# Why Payment-Grade Idempotency

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** `internal/idempotency`, sidecar middleware, Redis claim/replay semantics

---

## Problem Statement

Clients retry POSTs on timeouts, mobile networks flap, and load balancers replay requests. Without idempotency, a payment or order creation endpoint can execute twice for one logical operation. I needed Stripe-style `Idempotency-Key` semantics: at-most-once upstream execution with safe replay of stored responses.

## Why the problem exists

HTTP is not naturally idempotent for POST/PUT/PATCH. Sidecars see duplicate requests before they reach the application. Rate limiting alone does not deduplicate. two different keys or a retried key after partial failure still double-charge. Distributed systems need an external ledger of "this key already produced response X."

## Design goals

- Header-driven: Standard `Idempotency-Key` on mutating methods only (`idempotency.IsMutatingMethod`).
- Request fingerprinting: `idempotency.Fingerprint(method, path, query, body)`. same key + different body → hash mismatch (409 semantics).
- Scoped keys: `BuildScope(tenant, user)` → Redis prefix `idem:{scope}:{key}` so tenants cannot collide.
- Processing lease: `IDEMPOTENCY_LOCK_TTL_MS` (default 60000) bounds stuck in-flight work.
- 24h replay window: `IDEMPOTENCY_COMPLETED_TTL_MS` (default 86400000) matches payment API norms.
- Fail-closed default: `IDEMPOTENCY_FAIL_OPEN=false` returns 503 when Redis claim fails.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **Database unique constraint on key** | Requires app schema change; slower than Redis claim. |
| **Upstream-only dedup** | Too late. sidecar already forwarded duplicate load. |
| **At-least-once + compensating transactions** | Correct financially but far heavier than replay cache. |
| **Client-generated UUID only (no server store)** | Cannot replay response body on retry. |
| **Synchronous mutex per key in Go** | Does not work across sidecar replicas. |

## Final architecture

Flow in `cmd/sidecar/main.go` → `serveIdempotent`:

1. `ReadBody` up to `IDEMPOTENCY_MAX_BODY_BYTES` (default 1MB).
2. `Claim(scope, key, requestHash)` runs `claim.lua` atomically.
3. Outcomes:
   - `ResultClaimed` → rate limit check → forward upstream → `Complete` with response.
   - `ResultReplay` → return cached status/headers/body (no upstream).
   - `ResultInProgress` → 409 + `Retry-After` from `retry_after_ms`.
   - `ResultHashMismatch` → reject conflicting payload.

Redis schema (from README):

```
idem:{scope}:{key}        HASH. status, request_hash, fence_token, lock_until
idem:body:{scope}:{key}   STRING. large bodies (>64KB inline threshold)
```

Metrics: `idempotency_claims_total{result}`, `idempotency_completes_total`, `idempotency_redis_duration_seconds`.

Enable with `ENABLE_IDEMPOTENCY=true` + `REDIS_ADDR` on sidecar.

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| Redis-backed replay | Fast, shared across sidecars | Memory for stored bodies |
| Lua claim | 100-way race → one winner | Extra RTT before upstream |
| Whitelisted response headers | Safer cache | Not all headers preserved |
| Fail-open option | Dev continuity | Double-submit risk in prod |

## Failure modes

- Redis outage: 503 unless `IDEMPOTENCY_FAIL_OPEN=true` (logs warning, proceeds without dedup).
- Worker crash mid-request: Lease expires; another claimant reclaims via `claim.lua` lines 55 to 66.
- Stale complete after reclaim: `ErrStaleFence` if old worker tries `Complete` with wrong fence token.
- Body too large: `ErrBodyTooLarge` → 413 before claim.

## Operational concerns

- Tune `IDEMPOTENCY_LOCK_TTL_MS` to p99 upstream latency + margin; too short causes duplicate upstream calls, too long blocks retries.
- Monitor `idempotency_claims_total{result="in_progress"}` spikes during slow backends.
- k6 race tests: `benchmarks/idempotency/idempotency-race.js`.
- Docker Compose sets `ENABLE_IDEMPOTENCY=true` by default on sidecar.

## Performance implications

Every mutating request with a key pays one Lua round-trip minimum; replays skip upstream. huge win under client retry storms. Large responses use external `idem:body:` key (`InlineThreshold` 65536 bytes). Idempotency runs before rate limit on claimed paths, so duplicate keys still consume limiter budget on first claim only.

## Lessons learned

During testing I fired 100 concurrent POSTs with one key (`benchmarks/idempotency/`). Before Lua, I saw multiple upstream executions. After `claim.lua`, exactly one forward. the rest got 409 or eventual replay. The lesson: **idempotency belongs at the edge** (sidecar), not buried in business logic, if you want fleet-wide guarantees without patching every service.

**References:** `internal/idempotency/store.go`, `docs/diagrams/idempotency-flow.mmd`, `docs/failure-modes/duplicate-requests.md`
