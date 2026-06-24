# Intelligent Routing — Engineering Journal

## Problem Statement

The sidecar fronts multiple upstream API gateways (regions, versions, canary pools). Static round-robin ignores health, latency, and error rate. I needed **weighted intelligent routing**: prefer fast healthy gateways, automatically failover when the primary fails, and learn from recent outcomes without a separate control plane.

## Why the problem exists

Multi-gateway deployments happen for:

- **Blue/green and canary** — shift traffic gradually.
- **Regional failover** — secondary region absorbs load when primary degrades.
- **Capacity bursting** — overflow pool handles spikes.

A dumb proxy sends traffic to dying gateways until operators manually drain them. Health probes alone are insufficient — a gateway can pass `/health` while returning 500s on business routes or running at 2s latency. I needed **outcome-aware scoring** stored in Redis so all sidecars share the same view.

## Design goals

1. **Score-based primary selection** — weighted random among gateways with score > 0.
2. **Deterministic failover ordering** — try next-best by score, capped by `MaxFailoverTries`.
3. **Background health probes** — `StartHealthProbes` hits `gateway.URL/health` on interval.
4. **Outcome recording** — latency and success after each forward update EMA/error rate in Redis.
5. **Circuit breaker integration** — skip gateways with open circuits (`internal/circuitbreaker`).
6. **Trace propagation** — span `sidecar.intelligent_route` in `router.go`.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| DNS-only failover | Minutes TTL; no latency awareness |
| Service mesh (Istio weights) | Heavier ops; less custom scoring |
| Pure round-robin | Ignores degradation |
| Central routing service | SPOF; extra hop |
| Client-side gateway pick | Leaks infra topology |

Embedded router in sidecar + Redis shared state matched our deployment model.

## Final architecture

**Components** (`internal/routing/`):

| File | Role |
|------|------|
| `router.go` | Forward, probes, execute, copy response |
| `scorer.go` | `ComputeScore`, `RankScores` |
| `selector.go` | `PickPrimary`, `FailoverOrder` |
| `store.go` | Redis gateway registry + outcome Lua |
| `config.go` | Weights, targets, probe interval |
| `types.go` | `GatewayState`, `ScoredGateway` |

**Score formula** (`scorer.go`):

```
score = weight × latencyFactor × healthFactor × errorFactor

latencyFactor = clamp(TargetLatencyMs / LatencyEMAMs, 0.1, 2.0)
healthFactor  = HealthScore / 100
errorFactor   = 1 - (errorRate × ErrorPenalty), min 0.05
```

`GatewayState.Selectable(cfg)` gates hard exclusions (disabled, probe failures).

**Forward flow** (`router.go`):

1. `ListGateways` from Redis
2. `selector.PickPrimary` — weighted random among candidates
3. Build try list: primary + `FailoverOrder` (score descending, exclude primary)
4. For each candidate: circuit `Allow` → `execute` → `RecordOutcome`
5. On success: set `X-Gateway-ID`, `X-Gateway-Score`, `X-Gateway-Failover` headers
6. On exhaustion: return last error

**Probes** — `probeAll` updates health independently of user traffic — catches idle gateway failures.

**Lua** — `lua/record_outcome.lua` atomically updates rolling stats used by scorer on next read.

## Tradeoffs

- **Weighted random vs best-of** — random avoids thundering herd on single "best" node; adds variance in A/B comparisons.
- **Insertion sort** in `RankScores` — O(n²) but n is 3–10 gateways; simplicity over heap.
- **Success = status < 500** — 4xx counts as success for circuit/routing (client fault); may keep routing to gateway that returns 404 for all — acceptable.
- **Shared Redis state** — stale scores if outcome recording fails silently (`_ = RecordOutcome`); metrics gap.
- **5s default HTTP client timeout** — `NewRouter` default; long uploads need config bump.

## Failure modes

1. **No selectable gateways** — `no healthy gateways available` error.
2. **All circuits open** — failover loop exhausts; combined routing + CB failure.
3. **Split brain scores** — two sidecars record outcomes concurrently — Lua EMA update is atomic per gateway key.
4. **Probe false positive** — `/health` OK but API broken — outcome recording eventually lowers score.
5. **Failover storm** — primary flaps; `RecordRoutingFailover` metrics spike; need CB dampening.

## Operational concerns

- Seed gateways on startup: `Router.Seed(ctx, gateways)`.
- Tune `TargetLatencyMs`, `ErrorPenalty`, `ProbeIntervalSec` in `routing/config.go`.
- Dashboard: `routing_decision_total`, `routing_failover_total` from `metrics` package.
- Run `benchmarks/routing/routing-test.js`; see `benchmarks/routing/summary.md`.
- Response headers expose chosen gateway for support correlation.

## Performance implications

Forward path adds: list gateways (1 Redis) + per-try circuit allow (1 Redis each) + outcome record (1 Redis) + HTTP upstream.

Worst case tries = 1 + `MaxFailoverTries` — multiply Redis ops. Under healthy primary, 1 try dominates.

Routing benchmarks should be compared against direct proxy baseline to quantify overhead.

## Lessons learned

Weighted random was a product decision — SRE wanted smooth load spread; pure lowest-latency caused hotspot on one node after deploy.

Combining **proactive probes** and **reactive outcomes** caught failures faster than either alone. Probes find idle breakage; outcomes find brownouts.

Integrating circuit breaker **before** execute, not after, saved wasted upstream calls — see `circuit-breakers.md`.

Failover headers (`X-Gateway-Failover: true`) paid for themselves in incident triage — I can grep logs for degraded-path traffic.

Keep scoring transparent — `X-Gateway-Score` makes "why this gateway?" answerable without Redis CLI.
