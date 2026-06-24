# Idempotency Architecture

> Engineering journal. Why and how I built the distributed idempotency layer in the sidecar.

## Problem Statement

I needed **exactly-once client semantics** for mutating HTTP requests (POST, PUT, PATCH). When a client retries, the upstream must not execute again. Instead, the sidecar should replay the response stored from the first successful attempt. I also needed to reject reuse of the same `Idempotency-Key` with a different payload, and to reclaim locks when a slow or crashed worker holds a processing lease too long.

## Why the problem exists

The sidecar pattern chains client, sidecar, central limiter, and upstream. Network timeouts, 502 responses, and automatic client retry policies make **duplicate side effects** common. A payment, inventory, or any non-idempotent upstream call can run twice. Pure in-memory deduplication does not work fleet-wide because each sidecar instance keeps its own state. Without Redis atomic Lua scripts, races appear between claim and complete.

## Design goals

1. **Atomic claim**. One Lua script handles new, replay, in-progress, hash mismatch, and expired reclaim paths.
2. **Request fingerprinting**. SHA-256 over method, path, sorted query, and body. Same key with a different body returns `422 Unprocessable Entity`.
3. **Tenant-scoped keys**. `BuildScope(tenantID, userID)` prevents cross-tenant collisions.
4. **Fencing tokens**. After reclaim, a stale worker cannot complete or fail with the old token.
5. **Sidecar integration**. Rate limit check runs after claim, then upstream forward. Response capture stores the result in Redis.
6. **Fail-closed by default**. Redis down returns `503`. Optional `IDEMPOTENCY_FAIL_OPEN=true` bypasses dedup.

## Alternative approaches considered

| Approach | Why I rejected it |
|----------|-------------------|
| Database unique constraint on key | Latency and schema coupling. Response body storage is awkward. |
| Kafka or event log dedup | Overkill for HTTP replay. High operational burden. |
| Optimistic locking without fence token | After reclaim, two workers could both complete. |
| Client-only idempotency | Malicious or buggy clients can bypass it. |
| Separate idempotency microservice | Extra hop. The sidecar already talks to Redis. |

## Final architecture

### Redis key layout

```
idem:{scope}:{key}           → HASH (metadata: status, request_hash, fence_token, …)
idem:body:{scope}:{key}      → STRING (large response bodies, >64 KB)
```

`scope` is the first 16 bytes of `SHA256(tenantID + "|" + userID)` as hex (32 characters).

### Lua lifecycle

**`claim.lua`**. Atomic entry point:

| Return code | Meaning |
|-------------|---------|
| `{1, fence_token}` | Claimed (new or reclaimed after lock expiry) |
| `{2, status, headers, body}` | Replay cached completed or failed response |
| `{3, retry_after_ms}` | Another holder is processing. Client gets `409 Conflict`. |
| `{0}` | Hash mismatch |

**`complete.lua`**. Sets `completed` only when status is `processing` and `fence_token` matches. Bodies at or below 64 KB go inline in the HASH. Larger bodies use a separate STRING key.

**`fail.lua`**. Marks `failed` for transient upstream errors. Retryable. Replay returns the same error response.

### Fingerprint (`internal/idempotency/fingerprint.go`)

```text
SHA256(UPPER(method) + "\n" + path + "\n" + sortedQuery(rawQuery) + "\n" + body)
```

Mutating methods: POST, PUT, PATCH only.

### Fencing flow

1. `Claim` generates a fence token with `uuid.New().String()`.
2. Lock expiry triggers reclaim with a new fence token written to Redis.
3. `Complete` or `Fail` with a mismatched token returns `ErrStaleFence` (Lua returns `{0}`).

### Sidecar integration (`cmd/sidecar/main.go`)

```
Client → Sidecar.ServeHTTP
  ├─ Idempotency-Key absent → serveNormal
  └─ Idempotency-Key + mutating method → serveIdempotent
        ├─ ReadBody + Fingerprint + BuildScope
        ├─ Claim
        ├─ replay / in_progress / hash_mismatch → immediate response
        ├─ claimed → checkRateLimit → forwardIdempotent
        │     ├─ ResponseCapturer wraps ResponseWriter
        │     ├─ router.Forward OR reverse proxy
        │     └─ Complete with captured status, headers, body
        └─ limiter error → Fail or fail-open path
```

With `ENABLE_IDEMPOTENCY=true`, the sidecar connects to Redis. The `X-Idempotency-Status` header reports `created`, `replayed`, `in_progress`, or `hash_mismatch`.

Replay header whitelist: `content-type`, `x-request-id`, `x-correlation-id`.

### Config defaults (`idempotency.DefaultConfig`)

- Lock TTL: 60s
- Completed TTL: 24h
- Max body: 1 MB
- Inline threshold: 64 KB

## Tradeoffs

- **Redis dependency**. Idempotency and rate limiting share the same Redis. Outage makes the sidecar fail-closed unless fail-open is enabled.
- **24h retention**. Stripe-style. Long-running workflows may need a longer TTL.
- **Failed state replay**. The client gets the same error again. Intentional retry needs a new key or a TTL wait.
- **ResponseCapturer**. Streaming and SSE responses buffer in memory. Very large responses hit `MaxBodyBytes` and reject.

## Failure modes

| Scenario | Behavior |
|----------|----------|
| Redis unavailable | `503 Idempotency store unavailable` (or fail-open bypass) |
| Worker crash after claim | Lock expires, reclaim runs, new worker proceeds |
| Stale worker completes after reclaim | `complete.lua` returns `{0}`. Client may retry. |
| Hash mismatch | `422`. Client bug or key reuse abuse. |
| Body too large | `413 Request Entity Too Large` before claim |
| Rate limit denial on claimed request | `429` stored via Complete. Replay also returns 429. |

## Operational concerns

- Admin API: `cmd/limiter/admin_idempotency.go`. Inspect or delete records by scope and key.
- Metrics: `idempotency_claims_total{result}`, `idempotency_completes_total`, `idempotency_redis_duration_seconds`.
- Traces: spans `idempotency.claim`, `idempotency.complete`, `idempotency.fail`, `sidecar.idempotency`.
- Env: `IDEMPOTENCY_LOCK_TTL_MS`, `IDEMPOTENCY_COMPLETED_TTL_MS`, `IDEMPOTENCY_MAX_BODY_BYTES`, `IDEMPOTENCY_FAIL_OPEN`.
- Monitor `hash_mismatch` and `in_progress` spikes. They often mean client misconfiguration or a thundering herd.

## Performance implications

- Every mutating idempotent request pays **at least one Redis round-trip** for claim. The success path also calls complete.
- The replay path skips upstream. That is a significant win on retries.
- Lua scripts are single-key atomic. Large bodies in an external STRING keep HASH size bounded.
- `ReadBody` holds the request body in memory. The `MaxBodyBytes` cap is essential.

## Lessons learned

I learned that **lock reclaim without a fencing token is dangerous**. On an expired lease, two workers can race. Including the sorted query string in the fingerprint was necessary. Without it, `?a=1&b=2` and `?b=2&a=1` would count as different requests. Completing rate-limit denials in the sidecar felt controversial, but I followed the Stripe model: a consistent 429 on replay is better than double-charging upstream. I kept `Hijack` and `Flush` support in `ResponseCapturer` so websocket paths do not crash, even though idempotency does not apply to them.
