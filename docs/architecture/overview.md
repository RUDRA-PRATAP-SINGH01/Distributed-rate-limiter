# System Architecture Overview

When I started building this distributed rate limiter, I was not trying to ship another Redis wrapper with a `/check` endpoint. I was trying to solve a fleet problem: dozens of application replicas each enforcing limits locally will drift, double-count under retries, and melt Redis the moment a client hammers a hot key. I needed a system where **one process owns quota truth**, **many sidecars sit on the data path**, and **control-plane knobs** (overrides, audit, routing weights) can change at runtime without redeploying binaries.

This document is the map I wish I had on day one.

---

## Problem Statement

I need to protect upstream services from abusive or accidental traffic bursts across a horizontally scaled deployment. The enforcement must be:

- **Consistent** — the same user cannot consume more than their budget because they hit different pods.
- **Low-latency** — a limit check cannot add hundreds of milliseconds to every API call.
- **Observable** — operators need to know who was denied, why, and whether Redis or a gateway is unhealthy.
- **Safe under partial failure** — Redis blips, gateway outages, and duplicate POST retries should not corrupt quota or double-charge side effects.

---

## Why the problem exists

The naive fix — an in-process token bucket per app instance — fails for three structural reasons I kept hitting in design reviews:

1. **State fragmentation.** Ten replicas mean ten independent buckets. A user with a limit of 100 req/min can send 100 to each replica.
2. **Retry amplification.** Idempotent payment or order APIs retry on timeouts. Without deduplication, each retry consumes quota and may execute twice upstream.
3. **Hot-path coupling.** Putting Redis on every request inside the app couples product code to infrastructure semantics (Lua scripts, key naming, circuit behavior).

I split the problem into a **data plane** (fast allow/deny on the request path) and a **control plane** (configuration, audit search, gateway registry) that can tolerate slightly higher latency.

---

## Design goals

| Goal | How I implemented it |
|------|----------------------|
| Single source of quota truth | `cmd/limiter` + Redis Lua (`internal/limiter/lua/*`) |
| Edge enforcement without owning state | `cmd/sidecar` on `:9090` calls limiter `:8080` |
| Runtime limit tuning | Admin API on `:8082`, overrides in `config:{level}:{id}` |
| Safe mutating requests | Idempotency layer in sidecar (`internal/idempotency`) |
| Gateway selection under degradation | Intelligent routing (`internal/routing`) + circuit breaker (`internal/circuitbreaker`) |
| Explainable denials | Async audit trail with worker pool (`internal/audit`) |
| HA for state store | Redis Sentinel via `REDIS_MODE=sentinel` (`internal/redis`, `docker-compose.ha.yml`) |

---

## Alternative approaches considered

### 1. Embedded limiter in every service

I rejected this early. It minimizes network hops but maximizes consistency bugs. Every team would fork algorithm parameters, and rolling out a Lua fix would require N service deploys.

### 2. API gateway as the only enforcement point

Kong, Envoy, or a cloud WAF could rate-limit at the edge. That works until you need **hierarchical limits** (global → tenant → user → endpoint), **idempotent replay with fencing**, and **weighted gateway failover** in one cohesive flow. I would have ended up with three products duct-taped together.

### 3. Synchronous audit on the hot path

Recording every allow/deny inline before responding would have added one Redis round-trip to `/check`. I chose **async append with a bounded worker pool** (default 4 workers, queue 4096). The tradeoff I accepted: under extreme overload, audit events may be dropped rather than slowing enforcement.

### 4. Fail-open everywhere when Redis is down

Tempting for availability metrics. I default to **fail-closed** on the central limiter (`checkRedisCircuit` returns 503 unless `CIRCUIT_FAIL_OPEN=true`). The sidecar has a separate `FAIL_OPEN` flag — I document it loudly in logs because it is a production foot-gun.

---

## Final architecture

### Component topology

```
                    ┌─────────────────────────────────────────┐
                    │           CONTROL PLANE (:8082)          │
                    │  overrides · idempotency admin · routing │
                    │  circuit reset · audit search            │
                    └──────────────────┬──────────────────────┘
                                       │ Redis (shared)
┌──────────┐    ┌──────────────┐       │       ┌─────────────────┐
│  Client  │───▶│ Sidecar :9090│───────┼──────▶│ Limiter :8080   │
└──────────┘    │  cache       │       │       │  /check         │
                │  singleflight│       │       │  /check_hier... │
                │  idempotency │       │       │  circuit→Redis  │
                │  routing     │       │       │  audit workers  │
                └──────┬───────┘       │       └────────┬────────┘
                       │               │                │
                       ▼               ▼                ▼
                 Gateway pool      Redis master      Prometheus
                 (gateway-a/b/c)   (+ Sentinel HA)   /metrics
                       │
                       ▼
                 Upstream / demo :8081
```

### Data plane vs control plane

**Data plane (latency-sensitive):**

| Process | Port | Responsibility |
|---------|------|----------------|
| `rate-sidecar` | `9090` | Identity resolution, denial cache, singleflight, optional idempotency claim/replay, optional gateway routing |
| `rate-limiter` | `8080` | Authoritative `/check` and `/check_hierarchical`, Redis circuit guard, audit enqueue |
| Redis | `6379` | Atomic quota, idempotency locks, circuit state, routing metrics, audit indexes |

**Control plane (ops / admin):**

| Surface | Port | Responsibility |
|---------|------|----------------|
| Limiter admin API | `8082` | CRUD for `config:*` overrides, idempotency inspection, gateway enable/weight, circuit reset, audit search |
| Health | `8080/health`, `9090/health` | Limiter pings Redis; sidecar pings limiter |
| Metrics | `/metrics` on both | Prometheus counters for cache, circuit, routing, audit |

The limiter binary intentionally runs **two HTTP servers** — `main.go` serves the hot path on `PORT` (default 8080) and `startAdminServer` on `ADMIN_PORT` (default 8082). In production I network-isolate 8082 to an internal security group while 8080 stays reachable only from sidecars.

### Shared internal packages

| Package | Role |
|---------|------|
| `internal/limiter` | Token bucket, sliding window, hierarchical Lua orchestration |
| `internal/redis` | Standalone vs Sentinel client factory, health checks |
| `internal/circuitbreaker` | Distributed breaker on `cb:{target}` hashes |
| `internal/idempotency` | Claim/complete/fail with fence tokens |
| `internal/routing` | Gateway registry, scoring, failover |
| `internal/audit` | Append-only event log with ZSET indexes |
| `internal/override` | Read-through cache for runtime limits |
| `internal/identity` | `X-User-ID` resolution |
| `internal/telemetry` | OpenTelemetry spans on limiter, sidecar, Redis |

### Redis as the coordination backbone

Every cross-replica invariant I care about runs in Lua: token refill + decrement, idempotency claim, circuit allow/record, audit append with trim. Go code orchestrates; Redis scripts enforce atomicity. See [redis-design.md](./redis-design.md) for key namespaces.

### Sentinel HA

For standalone dev I use `docker-compose.yml` with a single `redis:7-alpine` container. For HA I overlay `docker-compose.ha.yml` with master, two replicas, and three Sentinels. `internal/redis.New` switches to `redis.NewFailoverClient` when `REDIS_MODE=sentinel`, so limiter and sidecar reconnect to the promoted master without config changes — only env vars `REDIS_MASTER_NAME` and `REDIS_SENTINEL_ADDRS`.

---

## Tradeoffs

**Central limiter adds a network hop.** I pay ~1–5 ms (LAN) per uncached check in exchange for one implementation of hierarchical limits and one place to attach audit. Sidecar denial cache (default 30 ms TTL) and singleflight amortize this on hot keys.

**Sidecar is stateless except for in-memory denial cache.** Rolling a sidecar pod loses cache — safe, because I never cache allows. Rolling the limiter is safe; quota lives in Redis.

**Admin API shares Redis with data plane.** A misconfigured audit retention job or KEYS scan in ops tooling competes with `/check`. I use indexed ZSET searches, not `KEYS *`, in production tooling.

**Circuit breaker fail-closed by default.** When `cb:redis` opens, `/check` returns 503 before touching Redis — protecting a dying Redis from a retry storm but reducing availability. I made `CIRCUIT_FAIL_OPEN` opt-in.

---

## Failure modes

| Failure | System behavior |
|---------|-----------------|
| Redis unreachable at startup | Limiter **fatals** on `Ping` — I refuse to start a liar service |
| Redis fails mid-request | Circuit records failure; after threshold, `checkRedisCircuit` blocks checks with 503 |
| All sidecars down | Clients cannot reach app — but quota is not corrupted |
| Limiter down, sidecar up | Sidecar returns 503 (or forwards if `FAIL_OPEN=true`) |
| Sentinel failover during request | go-redis FailoverClient reconnects; brief errors possible; circuit may open |
| Audit queue full | `Record` drops event, increments `audit_dropped` metric — enforcement continues |
| Split-brain Redis (misconfigured Sentinel) | Undefined — I assume correct quorum deployment |

---

## Operational concerns

- **Secrets:** `INTERNAL_API_KEY` protects `/check`; `ADMIN_API_KEY` protects `:8082`. Sidecar forwards the internal key in `X-Internal-API-Key`.
- **Identity:** Production must set `X-User-ID` from an auth gateway. `ALLOW_QUERY_USER_ID=true` is demo-only.
- **Path allowlist:** Sidecar warns if `ALLOWED_PATHS` is unset — everything is proxied.
- **Graceful shutdown:** Limiter drains in-flight checks for 5 s on SIGTERM — important for rolling deploys.
- **Observability:** Jaeger OTLP on `:4318`, Prometheus `/metrics`. I trace `limiter.check`, `sidecar.proxy`, `sidecar.idempotency`, `sidecar.intelligent_route`.

---

## Performance implications

- **Hierarchical check:** One Lua round-trip for four token buckets (`hierarchical.lua`) vs four separate `/check` calls.
- **Sidecar singleflight:** 100 concurrent requests for the same cache key share one limiter HTTP call.
- **Denial-only cache:** Prevents Redis meltdown under sustained 429 storms; allows always re-hit Redis so tokens stay accurate.
- **Audit async:** Hot path enqueues to channel; workers batch Redis appends off-thread.
- **Connection pooling:** Redis `PoolSize` default 100, `MinIdleConns` 10 per process.

Benchmarks under `benchmarks/` document saturation behavior; routing and idempotency race tests validate correctness under concurrency.

---

## Lessons learned

1. **Never cache allows at the edge.** I tried it in an early prototype. One cached "allowed" entry let a user bypass limits until TTL expired. Denial-only cache is non-negotiable.

2. **Split data and control ports early.** Mixing admin CRUD on `:8080` would have complicated mTLS and rate-limiting the limiter itself.

3. **Fence tokens are worth the Redis fields.** Without them, a slow client could complete an idempotency key after a lock reclaim and overwrite a newer owner's response.

4. **Treat `StateUnknown` on gateway circuits as closed-for-selection.** When `enrichCircuit` cannot read `cb:{id}`, I mark the gateway non-selectable rather than blindly sending traffic into a black hole.

5. **Start strict, loosen deliberately.** Fail-closed circuits, fatal Redis ping at boot, and loud `FAIL_OPEN` warnings saved me from shipping "available but wrong" configurations.

---

## Related documents

- [request-flow.md](./request-flow.md) — end-to-end sequence for normal vs idempotent paths
- [sidecar-architecture.md](./sidecar-architecture.md) — cache, singleflight, identity
- [redis-design.md](./redis-design.md) — key layout and Lua patterns
- [routing-architecture.md](./routing-architecture.md) — scorer, failover, circuit integration
- Diagram: [../diagrams/request-flow.mmd](../diagrams/request-flow.mmd)
