# Intelligent Routing Benchmark Summary

## Setup

Three simulated payment gateways:

| Gateway | Latency | Error Rate | Weight |
|---------|---------|------------|--------|
| gateway-a | 15 ms | 1% | 100 |
| gateway-b | 50 ms | 5% | 80 |
| gateway-c | 120 ms | 35% | 60 |

## Run

```bash
docker compose up --build -d
k6 run benchmarks/routing/routing-test.js
```

## Expected Behavior

- **gateway-a** receives majority of traffic (highest score)
- **gateway-c** traffic drops as error rate increases (circuit opens)
- **Failover** headers appear when primary gateway returns 5xx
- Admin API shows live scores: `GET /admin/routing/gateways`

## Scoring Model

```
routing_score = weight × latency_factor × health_factor × error_factor

latency_factor = min(2.0, target_latency / latency_ema)
health_factor  = health_score / 100
error_factor   = max(0.05, 1 - error_rate × penalty)
```

## Observability

| Metric | Description |
|--------|-------------|
| `routing_decisions_total{gateway,failover}` | Routing choices |
| `routing_outcomes_total{gateway,result}` | Success/failure per gateway |
| `routing_failovers_total{gateway}` | Failover count |
| `routing_gateway_health_score{gateway}` | Live health 0-100 |
| `routing_gateway_latency_seconds{gateway}` | Observed latency |
| `routing_circuit_open{gateway}` | Circuit breaker state |
