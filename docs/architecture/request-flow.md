# Request Flow

When I wired the first end-to-end demo — client → sidecar → limiter → Redis → demo backend — I assumed one code path would suffice. Then I added idempotency for POST retries, hierarchical limits for multi-tenant SaaS, and intelligent routing to three simulated gateways. The sidecar now branches into **normal** and **idempotent** flows that share limiter calls but diverge sharply around upstream execution and Redis idempotency keys.

This document follows the canonical sequence in [../diagrams/request-flow.mmd](../diagrams/request-flow.mmd).

---

## Problem Statement

Every HTTP request entering the system must:

1. Resolve **who** is consuming quota (identity).
2. Decide **allow or deny** before expensive upstream work.
3. For mutating APIs with `Idempotency-Key`, guarantee **at-most-once side effects** with safe replay.
4. Forward allowed traffic to either a static upstream (`UPSTREAM_URL`) or a **scored gateway pool** (`ENABLE_ROUTING=true`).

The flow must remain correct under concurrent duplicate requests, limiter outages, and partial gateway failure.

---

## Why the problem exists

A rate limiter alone does not solve **duplicate execution**. Payment processors retry POSTs; mobile clients double-tap submit. If I only rate-limit, I still get double charges.

Conversely, idempotency without rate limiting lets a client replay a cached 200 forever without consuming quota — or worse, hammer upstream during a replay storm.

I needed orthogonal layers on the same sidecar hop:

| Layer | Question answered |
|-------|-------------------|
| Identity | Which bucket? |
| Rate limit | Is there budget? |
| Idempotency | Have we seen this key before? |
| Routing | Which gateway is healthiest? |

---

## Design goals

- **Fail fast on identity** — missing `X-User-ID` returns 400 before Redis.
- **Limiter before upstream** — never forward traffic I have not budgeted (except explicit fail-open).
- **Idempotent replays skip upstream** — completed keys return cached status/body from Redis.
- **Idempotent claims serialize** — concurrent same-key requests get 409 + `Retry-After`, not parallel upstream calls.
- **Observability** — OpenTelemetry spans: `sidecar.proxy`, `sidecar.idempotency`, `sidecar.rate_limit_check`, `limiter.check`.

---

## Alternative approaches considered

### Rate limit after upstream (reverse proxy first)

Some meshes limit at egress. I rejected this: upstream work (DB writes, payment calls) would already have started before denial.

### Idempotency in the application only

App-level dedup databases work but duplicate limiter integration per service. Centralizing in the sidecar means any service behind `:9090` gets dedup without code changes — at the cost of body buffering and Redis storage.

### Synchronous limiter call inside idempotent replay path

For **completed** idempotency keys I return the cached response with **zero** limiter calls — correct, because quota was consumed on the original execution.

For **in-progress** collisions I return 409 without upstream — the first holder still owns the lock.

When the sidecar **claims** a new key, it calls the limiter normally. I briefly considered skipping limit on replay via `idempotent_replay=true` on the limiter for claimed-but-not-completed paths; today only the limiter's explicit query param short-circuits quota for replays initiated by trusted callers, not the sidecar's happy path.

### GET /check from sidecar vs gRPC

HTTP keeps debugging simple (`curl` limiter from inside the mesh). I accept JSON parse overhead vs a binary protocol.

---

## Final architecture

### Entry: `Sidecar.ServeHTTP`

```
Client → :9090 (sidecar)
         │
         ├─ /health, /metrics → handled separately (not proxied)
         ├─ path ∉ ALLOWED_PATHS → 404
         ├─ identity.ResolveUserID → 400 if missing
         │
         ├─ mutating method + Idempotency-Key + ENABLE_IDEMPOTENCY
         │       → serveIdempotent()
         └─ else → serveNormal()
```

Path allowlist uses prefix matching: `/api` matches `/api/login` and `/api/v2/foo`.

### Normal path (`serveNormal`)

Reference: diagram lines 36–46 in `request-flow.mmd`.

1. **Denial cache lookup** — key is `userID` or `tenant|user|path` in hierarchical mode. If entry exists, not expired, and `Allowed == false`, return 429 immediately.
2. **Allowed cache entries are ignored** — I log "ignoring cache, will call central limiter" in debug mode.
3. **singleflight** — `limitFlight.Do(cacheKey, checkRateLimit)` collapses concurrent misses.
4. **checkRateLimit** — HTTP GET to limiter:
   - Flat: `GET {RATE_LIMITER_URL}/check`
   - Hierarchical: `GET {RATE_LIMITER_URL}/check_hierarchical?endpoint={path}`
   - Headers: `X-User-ID`, optional `X-Tenant-ID`, `X-Internal-API-Key`
5. **Circuit guard** — if `limiterCircuit` is configured, `Allow(central-limiter)` must pass before HTTP call.
6. **On deny** — store `CacheEntry` with TTL (`CACHE_TTL_MS`, default 30 ms), return 429 with `X-RateLimit-*` and `Retry-After`.
7. **On allow** — set rate limit headers, `forwardRequest`:
   - `router.Forward` if `ENABLE_ROUTING`
   - else `httputil.ReverseProxy` to `UPSTREAM_URL`

### Idempotent path (`serveIdempotent`)

Reference: diagram lines 13–34 in `request-flow.mmd`.

1. **Validate key** — format rules in `idempotency.ValidateKey`.
2. **Read body** — up to `IDEMPOTENCY_MAX_BODY_BYTES` for fingerprinting and later proxy.
3. **Build scope** — `SHA256(tenant|user)[:16]` hex isolates keys per tenant+user.
4. **Fingerprint** — `SHA256(method, path, sortedQuery, body)`.
5. **claim.lua** in Redis:

| Result | Sidecar action |
|--------|----------------|
| `ResultReplay` | Write cached response to client; **no limiter, no upstream** |
| `ResultInProgress` | 409 + `Retry-After` from lock TTL remainder |
| `ResultHashMismatch` | 409 — same key, different payload |
| `ResultClaimed` | Proceed; hold `fence_token` |

6. **Rate limit** — `checkRateLimit` (same as normal). On deny: `complete.lua` stores 429 body, return 429.
7. **On allow** — `forwardIdempotent`:
   - Route or reverse-proxy through `ResponseCapturer`
   - `complete.lua` with matching `fence_token` stores status, headers, body
   - Set `X-Idempotency-Status: created`
8. **On limiter error** — if `FAIL_OPEN`, forward anyway (dangerous); else `fail.lua` and 503.

### Limiter path (`/check` and `/check_hierarchical`)

The limiter is not in the diagram's first hop but is on the critical path for both flows:

1. `auth.RequireAPIKey(INTERNAL_API_KEY)`
2. `checkRedisCircuit` — `cb:redis` must allow (fail-closed unless `CIRCUIT_FAIL_OPEN`)
3. `limiterInstance.Allow` or `hierarchicalLimiter.AllowWithParams` — Lua in Redis
4. `recordRedisCircuit` — classify Redis error/latency
5. `recordAudit` — async enqueue (allowed / denied / error)
6. JSON response + `X-RateLimit-Limit`, `X-RateLimit-Remaining`, optional `Retry-After`

**Special case:** `?idempotent_replay=true` on `/check` returns synthetic `allowed: true` without touching Redis — a trusted shortcut for callers that already deduplicated elsewhere. The sidecar does not use this on the standard idempotent path today.

### Hierarchical keys on limiter

For `/check_hierarchical`, the limiter constructs:

```
rate:global
rate:tenant:{tenantID}
rate:user:{userID}
rate:endpoint:{tenantID}:{endpoint}
```

Overrides from `config:*` merge via `effectiveHierarchicalLimits` before Lua runs.

---

## Tradeoffs

**Idempotent path buffers full body in memory.** Large uploads hit `IDEMPOTENCY_MAX_BODY_BYTES` with 413. I chose correctness over streaming for mutating APIs.

**Complete/fail requires fence token match.** A stale worker cannot overwrite Redis after lock reclaim — but legitimate completes fail with `ErrStaleFence` if the client retried after lock expiry and another worker claimed.

**Normal path does not deduplicate.** Duplicate GETs each consume quota — by design.

**Routing reads body twice in some paths.** `forwardRequest` and idempotent forward call `readRequestBody` to support POST through router — small CPU cost for correctness.

---

## Failure modes

| Scenario | Normal path | Idempotent path |
|----------|-------------|-----------------|
| Limiter 503 | 503 to client (or fail-open forward) | `fail.lua` + 503 unless fail-open |
| Redis down on claim | N/A | 503 unless `IDEMPOTENCY_FAIL_OPEN` → degrades to normal path |
| Upstream timeout after allow | Client sees gateway error; quota already consumed | `complete.lua` stores error response if capturer got bytes |
| Concurrent same idempotency key | N/A | First claims; others 409 until complete or lock TTL |
| Lock expires mid-flight | N/A | New claimant gets new fence; old complete fails stale fence |
| Circuit open on central-limiter | 503 before HTTP to limiter | Same |
| All gateways fail (routing) | 503 "all gateways unavailable" | `fail.lua` with 503 JSON |

---

## Operational concerns

- **Header contract:** Clients must send `X-User-ID`. Tenant-aware hierarchical sidecar cache needs `X-Tenant-ID` or `tenant_id` query param.
- **Idempotency-Key** only activates on POST/PUT/PATCH with `ENABLE_IDEMPOTENCY=true`.
- **Health:** Sidecar `/health` checks limiter `/health`, not Redis directly — idempotency/routing Redis could be down while health is green. I rely on metrics and claim errors for that gap.
- **Debug:** `DEBUG=true` logs cache key decisions — noisy but essential during cache poisoning investigations.

---

## Performance implications

| Step | Typical cost driver |
|------|---------------------|
| Denial cache hit | In-memory map lookup — microseconds |
| singleflight miss | One HTTP RTT to limiter + one Redis Lua |
| Idempotency replay | One Redis Lua (claim returns cached) — no limiter |
| Idempotency claim | claim.lua + limiter + upstream + complete.lua — 3+ Redis scripts |
| Hierarchical vs flat | Same one Lua; hierarchical checks 4 buckets in-script |

Default denial TTL 30 ms is aggressively short — I tuned it to catch burst abuse without stale 429s after refill.

---

## Lessons learned

1. **Branch at the top on idempotency key presence** — mixing dedup into `serveNormal` created impossible state around body teeing.

2. **Store 429 in idempotency complete** — a denied mutating request should replay as 429, not re-hit upstream on retry.

3. **The diagram's "ID" participant is Redis-backed idempotency**, not a separate service — keeping it in Redis avoids another failure domain.

4. **`idempotent_replay` on limiter is a scalpel** — useful for admin replay tools (`audit.Replay` hints), not for general clients.

5. **Record routing outcomes only after gateway response** — `Router.Forward` updates `route:gw:{id}` and `cb:{id}` per attempt, so failover loops produce accurate per-gateway metrics.

---

## Sequence reference

The Mermaid source at [../diagrams/request-flow.mmd](../diagrams/request-flow.mmd) is the authoritative numbering:

- Steps 10–34: mutating + `Idempotency-Key`
- Steps 36–46: normal path with denial cache

For idempotency-only detail see [../diagrams/idempotency-flow.mmd](../diagrams/idempotency-flow.mmd). For routing after allow see [routing-architecture.md](./routing-architecture.md).
