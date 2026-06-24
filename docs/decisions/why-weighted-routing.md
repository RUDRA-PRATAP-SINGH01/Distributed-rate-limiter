# Why Weighted Gateway Routing

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** `internal/routing`, dynamic scoring, failover, circuit-aware selection

---

## Problem Statement

Payment traffic often fans out to multiple processor gateways with different capacity deals, latency profiles, and error rates. Round-robin ignores capacity contracts; pure lowest-latency ignores configured weights. I needed **weighted random selection** biased by live health, with automatic failover when a gateway degrades.

## Why the problem exists

A static `UPSTREAM_URL` sidecar cannot shift load when `gateway-b` is timing out while `gateway-a` is healthy. Operations teams negotiate weights (70% primary, 30% backup) but weights alone send traffic to dead backends. The router must combine **static weight** with **dynamic health_score**, EMA latency, and circuit state.

## Design goals

- Env-declared gateways: `GATEWAYS=gateway-a|http://gateway-a:8081|100,gateway-b|...|50` parsed by `routing.ParseGatewaysEnv`.
- Weighted primary pick: `Selector.PickPrimary`. roll against sum of scores from `ComputeScore`.
- Score-based failover: `FailoverOrder` tries next highest scores up to `ROUTING_MAX_FAILOVER_TRIES` (default 3).
- Circuit-aware: `GatewayState.Selectable` excludes `StateOpen` and `StateUnknown`.
- Observable decisions: Headers `X-Gateway-ID`, `X-Gateway-Score`, `X-Gateway-Failover`; metrics `routing_decisions_total`, `routing_failovers_total`.
- Background probes: `ROUTING_PROBE_INTERVAL_SEC` (default 15) hits `{gateway}/health`.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **DNS round-robin** | No latency or error awareness. |
| **Pure least-connections** | Hard to implement without connection pools per gateway at sidecar. |
| **Sticky sessions by user** | Bad for failover. user stuck on failed gateway. |
| **External service mesh routing** | Heavier than Redis-backed scores I already had. |
| **Fixed priority list only** | Wastes capacity on secondary when primary is merely slow, not down. |

## Final architecture

Score formula in `internal/routing/scorer.go`:

```
score = weight × latencyFactor × healthFactor × errorFactor
```

Where:

- `latencyFactor = clamp(ROUTING_TARGET_LATENCY_MS / latency_ema_ms, 0.1, 2.0)`
- `healthFactor = health_score / 100`
- `errorFactor = 1 - (error_rate × ROUTING_ERROR_PENALTY)` (min 0.05)
- `Selectable` false if disabled, circuit open/unknown, or `health_score < ROUTING_MIN_HEALTH_SCORE`

`routing.Router.Forward` flow:

1. `ListGateways` from Redis (`route:gw:{id}` hashes).
2. `PickPrimary` → `metrics.RecordRoutingDecision(gateway, false)`.
3. On failure, failover candidates → `metrics.RecordRoutingFailover`.
4. Per attempt: `breaker.Allow(gateway-id)` → HTTP execute → `RecordOutcome` (Lua EMA update + circuit `Record`).

Enable: `ENABLE_ROUTING=true`, `REDIS_ADDR`, `GATEWAYS=...` on sidecar (`cmd/sidecar/main.go`).

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| Weighted random | Spreads load proportionally | Non-deterministic per request. harder to reproduce |
| Dynamic score multipliers | Adapts to latency/errors | Tuning `ROUTING_TARGET_LATENCY_MS`, `ROUTING_EMA_ALPHA` required |
| Redis-persisted metrics | Shared view across sidecars | Extra Lua on every outcome |
| Max failover tries cap | Bounds tail latency | May exhaust before healthy gateway tried |

## Failure modes

- All scores zero: `no healthy gateways available`. 503 `all gateways unavailable`.
- `StateUnknown` on circuit read error: Gateway excluded. safer than routing to unknown breaker state (`routing/store.go` `enrichCircuit`).
- Stale health_score: Probes and outcomes eventually correct; sudden drain needs `SetEnabled` admin API.
- Redis list empty: `no gateways configured` if seed failed.

## Operational concerns

- Admin: `GET /admin/routing/gateways` on `:8082` with `ADMIN_API_KEY`.
- Tune weights live via `SetWeight` without redeploying sidecars (Redis hash field `weight`).
- Monitor `routing_gateway_health_score`, `routing_circuit_open`, `circuit_breaker_state{target="gateway-*"}`.
- Docker Compose runs `gateway-a/b/c` simulators with routing enabled.

## Performance implications

Each routed request: 1× `ListGateways` (N HGETALL), 1× circuit Allow per attempt, 1× upstream HTTP, 1× outcome Lua. Failover multiplies upstream attempts. cap `ROUTING_MAX_FAILOVER_TRIES` to control tail latency. Weighted random is O(n) over small n (typically 3 to 10 gateways). negligible vs network.

## Lessons learned

I first shipped strict priority failover and watched the secondary sit idle while primary degraded slowly (high latency, not hard errors). Combining **weight × health** let traffic drift naturally before circuits opened. During k6 (`benchmarks/routing/routing-test.js`), failover headers (`X-Gateway-Failover: true`) correlated with `routing_failovers_total`. validation I still use in demos. Weighted routing is the right default when gateways are **commercially weighted**, not just redundant clones.

**References:** `internal/routing/router.go`, `internal/routing/scorer.go`, `internal/routing/selector.go`, `docs/diagrams/routing-flow.mmd`
