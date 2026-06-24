# Why a Sidecar Proxy

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** `cmd/sidecar`, enforcement placement, denial caching, limiter delegation

---

## 1. Problem Statement

I needed rate limiting on services I do not control the codebase of — legacy HTTP backends, polyglot microservices, and demo apps. Injecting limiter SDKs into every language stack was unrealistic. I also wanted a place to add idempotency, intelligent routing, and tracing without bloating the central limiter’s hot path.

## 2. Why the problem exists

Centralized enforcement at an API gateway works until teams need per-service path allowlists, tenant headers, and payment idempotency at the edge. Embedding limits inside each app duplicates Redis wiring and drifts configuration. A sidecar sits on the data path: every request passes through it before upstream work, matching the service-mesh-lite pattern documented in `cmd/sidecar/main.go`.

## 3. Design goals

- **Thin enforcement client:** Sidecar calls `RATE_LIMITER_URL` (`/check` or `/check_hierarchical`); Redis quota state stays in the limiter.
- **Safe caching:** Only denials are cached (`CACHE_TTL_MS`); allowances always re-hit the limiter so attackers cannot freeze quota at "allowed".
- **Thundering herd control:** `singleflight.Group` collapses concurrent limiter calls for the same cache key.
- **Explicit degradation:** `FAIL_OPEN=false` (default) returns 503 when the limiter is down; never silently unlimited in production.
- **Composable features:** `ENABLE_IDEMPOTENCY`, `ENABLE_ROUTING`, `ENABLE_CIRCUIT_BREAKER` toggle subsystems without new binaries.

## 4. Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **Library SDK per language** | High integration cost; inconsistent behavior across stacks. |
| **Envoy/Istio WASM filter** | Powerful but heavy ops burden for this project's scope. |
| **Limiter as API gateway replacement** | Gateway would own routing, idempotency, and quotas — too monolithic. |
| **Sidecar with embedded Redis limits** | Sidecars would fight over counters; violates single SoT. |
| **CDN edge rate limits** | Coarse granularity; no hierarchical tenant quotas. |

## 5. Final architecture

```
Client → Sidecar (:9090) → [limiter HTTP + optional Redis] → Upstream / GATEWAYS
```

Key code paths in `cmd/sidecar/main.go`:

- `ServeHTTP` → identity resolution (`identity.ResolveUserID`) → idempotency branch or `serveNormal`.
- `checkRateLimit` → optional `limiterCircuit.Allow(central-limiter)` → HTTP GET to limiter with `X-User-ID`, `X-Tenant-ID`, `INTERNAL_API_KEY`.
- `forwardRequest` / `forwardIdempotent` → reverse proxy or `routing.Router.Forward`.

Env vars: `UPSTREAM_URL`, `RATE_LIMITER_URL`, `PORT`, `FAIL_OPEN`, `USE_HIERARCHICAL`, `CACHE_TTL_MS`, `ALLOWED_PATHS`, `ALLOW_QUERY_USER_ID`, `INTERNAL_API_KEY`, `RATE_LIMIT`.

When `ENABLE_ROUTING=true`, `UPSTREAM_URL` is optional; `GATEWAYS=id|url|weight,...` seeds `routing.Router`.

## 6. Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| HTTP to central limiter | Simple, language-agnostic | Extra hop latency (~1–3ms LAN) |
| Denial-only cache | Protects Redis under abuse | Brief 429 stickiness after quota refills |
| Shared `http.Client` timeout 5s | Predictable upstream bounds | Slow backends block sidecar worker |
| Optional Redis in sidecar | Idempotency/routing without limiter changes | Second Redis client pool to tune |

## 7. Failure modes

- **Limiter down + `FAIL_OPEN=false`:** 503 `"Rate limiter unavailable"` — distinct from 429.
- **Limiter down + `FAIL_OPEN=true`:** Request forwards; logs `WARNING: FAIL_OPEN enabled`.
- **Circuit open on `central-limiter`:** `checkRateLimit` errors unless `CIRCUIT_FAIL_OPEN=true` on Allow Redis errors only (open state still blocks).
- **Missing `ALLOWED_PATHS`:** Warning at startup; all paths proxied — production hardening gap.

## 8. Operational concerns

- Sidecar `/health` probes limiter `/health`; unhealthy sidecar should be drained from LB.
- Set `ALLOWED_PATHS=/api` (or tighter) in production; default `/` is dev-friendly only.
- Align `INTERNAL_API_KEY` between sidecar and limiter when `STRICT_SECURITY=true`.
- Metrics: `rate_limiter_sidecar_cache_hits_total`, `rate_limiter_requests_total{handler,allowed}`.

## 9. Performance implications

Denial cache and singleflight dramatically cut limiter QPS during retry storms — benchmarks in `benchmarks/saturation/` show the effect. Idempotent POSTs add Redis claim latency before limiter check. Hierarchical mode (`USE_HIERARCHICAL=true`) uses `/check_hierarchical` with endpoint in query string; cache keys include `tenant|user|path` for finer granularity.

## 10. Lessons learned

I built the limiter first and tried calling it directly from clients — that leaked user identity in query strings and made caching impossible. The sidecar forced clean headers (`identity.UserIDHeader`) and gave me a single choke point for idempotency and routing. The tradeoff I accepted: **one more network hop** for **operational uniformity**. In production I would run one sidecar per pod (or per VM), never one global sidecar — otherwise it becomes a hidden gateway SPOF.

**References:** `cmd/sidecar/main.go`, `docs/diagrams/sidecar-flow.mmd`, `README.md` (Sidecar Architecture)
