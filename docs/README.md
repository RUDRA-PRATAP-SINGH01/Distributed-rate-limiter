# Documentation

I built this distributed rate limiter to understand how production systems enforce traffic quotas at scale, not a single-process token bucket, but something that stays correct with ten sidecars, duplicate POST retries, and a Redis outage. This folder is the engineering record: architecture, decisions, failure modes, benchmarks, and interview prep. The root [README.md](../README.md) covers setup and API reference; **this document is the map**.

---

## Project motivation

I started this project after reading about API gateways and SaaS quota models and realizing I did not actually understand what happens under concurrency when limits live in a shared store. I wanted to answer:

- How do you enforce limits across N replicas without drift?
- What breaks when Redis dies mid-request?
- How do idempotent retries interact with quota consumption?
- Where do you put enforcement so application code stays clean?

This is a learning and portfolio project, but I treated it like something I would operate: benchmarks, chaos tests, ADRs, failure-mode docs, and tracing, not just a `/check` endpoint.

---

## Problems solved

| Problem | How I addressed it |
|---------|-------------------|
| Per-instance counters diverge under horizontal scale | Central limiter + Redis Lua atomic scripts |
| GET/SET races over-admit under load | Embedded Lua (`token_bucket.lua`, `hierarchical.lua`) |
| Multi-tenant quota stacking | Four-level hierarchical check in one atomic script |
| Backend coupled to infrastructure | Sidecar proxy. apps never import limiter code |
| Duplicate POST retries double-execute | Idempotency layer with claim/complete and fencing tokens |
| Stale "allowed" cache bypasses limits | Denial-only edge cache (30 ms TTL) |
| Redis outage causes retry storm | Distributed circuit breaker on `cb:redis`, fail-closed default |
| Limit changes require redeploy | Admin API + `config:{level}:{id}` overrides |
| Denials are invisible to ops | Async audit trail with searchable indexes |
| Gateway degradation routes to black holes | Weighted routing + circuit enrichment; `StateUnknown` → non-selectable |
| Failover during Redis promotion | Sentinel mode via `REDIS_MODE=sentinel` |

---

## Architecture summary

```
                    ┌─────────────────────────────────────────┐
                    │           CONTROL PLANE (:8082)          │
                    │  overrides · idempotency admin · routing │
                    │  circuit reset · audit search            │
                    └──────────────────┬──────────────────────┘
                                       │ Redis (shared)
┌──────────┐    ┌──────────────┐       │       ┌─────────────────┐
│  Client  │───▶│ Sidecar :9090│───────┼──────▶│ Limiter :8080   │
└──────────┘    │  denial cache│       │       │  /check         │
                │  singleflight│       │       │  /check_hier... │
                │  idempotency │       │       │  circuit→Redis  │
                │  routing     │       │       │  audit workers  │
                └──────┬───────┘       │       └────────┬────────┘
                       │               │                │
                       ▼               ▼                ▼
                 Gateway pool      Redis master      Prometheus / Jaeger
                 (optional)        (+ Sentinel HA)
                       │
                       ▼
                 Upstream / demo :8081
```

**Data plane** (latency-sensitive): sidecar `:9090`, limiter `:8080`, Redis Lua on the hot path.

**Control plane** (ops): limiter admin `:8082`. overrides, audit search, gateway registry, circuit reset.

Start here: [architecture/overview.md](architecture/overview.md) · [architecture/request-flow.md](architecture/request-flow.md)

---

## Core features

| Feature | Package / binary | Doc |
|---------|------------------|-----|
| Token bucket & sliding window | `internal/limiter` | [deep-dives/distributed-rate-limiting.md](deep-dives/distributed-rate-limiting.md) |
| Hierarchical quotas (global → tenant → user → endpoint) | `lua/hierarchical.lua` | [deep-dives/hierarchical-quotas.md](deep-dives/hierarchical-quotas.md) |
| Sidecar proxy with denial cache & singleflight | `cmd/sidecar` | [architecture/sidecar-architecture.md](architecture/sidecar-architecture.md) |
| Runtime overrides | `internal/override`, admin `:8082` | [architecture/overview.md](architecture/overview.md) |
| Idempotency (claim / replay / fence) | `internal/idempotency` | [deep-dives/idempotency.md](deep-dives/idempotency.md) |
| Intelligent gateway routing | `internal/routing` | [deep-dives/intelligent-routing.md](deep-dives/intelligent-routing.md) |
| Distributed circuit breakers | `internal/circuitbreaker` | [deep-dives/circuit-breakers.md](deep-dives/circuit-breakers.md) |
| Async audit trail | `internal/audit` | [deep-dives/audit-trail.md](deep-dives/audit-trail.md) |
| Redis Sentinel HA | `internal/redis`, `docker-compose.ha.yml` | [architecture/sentinel-ha-architecture.md](architecture/sentinel-ha-architecture.md) |
| OpenTelemetry tracing | `internal/telemetry` | [deep-dives/distributed-tracing.md](deep-dives/distributed-tracing.md) |
| Prometheus metrics | `/metrics` on limiter and sidecar | [architecture/observability-architecture.md](architecture/observability-architecture.md) |

---

## Design decisions index

Architecture Decision Records. each explains why I chose an approach and what I rejected.

| Decision | Document |
|----------|----------|
| Redis as coordination backbone | [decisions/why-redis.md](decisions/why-redis.md) |
| Lua for atomic read-modify-write | [decisions/why-lua.md](decisions/why-lua.md) |
| Sidecar extraction pattern | [decisions/why-sidecar.md](decisions/why-sidecar.md) |
| Idempotency in the proxy layer | [decisions/why-idempotency.md](decisions/why-idempotency.md) |
| Fencing tokens on lock reclaim | [decisions/why-fencing-tokens.md](decisions/why-fencing-tokens.md) |
| Weighted gateway routing | [decisions/why-weighted-routing.md](decisions/why-weighted-routing.md) |
| Redis Sentinel for HA | [decisions/why-sentinel.md](decisions/why-sentinel.md) |
| OpenTelemetry over ad-hoc logging | [decisions/why-otel.md](decisions/why-otel.md) |

Interview-oriented synthesis: [interviews/design-decisions.md](interviews/design-decisions.md) · [interviews/tradeoffs.md](interviews/tradeoffs.md)

---

## Benchmarks summary

Final evidence: [benchmarks/final-benchmark-report.md](benchmarks/final-benchmark-report.md) (commit `a1de9ec`, 2026-07-10). Raw artifacts: `benchmarks/results/a1de9ec-final/`.

### Rate limiter throughput (i9-14900HX, 32GB RAM, Docker Compose — final run)

| Workload | Target RPS | Actual RPS | p99 | Error rate | Verdict |
|----------|------------|------------|-----|------------|---------|
| Sidecar e2e | 1,000 | **872** | **11 ms** | 0% | **Max sustainable** |
| Direct sliding | 1,000 | **871** | **8 ms** | 0% | **Max sustainable** |
| Sidecar e2e | 5,000 | 1,504 | 383 ms | 0% | Saturated |
| Direct token bucket | 5,000 | 4,161 | 148 ms | 0% | High throughput; p99 > 100 ms |

**Key finding:** ~**870–872 actual RPS** sustainable end-to-end (p99 < 100 ms). Sliding window saturates well below 5,000 target on single Redis master.

### Correctness tests (final / prior runs)

| Test | Result |
|------|--------|
| Multi-sidecar quota (60 concurrent) | **10 allowed / 50 denied** (cap=10) |
| Idempotency burst (40 parallel, 2 sidecars) | **1×200, 39×409** |
| Singleflight | 100 concurrent → **1 limiter call** (Go test) |
| Denial cache hammer | p99 **7 ms** on cached denials |

Run: `powershell -File benchmarks/final/run-targeted-benchmarks.ps1`

---

## Failure handling

I default to **fail-closed**: infrastructure failure returns **503**, quota exhaustion returns **429**. I never conflate them.

| Failure | Behavior | Doc |
|---------|----------|-----|
| Redis unreachable at startup | Limiter fatals on `Ping` | [failure-modes/redis-outage.md](failure-modes/redis-outage.md) |
| Redis fails mid-request | `cb:redis` opens → 503 before Lua | [failure-modes/redis-outage.md](failure-modes/redis-outage.md) |
| Sentinel failover | Brief write errors; client reconnects | [failure-modes/sentinel-failover.md](failure-modes/sentinel-failover.md) |
| Limiter down, sidecar up | 503 (or forward if `FAIL_OPEN=true`) | [failure-modes/gateway-timeout.md](failure-modes/gateway-timeout.md) |
| Duplicate POST retries | Idempotency claim/replay; 409 in-progress | [failure-modes/duplicate-requests.md](failure-modes/duplicate-requests.md) |
| Idempotency lease expires | Lock reclaimed; fence rejects stale worker | [failure-modes/lease-expiration.md](failure-modes/lease-expiration.md) |
| All gateways unhealthy | Routing returns 503 | [failure-modes/routing-failures.md](failure-modes/routing-failures.md) |
| Circuit breaker stuck open | Admin reset; half-open recovery | [failure-modes/circuit-breaker-failures.md](failure-modes/circuit-breaker-failures.md) |
| Audit queue full | Event dropped; enforcement continues | [failure-modes/audit-failures.md](failure-modes/audit-failures.md) |

Chaos validation: [deep-dives/chaos-testing.md](deep-dives/chaos-testing.md) · `chaos/chaos_test.ps1`

---

## Observability

| Signal | Where | Doc |
|--------|-------|-----|
| Prometheus metrics | `:8080/metrics`, `:9090/metrics` | [architecture/observability-architecture.md](architecture/observability-architecture.md) |
| OpenTelemetry traces | OTLP → Jaeger `:4318` / UI `:16686` | [deep-dives/distributed-tracing.md](deep-dives/distributed-tracing.md) |
| Health checks | `/health` on limiter (Redis) and sidecar (limiter) | [architecture/overview.md](architecture/overview.md) |
| Audit search | Admin `GET /admin/audit/search` | [deep-dives/audit-trail.md](deep-dives/audit-trail.md) |
| Correlation headers | `X-Request-ID`, `X-Trace-ID`, W3C `traceparent` | [deep-dives/distributed-tracing.md](deep-dives/distributed-tracing.md) |

I intentionally keep Prometheus label cardinality low. no per-user labels. That trades drill-down convenience for metrics stability under abuse.

Key metrics: `rate_limiter_requests_total`, `rate_limiter_redis_duration_seconds`, `circuit_breaker_state`, `audit_dropped`, `routing_failovers_total`, `idempotency_claims_total`.

---

## Reliability

| Mechanism | Purpose |
|-----------|---------|
| Redis Lua atomicity | No lost updates on concurrent `/check` |
| Denial-only cache | Safe edge optimization; no quota bypass |
| Singleflight | Coalesce burst checks for same cache key |
| Distributed circuit breaker (`cb:redis`, `cb:{gateway}`) | Stop retry storms into failing dependencies |
| Fencing tokens | Stale idempotency worker cannot overwrite fresh response |
| Sentinel failover | Automatic master promotion; `FailoverClient` rediscovery |
| Graceful shutdown | 5s drain on limiter SIGTERM |
| Fatal Redis ping at boot | Refuse to serve as false healthy |
| Async audit with bounded queue | Hot path never blocks on audit durability |

HA overlay: `docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up`

---

## Full documentation index

### Architecture

| Document | Description |
|----------|-------------|
| [architecture/overview.md](architecture/overview.md) | System map, data vs control plane, design goals |
| [architecture/request-flow.md](architecture/request-flow.md) | End-to-end sequences (normal, hierarchical, idempotent) |
| [architecture/sidecar-architecture.md](architecture/sidecar-architecture.md) | Denial cache, singleflight, identity resolution |
| [architecture/redis-design.md](architecture/redis-design.md) | Key namespaces, Lua patterns |
| [architecture/routing-architecture.md](architecture/routing-architecture.md) | Gateway scorer, failover, circuit integration |
| [architecture/idempotency-architecture.md](architecture/idempotency-architecture.md) | Claim/complete state machine |
| [architecture/circuit-breaker-architecture.md](architecture/circuit-breaker-architecture.md) | Three-state breaker on Redis |
| [architecture/audit-trail-architecture.md](architecture/audit-trail-architecture.md) | Async append, indexes, trim |
| [architecture/observability-architecture.md](architecture/observability-architecture.md) | Metrics, traces, health |
| [architecture/sentinel-ha-architecture.md](architecture/sentinel-ha-architecture.md) | Sentinel topology, client failover |

### Deep dives

| Document | Description |
|----------|-------------|
| [deep-dives/distributed-rate-limiting.md](deep-dives/distributed-rate-limiting.md) | Algorithms, atomicity, hot keys |
| [deep-dives/hierarchical-quotas.md](deep-dives/hierarchical-quotas.md) | Four-level stacked limits |
| [deep-dives/redis-lua-atomicity.md](deep-dives/redis-lua-atomicity.md) | Why Lua, script structure |
| [deep-dives/idempotency.md](deep-dives/idempotency.md) | Claim, replay, 409 contract |
| [deep-dives/fencing-tokens.md](deep-dives/fencing-tokens.md) | Lease reclaim safety |
| [deep-dives/intelligent-routing.md](deep-dives/intelligent-routing.md) | Weighted scoring, EMA health |
| [deep-dives/circuit-breakers.md](deep-dives/circuit-breakers.md) | Distributed breaker semantics |
| [deep-dives/audit-trail.md](deep-dives/audit-trail.md) | Event model, search, retention |
| [deep-dives/distributed-tracing.md](deep-dives/distributed-tracing.md) | Span hierarchy, OTLP config |
| [deep-dives/sentinel-failover.md](deep-dives/sentinel-failover.md) | Failover timeline, client behavior |
| [deep-dives/chaos-testing.md](deep-dives/chaos-testing.md) | Fault injection, expected behavior |

### Design decisions (ADRs)

| Document | Description |
|----------|-------------|
| [decisions/why-redis.md](decisions/why-redis.md) | Redis as coordination backbone |
| [decisions/why-lua.md](decisions/why-lua.md) | Atomic scripts vs WATCH/MULTI |
| [decisions/why-sidecar.md](decisions/why-sidecar.md) | Sidecar vs embedded limiter |
| [decisions/why-idempotency.md](decisions/why-idempotency.md) | Proxy-layer deduplication |
| [decisions/why-fencing-tokens.md](decisions/why-fencing-tokens.md) | Stale worker rejection |
| [decisions/why-weighted-routing.md](decisions/why-weighted-routing.md) | Score-based gateway selection |
| [decisions/why-sentinel.md](decisions/why-sentinel.md) | Sentinel vs Cluster |
| [decisions/why-otel.md](decisions/why-otel.md) | OpenTelemetry adoption |

### Failure modes

| Document | Description |
|----------|-------------|
| [failure-modes/redis-outage.md](failure-modes/redis-outage.md) | Fail-closed behavior, circuit, recovery |
| [failure-modes/sentinel-failover.md](failure-modes/sentinel-failover.md) | Promotion blip, split-brain risk |
| [failure-modes/duplicate-requests.md](failure-modes/duplicate-requests.md) | Retry storms, idempotency path |
| [failure-modes/lease-expiration.md](failure-modes/lease-expiration.md) | Lock reclaim, fencing |
| [failure-modes/gateway-timeout.md](failure-modes/gateway-timeout.md) | Upstream timeout, sidecar behavior |
| [failure-modes/routing-failures.md](failure-modes/routing-failures.md) | All gateways down, StateUnknown |
| [failure-modes/circuit-breaker-failures.md](failure-modes/circuit-breaker-failures.md) | Stuck open, false trips |
| [failure-modes/audit-failures.md](failure-modes/audit-failures.md) | Queue overflow, dropped events |

### Diagrams

| Resource | Description |
|----------|-------------|
| [diagrams/README.md](diagrams/README.md) | Index of all diagrams (render on GitHub) |
| [diagrams/request-flow.md](diagrams/request-flow.md) | Client → sidecar → limiter → upstream |
| [diagrams/sidecar-flow.md](diagrams/sidecar-flow.md) | Sidecar internal branches |
| [diagrams/routing-flow.md](diagrams/routing-flow.md) | Gateway scoring and failover |
| [diagrams/idempotency-flow.md](diagrams/idempotency-flow.md) | Idempotency state machine |
| [diagrams/fencing-flow.md](diagrams/fencing-flow.md) | Lease reclaim + stale rejection |
| [diagrams/circuit-breaker.md](diagrams/circuit-breaker.md) | Breaker state transitions |
| [diagrams/sentinel-failover.md](diagrams/sentinel-failover.md) | Sentinel quorum + rediscovery |
| [diagrams/audit-flow.md](diagrams/audit-flow.md) | Async append + indexes |
| [diagrams/tracing-flow.md](diagrams/tracing-flow.md) | OTel spans across services |
| [diagrams/redis-layout.md](diagrams/redis-layout.md) | Key namespace map |

### Interview preparation

| Document | Description |
|----------|-------------|
| [interviews/design-decisions.md](interviews/design-decisions.md) | Decision narrative for system design loops |
| [interviews/tradeoffs.md](interviews/tradeoffs.md) | Honest cost/benefit accounting |
| [interviews/common-questions.md](interviews/common-questions.md) | Q&A prep with codebase-backed answers |
| [interviews/system-design-discussion.md](interviews/system-design-discussion.md) | 45-minute walkthrough script |

### Benchmarks (repo root)

| Resource | Description |
|----------|-------------|
| [../benchmarks/summary.md](../benchmarks/summary.md) | Throughput, saturation, correctness results |
| [../benchmarks/methodology.md](../benchmarks/methodology.md) | Test design, k6 configuration |
| [../benchmarks/environment.md](../benchmarks/environment.md) | Hardware and Docker topology |
| [../benchmarks/idempotency/summary.md](../benchmarks/idempotency/summary.md) | Idempotency race and replay numbers |
| [../benchmarks/routing/summary.md](../benchmarks/routing/summary.md) | Routing benchmark results |
| [../benchmarks/circuitbreaker/summary.md](../benchmarks/circuitbreaker/summary.md) | Circuit breaker benchmark results |
| [../benchmarks/audit/summary.md](../benchmarks/audit/summary.md) | Audit trail benchmark results |
| [../benchmarks/sentinel/summary.md](../benchmarks/sentinel/summary.md) | Sentinel failover benchmark results |

---

## Future work

Things I know are missing or would build next:

| Item | Why |
|------|-----|
| **Redis Cluster / tenant sharding** | Single-master knee at ~1k RPS; noisy neighbor isolation |
| **gRPC limiter API** | Lower overhead than HTTP `/check` at extreme scale |
| **Adaptive denial cache TTL** | Scale TTL with denial rate to reduce Redis load further |
| **Sync audit option for compliance** | Trade latency for durability on select tenants |
| **Admin API RBAC** | Beyond API key. role-based override permissions |
| **Rate limit the admin API** | Prevent ops tooling from becoming abuse vector |
| **Formal SLO dashboards** | Wire benchmark thresholds to alerts (p99 > 100 ms, circuit open) |
| **Multi-region quota** | Regional Redis with approximate global caps |
| **Sidecar Redis readiness in `/health`** | Phase 3C: sidecar `/health` checks local Redis when idempotency/routing enabled |
| **Structured logging** | Migrate from stdlib `log` to `log/slog` with trace correlation (audit §8) |

---

## Lessons learned

1. Never cache allows at the edge: I tried it. One cached "allowed" entry bypassed limits until TTL expired. Denial-only cache is non-negotiable.

2. The GET/SET race is not theoretical: I saw over-admission in k6 at ~1,000 RPS. If you need correctness, pay for Lua.

3. Split data and control ports early: `:8080` for sidecars, `:8082` for admin. simplifies network policy and mTLS.

4. Fence tokens are worth the Redis fields: Without them, slow clients corrupt idempotency after lease reclaim.

5. Start strict, loosen deliberately: Fail-closed circuits, fatal Redis ping, loud `FAIL_OPEN` warnings. "available but wrong" is worse than 503.

6. Numbers without chaos tests are incomplete: Throughput benchmarks find the knee; chaos tests verify quota does not corrupt when Redis dies.

7. Algorithm choice is a product decision: Token bucket for smooth UX; sliding window for SLA language. I kept them as separate embeds.

8. Low-cardinality metrics is a feature: Per-user Prometheus labels would OOM under real traffic.

9. `StateUnknown` on gateway circuits means non-selectable: Do not route into a black hole because Redis read failed.

10. Document foot-guns loudly: Every fail-open flag logs a warning at startup because I know I will enable them to unblock a demo.

---

## Where to start

| If you want to… | Read |
|-----------------|------|
| Understand the whole system | [architecture/overview.md](architecture/overview.md) |
| Follow a request end-to-end | [architecture/request-flow.md](architecture/request-flow.md) |
| Prep for an interview | [interviews/system-design-discussion.md](interviews/system-design-discussion.md) |
| Debug Redis outage behavior | [failure-modes/redis-outage.md](failure-modes/redis-outage.md) |
| See performance limits | [../benchmarks/summary.md](../benchmarks/summary.md) |
| Understand a specific ADR | [decisions/](decisions/) |

---

*Last updated: June 2025. matches the codebase at this revision.*
