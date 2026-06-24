# Failure Mode: Circuit Breaker Failures

**Status:** Documented  
**Severity:** High (cascading denials)  
**Components:** `internal/circuitbreaker`, targets `redis`, `central-limiter`, `gateway-{id}`

---

## Problem Statement

Circuit breakers exist to stop hammering failed dependencies. When breakers mis-trip, stick open, or disagree across targets, the system can deny healthy traffic (false open) or allow unhealthy traffic (`CIRCUIT_FAIL_OPEN`). I needed distributed state in Redis with explicit half-open recovery and per-target isolation.

## Why the problem exists

Without breakers, a dying Redis or gateway absorbs unbounded retry traffic from every sidecar and limiter pod. a classic cascading failure. With breakers, **state must be shared** (`cb:{target}` hashes) so one pod's observations protect the fleet. Misconfiguration of thresholds causes false positives.

## Design goals

- Three states: `closed`, `open`, `half_open` (+ `unknown` when Redis read fails).
- Trip on failure rate, consecutive failures, latency spike, timeout rate (`allow.lua`, `record.lua`).
- Half-open probe budget: `CB_HALF_OPEN_MAX_PROBES`, `CB_HALF_OPEN_SUCCESS_REQUIRED`.
- Targets: `redis`, `central-limiter`, per-gateway IDs from routing.
- Fail-closed on Allow errors unless `CIRCUIT_FAIL_OPEN=true`.
- Admin reset: `DELETE /admin/circuit/{target}`.

## Alternative approaches considered

| Alternative | Gap |
|-------------|-----|
| In-process breaker per pod | No fleet-wide coordination |
| Retry without breaker | Cascading overload |
| Always fail-open on Redis errors | Hides dependency death |

## Final architecture

**Integration points:**

| Component | Target | When |
|-----------|--------|------|
| Limiter `/check` | `redis` | `checkRedisCircuit` before Lua |
| Sidecar `checkRateLimit` | `central-limiter` | Before limiter HTTP |
| `routing.Router.Forward` | `gateway-{id}` | Before each upstream attempt |

**Sidecar limiter circuit** (`checkRateLimit`):

```go
allow, err := s.limiterCircuit.Allow(ctx, circuitbreaker.TargetCentralLimiter)
if err != nil {
    if !s.limiterCircuit.Config().FailOpen { return error }
} else if !allow.Allowed {
    return error // circuit open or half_open exhausted
}
```

**Limiter redis circuit** (`cmd/limiter/circuit.go`): symmetric with `TargetRedis`.

**Routing enrichment:** `GetState` error → `StateUnknown` → gateway not selectable.

**Metrics:**

- `circuit_breaker_state{target}`. 0/1/2
- `circuit_breaker_rejections_total{target,state}`
- `circuit_breaker_transitions_total{target,from,to}`
- `circuit_breaker_outcomes_total{target,outcome}`

Env: `ENABLE_CIRCUIT_BREAKER` (sidecar, default true when idempotency on), `CB_*` thresholds, `CIRCUIT_FAIL_OPEN`.

## Tradeoffs

| Fail-closed (default) | CIRCUIT_FAIL_OPEN=true |
|-----------------------|------------------------|
| Outage stops traffic | Traffic continues during Redis CB read failures |
| Clear 503 semantics | Dangerous in production |

Half-open probes trade **gradual recovery** vs **brief risk** on still-sick dependency.

## Failure modes

| Symptom | Cause | Fix |
|---------|-------|-----|
| All traffic 503, `circuit_state: open` on redis | Redis down or slow | Restore Redis; admin reset |
| Sidecar 503, limiter healthy | `central-limiter` circuit open | Check limiter latency/errors; tune thresholds |
| Gateway never receives traffic | `StateOpen` or `StateUnknown` | Reset circuit; fix Redis reads |
| Flapping open/closed | Cooldown too short | Raise `CB_OPEN_COOLDOWN_MS` |
| Breaker bypass during Redis errors | `CIRCUIT_FAIL_OPEN=true` | Disable in prod |
| Half-open probe storm | Many pods probe simultaneously | Expected brief window. monitor transitions |

## Operational concerns

```bash
# List all circuits
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit

# Force close gateway-c after recovery
curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/gateway-c
```

Page on `circuit_breaker_rejections_total` rate increase correlated with dependency outages.

Chaos: `benchmarks/circuitbreaker/circuit-test.js`, `chaos/network_partition.py` (sidecar-limiter partition trips `central-limiter`).

## Performance implications

Every protected call adds 1× `allow.lua` RTT before work + 1× `record.lua` after. Under closed state this is cheap; under open state Allow fast-fails without upstream call. **shedding load** is the performance win. Redis-backed breakers add contended keys on hot targets. `cb:redis` is global; gateway targets shard naturally.

## Lessons learned

I conflated limiter HTTP errors with redis circuit early on. two targets matter. Separating `TargetRedis` vs `TargetCentralLimiter` let me partition a network split (`chaos/network_partition.py`) and see exactly which breaker opened. `StateUnknown` for routing was added after a bad deploy where GetState failed and traffic hit open circuits anyway. Breakers protect the system from dependencies. **mis-tuned breakers protect dependencies from the system** (wrongly). Tune with real `circuit_breaker_failure_rate` graphs, not defaults.

**References:** `internal/circuitbreaker/`, `cmd/limiter/circuit.go`, `docs/diagrams/circuit-breaker.md`, `docs/decisions/why-lua.md`
