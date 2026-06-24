# Intelligent Routing Benchmarks

## Problem Statement

My sidecar does more than rate-limit; it also routes across **payment gateway-style flaky upstreams**. Static round-robin would keep sending traffic to gateway-c (35% errors, 120 ms latency) and both p99 and chargebacks would suffer. I needed to benchmark the weighted scoring model: latency EMA, error rate, health score, and circuit breaker interaction. `routing-test.js` should prove that gateway-a takes the majority of traffic and gateway-c traffic drops when it degrades.

## Why the problem exists

Multi-gateway setups have specific pain points:

- **Latency variance**. A 15 ms gateway and a 120 ms gateway at the same weight create an unfair load split.
- **Error correlation**. A high-error gateway can trip the circuit and zero its score, but slow-burn 5% errors need a gradual shift.
- **Failover semantics**. Primary 5xx triggers a secondary attempt. Clients need `X-Routed-Via` and failover headers for debugging.
- **Observability gap**. Without `routing_decisions_total` metrics, routing stays a black box.

## Design goals

1. **Score-based selection**. `weight × latency_factor × health_factor × error_factor`.
2. **Circuit integration**. At `ROUTING_CIRCUIT_ERROR_RATE=0.5`, bad gateways fast-fail.
3. **Live admin visibility**. `GET /admin/routing/gateways` exposes scores.
4. **k6 reproducibility**. `routing/routing-test.js` with simulated gateways.
5. **Probe recovery**. `ROUTING_PROBE_INTERVAL_SEC=15` for half-open style probes.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Pure round-robin | Ignores latency and errors |
| Sticky sessions | Uneven load on gateway failure |
| External service mesh (Istio) | Heavier ops; sidecar-local routing sufficient |
| DNS-weighted only | No real-time error feedback |
| Client-side gateway pick | Duplicates logic; violates sidecar pattern |

Weighted score with Redis-backed state (`internal/routing/router.go`) won. Aligns with the circuit breaker shared store.

## Final architecture

**Simulated gateways** (`docker-compose.yml`):

| Gateway | Latency | Error Rate | Weight |
|---------|---------|------------|--------|
| gateway-a | 15 ms | 1% | 100 |
| gateway-b | 50 ms | 5% | 80 |
| gateway-c | 120 ms | 35% | 60 |

**Sidecar env:**

```
ENABLE_ROUTING=true
GATEWAYS=gateway-a|http://gateway-a:8081|100,gateway-b|http://gateway-b:8081|80,gateway-c|http://gateway-c:8081|60
ROUTING_TARGET_LATENCY_MS=100
ROUTING_CIRCUIT_ERROR_RATE=0.5
ROUTING_CIRCUIT_MIN_SAMPLES=10
ROUTING_PROBE_INTERVAL_SEC=15
```

**Scoring model** (`benchmarks/routing/summary.md`):

```
routing_score = weight × latency_factor × health_factor × error_factor
latency_factor = min(2.0, target_latency / latency_ema)
health_factor  = health_score / 100
error_factor   = max(0.05, 1 - error_rate × penalty)
```

**Run:**

```bash
docker compose up --build -d
k6 run benchmarks/routing/routing-test.js
```

**Admin inspection:**

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/routing/gateways
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/routing/gateways/gateway-a
```

**Metrics:** `routing_decisions_total{gateway,failover}`, `routing_outcomes_total`, `routing_failovers_total`, `routing_gateway_health_score`, `routing_circuit_open`.

## Tradeoffs

- **Redis round-trip per route decision**. Adds roughly 1 to 3 ms vs a static pick. Acceptable inside the sidecar hot path.
- **Simulated gateways**. Production latency distributions are messier. Local test is directional, not an absolute SLA.
- **Score lag**. EMA smoothing delays reaction to a sudden outage. Tune `CB_EMA_ALPHA`.
- **gateway-c still gets 5% floor**. `error_factor` min 0.05 prevents total starvation (probe traffic).
- **Coupled to circuit breaker**. Routing benchmarks overlap `circuitbreaker/circuit-test.js` when errors spike.

## Failure modes

1. **All circuits open**. No healthy gateway. 503 storm. Admin reset via `DELETE /admin/circuit/{target}`.
2. **Stale health scores**. Redis partition. Routing picks blind. Chaos `network_partition.py` exposes this.
3. **Weight misconfiguration**. `GATEWAYS` parse error at startup. Sidecar fails fast on boot.
4. **Failover loop**. Both gateways return 5xx. Client sees chained latency. k6 thresholds fail.
5. **Probe stampede**. Too many half-open probes after recovery. Tune `CB_HALF_OPEN_MAX_PROBES`.

## Operational concerns

- Baseline stack throughput before routing test: roughly **1,000 actual RPS** sustainable (`benchmarks/summary.md`).
- Run `routing-test.js` after compose is healthy. Check Jaeger (`:16686`) for span `routing.select`.
- During an incident: `GET /admin/routing/gateways` for live scores. `PUT /admin/limits/...` if quota is also involved.
- Pair with `circuitbreaker/circuit-test.js` when validating gateway-c trip behavior.
- Chaos: `high_latency.py` degrades Redis RTT. Routing scores lag; they do not go instantly wrong.

## Performance implications

Routing layer throughput ceiling is the same order as bare sidecar. Roughly **1,000 RPS** sustainable on my laptop stack before collapse:

| Target RPS | Actual RPS | p99 | Errors |
|------------|------------|-----|--------|
| 1,000 | 1,000 | 3.2 ms | 0% |
| 5,000 | 1,353 | 3.5 s | 10% |

**Expected routing behavior** (qualitative from `routing/summary.md`):

- gateway-a receives **majority** traffic (highest score)
- gateway-c traffic **drops** as error rate increases and circuit opens
- Failover headers appear on primary 5xx

Circuit breaker micro-benchmarks (`circuitbreaker/summary.md`): `BenchmarkCircuitAllow` at ~8k ops/sec, ~120µs/op on miniredis. Routing adds Redis reads on top.

## Lessons learned

I started with fixed weights. Gateway-c ate 33% of traffic and blew the error budget. Dynamic scoring shifted the majority to gateway-a without a manual config change.

Failover headers saved hours in production debugging. Asserting them in the k6 test is now a standard check.

Routing and circuit breaker share one Redis store. `DELETE /admin/circuit/gateway-c` manual reset drill is on the release checklist.

Next: automate per-gateway request count assertions in routing-test.js, not just a qualitative summary.
