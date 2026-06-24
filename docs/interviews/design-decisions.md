# Design Decisions. Interview Guide

I built this platform to answer one question in interviews: *why did you make each choice, and what would you change if you had to do it again?* This document is my structured narrative for that conversation. Every section maps to how I actually reasoned through the problem, not a retrofitted justification.

---

## Problem Statement

When an interviewer asks "design a distributed rate limiter," they are really asking whether I understand **shared state under concurrency**, **failure semantics**, and **operational boundaries**. I needed a system that:

- Enforces quotas consistently across N sidecar replicas
- Supports multi-tenant hierarchical limits (global → tenant → user → endpoint)
- Handles duplicate POST retries without double-charging quota or executing side effects twice
- Degrades predictably when Redis or the limiter fails
- Can be operated without redeploying binaries for limit changes

The decisions documented here are the ones I would defend in a senior/staff system design loop.

---

## Why the problem exists

Rate limiting looks trivial until you deploy horizontally. Three structural forces made naive solutions fail for me:

1. State fragmentation: Ten app replicas with in-memory counters mean a user with a 100 req/min limit can send 100 to each replica.
2. Non-atomic read-modify-write: Moving counters to Redis with plain `GET`/`SET` just moved the race condition. I saw over-admission in k6 runs at ~1,000 RPS.
3. Cross-cutting concerns on the hot path: Quota, idempotency, routing, audit, and circuit breaking all need shared coordination. Embedding each in application code couples product teams to infrastructure semantics.

I separated **data plane** (fast allow/deny) from **control plane** (overrides, audit search, gateway registry) because they have different latency and availability budgets.

---

## Design goals

| Goal | Decision it drove |
|------|-------------------|
| Single source of quota truth | Central limiter (`cmd/limiter`). sidecars never decrement tokens directly |
| Atomic multi-level checks | One Lua script for four hierarchical buckets (`hierarchical.lua`) |
| Backend stays clean | Sidecar proxy pattern. apps do not import limiter code |
| Fail closed by default | Redis circuit on `cb:redis`; 503 for infra failure, 429 for quota exhaustion |
| Runtime tuning | Admin API on `:8082`, overrides in `config:{level}:{id}` with read-through cache |
| Explainable denials | Async audit trail with bounded worker pool |
| HA without code forks | `REDIS_MODE=sentinel` switches `go-redis` to `FailoverClient` |
| Observable hot path | OpenTelemetry spans + low-cardinality Prometheus metrics |

---

## Alternative approaches considered

### Embedded limiter in every service

**Rejected.** Fast locally, wrong globally. Every team forks algorithm parameters; rolling out a Lua fix requires N deploys.

### API gateway as sole enforcement point

**Rejected for this scope.** Kong/Envoy edge limits work for flat per-IP rules. I needed hierarchical quotas, idempotent replay with fencing tokens, and weighted gateway failover in one cohesive flow. that would have been three products duct-taped together.

### PostgreSQL row locks for counters

**Rejected.** Correct but too slow for hot `/check` paths. Lock contention under abuse would have moved the bottleneck from Redis to the database.

### etcd / Consul for coordination

**Rejected for counters.** Strong consistency but poor fit for high-frequency increments and TTL-based key eviction.

### Caching both allows and denials at the edge

**Rejected after I tried it.** A cached "allowed" response lets an attacker freeze their quota state until TTL expires. **Denial-only cache** is non-negotiable.

### Fail-open when Redis is down

**Rejected as default.** Availability metrics look good; upstream services get DDoS'd. I made `FAIL_OPEN` and `CIRCUIT_FAIL_OPEN` explicit opt-in with loud startup warnings.

### Synchronous audit on the hot path

**Rejected.** One extra Redis round-trip per `/check` would have doubled p99 under load. Async append with drop-on-overflow is the trade I accepted.

---

## Final architecture

### Core decision stack

```
Client → Sidecar (:9090) → Central Limiter (:8080) → Redis Lua → allow/deny
              ↓                      ↓
         Denial cache          Admin API (:8082)
         Singleflight          Overrides / audit / routing
         Idempotency           Circuit breaker (cb:redis)
         Gateway routing
```

### Decision-by-decision map

| Decision | Where it lives | Rationale |
|----------|----------------|-----------|
| **Redis as SoT** | `internal/redis`, all Lua scripts | Sub-ms reads, TTL, atomic scripts. see [why-redis.md](../decisions/why-redis.md) |
| **Lua for atomicity** | `internal/limiter/lua/*` | No interleaved commands during refill+deduct. see [why-lua.md](../decisions/why-lua.md) |
| **Sidecar extraction** | `cmd/sidecar` | Mirrors Envoy/Istio; backend unaware of limits. see [why-sidecar.md](../decisions/why-sidecar.md) |
| **Separate admin port** | `:8082` in `cmd/limiter/main.go` | Network-isolate control plane from data plane |
| **Hierarchical 4-level** | `hierarchical.lua` | SaaS quota model; one RTT for four checks |
| **Idempotency in sidecar** | `internal/idempotency` | Fleet-wide dedup without app changes. see [why-idempotency.md](../decisions/why-idempotency.md) |
| **Fencing tokens** | `claim.lua`, `complete.lua` | Stale worker cannot overwrite newer owner's response. see [why-fencing-tokens.md](../decisions/why-fencing-tokens.md) |
| **Weighted routing** | `internal/routing` | Health score + circuit state + weight. see [why-weighted-routing.md](../decisions/why-weighted-routing.md) |
| **Sentinel HA** | `docker-compose.ha.yml`, `REDIS_MODE=sentinel`. see [why-sentinel.md](../decisions/why-sentinel.md) |
| **OTel tracing** | `internal/telemetry`. see [why-otel.md](../decisions/why-otel.md) |
| **503 vs 429** | Limiter handlers | Operators distinguish infra failure from quota exhaustion |

### Ports and responsibilities

| Port | Process | Plane |
|------|---------|-------|
| `9090` | Sidecar | Data. client-facing proxy |
| `8080` | Limiter | Data. `/check`, `/check_hierarchical` |
| `8082` | Limiter admin | Control. overrides, audit, routing, circuit reset |
| `6379` | Redis | State. counters, idempotency, circuits, audit |

---

## Tradeoffs

| Decision | What I gained | What I paid |
|----------|---------------|-------------|
| Central limiter | One implementation, consistent enforcement | ~1 to 5 ms network hop per uncached check |
| Sidecar | Zero app code changes | Another container to deploy and monitor |
| Redis + Lua | Correctness under concurrency | Redis outage blocks enforcement |
| Denial-only cache | Prevents quota bypass; amortizes 429 storms | Allowed requests always hit Redis |
| Fail-closed circuits | Protects dying Redis from retry storms | User-visible 503 during outages |
| Async audit | Hot path stays fast | Events may drop when queue is full |
| Low-cardinality metrics | Prometheus stays stable | No per-user dashboards out of the box |
| Single Redis master (default) | Simple local/dev setup | Brief write unavailability during Sentinel failover |
| Runtime overrides with 5s TTL cache | Fast admin changes | Up to 5s propagation delay |

---

## Failure modes

| Failure | My design response |
|---------|-------------------|
| Redis unreachable at startup | Limiter **fatals** on `Ping`. refuses to start a liar service |
| Redis fails mid-request | `cb:redis` opens; `checkRedisCircuit` returns 503 before touching Redis |
| Limiter down, sidecar up | Sidecar returns 503 (or forwards if `FAIL_OPEN=true`) |
| All sidecars down | Clients cannot reach app. quota is not corrupted |
| Sentinel failover | `FailoverClient` reconnects; brief errors; circuit may open |
| Audit queue full | Event dropped, `audit_dropped` metric increments. enforcement continues |
| Idempotency lease expires | Lock reclaimed; stale worker rejected via fence token |
| Cached denial stale | Safe. user retries and gets fresh Redis evaluation |
| Split-brain Redis (misconfigured Sentinel) | Undefined. I assume correct quorum deployment |

---

## Operational concerns

- Secrets: `INTERNAL_API_KEY` protects `/check`; `ADMIN_API_KEY` protects `:8082`. Rotate separately.
- Identity: Production must set `X-User-ID` from an auth gateway. `ALLOW_QUERY_USER_ID=true` is demo-only.
- Fail-open flags: `FAIL_OPEN`, `IDEMPOTENCY_FAIL_OPEN`, `CIRCUIT_FAIL_OPEN`. document in runbooks; never enable all three in prod without explicit risk acceptance.
- Graceful shutdown: Limiter drains in-flight checks for 5s on SIGTERM. important for rolling deploys.
- Override discipline: Unbounded `config:*` keys need ops cleanup; audit `AUDIT_MAX_EVENTS` caps growth.
- HA overlay: `docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up` for Sentinel testing.

---

## Performance implications

From my benchmark suite (`benchmarks/summary.md`):

- ~1,000 actual RPS: Sustained with p99 < 100 ms and 0% errors. practical ceiling per sidecar+Redis pair on my hardware.
- Beyond **5,000 target RPS**, actual throughput plateaus (~1,353 RPS) with p99 at 3.5s and 10% errors. Redis single-threaded execution is the knee.
- Hierarchical check: One Lua RTT for four buckets vs four sequential calls. longer script CPU but one network round-trip.
- Sidecar singleflight: 100 concurrent requests for the same cache key share one limiter HTTP call.
- Denial-only cache (30 ms TTL): Prevents Redis meltdown under sustained 429 storms.
- Idempotency replay: ~942 RPS, p95 5.7 ms, 0% errors. cached responses skip upstream entirely.

---

## Lessons learned

1. Never cache allows at the edge: I tried it in an early prototype. One cached "allowed" entry let a user bypass limits until TTL expired.

2. Split data and control ports early: Mixing admin CRUD on `:8080` would have complicated mTLS and rate-limiting the limiter itself.

3. Fence tokens are worth the Redis fields: Without them, a slow client could complete an idempotency key after lock reclaim and overwrite a newer owner's response.

4. Start strict, loosen deliberately: Fail-closed circuits, fatal Redis ping at boot, and loud `FAIL_OPEN` warnings saved me from shipping "available but wrong" configurations.

5. Algorithm choice matters for product semantics: Token bucket feels fair (smooth refill); sliding window is easier to explain in SLAs ("500 per minute"). I kept them as separate embeds rather than one mega-script.

6. Numbers without chaos tests are not useful: K6 throughput told me the saturation point; `chaos/chaos_test.ps1` told me whether quota corrupted when Redis died.

7. Treat `StateUnknown` on gateway circuits as non-selectable: When `enrichCircuit` cannot read `cb:{id}`, I mark the gateway non-selectable rather than blindly sending traffic into a black hole.

---

## Related documents

- [Architecture overview](../architecture/overview.md)
- [All ADRs in decisions/](../decisions/)
- [Tradeoffs deep dive](./tradeoffs.md)
- [Common interview questions](./common-questions.md)
- [System design walkthrough](./system-design-discussion.md)
