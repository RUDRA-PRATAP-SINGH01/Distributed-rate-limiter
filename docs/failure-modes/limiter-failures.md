# Failure Mode: Central Limiter Failures

**Sources:** `cmd/sidecar/limiter_http.go`, `cmd/sidecar/main.go`, `docs/README.md` (verified timings)

**Severity:** High  
**Components:** Sidecar → limiter HTTP, circuit target `central-limiter`

---

## Symptom

Sidecar cannot reach or trust the central limiter → **503 Service Unavailable** to clients (fail-closed default). Upstream is **not** called on fail-closed path.

---

## Bounded latency (~504ms → 503)

Sidecar limiter HTTP client defaults (`cmd/sidecar/limiter_http.go`):

| Setting | Default |
|---------|---------|
| `ClientTimeout` | 1500ms |
| `DialTimeout` | 500ms |
| `ResponseHeaderTimeout` | 1000ms |
| `TLSHandshakeTimeout` | 1000ms |

Override via `SIDECAR_LIMITER_*_TIMEOUT_MS` env vars.

**Verified (limiter unreachable, sidecar):** ~**504 ms** → 503 (`docs/README.md`, commit `a1de9ec`).

Typical fast-fail: connection refused bounded by dial timeout (~500ms) before full client timeout.

Tests: `cmd/sidecar/limiter_http_test.go`, `cmd/sidecar/sidecar_test.go` (`TestSidecar_LimiterErrors`, `TestSidecar_LimiterTimeouts`).

---

## Failure categories

| Condition | Sidecar behavior | Upstream called? |
|-----------|------------------|------------------|
| Limiter connection error | 503 `Rate limiter unavailable` | No |
| Limiter timeout | 503 | No |
| Limiter 5xx | 503; records CB failure | No |
| Limiter 429 | 429 to client (quota) | No |
| Malformed JSON body | 503 | No |
| Circuit `central-limiter` open | 503 | No |
| Any of above + `FAIL_OPEN=true` | Forward with warning log | **Yes** |

Idempotency path: failed check calls `failIdempotent` with 503 body before returning.

---

## Circuit breaker integration

Before HTTP call (`checkRateLimit`):

```go
allow, err := s.limiterCircuit.Allow(ctx, circuitbreaker.TargetCentralLimiter)
```

Enabled when idempotency is on and `ENABLE_CIRCUIT_BREAKER != "false"`, or when routing is enabled.

429 from limiter does **not** trip the breaker (`TestCheckRateLimit429KeepsCBClosedAndSpanUnset`). 5xx and transport errors do.

---

## Sidecar health

`/health` probes `GET {RATE_LIMITER_URL}/health`. If limiter is down or unhealthy (including limiter's own Redis failure), sidecar returns **503** even if sidecar-local Redis is fine.

---

## Limiter process failure modes

| Failure | Client impact |
|---------|---------------|
| Process crash | Sidecar 503 until restart |
| Redis down inside limiter | Limiter 503 on `/check`; sidecar health 503 |
| Missing/invalid `INTERNAL_API_KEY` on sidecar | Limiter 401; sidecar treats as check failure → 503 |
| Slow limiter (under timeout) | 503 at client timeout |

Limiter does not implement fail-open — only the sidecar does via `FAIL_OPEN=true`.

---

## Operational response

1. Check limiter `/health` and logs (`component=limiter`).
2. Verify `RATE_LIMITER_URL` and network from sidecar pod.
3. Inspect `circuit_breaker_state{target="central-limiter"}`.
4. Admin reset: `DELETE /admin/circuit/central-limiter` after limiter recovery.
5. Never enable `FAIL_OPEN=true` in production without explicit risk acceptance.

---

## Related

- [redis-failures.md](redis-failures.md)
- [circuit-breaker-failures.md](circuit-breaker-failures.md)
- `chaos/network_partition.py` — partition sidecar↔limiter trips `central-limiter` CB
