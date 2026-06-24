# Common Interview Questions. Interview Guide

This is my Q&A prep for system design and backend interviews about this project. I wrote answers I would actually give in a 45-minute loop. concise opening, depth on follow-ups, and honest limits where I would not overclaim.

---

## Problem Statement

Interviewers ask predictable questions about distributed rate limiters: algorithms, Redis, consistency, failure modes, scaling. I organized the questions I have been asked (or expect) and mapped each answer to what I actually built, not textbook theory disconnected from this codebase.

---

## Why the problem exists

"Design a rate limiter" is a proxy for testing whether I understand:

- Shared mutable state under concurrency
- CAP-adjacent reasoning without buzzwords
- Operational semantics (429 vs 503, idempotency, observability)
- The gap between "works on one machine" and "works under abuse"

I use this project as my anchor because I have benchmarks, chaos tests, and documented failure modes, not just a whiteboard diagram.

---

## Design goals

My answers consistently emphasize:

1. Correctness over availability for quota enforcement
2. One Lua script per check
3. Clear HTTP semantics: 429 quota, 503 infrastructure
4. Operability: Runtime overrides, audit, metrics, traces
5. Measurable claims: I cite ~1,000 RPS sustainable, not "handles millions"

---

## Alternative approaches considered

When asked "why not X?", I have a prepared rejection for each common alternative:

| Interviewer suggests | My answer |
|---------------------|-----------|
| In-memory per instance | Limits multiply by replica count; races on concurrent requests |
| Sticky sessions | Uneven load; fragile on pod churn |
| Database counters | Too slow; lock contention at hot keys |
| Fixed window `INCR` | Burst at window boundaries; no smooth refill |
| API gateway only | No hierarchical quotas + idempotency + routing in one flow |
| Redis without Lua | I saw over-admission in load tests with GET/SET |
| Kafka for rate events | Async; cannot block request path in <5 ms |

---

## Final architecture

### Quick elevator pitch (30 seconds)

> I built a Go rate limiting platform with a central limiter service and sidecar proxies. Sidecars call the limiter over HTTP; the limiter executes atomic Lua scripts in Redis for token bucket, sliding window, and four-level hierarchical quotas. The sidecar also handles idempotent POST dedup, weighted gateway routing, and denial-only caching. Redis outage fails closed with 503. I have k6 benchmarks and chaos tests proving saturation and recovery behavior.

### Architecture one-liner for whiteboard

```
Client → Sidecar → Limiter → Redis (Lua) → allow/deny
              ↓
         Backend (if allowed)
```

---

## Tradeoffs

I lead with tradeoffs when answers get long. interviewers reward self-awareness.

**Short form:** "I paid a network hop and Redis dependency for correctness and a single enforcement implementation."

**If they push on availability:** "I can enable fail-open, but that removes protection. I default fail-closed and use Sentinel for HA."

**If they push on latency:** "Denial cache and singleflight amortize the hop. At ~1,000 RPS I measure p99 under 100 ms."

---

## Failure modes

### Q: What happens when Redis dies?

**A:** Limiter circuit `cb:redis` opens after consecutive failures. `/check` returns 503 before touching Redis. Sidecar returns 503 to clients unless `FAIL_OPEN=true`. On restart, limiter fatals if Redis is unreachable; it refuses to boot as a false healthy service. Chaos test `chaos/chaos_test.ps1` kills the Redis container and verifies 503 + recovery without quota corruption.

### Q: What happens when the limiter dies but sidecar is up?

**A:** Sidecar health check fails; client gets 503. With `FAIL_OPEN=true`, sidecar forwards without checking. documented foot-gun.

### Q: Can two sidecars both allow when one token remains?

**A:** No, if they go through the limiter → Lua path. The script reads, refills, and deducts atomically. I proved the opposite with non-atomic GET/SET. that path over-admits under load.

### Q: What about duplicate POST retries?

**A:** Idempotency layer in sidecar. `Idempotency-Key` header → Redis claim via Lua. First request executes upstream; concurrent duplicates get 409 in-progress; completed key replays cached response. Fence token prevents stale worker from overwriting after lease reclaim.

### Q: What if audit drops events?

**A:** Queue full → event dropped, `audit_dropped` increments. Enforcement continues. I chose not to slow `/check` for audit durability.

---

## Operational concerns

### Q: How do you change limits without redeploying?

**A:** Admin API on `:8082` writes `config:{level}:{id}` in Redis. Limiter reads overrides with 5s read-through cache. Levels: global, tenant, user, endpoint.

### Q: How do you know who got rate limited?

**A:** Async audit trail indexed by tenant, user, time. Search via admin API. Prometheus counters by handler and allowed/denied. no per-user labels.

### Q: How do you secure the admin API?

**A:** `ADMIN_API_KEY` header, separate port, intended for internal network only. I document firewall/private subnet requirement; full RBAC is out of scope.

### Q: How do you trace a slow request?

**A:** OpenTelemetry spans: `sidecar.rate_limit_check` → `limiter.check` → `redis.eval`. W3C `traceparent` propagates sidecar to limiter. Jaeger UI on `:16686`.

---

## Performance implications

### Q: How much traffic can it handle?

**A:** On my machine (i9-14900HX, 32GB), ~**1,000 actual RPS** sustained with p99 < 100 ms and 0% errors. At 5,000 target RPS, actual plateaus at ~1,353 with p99 3.5s and 10% errors. Redis single-threaded execution is the bottleneck. I would shard by tenant or use Redis Cluster before claiming 10k+ RPS.

### Q: Token bucket vs sliding window. when to use which?

**A:** Token bucket: smooth refill, burst-friendly, good UX. Sliding window: hard "N per minute" SLA language. I implement both as separate Lua embeds. Hierarchical uses token bucket at four levels in one script.

### Q: What is the hot-key problem?

**A:** All requests for one user hit one Redis key → one shard/thread. Under abuse, latency rises but correctness holds (my hot-key test: 99.9% rejection at 5,000 target RPS). Mitigation would be local leaky-bucket approximation per sidecar with periodic sync. I have not built that.

### Q: How does idempotency perform?

**A:** Race test: 100 VUs, one key → 1 upstream execution, p95 claim 14.9 ms. Replay test: ~942 RPS, p95 5.7 ms, 0% errors, no upstream calls.

---

## Lessons learned

### Q: What would you do differently?

**A:** Three things: (1) Redis Cluster or key sharding earlier if I needed >1k RPS per tenant, (2) optional sync audit for compliance tenants willing to pay latency, (3) formal SLO dashboards wired to benchmark thresholds so ops gets alerted before p99 hits 3.5s.

### Q: What are you most proud of?

**A:** The denial-only cache lesson. I built the wrong thing, load-tested it, found the bypass, and fixed it. That iteration story is more credible than getting it right the first time.

### Q: How do you test correctness?

**A:** Unit tests on algorithms, k6 enforcement test (500/min → 98% rejected), idempotency race (100 VUs → 1 execution), chaos tests (Redis kill → 503, no corruption). Numbers in `benchmarks/summary.md`.

### Q: Explain hierarchical rate limiting.

**A:** Four stacked buckets: global, tenant, user, endpoint. All four must have tokens; decrement happens only if all pass. in one Lua script so partial commits are impossible. Endpoint-level caps protect expensive routes like `/export`.

### Q: What is a fencing token?

**A:** Monotonic counter on idempotency lock. When lease expires and another worker claims, the stale worker's complete call fails because its fence is lower than current. Prevents slow client from overwriting fresh response.

---

## Question index (quick lookup)

| Topic | Section |
|-------|---------|
| Redis down | §7 Failure modes |
| Algorithm choice | §9 Performance |
| Scaling limits | §9 Performance |
| Idempotency | §7, §10 |
| Overrides / ops | §8 Operational |
| Why sidecar | §4 Alternatives, §5 Architecture |
| Tradeoffs | §6 |
| What I'd change | §10 Lessons |

---

## Related documents

- [System design walkthrough](./system-design-discussion.md)
- [Design decisions](./design-decisions.md)
- [Tradeoffs](./tradeoffs.md)
- [Request flow](../architecture/request-flow.md)
- [Deep dive: distributed rate limiting](../deep-dives/distributed-rate-limiting.md)
