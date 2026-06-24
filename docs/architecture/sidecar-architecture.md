# Sidecar Architecture

When I extracted rate limiting from application code into `cmd/sidecar`, I was building a **service-mesh-lite** reverse proxy: one binary per pod (or per node) that every client hits on `:9090` before traffic reaches the real service on `:8081` or a gateway pool. The sidecar does not own quota — it **asks** the central limiter on `:8080` — but it owns every optimization and safety valve on the edge path.

This is the component I would clone per deployment region, not the limiter.

---

## Problem Statement

Application teams should not import Redis Lua, circuit breaker state machines, or idempotency fencing into their handlers. They should deploy a sidecar that:

- Enforces identity and path policy at the edge.
- Amortizes limiter load under flash crowds.
- Optionally deduplicates mutating requests.
- Optionally routes to the best upstream gateway.
- Degrades predictably when dependencies fail.

---

## Why the problem exists

Without a sidecar, each service variant implements its own:

- HTTP client to a limiter (or worse, direct Redis).
- Retry behavior when limiter times out.
- Cache of recent 429s.

I centralized that logic once in `Sidecar` struct (`cmd/sidecar/main.go`) so behavior is identical whether the upstream is `demo:8081` or three payment gateways.

The sidecar also solves **coordination locality**: `singleflight` and denial cache are process-local — they reduce fleet-wide Redis QPS without claiming global consistency for allows.

---

## Design goals

| Goal | Mechanism |
|------|-----------|
| Accurate token accounting | Never cache allowed decisions |
| Abuse resilience | Cache denials briefly; singleflight on misses |
| Trusted identity | Header-first `X-User-ID`; query opt-in only |
| Blast radius containment | `ALLOWED_PATHS` prefix allowlist |
| Optional strictness | `FAIL_OPEN`, `IDEMPOTENCY_FAIL_OPEN` default false |
| One reverse proxy instance | `httputil.NewSingleHostReverseProxy` at startup — per-request creation leaks goroutines |
| Shared Redis connection | One `UniversalClient` for idempotency + routing when both enabled |

---

## Alternative approaches considered

### Envoy/Istio external auth filter

Production-grade, but configuring idempotency fencing and custom hierarchical cache keys in WASM or Lua filters exceeded my iteration budget. The Go sidecar keeps all logic in `internal/*` packages I can unit test.

### Sidecar caches allows with short TTL

I prototyped this. Attackers could park on "allowed" for the TTL window. I deleted the branch and added an explicit comment in code: *"attackers could freeze their quota at allowed forever."*

### Sidecar talks to Redis for quota directly

Would eliminate the limiter HTTP hop but duplicates algorithm logic and bypasses audit/circuit on the limiter. I kept Redis on the sidecar only for **idempotency** and **routing/circuit** state — orthogonal concerns.

### Pull-based limiter (sidecar subscribes to quota stream)

Elegant for massive scale; overkill for my target (thousands of RPS, single Redis cluster). Push/check-on-demand won.

---

## Final architecture

### Process layout

```
┌─────────────────────────────────────────────────────────┐
│                      Sidecar :9090                       │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────────┐ │
│  │ identity    │  │ path filter  │  │ metrics/auth  │ │
│  └──────┬──────┘  └──────────────┘  └───────────────┘ │
│         ▼                                                │
│  ┌──────────────────────────────────────────────────┐   │
│  │ Idempotency branch (optional)                     │   │
│  │  claim → limit → route/proxy → complete/fail    │   │
│  └──────────────────────────────────────────────────┘   │
│         │ else                                           │
│  ┌──────────────────────────────────────────────────┐   │
│  │ Normal branch                                     │   │
│  │  denial cache → singleflight → limit → forward    │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
         │ HTTP                           │ HTTP / routing
         ▼                                ▼
    Limiter :8080                    Upstream / Gateways
```

### Core struct

`Sidecar` holds:

- `cache sync.Map` — denial entries keyed by `cacheKey`
- `limitFlight singleflight.Group` — per-key coalescing
- `ttl time.Duration` — denial cache TTL (default 30 ms)
- `failOpen bool` — forward on limiter errors
- `useHierarchical bool` — switches `/check` vs `/check_hierarchical`
- `idempotency idempotency.Store` — Redis Lua backend
- `router *routing.Router` — when `ENABLE_ROUTING=true`
- `limiterCircuit *circuitbreaker.Breaker` — target `central-limiter` and per-gateway when routing enabled

### Identity resolution

`internal/identity.ResolveUserID`:

1. `X-User-ID` header (production path).
2. `?user_id=` query only if `ALLOW_QUERY_USER_ID=true`.

The sidecar forwards identity to the limiter via **header**, not query string — harder to spoof from browser address bars and consistent with hierarchical tenant headers.

Tenant resolution (`tenantID` helper):

1. `X-Tenant-ID` header
2. `tenant_id` query param
3. default `"default"`

### Cache key design

Flat mode: `cacheKey = userID`

Hierarchical mode: `tenantID + "|" + userID + "|" + r.URL.Path`

I scope by path so `/api/login` and `/api/search` do not share denial cache entries when endpoint buckets differ in the limiter.

### Denial-only cache semantics

```go
if !entry.Allowed {
    // serve cached 429
    return
}
// allowed entries: fall through to limiter
```

On every limiter response (hit or miss), I **store both allow and deny** in the map — but only **read** denials. Allows in the map are overwritten each check; they exist mainly as debugging artifacts and expire via TTL without affecting correctness.

### singleflight

`limitFlight.Do(cacheKey, func() { return checkRateLimit(...) })`

When 100 goroutines miss cache for `user-42`, one executes `checkRateLimit`; others receive the same `limitResult`. This protects the limiter during thundering herds **after** a cache miss — not on denial hits.

Shared error propagation: if the leader's limiter call fails, all waiters get the same error (503 or fail-open forward).

### checkRateLimit HTTP client

- 5 s timeout
- `telemetry.NewHTTPTransport` for trace propagation
- Parses `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`
- Records circuit outcome via `ClassifyHTTP` on defer

Circuit check order:

1. `limiterCircuit.Allow(central-limiter)` — if denied, error before network
2. HTTP to limiter
3. `Record` with classified outcome

When routing is enabled, the same breaker instance also guards per-gateway IDs in `Router.Forward`.

### Fail-open behavior

Two independent flags:

| Flag | Effect |
|------|--------|
| `FAIL_OPEN=true` | Limiter errors → log warning, forward to upstream anyway |
| `IDEMPOTENCY_FAIL_OPEN=true` | Redis claim errors → degrade to `serveNormal` |

I log `WARNING: FAIL_OPEN=true` at startup. This is an availability lever for disasters, not a default.

Circuit breaker has its own `CIRCUIT_FAIL_OPEN` on the **limiter** process for Redis — sidecar fail-open is separate.

### Forwarding paths

**Static upstream:** `UPSTREAM_URL` required when `ENABLE_ROUTING=false`.

**Intelligent routing:** `GATEWAYS=id|url|weight,...` seeds `route:gw:*` in Redis. `router.Forward` picks gateway, sets `X-Gateway-ID`, `X-Gateway-Score`, `X-Gateway-Failover` headers on success.

Idempotent forwards use `ResponseCapturer` to buffer response for `complete.lua`.

### Redis connection (`connectSidecarRedis`)

Required when `ENABLE_IDEMPOTENCY` or `ENABLE_ROUTING`. Supports standalone (`REDIS_ADDR`) and Sentinel (`REDIS_MODE=sentinel`). Fails fast on `Ping` — same discipline as limiter.

### Configuration surface (docker-compose defaults)

| Env | Default | Notes |
|-----|---------|-------|
| `PORT` | `9090` | Client-facing |
| `RATE_LIMITER_URL` | required | e.g. `http://limiter:8080` |
| `CACHE_TTL_MS` | `30` | Denial cache |
| `FAIL_OPEN` | `false` | |
| `USE_HIERARCHICAL` | `false` in compose | |
| `ALLOWED_PATHS` | `/` in compose | |
| `ENABLE_IDEMPOTENCY` | `true` in compose | |
| `ENABLE_ROUTING` | `true` in compose | |

---

## Tradeoffs

**Process-local cache is not shared across sidecars.** Two pods can both miss cache and double-call limiter — acceptable; double **allow** is impossible because limiter decrements atomically. Double **deny cache miss** only adds load.

**30 ms denial TTL is a guess.** Too long → users see 429 after refill. Too short → less protection. I expose `CACHE_TTL_MS` for tuning per workload.

**Body read for routing/idempotency** prevents true streaming uploads through the sidecar for those modes.

**Health check indirect** — sidecar health reflects limiter health only, not sidecar-local Redis.

---

## Failure modes

| Mode | Behavior |
|------|----------|
| Limiter timeout | 503 unless fail-open |
| Circuit open on `central-limiter` | Error before HTTP; same as above |
| Idempotency Redis down | 503 or degrade to normal if fail-open |
| Invalid `UPSTREAM_URL` | Fatal at startup |
| `ENABLE_ROUTING` without `GATEWAYS` | Fatal at startup |
| Stale denial cache | User waits up to TTL then gets accurate limiter result |
| singleflight panic in worker | Would propagate — I keep checkRateLimit panic-free |

---

## Operational concerns

- Deploy sidecar **co-located** with app instances (K8s sidecar container or same host).
- Set `ALLOWED_PATHS` to exact API roots — unset allows entire URL space (startup warning).
- Protect `:9090` at ingress; it trusts `X-User-ID` from inner mesh.
- `INTERNAL_API_KEY` must match limiter configuration.
- For TLS termination, set `TLS_CERT_FILE` + `TLS_KEY_FILE` on sidecar.
- Metrics on `/metrics` — optional `METRICS_REQUIRE_AUTH`.

Rolling restart: in-memory cache clears — safe, no state loss that matters.

---

## Performance implications

| Technique | Effect |
|-----------|--------|
| Denial cache | O(1) `sync.Map` hit avoids 1 HTTP + 1 Redis per request |
| singleflight | Collapses N concurrent misses to 1 limiter call |
| Shared `http.Client` | Connection reuse to limiter and gateways |
| Reverse proxy reuse | Avoids goroutine leak from per-request proxies |
| Idempotency replay | Skips limiter + upstream entirely |

Metrics: `RecordCacheHit`, `RecordCacheMiss` on sidecar; correlate with limiter QPS during load tests.

---

## Lessons learned

1. **The comment about allowed-cache attacks is there because I did it wrong once.** New contributors should not "optimize" by caching allows.

2. **singleflight key must match cache key** — using only `userID` in hierarchical mode would coalesce unrelated endpoints incorrectly... actually they'd share limiter calls for same user which might be OK for flat check but wrong for endpoint-scoped hierarchical limits. Path in the key fixes this.

3. **Set `r.Host = target.Host` on reverse proxy** — virtual hosting breaks silently without it.

4. **Drain limiter health response body** in sidecar `/health` — `io.Copy(Discard)` prevents keep-alive connection leaks.

5. **Idempotency fail-open to normal path** is a conscious degradation ladder: lose dedup, keep rate limits, still serve traffic.

---

## Related documents

- [request-flow.md](./request-flow.md) — full sequence diagrams
- [routing-architecture.md](./routing-architecture.md) — gateway selection after allow
- [redis-design.md](./redis-design.md) — `idem:*` key layout
