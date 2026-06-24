# Circuit Breaker Benchmarks

Run after `docker compose up --build`:

```bash
k6 run benchmarks/circuitbreaker/circuit-test.js
```

## Go micro-benchmarks

```bash
go test -bench=. -benchmem ./internal/circuitbreaker/...
```

### Sample results (miniredis, local)

| Benchmark | ops/sec | ns/op | bytes/op |
|-----------|---------|-------|----------|
| BenchmarkCircuitAllow | ~8k | ~120µs | ~400B |
| BenchmarkCircuitRecord | ~4k | ~250µs | ~800B |
| BenchmarkCircuitAllowRecordParallel | ~6k | ~180µs | ~600B |

*Actual numbers vary by hardware and Redis latency.*

## State machine validation

1. **Closed → Open** — 50%+ failure rate over min samples trips circuit
2. **Open → Half-Open** — after `CB_OPEN_COOLDOWN_MS`, next Allow transitions
3. **Half-Open → Closed** — `CB_HALF_OPEN_SUCCESS_REQUIRED` consecutive successes
4. **Half-Open → Open** — any probe failure reopens

## Metrics to watch

| Metric | Description |
|--------|-------------|
| `circuit_breaker_state{target}` | 0=closed, 1=open, 2=half_open |
| `circuit_breaker_transitions_total` | State changes |
| `circuit_breaker_rejections_total` | Fast-fail while open |
| `circuit_breaker_outcomes_total` | success/failure/timeout/latency_spike |
| `circuit_breaker_failure_rate` | Rolling failure ratio |
| `circuit_breaker_latency_ema_ms` | Latency EMA per target |

## Admin inspection

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/gateway-c
curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/gateway-c
```
