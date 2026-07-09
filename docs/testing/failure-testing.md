# Failure Testing

**Sources:** `cmd/limiter/redis_failure_test.go`, `cmd/sidecar/sidecar_test.go`, `cmd/sidecar/circuit_breaker_test.go`, `chaos/chaos_test.ps1`, `internal/audit/shutdown_test.go`

---

## Philosophy

Failures must produce **predictable HTTP semantics**:

| Condition | Expected status |
|-----------|-----------------|
| Quota exhausted | **429** |
| Redis / limiter / circuit infrastructure | **503** |
| Bad identity | **400** |
| Missing API key | **401** |

Never confuse 503 (infra) with 429 (quota) in tests or alerts.

---

## Limiter Redis failure

`cmd/limiter/redis_failure_test.go`:

- Close miniredis client → `/check` and `/check_hierarchical` return **503**.
- Body must not leak Redis address.
- `TestRedisFailure_CircuitBreakerTrips` — repeated failures populate `circuit_state` in response.

---

## Sidecar fail-closed

`cmd/sidecar/sidecar_test.go`:

| Test | Scenario | Assert |
|------|----------|--------|
| `TestSidecar_LimiterErrors` | Limiter returns error | 503, upstream count 0 |
| `TestSidecar_LimiterTimeouts` | Blocked limiter | 503, upstream count 0 |
| `TestSidecar_MalformedResponses` | Invalid JSON | 503 |

With `FAIL_OPEN=true`, upstream **is** called despite limiter errors (separate test cases in same file).

---

## Limiter HTTP client bounds

`cmd/sidecar/limiter_http_test.go`:

- Connection refused within `ClientTimeout` budget.
- Delayed headers timeout at `ResponseHeaderTimeout`.
- 5xx from limiter → sidecar 503 + CB failure recorded.
- 429 → CB stays closed.

---

## Circuit breaker

`cmd/sidecar/circuit_breaker_test.go`:

- Repeated limiter failures trip `central-limiter` open.
- Open breaker → 503 without hitting limiter.
- Half-open probe limits (see benchmark row in `docs/README.md`).

Limiter redis CB: `cmd/limiter/circuit.go` + redis failure tests.

---

## Audit failure isolation

`internal/audit/store_test.go` — append errors do not panic handlers.  
Queue full → `audit_dropped_total` increment; check path unaffected.

`TestShutdown_RedisUnavailableBounded` — shutdown completes within timeout when Redis unavailable.

---

## Chaos (integration)

`chaos/chaos_test.ps1` — full stack required:

1. Fresh user → 200 through sidecar.
2. Stop Redis → **503** (fail if 200 and `FAIL_OPEN` mis-set).
3. Restart Redis → recovery.

Run manually in staging; not in GitHub Actions today.

---

## Health failure tests

- `cmd/limiter/health_test.go` — unhealthy Redis → 503 JSON.
- `cmd/sidecar/health_test.go` — limiter down or Redis down → 503.

---

## Telemetry on failures

`internal/telemetry/http_transport_test.go` — client 503 marks span error; 429 does not.

Ensure failure tests remain aligned when changing timeout defaults in `limiter_http.go` or `redis/timeouts.go`.
