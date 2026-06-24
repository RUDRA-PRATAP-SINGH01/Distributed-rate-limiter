# Tradeoffs. Interview Guide

Interviewers do not want a perfect design. they want to hear me articulate **what I traded and why**. This document is my honest accounting of every meaningful tradeoff in the distributed rate limiter, structured for system design discussions.

---

## Problem Statement

Every architectural choice has a cost. I built this system knowing I could not optimize for correctness, availability, latency, operability, and simplicity simultaneously. The problem I needed to solve in interviews (and in production) was: **given my constraints, which costs am I willing to pay, and which failure modes am I willing to accept?**

---

## Why the problem exists

Distributed rate limiting sits at the intersection of conflicting forces:

- Consistency vs latency: Shared state requires a network round-trip; caching reduces latency but risks stale decisions.
- Availability vs correctness: Fail-open keeps traffic flowing but removes protection entirely.
- Simplicity vs features: Hierarchical quotas, idempotency, routing, and audit each add Redis keys, Lua complexity, and failure surfaces.
- Observability vs cardinality: Per-user Prometheus labels would OOM the metrics stack under real traffic.

I made these tradeoffs explicit in code (env flags, separate ports, denial-only cache) rather than hiding them in defaults.

---

## Design goals

When evaluating tradeoffs, I ranked priorities in this order:

1. No silent over-admission, no quota corruption on failure
2. Predictable degradation: 503/429 with clear semantics, not silent bypass
3. Operability: Runtime overrides, audit trail, circuit visibility
4. Latency: Sub-10 ms p99 at sustainable load (~1,000 RPS)
5. Availability during Redis outage: Explicitly deprioritized vs correctness

This ordering drove fail-closed defaults and denial-only caching.

---

## Alternative approaches considered

### Optimize for availability (fail-open everywhere)

I could have defaulted `FAIL_OPEN=true`, `CIRCUIT_FAIL_OPEN=true`, and skipped Redis ping at startup. Demos would never show 503. Production would have no rate limiting during outages. I rejected this as the default.

### Optimize for latency (local counters + periodic sync)

Gossip or eventual consistency between sidecars would eliminate the limiter hop. Limits would drift for minutes after scale events. I rejected this for quota enforcement.

### Optimize for simplicity (flat per-user limits only)

No hierarchical Lua, no four-key atomic script. Simpler but inadequate for multi-tenant SaaS. I accepted the Lua complexity.

### Optimize for observability (per-user metrics)

Rich dashboards per tenant. Prometheus would explode under cardinality. I chose handler-level labels only.

### Optimize for zero network hops (embed Redis in sidecar)

Each sidecar could run its own Redis client and Lua. Quota logic would fork across N deployments. I centralized in one limiter binary.

---

## Final architecture

### Tradeoff matrix (the decisions I actually shipped)

| Layer | Choice | Benefit | Cost |
|-------|--------|---------|------|
| **Topology** | Central limiter + sidecar | Single source of truth; backend stays clean | Extra hop; limiter is a dependency |
| **State** | Redis single master (+ Sentinel optional) | Proven, fast, Lua atomicity | SPOF for admission; failover blip |
| **Atomicity** | Lua scripts | No race on refill+deduct | Script CPU on primary; hot-key bottleneck |
| **Edge cache** | Denial-only, 30 ms TTL | Amortizes 429 storms; safe semantics | Allows always hit Redis |
| **Coalescing** | Singleflight on cache key | One limiter call per burst | Adds mutex contention on hot keys |
| **Idempotency** | Redis-backed in sidecar | Fleet-wide dedup without app changes | Extra memory; 24h TTL; fence complexity |
| **Audit** | Async worker pool (4 workers, queue 4096) | Hot path unaffected | Events drop under extreme overload |
| **Circuits** | Distributed on `cb:{target}` | Shared breaker state across replicas | Redis read on every check path |
| **Routing** | Weighted score + circuit enrichment | Graceful gateway degradation | `StateUnknown` → gateway excluded |
| **Admin** | Separate `:8082` port | Network isolation for control plane | Two surfaces to secure |
| **Metrics** | Low-cardinality labels | Stable Prometheus | No per-user drill-down |
| **Tracing** | OTLP to Jaeger | End-to-end latency breakdown | Span export overhead; sampling needed at scale |
| **Overrides** | Redis + 5s read-through cache | No redeploy for limit changes | Up to 5s propagation delay |
| **Status codes** | 503 infra / 429 quota | Clear ops signal | Clients must handle both |

### Latency budget (sustainable load)

```
Client → Sidecar:        ~0.5 ms (local)
Sidecar → Limiter:       ~1 to 3 ms (LAN HTTP)
Limiter → Redis Lua:     ~0.5 to 2 ms
─────────────────────────────────
Uncached allow total:    ~2 to 6 ms p50
Cached 429 denial:       ~0.1 ms (memory hit)
```

---

## Tradeoffs

### The tradeoffs interviewers ask about most

**"Why not just use an API gateway?"**

Gateways excel at edge policies (IP rate limits, WAF). I needed hierarchical tenant quotas, idempotent POST dedup with fencing, and weighted failover across gateway pools. all sharing Redis state. A gateway-only approach would still need a coordination layer; I built the coordination layer first and put the proxy in a sidecar.

**"Why fail-closed? Your availability SLO suffers."**

Because the alternative is fail-open, which means **no rate limiting** during Redis outage. For abuse-prone APIs, that is a security incident, not a degradation. I expose opt-in fail-open for dev with loud warnings.

**"Why cache denials but not allows?"**

Caching allows creates a quota bypass: one "allowed" entry freezes token consumption until TTL expires. Denials are safe to cache. repeating a 429 does not grant extra capacity.

**"Why central limiter instead of sidecar → Redis directly?"**

I wanted one place to attach audit, circuit guards, override resolution, and algorithm changes. Sidecars stay thin HTTP proxies. The cost is one network hop. amortized by singleflight and denial cache.

**"Why async audit instead of sync?"**

Sync audit adds one Redis RTT to every `/check`. At 1,000 RPS that is 1,000 extra writes/sec on the same Redis primary executing quota Lua. I chose drop-on-overflow over slowing enforcement.

**"Why single Redis master instead of Cluster?"**

Operational simplicity for my scope. Sentinel met my HA bar (automatic failover, client rediscovery). Cluster adds slot migration complexity I deferred until hot-key sharding becomes the bottleneck.

---

## Failure modes

| Tradeoff | Failure it accepts | Mitigation |
|----------|-------------------|------------|
| Fail-closed | 503 during Redis outage | Sentinel HA; circuit half-open recovery |
| Denial-only cache | Stale 429 for up to 30 ms | Short TTL; allows always fresh |
| Async audit | Lost audit events under overload | `audit_dropped` metric; queue sizing |
| Override cache | Stale limits for up to 5s | TTL tuning; direct Redis read in admin |
| Low-cardinality metrics | Cannot trace single abusive user | Audit search by tenant/user |
| Single master | Write unavailability during failover | `redis_failover_reconnects_total`; chaos tests |
| Idempotency 24h TTL | Keys expire; retries may re-execute | Document TTL; client idempotency key discipline |
| `StateUnknown` circuit | Gateway temporarily excluded | Admin circuit reset; Redis health |

---

## Operational concerns

- Document fail-open flags in runbooks: `FAIL_OPEN`, `IDEMPOTENCY_FAIL_OPEN`, `CIRCUIT_FAIL_OPEN` change the tradeoff surface. operators must know which are enabled.
- Monitor the tradeoffs you accepted: `audit_dropped`, `circuit_breaker_state`, `sidecar_cache_hits_total` vs `misses_total`, `routing_failovers_total`.
- Capacity planning uses actual RPS, not target RPS: My saturation knee is ~1,000 actual RPS. planning for 10,000 requires Redis sharding or multiple limiter shards, which I have not built.
- Override propagation delay: After admin changes, wait 5s or flush override cache before validating in benchmarks.

---

## Performance implications

| Tradeoff | Measured impact |
|----------|-----------------|
| Central limiter hop | +1 to 3 ms vs hypothetical embedded limiter |
| Denial cache | 429 storms: near-zero Redis load for repeat denials |
| Singleflight | 100 VUs same key → 1 limiter HTTP call (idempotency race test) |
| Hierarchical Lua | One RTT but ~4× script CPU vs single bucket |
| Hot-key contention | 5,000 target RPS, 10 users: 99.9% rejected, p99 < 25 ms (correct, not fast) |
| Saturation | 5,000 target → 1,353 actual, p99 3.5s, 10% errors |
| Idempotency replay | ~942 RPS, p95 5.7 ms. no upstream calls |

---

## Lessons learned

1. Name the tradeoff out loud in the interview: "I chose correctness over availability during Redis outage" is a complete answer. Unnamed tradeoffs sound like oversights.

2. Defaults encode values: Fail-closed and denial-only cache are not implementation details. they are policy decisions I would defend to a security reviewer.

3. The cheapest tradeoff is often the one you do not build: I deferred Redis Cluster, per-user metrics, and RBAC on the admin API because each would have shifted my correctness/latency balance.

4. Benchmark the tradeoff, do not assume it: I thought singleflight would matter more than it did at 100 RPS; at 5,000 RPS on hot keys, Redis script CPU dominated.

5. Every fail-open flag is a foot-gun with a safety label: I log warnings at startup because I know future-me will enable them to "fix" a demo.

---

## Related documents

- [Design decisions](./design-decisions.md)
- [Architecture overview. Tradeoffs section](../architecture/overview.md)
- [Redis outage failure mode](../failure-modes/redis-outage.md)
- [Benchmark summary](../../benchmarks/summary.md)
