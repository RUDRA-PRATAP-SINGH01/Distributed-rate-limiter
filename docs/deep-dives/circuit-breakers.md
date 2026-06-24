# Circuit Breakers

## Problem Statement

Upstream gateways and Redis dependencies fail gradually. latency spikes before hard outages. Blind retries amplify load on dying services ("retry storm"). I needed **distributed circuit breakers** at the sidecar: stop sending traffic to targets that are failing, probe recovery in half-open state, and share breaker state across all sidecar replicas.

## Why the problem exists

The routing layer (`internal/routing/router.go`) will failover across gateways, but without breakers it hammers an unhealthy gateway on every request until scores drop. Failover helps only when multiple gateways exist. a single-gateway deployment still needs fast-fail.

Circuit breakers implement the bulkhead pattern: fail fast locally when error budget is exhausted, give upstream time to recover, admit limited probe traffic to test recovery.

Because sidecars are stateless, breaker state must live in **Redis with atomic transitions**. same lesson as rate limiting.

## Design goals

1. Three states: Closed, open, half-open (`internal/circuitbreaker/types.go`).
2. Atomic allow: `allow.lua` gates calls and manages half-open probe budget.
3. Rich outcome taxonomy: Success, failure, timeout, latency spike (`ClassifyHTTP`, `ClassifyError` in `breaker.go`).
4. EMA latency tracking: Smooth noise vs raw last-sample.
5. Configurable thresholds: Failure rate, consecutive failures, timeout rate, half-open success count.
6. Ops reset: `Reset()` forces closed for incident recovery.

## Alternative approaches considered

| Approach | Issue |
|----------|-------|
| In-process breaker per sidecar | Divergent state; uneven protection |
| Hystrix-style (Java) | Not available in Go sidecar; reinvent |
| Retry-only with backoff | Still loads failing service |
| Mesh outlier detection | Less control over thresholds |
| Open on any single 500 | Too flaky |

Redis-backed breaker with Lua transitions won.

## Final architecture

**Package layout** (`internal/circuitbreaker/`):

- `breaker.go`. high-level API, HTTP/error classification
- `store.go`. `Allow`, `Record`, `GetState`, `Reset`, `ListTargets`
- `config.go`. thresholds, cooldowns, EMA alpha
- `lua/allow.lua`. admission gate
- `lua/record.lua`. outcome ingestion + state machine

**Key prefix:** `cb:{target}` where target is gateway ID in routing integration.

**Allow flow** (`allow.lua`):

- Missing key → initialize closed state
- Open + cooldown elapsed → transition half-open, reset probe counters
- Half-open + probes < max → allow, increment `half_open_calls`
- Half-open + probes exhausted → reopen, deny
- Closed → allow

**Record flow** (`record.lua`. invoked from `store.go`):

- Increments outcome counters by `OutcomeKind`
- Updates latency EMA: `EMAAlpha` blend
- Evaluates open conditions: failure rate > threshold (min samples), consecutive failures, timeout rate, latency spikes
- Half-open: count successes toward `HalfOpenSuccessRequired` → close; failure → reopen

**Router integration** (`router.go` lines 133 to 160):

```go
allow, err := r.breaker.Allow(ctx, candidate.State.ID)
// ...
success := err == nil && resp != nil && resp.StatusCode < 500
timeout := ClassifyHTTP(...).Kind == OutcomeTimeout
_ = r.store.RecordOutcome(...) // routing stats
// breaker Record called separately in handler wiring
```

`ClassifyHTTP` maps `context.DeadlineExceeded`, net timeouts, 5xx, and latency ≥ threshold to appropriate kinds.

## Tradeoffs

- Redis dependency: Breaker unavailable → routing skips or errors depending on wiring; prefer fail-open vs fail-closed per product (current: skip gateway on allow error).
- Too low slows recovery detection; too high loads dying service.
- 4xx as success: Avoids opening on client errors; may mask broken auth config.
- SCAN for ListTargets: `cb:*` scan is O(keys); fine at gateway count scale.
- No per-tenant breakers: Target is gateway ID only.

## Failure modes

1. Flapping: Open → half-open → single failure → open; tune cooldown and success required.
2. Stale closed under sustained 504: Min samples not reached; consecutive failure path should catch.
3. Latency spike count without hard failure. can open on slow but successful deps.
4. Allow/Record race: two sidecars allow in half-open; both record. probe budget limits blast radius.
5. Manual `Reset` before root cause fixed → immediate relapse.

## Operational concerns

- Metrics: `circuit_state`, `circuit_transition_total`, `circuit_rejection_total`, `circuit_failure_rate` per target.
- Run `benchmarks/circuitbreaker/circuit-test.js`; see `benchmarks/circuitbreaker/summary.md`.
- Document `Reset` API for ops. audit who reset what.
- Align `LatencyThresholdMs` with routing `TargetLatencyMs` for consistent behavior.
- `go test -bench=. ./internal/circuitbreaker/...` for Lua path micro-benchmarks.

## Performance implications

Two Redis scripts per routed request worst case (allow + record). ~2 RTTs additive to routing.

`metrics.RecordCircuitRedisDuration` tracks breaker-specific latency separately from limiter.

Under closed healthy state, `allow.lua` is short. EXISTS/HGET path.

Open state fast-fail avoids expensive upstream HTTP. net win under degradation.

## Lessons learned

Classification logic belongs in Go (`ClassifyHTTP`) not Lua. easier to test; Lua gets integer kind enum.

Half-open probe budget (`half_open_max_probes` in allow.lua) prevented recovery storms. I copied this from Netflix Hystrix docs but tuned with k6.

Distributed breakers **amplify coordination value of Redis**. second subsystem after limiter that justified Lua investment.

Pair with routing, not replace it. breaker says "don't try"; routing says "try someone else."

I log transitions (`transition` field from record.lua). "none" vs "open" vs "closed". essential for postmortem timelines.
