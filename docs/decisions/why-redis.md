# Why Redis

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** Central quota state, idempotency, routing metrics, circuit breakers, audit trail

---

## Problem Statement

When I started building this platform, I needed a single source of truth for rate-limit counters that every limiter instance and every sidecar could agree on. In-memory counters per process would diverge the moment I scaled past one replica. I also needed sub-millisecond reads under abuse, TTL-based eviction, and atomic read-modify-write without building my own distributed lock service.

## Why the problem exists

HTTP rate limiting is inherently a shared-state problem. A user can hit any sidecar behind a load balancer; each sidecar must see the same remaining quota. Payment-grade idempotency, gateway health scores, and circuit breaker state have the same requirement: many writers, one coherent view. Without a fast external store, I would either accept inconsistent enforcement or funnel all traffic through one process.

## Design goals

- Fleet-wide consistency: One `REDIS_ADDR` (or Sentinel cluster) backs all limiter pods.
- Fail fast on startup: `cmd/limiter/main.go` calls `redisclient.Ping` before serving; a limiter that boots without Redis would lie in `/health`.
- Mode flexibility: `REDIS_MODE=standalone` for dev, `REDIS_MODE=sentinel` for HA via `internal/redis/client.go`.
- Observable dependency: Redis is a first-class circuit target (`circuitbreaker.TargetRedis` → key `cb:redis`).
- Keep sidecars dumb: Sidecars call the central limiter over HTTP; only idempotency/routing paths open Redis directly when `ENABLE_IDEMPOTENCY` or `ENABLE_ROUTING` is true.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **Per-process in-memory limits** | Fast but wrong under multiple replicas; only kept as reference algorithms in tests. |
| **PostgreSQL row locks** | Correct but too slow for hot `/check` paths at 10k+ RPS. |
| **Dedicated coordination service (etcd)** | Strong consistency but poor fit for high-frequency counter increments and TTL keys. |
| **Redis Cluster from day one** | Operational overhead I deferred; Sentinel + single master met my HA bar first. |
| **Embedding limits in the app DB** | Couples quota to application schema and complicates sidecar extraction. |

## Final architecture

Redis holds all authoritative counters and auxiliary state:

```
limit:*                    . token bucket / sliding window keys (Lua in internal/limiter/lua/)
config:*                   . runtime overrides (override.Store)
idem:{scope}:{key}         . idempotency metadata
route:gw:{id}              . gateway definitions and EMA metrics
cb:{target}                . distributed circuit breaker state
audit:event:{id}           . immutable audit events
```

Connection wiring:

- Limiter: `redisclient.LoadConfigFromEnv()` → `redisclient.New(cfg)` in `cmd/limiter/main.go`.
- Sidecar: `connectSidecarRedis()` in `cmd/sidecar/main.go` when idempotency or routing needs shared state.
- Env vars: `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_DB`, `REDIS_MODE`, `REDIS_MASTER_NAME`, `REDIS_SENTINEL_ADDRS`, `REDIS_SENTINEL_PASSWORD`.

The limiter process owns quota Lua execution; sidecars never decrement tokens directly.

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| Redis as SoT | Simple mental model, proven at scale | Redis outage blocks enforcement (by design) |
| Single master + replicas | Straightforward Sentinel failover | Brief write unavailability during promotion |
| Universal client (`go-redis/v9`) | Same code for standalone and Sentinel | Pool tuning (`PoolSize` default 100) matters under burst |
| Password optional in dev | Fast local iteration | `STRICT_SECURITY` path expects production keys |

## Failure modes

- Redis unreachable: Limiter returns 503 from `checkRedisCircuit()` when `cb:redis` is open or Allow fails; sidecar returns 503 unless `FAIL_OPEN=true`.
- Split brain during failover: Writes may fail until `FailoverClient` reconnects; metric `redis_failover_reconnects_total` increments on recovery ping.
- Hot keys: A single abusive `user_id` concentrates on one Redis hash; hierarchical keys spread load but do not eliminate hot tenants.
- Memory pressure: Audit `AUDIT_MAX_EVENTS` and idempotency `IDEMPOTENCY_COMPLETED_TTL_MS` cap growth; unbounded override keys need ops discipline.

## Operational concerns

- Monitor `rate_limiter_redis_duration_seconds`, `circuit_breaker_state{target="redis"}`, and limiter `/health` JSON (`redis.connected`, `redis.role`).
- Never point dev sidecars at prod Redis without `INTERNAL_API_KEY` alignment.
- For HA: `docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up`.
- `CIRCUIT_FAIL_OPEN=true` bypasses Redis circuit errors on Allow. dev only; production should stay fail-closed.

## Performance implications

Every allowed `/check` costs at least one Redis round-trip (Lua `EVAL`). Hierarchical checks run `hierarchical.lua` with four bucket levels. more work but one RTT. Sidecar denial cache (`CACHE_TTL_MS`, default 30ms in compose) cuts limiter load for repeated 429s but never caches allowances. Redis pool defaults (`PoolSize=100`, `MinIdleConns=10`) target concurrent sidecar fleets; saturation shows up as `rate_limiter_redis_duration_seconds` tail growth before CPU limits.

## Lessons learned

I initially underestimated how much **operational clarity** mattered: separating 503 (infrastructure) from 429 (quota) forced me to treat Redis as a hard dependency with explicit circuit breaking, not a best-effort cache. Redis was the right call because every subsystem I added later. idempotency, routing, audit. already needed fast atomic scripts. If I rebuilt from scratch, I would still choose Redis; I would invest earlier in Sentinel drills and hot-key dashboards.

**References:** `internal/redis/`, `cmd/limiter/main.go`, `cmd/sidecar/main.go`, `docs/diagrams/redis-layout.mmd`
