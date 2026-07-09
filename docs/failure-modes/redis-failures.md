# Failure Mode: Redis Failures

**Sources:** `internal/redis/timeouts.go`, `cmd/limiter/circuit.go`, `cmd/limiter/main.go`, `cmd/sidecar/main.go`, `docs/README.md` (verified outage timings)

**Severity:** Critical  
**Components:** Limiter, sidecar (idempotency/routing), circuit target `redis`

---

## Symptom

Infrastructure failure returns **503 Service Unavailable**, not 429. Quota exhaustion remains 429.

---

## Bounded latency (~1s → 503)

Default Redis client timeouts (`internal/redis/timeouts.go`):

| Setting | Default |
|---------|---------|
| `DialTimeout` | 500ms |
| `ReadTimeout` | 500ms |
| `WriteTimeout` | 500ms |
| `PoolTimeout` | 1s |
| `MaxRetries` | 0 (disabled) |
| `DialerRetries` | 1 |

Documented outage budget: pool wait (1s) + one dial (500ms) + read/write per command, with no command retries.

**Verified (sidecar path, Redis down):** ~**1003–1006 ms** → 503 (`docs/README.md`, commit `a1de9ec`).

Limiter `/check` with Redis unreachable returns 503 JSON:

```json
{"error":"Rate limiter unavailable"}
```

Tests: `cmd/limiter/redis_failure_test.go`, `internal/redis/timeouts_test.go` (`TestPingUnreachableBoundedLatency`).

---

## Limiter path

1. Startup **fatals** if initial `Ping` fails — no false healthy boot.
2. Each `/check` / `/check_hierarchical`:
   - `checkRedisCircuit` → `redisCircuit.Allow(ctx, TargetRedis)` (`cmd/limiter/circuit.go`).
   - Circuit open / Allow error → 503 with `circuit_state`.
   - Redis Lua error → 503 + audit `DecisionError`.
3. `/health` → 503 when `CheckHealth` ping fails; includes `redis.error`.

---

## Sidecar path

When Redis is required (`ENABLE_IDEMPOTENCY` or `ENABLE_ROUTING`):

- `/health` → 503 if Redis ping fails.
- Idempotency claim failure → 503 unless `IDEMPOTENCY_FAIL_OPEN=true`.
- Routing `ListGateways` failure → 503 `all gateways unavailable`.

Flat rate-limit path goes through **central limiter** HTTP; sidecar 503 on Redis outage often reflects limiter health probe failure on `/health`.

Chaos: `chaos/chaos_test.ps1` stops `rate-redis`, expects sidecar **503** (not 200 with `FAIL_OPEN=false`).

---

## Circuit breaker (`TargetRedis`)

After Redis errors, `recordRedisCircuit` classifies outcomes into `cb:redis` state in Redis (`record.lua`).

| Response | Meaning |
|----------|---------|
| `circuit_state: open` | Breaker denying checks |
| `circuit_state: unavailable` | Allow() failed (e.g. Redis read for CB state) |

Env: `CIRCUIT_FAIL_OPEN=true` bypasses CB read errors (dangerous in production).

Recovery: restore Redis; optional admin `DELETE /admin/circuit/redis`; half-open probes per `CB_HALF_OPEN_*` settings.

---

## Error body safety

`TestRedisFailure_Handling` asserts error bodies do not leak Redis addresses or internal details — generic `"Rate limiter unavailable"` only.

---

## Related

- [recovery-behavior.md](recovery-behavior.md) — restart and SCRIPT FLUSH
- [circuit-breaker-failures.md](circuit-breaker-failures.md)
- `docs/failure-modes/redis-outage.md` — extended runbook context
