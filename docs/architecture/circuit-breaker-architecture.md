# Circuit Breaker Architecture

> इंजीनियरिंग जर्नल — मैंने Redis-backed distributed circuit breaker क्यों बनाया और fail-closed default क्यों रखा।

## Problem Statement

Rate limiter fleet में कई fragile dependencies हैं: **Redis** (quota state), **central-limiter** (HTTP check), और **per-gateway upstreams** (routing mode में)। जब कोई dependency degrade हो, तो हर request उसी slow/failing path पर जाकर cascade failure और connection exhaustion कर सकता है। मुझे fleet-wide, coordinated backpressure चाहिए था — local in-process breaker insufficient था क्योंकि हर pod अलग sample देखता।

## Why the problem exists

Sidecar हर request पर central limiter को HTTP call करता है; limiter हर check पर Redis Lua चलाता है। Redis slow हो या limiter overloaded, timeout chain पूरी stack को drag करती है। Routing mode में multiple gateways हैं — एक unhealthy gateway पर blind retry waste है। बिना breaker के `FAIL_OPEN` sidecar traffic pass कर देता है (quota bypass); बिना coordinated state के कुछ pods open, कुछ closed रह सकते हैं।

## Design goals

1. **Three-state machine**: `closed` → `open` → `half_open` → `closed` / reopen।
2. **Atomic allow + record** via `allow.lua` and `record.lua`।
3. **Multiple trip signals**: failure rate, consecutive failures, latency EMA spike, timeout rate।
4. **Half-open probe budget** — controlled recovery validation।
5. **Named targets**: `redis`, `central-limiter`, और dynamic `gateway-id`।
6. **Fail-closed on Redis errors** unless `CIRCUIT_FAIL_OPEN=true`।

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Per-process `gobreaker` only | No fleet coordination; uneven protection |
| Hystrix-style thread pools | Go HTTP model में awkward; doesn't fit sidecar proxy |
| Simple error counter in app memory | Lost on restart; no cross-instance visibility |
| Always fail-open on dependency errors | Production outage = unlimited traffic to broken Redis |
| External service mesh circuit breaking | Heavier ops; wanted app-level semantics tied to our ClassifyHTTP |

## Final architecture

### Redis state

```
cb:{target}  → HASH
  state: closed | open | half_open
  failure_count, success_count, timeout_count, latency_spike_count, total_count
  consecutive_failures, latency_ema_ms
  opened_at, half_open_at, half_open_calls, half_open_successes
```

### `allow.lua` — pre-call gate

```
closed     → allow (probes_remaining = -1)
open       → if now - opened_at >= cooldown → transition half_open, else reject
half_open  → if half_open_calls >= max_probes → reopen; else increment probes, allow
```

Returns: `{allowed, state_code, probes_remaining}` where state_code 0=closed, 1=open, 2=half_open.

### `record.lua` — post-call state machine

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

- Success increments `half_open_successes`; `>= half_open_success_required` (default 2) → close
- Any failure → reopen (`transition = reopened`)

**Counter decay**: `total_count > 1000` पर सभी counters halve — rolling window effect without separate time buckets.

### Integration points

**Central limiter — Redis target** (`cmd/limiter/circuit.go`):

```text
/check handler:
  checkRedisCircuit() → Allow("redis") before Lua limit check
  defer recordRedisCircuit() → ClassifyError on Redis op latency
```

Redis circuit open → `503` with `circuit_state` in JSON body।

**Sidecar — central-limiter target** (`cmd/sidecar/main.go`):

```text
checkRateLimit():
  Allow("central-limiter") → fail if open (unless CIRCUIT_FAIL_OPEN)
  HTTP call to limiter
  defer Record("central-limiter", ClassifyHTTP(...))
```

**Routing — gateway-id targets** (`internal/routing/router.go`, `store.go`):

```text
Forward():
  for each gateway candidate:
    Allow(gatewayID) → skip if open
    execute upstream
    RecordOutcome → breaker.Record(gatewayID, success|failure|timeout)
```

Gateway ID = `GATEWAYS` env में configured identifier (e.g. `gateway-a`).

### Fail-closed: `CIRCUIT_FAIL_OPEN`

`circuitbreaker.LoadConfigFromEnv()`:

```go
if os.Getenv("CIRCUIT_FAIL_OPEN") == "true" {
    cfg.FailOpen = true
}
```

जब `FailOpen=false` (default):

- `Allow()` Redis error → request rejected (`503` limiter, error return sidecar)
- Sidecar limiter circuit unavailable → no forward

जब `FailOpen=true`:

- Redis errors on `Allow` ignored; traffic proceeds (dangerous — documented in config comment)

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

### Admin / ops

- `Breaker.Reset(ctx, target)` — force closed (routing `ResetCircuit` per gateway)
- `Breaker.List(ctx)` — scan `cb:*` keys

## Tradeoffs

- **Shared Redis for breaker state** — breaker Redis पर depend करता है; ironic but practical (same cluster already required)।
- **Half-open probe exhaustion reopens** — cooldown loop; aggressive failures slow recovery।
- **Gateway ID as target string** — unbounded targets if misconfigured; monitor key count।
- **Fail-closed** — availability hit during Redis blip; intentional safety over liveness।
- **Legacy gauge** `routing_circuit_open` still updated alongside `circuit_breaker_state`।

## Failure modes

| Scenario | Effect |
|----------|--------|
| Redis down + fail-closed | All protected paths return 503 / skip gateways |
| Redis down + CIRCUIT_FAIL_OPEN=true | Breaker blind; dependencies hammered |
| Flapping dependency | Rapid open/half-open cycles; monitor transitions metric |
| Half-open probe quota exhausted | Circuit reopens without full recovery test |
| Clock skew across nodes | `now_ms` from each caller; minor skew usually OK |
| Stale open state after fix | Manual `Reset` or wait cooldown + half-open success |

## Operational concerns

- Prometheus: `circuit_breaker_state{target}`, `circuit_breaker_transitions_total`, `circuit_breaker_rejections_total`, `circuit_breaker_outcomes_total`, `circuit_breaker_failure_rate`, `circuit_breaker_latency_ema_ms`, `circuit_breaker_redis_duration_seconds`।
- Alert on `circuit_breaker_state{target="redis"} == 1` — limiter effectively down for checks।
- Alert on `central-limiter` open — sidecar cannot rate-limit。
- `CIRCUIT_FAIL_OPEN=true` never in production without explicit risk acceptance。
- Sidecar: `ENABLE_CIRCUIT_BREAKER` defaults on when idempotency enabled; routing always wires breaker。

## Performance implications

- Every protected call: **+1 Redis round-trip** for `Allow`; **+1 for `Record`** after completion。
- Lua scripts lightweight (~sub-ms on healthy Redis); under load, breaker Redis ops add to tail latency。
- Counter halving prevents unbounded HASH growth without TTL churn。
- `ClassifyHTTP` / `ClassifyError` pure CPU — negligible。

## Lessons learned

मैंने **fail-closed default** deliberately choose किया — rate limiter में fail-open = quota bypass, जो outage के दौरान worse है। Fleet-wide Redis state ने "split brain" breakers solve किए जहाँ कुछ pods अभी भी failing Redis को hit कर रहे थे। Half-open probe budget exhausted → reopen logic controversial है — alternative था indefinite half-open stall; मैंने cooldown retry prefer किया। Gateway-level targets ने routing और breaker को loosely couple किया — same `Breaker` type, different target strings। `CIRCUIT_FAIL_OPEN` env name intentionally scary रखा ताकि ops grep करें।
