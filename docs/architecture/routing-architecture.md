# Routing Architecture

When I added payment-gateway simulation (`gateway-a`, `gateway-b`, `gateway-c` in `docker-compose.yml`), a static `UPSTREAM_URL` was no longer enough. Gateway C was slow and error-prone but could not simply be removed; it was the DR endpoint with lower weight. I built `internal/routing` so the sidecar **scores**, **samples**, and **failovers** across gateways using live metrics in Redis, while a distributed circuit breaker (`cb:{gatewayID}`) prevents hammering dead endpoints.

This is optional: `ENABLE_ROUTING=true` on the sidecar. Without it, `httputil.ReverseProxy` handles a single upstream.

---

## Problem Statement

Given multiple upstream gateways with different latency, error rates, and static weights, I need to:

1. Distribute traffic proportionally: To healthy capacity, not round-robin.
2. Detect degradation: Faster than DNS TTL or manual ops toggles.
3. Fail over: Within a single client request when the primary gateway errors.
4. Integrate with circuit breakers: So open circuits exclude gateways from selection.
5. Expose decision metadata: (`X-Gateway-ID`, score, failover flag) for support tickets.

---

## Why the problem exists

Weighted DNS does not see 5xx responses. Kubernetes Service endpoints spread load evenly. they ignore that gateway-b has 50 ms P99 while gateway-c throws 35% errors in my chaos tests.

Client-side retries multiply traffic to already-failing nodes unless something centralizes health signal. The sidecar already sits on the request path after rate limiting; routing is the next hop.

I store gateway state in Redis (not in sidecar memory) so **all sidecar replicas share the same health picture** updated by every replica's forward attempts and background probes.

---

## Design goals

| Goal | Implementation |
|------|----------------|
| Weighted load spread | `PickPrimary`. weighted random by score |
| Latency awareness | EMA latency in `route:gw:{id}` + scorer latency factor |
| Error awareness | Error rate penalty + circuit `record.lua` |
| Admin control | Enable/disable, weight CRUD on admin `:8082` |
| Passive + active health | Request outcomes + periodic `/health` probes |
| Bounded failover | `MaxFailoverTries` (default 3) |
| Safe unknown circuits | `StateUnknown` → not selectable |

---

## Alternative approaches considered

### Pure round-robin

Simple, ignores weight and latency. Bad for payment routing where DR gateway should get 10% traffic, not 33%.

### Pick lowest latency only (always best)

Starves exploration. a recovering gateway never receives traffic to prove health. Weighted random preserves minimum share for low-weight nodes.

### Sidecar-local health cache only

Each pod would diverge. Redis-backed `route:gw:*` gives fleet-consistent scores within Redis RTT.

### Embed routing in Envoy

Viable at scale; my Go router keeps custom headers, integrates with existing `circuitbreaker` package, and reuses the same Redis cluster as idempotency.

### Separate health service polling gateways

I folded passive health into `record_outcome.lua` and active health into `StartHealthProbes`. fewer moving parts, at cost of probe traffic every `ROUTING_PROBE_INTERVAL_SEC` (default 15 s).

---

## Final architecture

### Components

```
┌──────────────┐     ListGateways      ┌─────────────────┐
│   Router     │◀──────────────────────│  RedisStore     │
│  selector    │                       │ route:gw:*      │
│  Forward()   │──── RecordOutcome ───▶│ route:index     │
└──────┬───────┘                       └────────┬────────┘
       │                                        │
       │ Allow/Record                           │ enrichCircuit
       ▼                                        ▼
┌──────────────┐                       ┌─────────────────┐
│   Breaker    │◀──── cb:{gwID} ──────│ circuitbreaker  │
└──────────────┘                       └─────────────────┘
```

**Router** (`router.go`):

- `Seed`. register gateways from `GATEWAYS` env at startup
- `StartHealthProbes`. background ticker hits `{gatewayURL}/health`
- `Forward`. select, try, failover loop, copy response

**Selector** (`selector.go`):

- `PickPrimary`. weighted random among score > 0
- `FailoverOrder`. descending score, exclude failed primary, cap tries

**Scorer** (`scorer.go`):

- `ComputeScore`. multiplicative formula
- `RankScores`. insertion sort (N ≤ ~10 gateways)

**RedisStore** (`store.go`):

- `RegisterGateway`, `ListGateways`, `RecordOutcome`, `UpdateHealthProbe`
- `enrichCircuit`. attaches `CircuitState` from `cb:{id}`

### Gateway registration

Env format:

```
GATEWAYS=gateway-a|http://gateway-a:8081|100,gateway-b|http://gateway-b:8081|80,gateway-c|http://gateway-c:8081|60
```

On first register, Redis HASH defaults:

- `enabled=1`
- `health_score=100`
- counters zeroed
- `route:index` SET receives gateway ID

### Scorer formula

For each `GatewayState` where `Selectable(cfg)` is true:

```
weight = max(state.Weight, 1)   // default weight 100 in store if unset

latency = max(state.LatencyEMAMs, 1)
latencyFactor = TargetLatencyMs / latency
latencyFactor = clamp(latencyFactor, 0.1, 2.0)

healthFactor = max(state.HealthScore / 100, 0)

errRate = ErrorCount / (SuccessCount + ErrorCount)   // 0 if no samples
errorFactor = 1 - (errRate * ErrorPenalty)           // ErrorPenalty default 2.0
errorFactor = max(errorFactor, 0.05)

score = weight * latencyFactor * healthFactor * errorFactor
```

**Interpretation I use in ops:**

- High weight → more traffic share.
- Latency above target → score drops (factor < 1); very fast gateways capped at 2× boost.
- Low `health_score` from `record_outcome.lua` drags traffic away.
- High error rate with penalty 2.0 can crush score toward 5% floor (`errorFactor` min).

`Selectable` returns false when:

- `enabled == false`
- `CircuitState == open` or `unknown`
- `HealthScore < MinHealthScore` (default 20)

### Weighted pick algorithm

`PickPrimary`:

1. `RankScores` descending
2. Filter `score > 0`
3. `roll = rng.Float64() * totalScore`
4. Cumulative sum until `roll <= acc`. classic roulette wheel

This is **probabilistic**, not strict proportion. variance is acceptable for my throughput targets. Over thousands of requests, empirical share converges to score ratios.

### Failover loop

`Forward` builds try list:

1. `primary` from `PickPrimary`
2. Append `FailoverOrder(states, primary.ID)`. next best scores, max `MaxFailoverTries`

For each candidate:

1. `breaker.Allow(gatewayID)`. skip if open or error
2. `execute`. HTTP with original method, path, query, headers, body
3. Classify success: `err == nil && status < 500`
4. `store.RecordOutcome`. updates `route:gw:*` + breaker `Record`
5. On success → copy response, set headers, return
6. On failure → continue; set `X-Gateway-Failover: true` on eventual success

Metrics: `RecordRoutingDecision`, `RecordRoutingFailover`, per-gateway latency histograms.

### Health score in Lua (`record_outcome.lua`)

After each outcome:

1. Update latency EMA: `ema = alpha * latency + (1-alpha) * ema`
2. Increment success or error count; halve at 1000 total
3. `error_rate = err / (succ + err)`
4. `latency_penalty = min(ema / 200, 1.0)`
5. `health = (1 - error_rate) * (1 - latency_penalty * 0.3) * 100`

Passive probes (`UpdateHealthProbe`) call the same `RecordOutcome` path. failed `/health` counts as error.

### Circuit integration and unknown state

`enrichCircuit` in `store.go`:

```go
snap, err := s.breaker.GetState(ctx, st.ID)
if err != nil {
    st.CircuitState = circuitbreaker.StateUnknown
    return
}
st.CircuitState = snap.State
```

`GatewayState.Selectable` treats **`StateUnknown` like open**. non-selectable.

**Why:** If I cannot read `cb:{id}`, I refuse to guess. Sending traffic to a gateway that might be circuit-open wastes failover budget and poisons metrics.

On the limiter, Redis circuit errors with fail-closed behave similarly. different service, same philosophy.

Circuit states per gateway:

| State | Routing behavior |
|-------|------------------|
| `closed` | Normal selection |
| `half_open` | `allow.lua` permits limited probes. may appear in selection |
| `open` | Excluded from `Selectable` |
| `unknown` | Excluded. Redis read failure on enrich |

Separate from routing health score, `record.lua` tracks failure rate, consecutive failures, timeout rate, latency spikes. can open circuit independently of health_score formula.

### Response headers

| Header | Meaning |
|--------|---------|
| `X-Gateway-ID` | Winning gateway |
| `X-Gateway-Score` | Score at selection time (%.2f) |
| `X-Gateway-Failover` | `true` if not first candidate |

OpenTelemetry span attributes mirror these on `sidecar.intelligent_route`.

---

## Tradeoffs

**Probabilistic routing ≠ strict SLA per gateway.** A gateway with 5% score still occasionally wins. intentional for recovery probes.

**Double accounting on probes + real traffic**. health probes consume gateway CPU and update same counters. I accept bias toward steady-state probe interval vs bursty user traffic.

**Failover serializes attempts**. latency on failure = sum of failed gateway RTTs. `MaxFailoverTries=3` caps worst case.

**Score computed at pick time, not re-checked on failover**. failover list is score-ordered snapshot; circuit may change between attempts. `Allow` catches that.

**Insertion sort**. O(n²) but n ≈ 3 to 10; simpler than heap for tiny sets.

---

## Failure modes

| Scenario | Outcome |
|----------|---------|
| No gateways in `route:index` | `Forward` error: no gateways configured |
| All scores zero | `no healthy gateways available` |
| All circuits open/unknown | Same. empty candidate set after filter |
| Primary 503, secondary OK | Failover success, `X-Gateway-Failover: true` |
| All attempts fail | 503 `all gateways unavailable` to client |
| Redis down during ListGateways | Forward fails immediately |
| Stale gateway URL in Redis | Errors until admin updates or re-seed |
| gateway-c high error rate | Score drops; circuit may open after `CB_MIN_SAMPLES` |

Chaos script `chaos/chaos_test.ps1` and `benchmarks/routing/` document observed failover under simulated degradation.

---

## Operational concerns

- Seed on startup: `router.Seed` upserts URL/weight. does not delete removed gateways from index; ops must disable via admin API.
- Admin routes: On limiter `:8082`: enable/disable, weight, circuit reset per gateway.
- Env tuning:

| Variable | Default | Role |
|----------|---------|------|
| `ROUTING_TARGET_LATENCY_MS` | 100 | Scorer target |
| `ROUTING_EMA_ALPHA` | 0.2 | Latency smoothing |
| `ROUTING_MIN_HEALTH_SCORE` | 20 | Cutoff |
| `ROUTING_MAX_FAILOVER_TRIES` | 3 | Failover cap |
| `ROUTING_PROBE_INTERVAL_SEC` | 15 | Active health |

- Allowed; static upstream unused.
- Idempotency + routing: `forwardIdempotent` uses same `Router.Forward` with captured response.

Reset playbook: admin `ResetCircuit(gatewayID)` + verify `health_score` recovers on successful probes.

---

## Performance implications

- ListGateways: O(n) HGETALL per gateway ID in SET. n small.
- Per forward: 1 list + 1 allow Lua + 1 HTTP + 1 record outcome Lua + 1 circuit record Lua per attempt.
- Probes: `n_gateways / probe_interval` background GETs. negligible at 3 gateways / 15 s.
- Weighted random: O(n) per request. dominates neither HTTP nor Redis.

Benchmark summary in `benchmarks/routing/summary.md`. failover adds RTT linear in failed attempts.

---

## Lessons learned

1. `StateUnknown` must block selection: Optimistic routing on Redis errors sent 40% of traffic to a gateway whose circuit state I could not read during a Sentinel blip.

2. Health drags weight gradually; circuit hard-stops after threshold. Using only one left blind spots.

3. Weighted pick needs a floor on errorFactor: Without `0.05` minimum, one bad gateway never receives probe traffic to recover.

4. Failover order by score, not registration order: First implementation tried SET iteration order; DR gateway never got second-chance tries.

5. Record outcome even on failure: Skipping metric update on 503 left health_score stale at 100.

6. Central-limiter circuit on sidecar: Routing enabled still rate-limits first; gateway circuits are a second layer. Do not conflate `cb:central-limiter` with `cb:gateway-a`.

---

## Related documents

- [request-flow.md](./request-flow.md). routing after rate limit allow
- [sidecar-architecture.md](./sidecar-architecture.md). sidecar integration
- [redis-design.md](./redis-design.md). `route:gw:*` and `cb:*` keys
- Diagram: [../diagrams/routing-flow.mmd](../diagrams/routing-flow.mmd)
