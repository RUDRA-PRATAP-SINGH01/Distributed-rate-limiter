# Token Bucket Algorithm

## Problem Statement

I need smooth, burst-tolerant rate limiting where a user can consume up to `capacity` tokens immediately, then refills at `refill_rate` tokens per second. In a distributed deployment, the refill-and-deduct sequence must be **atomic** — two sidecars reading "1 token left" and both allowing is unacceptable.

Production path: `RedisAtomicTokenBucket` in `internal/limiter/redis_atomic_token_bucket.go` with embedded `lua/token_bucket.lua`. Redis key prefix: **`rate:{id}`**.

## Why the problem exists

Token bucket state is a pair of numbers (`tokens`, `last_refill`) that change together every request. Splitting that across Go goroutines or multiple Redis commands creates a classic read-modify-write race. An early non-atomic Go prototype was eliminated (H-03), and production strictly uses `RedisAtomicTokenBucket` with Lua.

Refill math also needs sub-second precision for fairness: fractional tokens accumulate in Redis as floats; `math.floor()` applies only to the allow/deny comparison and the returned `remaining`, not to the stored balance (`redis_atomic_token_bucket_test.go`, Suite 1C).

## Design goals

| Goal | Implementation |
|------|----------------|
| One atomic round-trip | Single `EVAL` via `redis.NewScript` |
| Key isolation | `rate:{userID}` per identity (`fmt.Sprintf("rate:%s", userID)`) |
| Millisecond refill | `redis.call('TIME')` in Lua; ARGV[3] is ignored (rolling-deploy compat) |
| Denied = no deduct | Script compares `floor(new_tokens) >= requested` before subtracting |
| Memory hygiene | `EXPIRE key 3600` on every write |
| Observable | `metrics.RecordRedisDuration` after each script |

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Go GET → compute → SET | Race under concurrency — rejected and removed (H-03) |
| Redis INCR without refill | No smooth refill; fixed-window semantics |
| MULTI/EXEC without Lua | Last writer wins under contention |
| Per-sidecar in-memory bucket | Effective limit multiplies by replica count |

Lua on the Redis primary is the production default.

## Final architecture

**Redis key:** `rate:{id}` — HASH with fields `tokens` (float), `last_refill` (ms timestamp).

**Script contract** (`internal/limiter/lua/token_bucket.lua`):

```
KEYS[1]  = rate:{id}
ARGV[1]  = capacity
ARGV[2]  = refill_rate (tokens/second)
ARGV[3]  = client now (ms) — unused; Lua uses redis.call('TIME')
ARGV[4]  = requested (always 1)
```

**Algorithm steps:**

1. `HMGET tokens,last_refill` — first visit initializes to full `capacity`.
2. `elapsed = (redis_now - last_refill) / 1000.0`; clamp negative skew to 0.
3. `new_tokens = min(capacity, tokens + elapsed * refill_rate)`.
4. If `floor(new_tokens) >= requested`: deduct, `allowed = 1`; else `allowed = 0`.
5. `HMSET` + `EXPIRE 3600`; return `{allowed, remaining}`.

**Go invocation:**

```go
key := fmt.Sprintf("rate:%s", userID)
tb.script.Run(ctx, rdb, []string{key}, capacity, refillRate, now, 1)
```

Limiter exposes this on `GET /check` when `ALGORITHM=token` (`cmd/limiter/main.go`).

## Tradeoffs

- **Float storage:** Enables fractional refill preservation; `remaining` is floored for clients.
- **Write on deny:** Script always writes refilled state and updates `last_refill`, even when denying — simplifies Lua at the cost of advancing refill clocks on rejected traffic.
- **Second-level perception vs ms math:** Clients see integer `remaining`; sub-token progress is invisible until a whole token accrues.
- **Idle key expiry:** After 3600 s idle, next request re-initializes to full capacity (burst on return).
- **Hot keys:** All traffic for one `rate:{id}` serializes on Redis single-threaded execution — correct but contended.

## Failure modes

1. **Lua/Redis error:** Go returns `(false, 0, err)`; limiter responds **503**, not 429 (`cmd/limiter/main.go`).
2. **Unexpected RESP shape:** Parsed via `luautil.LuaInt`; malformed result → error, fail-closed.
3. **capacity=0 misconfig:** All requests denied; constructor does not validate (`TestTokenBucket_ZeroCapacity_Finding`).
4. **Clock skew:** Sidecars pass local `now`; multi-second skew causes refill drift across nodes.
5. **Wiring non-atomic path:** Operational risk if `RedisTokenBucket` replaces `RedisAtomicTokenBucket`.

## Operational concerns

| Variable | Default (compose) | Role |
|----------|-----------------|------|
| `ALGORITHM` | `sliding` | Set `token` for this algorithm |
| `CAPACITY` | `10` | Max burst |
| `REFILL_RATE` | `1.0` | Tokens per second |

- Monitor `rate_limiter_redis_duration_seconds` and allow/deny ratio for `handler="check"`.
- After deploy, script SHA is tied to binary via `//go:embed` — no out-of-band script drift.
- Hierarchical mode reuses the same token math inside `hierarchical.lua` (four keys) — see [hierarchical-rate-limiting.md](hierarchical-rate-limiting.md).

## Performance implications

From `benchmarks/results/a1de9ec-final/` (commit `a1de9ec`):

| Target RPS | Actual RPS | p99 | Non-429 errors |
|------------|------------|-----|----------------|
| 1,000 (direct `/check`) | **869** | 145 ms | 0% |
| 5,000 (direct `/check`) | **4,161** | 148 ms | 0% |

One Lua RTT per check (~2–10 ms on local Docker). Sustainable end-to-end via sidecar is ~872 RPS (sliding default; token within ~5% on same hardware per `benchmarks/summary.md`).

**Test-proven invariants** (`redis_atomic_token_bucket_test.go`):

- Exactly `capacity` allowed under `capacity + M` concurrent goroutines (Suite 1H).
- Denied requests do not mutate stored token balance (Suite 1F).
- `0 ≤ stored_tokens ≤ capacity` after arbitrary sequences (Suite 1L).

## Lessons learned

I shipped Go-side refill first because benchmarks "mostly worked." A 30-goroutine same-user test found double-allow. **If quota correctness matters, pay for Lua** — the ~0.1 ms script cost is cheaper than explaining overruns to tenants.

Token bucket vs sliding window is a product choice: "burst 10 then 1/sec" → token bucket; "hard 10 per 60 s" → sliding (`sw:{id}`). Same k6 harness, different `ALGORITHM` env.
