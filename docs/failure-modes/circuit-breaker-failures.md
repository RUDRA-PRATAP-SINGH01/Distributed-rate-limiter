# Failure Mode: Circuit Breaker Failures

**Sources:** `internal/circuitbreaker/`, `cmd/limiter/circuit.go`, `cmd/sidecar/main.go`, `internal/metrics/metrics.go`

**Severity:** High (cascading denials)  
**Targets:** `redis`, `central-limiter`, `gateway-{id}`

---

## Purpose

Stop hammering failed dependencies. State is **shared in Redis** (`cb:{target}`) so one pod's observations protect the fleet.

States (`circuit_breaker_state` gauge): **0=closed**, **1=open**, **2=half_open**.

---

## Integration points

| Component | Target | When |
|-----------|--------|------|
| Limiter `/check` | `redis` | `checkRedisCircuit` before Lua |
| Sidecar `checkRateLimit` | `central-limiter` | Before limiter HTTP |
| `routing.Router.Forward` | `gateway-{id}` | Before upstream attempt |

Limiter (`cmd/limiter/circuit.go`):

- Allow error + `CIRCUIT_FAIL_OPEN=false` → 503, `circuit_state: unavailable`
- Allow denied → 503, `circuit_state: open|half_open`
- After Redis op: `recordRedisCircuit` classifies error/latency

Sidecar (`checkRateLimit`): symmetric with `TargetCentralLimiter`. 429 from limiter does not count as breaker failure.

---

## Configuration (`internal/circuitbreaker/config.go`)

| Variable | Default |
|----------|---------|
| `CB_FAILURE_RATE` | `0.5` |
| `CB_MIN_SAMPLES` | `10` |
| `CB_CONSECUTIVE_FAILURES` | `5` |
| `CB_LATENCY_THRESHOLD_MS` | `500` |
| `CB_TIMEOUT_RATE` | `0.3` |
| `CB_OPEN_COOLDOWN_MS` | `30000` |
| `CB_HALF_OPEN_MAX_PROBES` | `3` |
| `CB_HALF_OPEN_SUCCESS_REQUIRED` | `2` |
| `CB_EMA_ALPHA` | `0.2` |
| `CIRCUIT_FAIL_OPEN` | `false` |

Sidecar: breaker enabled with idempotency (`ENABLE_CIRCUIT_BREAKER` default true) or routing.

---

## Metrics

- `circuit_breaker_state{target}`
- `circuit_breaker_rejections_total{target,state}`
- `circuit_breaker_transitions_total{target,from,to}`
- `circuit_breaker_outcomes_total{target,outcome}`
- `circuit_breaker_failure_rate{target}`
- `circuit_breaker_latency_ema_ms{target}`
- `circuit_breaker_redis_duration_seconds`

Legacy: `routing_circuit_open{gateway}` — updated by `RecordCircuitState` for gateways.

---

## Failure modes

| Symptom | Cause | Response |
|---------|-------|----------|
| All checks 503, `circuit_state: open` on `redis` | Redis down/slow | Restore Redis; admin reset |
| Sidecar 503, limiter healthy | `central-limiter` open | Fix limiter latency; tune thresholds |
| Gateway starved | `StateOpen` or `StateUnknown` | Reset circuit; fix Redis reads |
| Flapping | Cooldown too short | Raise `CB_OPEN_COOLDOWN_MS` |
| Traffic during CB read errors | `CIRCUIT_FAIL_OPEN=true` | Disable in prod |
| Half-open probe burst | Many replicas probe | Expected brief window; watch transitions |

**Verified:** 64 concurrent requests → 3 admitted, 61×503 under half-open budget (`docs/README.md`).

---

## Admin recovery

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit
curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/gateway-c
curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/redis
```

`internal/circuitbreaker/store.go` — `Reset` forces closed state (ops recovery).

---

## Tests

- `cmd/sidecar/circuit_breaker_test.go`
- `cmd/limiter/redis_failure_test.go` (`TestRedisFailure_CircuitBreakerTrips`)
- `benchmarks/circuitbreaker/circuit-test.js`

---

## Tradeoff

Fail-closed (default) protects upstream; `CIRCUIT_FAIL_OPEN=true` continues traffic during CB store read failures — dangerous in production.

Half-open probes trade gradual recovery vs brief risk on still-sick dependencies.
