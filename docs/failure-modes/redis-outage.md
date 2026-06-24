# Failure Mode: Redis Outage

**Status:** Documented  
**Severity:** Critical  
**Components:** Limiter, Sidecar (idempotency/routing), Circuit breaker target `redis`

---

## 1. Problem Statement

When Redis is unreachable or refusing commands, the central limiter cannot execute quota Lua scripts, idempotency claims fail, routing cannot list gateways, and circuit breaker state reads error. I designed the system to **fail closed** by default so an outage does not become unbounded traffic amplification.

## 2. Why the problem exists

Redis is the authoritative store — not a cache. There is no local fallback that preserves fleet-wide correctness. Any "continue without Redis" path is an explicit policy decision (`FAIL_OPEN`, `IDEMPOTENCY_FAIL_OPEN`, `CIRCUIT_FAIL_OPEN`), not an accident.

## 3. Design goals

- Return **503 Service Unavailable** for infrastructure failure, **429** only for quota exhaustion.
- Trip `cb:redis` circuit after sustained Redis errors (`CB_FAILURE_RATE`, `CB_CONSECUTIVE_FAILURES`).
- Surface health in limiter `/health` JSON (`redis.connected: false`).
- Allow dev-only bypass via env flags with loud startup warnings.

## 4. Alternative approaches considered

| Alternative | Why I did not default to it |
|-------------|----------------------------|
| **FAIL_OPEN=true in prod** | Turns outage into DDoS vulnerability. |
| **Local token bucket fallback** | Per-node counters diverge instantly. |
| **Queue requests until Redis returns** | Unbounded memory; cascading latency. |
| **Read from replica during master outage** | Writes still fail; stale reads break limits. |

## 5. Final architecture

**Limiter path** (`cmd/limiter/circuit.go`):

1. `checkRedisCircuit` calls `redisCircuit.Allow(ctx, TargetRedis)`.
2. On Allow error: if `CIRCUIT_FAIL_OPEN=true` → proceed; else 503 JSON `{"error":"Rate limiter unavailable","circuit_state":"unavailable"}`.
3. On Allow denied (open/half-open exhausted): 503 with `circuit_state: open|half_open`.
4. After each Redis op: `recordRedisCircuit` classifies error/timeout into `cb:redis` via `record.lua`.

**Sidecar path** (`cmd/sidecar/main.go`):

- Limiter HTTP failure → 503 unless `FAIL_OPEN=true` → forward with warning log.
- Idempotency claim `ErrStoreUnavailable` → 503 unless `IDEMPOTENCY_FAIL_OPEN=true`.
- Routing `ListGateways` error → `Forward` returns error → 503 `all gateways unavailable`.

**Startup:** Limiter fatals on initial `Ping` failure — process never serves false healthy.

## 6. Tradeoffs

| Fail-closed | Fail-open (dev) |
|-------------|-----------------|
| Protects upstream | Keeps demos alive |
| User-visible 503 | Risk of unlimited traffic |
| Clear ops signal | Masks Redis dependency in tests |

## 7. Failure modes

| Symptom | Likely cause | User impact |
|---------|--------------|-------------|
| 503 from sidecar, limiter healthy | Network partition sidecar→limiter | No upstream |
| 503 from limiter, `circuit_state: open` | Redis latency/errors tripped breaker | All checks denied |
| Chaos test expects 503, gets 200 | `FAIL_OPEN=true` set | `chaos/chaos_test.ps1` FAIL message |
| Idempotency 503 | Redis down on sidecar | No dedup; safe vs double-charge |
| Audit sync path errors | `append.lua` fails | Decision still returned; audit `error` decision logged if partial |

## 8. Operational concerns

**Detect:**

- `circuit_breaker_state{target="redis"}` == 1 (open)
- `rate_limiter_redis_duration_seconds` tail spike
- Limiter `/health` redis section
- `redis_failover_reconnects_total` after Sentinel recovery

**Respond:**

1. Verify `REDIS_ADDR` / Sentinel endpoints.
2. Check Redis memory, persistence, slowlog.
3. Admin: `DELETE /admin/circuit/redis` to force close after recovery (ops only).
4. Never enable `CIRCUIT_FAIL_OPEN` in prod without executive risk acceptance.

**Env vars:** `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_MODE`, `CIRCUIT_FAIL_OPEN`, `FAIL_OPEN`, `IDEMPOTENCY_FAIL_OPEN`.

## 9. Performance implications

During partial Redis slowness (not hard down), latency EMA may trip `OutcomeLatencySpike` before hard errors — circuit opens preemptively. Recovery uses half-open probes (`CB_HALF_OPEN_MAX_PROBES`, `CB_HALF_OPEN_SUCCESS_REQUIRED`) — gradual traffic restoration, not instant flood.

## 10. Lessons learned

I deliberately mixed up 503 and 429 once in an early prototype and operators thought rate limits were "broken" during a Redis incident. Separating them made paging rules trivial: **503 pages infra, 429 pages product/quota**. The chaos script checking `FAIL_OPEN` exists because I forgot the flag during a demo and misread a healthy-looking 200.

**References:** `cmd/limiter/circuit.go`, `cmd/sidecar/main.go`, `chaos/chaos_test.ps1`, `internal/circuitbreaker/`
