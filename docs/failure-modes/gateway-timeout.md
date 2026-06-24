# Failure Mode: Gateway Timeout

**Status:** Documented  
**Severity:** Medium–High (tail latency, circuit trips)  
**Components:** `routing.Router`, sidecar `http.Client`, circuit breaker `OutcomeTimeout`

---

## 1. Problem Statement

Upstream payment gateways occasionally hang — TCP connects but response never arrives. Without classification, hung requests block sidecar workers and poison gateway health scores slowly. I needed explicit timeout detection that feeds routing EMA, circuit breaker `timeout_count`, and optional failover to alternate gateways.

## 2. Why the problem exists

The sidecar `http.Client` uses a fixed **5 second** timeout (`cmd/sidecar/main.go` `NewSidecar`). Gateway routers reuse this client in `routing.NewRouter`. When `client.Do` exceeds deadline, Go returns `context.DeadlineExceeded` or `net.Error` with `Timeout() == true`. Routing must treat this differently from fast 500 responses.

## 3. Design goals

- Classify timeouts via `circuitbreaker.ClassifyHTTP` → `OutcomeTimeout`.
- Trip circuits on `CB_TIMEOUT_RATE` threshold after `CB_MIN_SAMPLES`.
- Record routing outcome with `timeout: true` flag for metrics integrity.
- Fail over to next scored gateway within `ROUTING_MAX_FAILOVER_TRIES`.
- Latency spikes (slow but completing) map to `OutcomeLatencySpike` when ≥ `CB_LATENCY_THRESHOLD_MS` (default 500ms).

## 4. Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **No client timeout (infinite wait)** | Worker exhaustion under hung gateways. |
| **Per-gateway timeout env** | Added complexity; unified 5s first. |
| **Retry same gateway on timeout** | Amplifies load on sick node. |
| **Count timeout as success** | Would never trip circuits — dangerous. |

## 5. Final architecture

`routing.Router.Forward` per candidate:

```go
resp, err := r.execute(ctx, req, body, candidate.State)
latency := time.Since(start)
success := err == nil && resp != nil && resp.StatusCode < 500
timeout := circuitbreaker.ClassifyHTTP(err, statusCodeOrZero(resp), latency,
    r.breaker.Config().LatencyThresholdMs).Kind == circuitbreaker.OutcomeTimeout
```

Then `store.RecordOutcome` with `Timeout: timeout` → Lua updates `latency_ema_ms`, `health_score` → `breaker.Record` with kind.

If primary times out:

1. Primary outcome recorded as failure/timeout.
2. `selector.FailoverOrder` yields next gateway.
3. `metrics.RecordRoutingFailover` increments.
4. Response headers include `X-Gateway-Failover: true` on success.

If all candidates timeout: 503 `all gateways unavailable` from sidecar `forwardIdempotent` / `forwardRequest`.

## 6. Tradeoffs

| Choice | Effect |
|--------|--------|
| 5s global timeout | Simple; may be long for user-facing APIs |
| Timeout → circuit input | Fast isolation of bad gateway |
| Failover on timeout | Extra tail latency (sequential tries) |
| Latency spike separate from timeout | Catches slow degradation before hard hang |

## 7. Failure modes

| Scenario | Behavior |
|----------|----------|
| Gateway hangs 4.9s every request | `OutcomeLatencySpike`, may open circuit without timeout flag |
| Gateway hangs >5s | `OutcomeTimeout`, failover attempted |
| All gateways slow | Sequential timeouts → multi-second 503 |
| Timeout during idempotent forward | `failIdempotent` stores 503 body; fence token released for retry |
| Limiter check timeout (separate path) | `checkRateLimit` HTTP error → sidecar 503 or FAIL_OPEN |

## 8. Operational concerns

**Metrics to watch:**

- `routing_outcomes_total{gateway,result="failure"}`
- `circuit_breaker_outcomes_total{target="gateway-*",outcome="timeout"}`
- `circuit_breaker_timeout_rate` (via failure/timeout counts in snapshot)
- `routing_gateway_latency_seconds` histogram tail

**Tuning:**

- `CB_LATENCY_THRESHOLD_MS` — lower to trip on slow gateways earlier.
- `CB_TIMEOUT_RATE` — default 0.3.
- `ROUTING_TARGET_LATENCY_MS` — scoring bias (default 100ms).
- Consider lowering sidecar client timeout in fork if p99 SLA < 5s.

**Traces:** Span `sidecar.intelligent_route` records `gateway.id` per attempt — compare timed-out vs successful spans.

## 9. Performance implications

Sequential failover on timeout means worst-case latency ≈ `timeout × (1 + failover_tries)`. Cap `ROUTING_MAX_FAILOVER_TRIES` (default 3) bounds tail at ~20s theoretical — unacceptable for sync APIs; ops should open circuit faster via lower thresholds. Background probes (`ROUTING_PROBE_INTERVAL_SEC`) detect recovery without user traffic paying full timeout cost.

## 10. Lessons learned

I initially counted timeouts only as generic failures and circuits took too long to open — error rate looked low because requests eventually failed after 5s, not quickly. Splitting `OutcomeTimeout` in `ClassifyHTTP` let `CB_TIMEOUT_RATE` trip independently. During k6 gateway saturation tests, failover headers correlated with timeout spikes — now my standard demo narrative for intelligent routing.

**References:** `internal/routing/router.go`, `internal/circuitbreaker/breaker.go`, `docs/failure-modes/routing-failures.md`
