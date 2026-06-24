# Circuit Breaker Architecture

> Engineering journal. Why I built a Redis-backed distributed circuit breaker and kept fail-closed as the default.

## Problem Statement

The rate limiter fleet has several fragile dependencies: **Redis** (quota state), **central-limiter** (HTTP check), and **per-gateway upstreams** (in routing mode). When any dependency degrades, every request can keep hitting the same slow or failing path, causing cascade failure and connection exhaustion. I needed fleet-wide, coordinated backpressure. A local in-process breaker was not enough because each pod samples failures independently.

## Why the problem exists

The sidecar calls the central limiter on every request. The limiter runs Redis Lua on every check. When Redis is slow or the limiter is overloaded, the timeout chain drags the whole stack. Routing mode has multiple gateways. Blind retry to one unhealthy gateway is waste. Without a breaker, `FAIL_OPEN` sidecars pass traffic and bypass quota. Without coordinated state, some pods can be open while others stay closed.

## Design goals

1. **Three-state machine**: `closed` to `open` to `half_open` to `closed` or reopen.
2. **Atomic allow and record** via `allow.lua` and `record.lua`.
3. **Multiple trip signals**: failure rate, consecutive failures, latency EMA spike, timeout rate.
4. **Half-open probe budget**. Controlled recovery validation.
5. **Named targets**: `redis`, `central-limiter`, and dynamic `gateway-id`.
6. **Fail-closed on Redis errors** unless `CIRCUIT_FAIL_OPEN=true`.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Per-process `gobreaker` only | No fleet coordination. Uneven protection. |
| Hystrix-style thread pools | Awkward in the Go HTTP model. Does not fit the sidecar proxy. |
| Simple error counter in app memory | Lost on restart. No cross-instance visibility. |
| Always fail-open on dependency errors | Production outage means unlimited traffic to broken Redis. |
| External service mesh circuit breaking | Heavier ops. I wanted app-level semantics tied to our `ClassifyHTTP`. |

## Final architecture

### Redis state

```
cb:{target}  → HASH
  state: closed | open | half_open
  failure_count, success_count, timeout_count, latency_spike_count, total_count
  consecutive_failures, latency_ema_ms
  opened_at, half_open_at, half_open_calls, half_open_successes
```

### `allow.lua`. Pre-call gate

```
closed     → allow (probes_remaining = -1)
open       → if now - opened_at >= cooldown → transition half_open, else reject
half_open  → if half_open_calls >= max_probes → reopen; else increment probes, allow
```

Returns: `{allowed, state_code, probes_remaining}` where state_code 0=closed, 1=open, 2=half_open.

### `record.lua`. Post-call state machine

Outcome kinds (`RecordInput.Kind`):

| Code | Kind | Counted as failure |
|------|------|-------------------|
| 0 | success | no |
| 1 | failure | yes |
| 2 | timeout | yes (+ timeout_count) |
| 3 | latency_spike | yes |

**Closed state trip conditions** (any true after `min_samples` where applicable):

- `failure_rate >= failure_rate_threshold` (default 0.5)
- `consecutive_failures >= threshold` (default 5)
- `latency >= threshold AND ema >= threshold` (default 500ms)
- `timeout_rate >= threshold` (default 0.3)

**Half-open recovery**:

- Success increments `half_open_successes`. When `>= half_open_success_required` (default 2), the circuit closes.
- Any failure reopens (`transition = reopened`).

**Counter decay**: When `total_count > 1000`, all counters halve. Rolling window effect without separate time buckets.

### Integration points

**Central limiter. Redis target** (`cmd/limiter/circuit.go`):

```text
/check handler:
  checkRedisCircuit() → Allow("redis") before Lua limit check
  defer recordRedisCircuit() → ClassifyError on Redis op latency
```

Redis circuit open returns `503` with `circuit_state` in the JSON body.

**Sidecar. central-limiter target** (`cmd/sidecar/main.go`):

```text
checkRateLimit():
  Allow("central-limiter") → fail if open (unless CIRCUIT_FAIL_OPEN)
  HTTP call to limiter
  defer Record("central-limiter", ClassifyHTTP(...))
```

**Routing. gateway-id targets** (`internal/routing/router.go`, `store.go`):

```text
Forward():
  for each gateway candidate:
    Allow(gatewayID) → skip if open
    execute upstream
    RecordOutcome → breaker.Record(gatewayID, success|failure|timeout)
```

Gateway ID is the configured identifier in `GATEWAYS` env (for example `gateway-a`).

### Fail-closed: `CIRCUIT_FAIL_OPEN`

`circuitbreaker.LoadConfigFromEnv()`:

```go
if os.Getenv("CIRCUIT_FAIL_OPEN") == "true" {
    cfg.FailOpen = true
}
```

When `FailOpen=false` (default):

- `Allow()` Redis error rejects the request (`503` on limiter, error return on sidecar)
- Sidecar limiter circuit unavailable means no forward

When `FailOpen=true`:

- Redis errors on `Allow` are ignored. Traffic proceeds (dangerous, documented in config comment)

### Default thresholds (`circuitbreaker.DefaultConfig`)

| Parameter | Default |
|-----------|---------|
| Failure rate | 50% |
| Min samples | 10 |
| Consecutive failures | 5 |
| Latency threshold | 500ms |
| Timeout rate | 30% |
| Open cooldown | 30s |
| Half-open max probes | 3 |
| Half-open successes required | 2 |
| EMA alpha | 0.2 |

Env overrides: `CB_FAILURE_RATE`, `CB_MIN_SAMPLES`, `CB_CONSECUTIVE_FAILURES`, `CB_LATENCY_THRESHOLD_MS`, `CB_TIMEOUT_RATE`, `CB_OPEN_COOLDOWN_MS`, `CB_HALF_OPEN_MAX_PROBES`, `CB_HALF_OPEN_SUCCESS_REQUIRED`, `CB_EMA_ALPHA`.

### Admin and ops

- `Breaker.Reset(ctx, target)`. Force closed (routing `ResetCircuit` per gateway)
- `Breaker.List(ctx)`. Scan `cb:*` keys

## Tradeoffs

- **Shared Redis for breaker state**. The breaker depends on Redis. Ironic but practical because the cluster is already required.
- **Half-open probe exhaustion reopens**. Cooldown loop. Aggressive failures slow recovery.
- **Gateway ID as target string**. Misconfiguration can create unbounded targets. Monitor key count.
- **Fail-closed**. Availability hit during a Redis blip. I chose safety over liveness.
- **Legacy gauge** `routing_circuit_open` still updates alongside `circuit_breaker_state`.

## Failure modes

| Scenario | Effect |
|----------|--------|
| Redis down + fail-closed | All protected paths return 503 or skip gateways |
| Redis down + CIRCUIT_FAIL_OPEN=true | Breaker is blind. Dependencies get hammered. |
| Flapping dependency | Rapid open and half-open cycles. Monitor transitions metric. |
| Half-open probe quota exhausted | Circuit reopens without a full recovery test |
| Clock skew across nodes | `now_ms` from each caller. Minor skew is usually OK. |
| Stale open state after fix | Manual `Reset` or wait for cooldown plus half-open success |

## Operational concerns

- Prometheus: `circuit_breaker_state{target}`, `circuit_breaker_transitions_total`, `circuit_breaker_rejections_total`, `circuit_breaker_outcomes_total`, `circuit_breaker_failure_rate`, `circuit_breaker_latency_ema_ms`, `circuit_breaker_redis_duration_seconds`.
- Alert when `circuit_breaker_state{target="redis"} == 1`. The limiter is effectively down for checks.
- Alert when `central-limiter` is open. The sidecar cannot rate-limit.
- Never set `CIRCUIT_FAIL_OPEN=true` in production without explicit risk acceptance.
- Sidecar: `ENABLE_CIRCUIT_BREAKER` defaults on when idempotency is enabled. Routing always wires the breaker.

## Performance implications

- Every protected call adds **one Redis round-trip** for `Allow` and **one for `Record`** after completion.
- Lua scripts are lightweight (sub-ms on healthy Redis). Under load, breaker Redis ops add to tail latency.
- Counter halving prevents unbounded HASH growth without TTL churn.
- `ClassifyHTTP` and `ClassifyError` are pure CPU. Negligible cost.

## Lessons learned

I **deliberately chose fail-closed as the default**. In a rate limiter, fail-open means quota bypass, which is worse during an outage. Fleet-wide Redis state fixed split-brain breakers where some pods still hit failing Redis. The half-open probe budget exhausted to reopen logic is controversial. The alternative was an indefinite half-open stall. I preferred cooldown retry. Gateway-level targets loosely coupled routing and the breaker. Same `Breaker` type, different target strings. I kept the `CIRCUIT_FAIL_OPEN` env name intentionally scary so ops will grep for it.
