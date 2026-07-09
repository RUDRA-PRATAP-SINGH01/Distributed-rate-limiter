# Sliding Window Algorithm

## Problem Statement

Some APIs need a hard cap: "maximum N requests in the last W seconds," without token-bucket burst smoothing. I implement this as a **sorted-set sliding log** in Redis, keyed **`sw:{id}`**, with atomic trim-count-add in `lua/sliding_window.lua`.

Production path: `RedisSlidingWindow` in `internal/limiter/redis_sliding_window.go`. Docker default: `ALGORITHM=sliding`, `CAPACITY=10`, `WINDOW_SEC=60`.

## Why the problem exists

A naive counter with a fixed wall-clock window creates boundary spikes (499 at 0:59 + 500 at 1:00). A true sliding window needs to know **when** each request happened. Redis HASH or STRING counters cannot prune by timestamp efficiently.

ZSET members give O(log N) insert and O(log N + M) range eviction — the standard pattern for distributed sliding windows. Without Lua, `ZREMRANGEBYSCORE` → `ZCARD` → conditional `ZADD` races across sidecars.

## Design goals

| Goal | Implementation |
|------|----------------|
| Ordered event log | ZSET: score = request timestamp (ms) |
| Collision-free members | `{now_ms}:{uuid}` per `Allow()` call |
| Atomic window enforcement | Single `sliding_window.lua` EVAL |
| Key isolation | `sw:{userID}` |
| TTL safety | `expireSec = max(1, ceil(window_ms/1000))` — Redis rejects sub-second EXPIRE on some configs |
| Deny without ZADD | Full window → return `{0, 0}` without adding a member |

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Fixed window INCR | Boundary double-counting |
| Token bucket | Allows initial burst up to capacity — different SLA semantics |
| ZSET without Lua | Count/add race under concurrency |
| One ZSET member per second | Collisions under burst within same second |

Unique member IDs (`fmt.Sprintf("%d:%s", now, uuid.NewString())`) plus Lua atomicity won.

## Final architecture

**Redis key:** `sw:{id}` — ZSET where:

- **score** = request timestamp in milliseconds (`now`)
- **member** = unique string `{now}:{uuid}`

**Script contract** (`internal/limiter/lua/sliding_window.lua`):

```
KEYS[1]  = sw:{id}
ARGV[1]  = now (ms)
ARGV[2]  = windowStart = now - window_ms
ARGV[3]  = limit
ARGV[4]  = expireSec (≥ 1)
ARGV[5]  = unique member id
```

**Algorithm steps:**

1. `ZREMRANGEBYSCORE key 0 windowStart` — evict events at or before window start (inclusive lower bound).
2. `count = ZCARD key`.
3. If `count < limit`: `ZADD`, `EXPIRE`, `allowed=1`, `remaining = limit - count - 1`.
4. Else: `allowed=0`, `remaining=0` (no ZADD on deny).
5. Return `{allowed, remaining}`.

**Go invocation** (`redis_sliding_window.go`):

```go
key := fmt.Sprintf("sw:%s", userID)
windowStart := now - rw.window.Milliseconds()
member := fmt.Sprintf("%d:%s", now, uuid.NewString())
rw.script.Run(ctx, rdb, []string{key}, now, windowStart, limit, expireSec, member)
```

## Tradeoffs

- **Memory vs token bucket:** One ZSET member per allowed request until pruned — higher memory under sustained load.
- **Prune inclusivity:** `ZREMRANGEBYSCORE` with `windowStart` removes entries scored exactly at the boundary — tested in `TestSlidingWindow_BoundaryAtWindowStart` (SOURCE + TEST).
- **Not a leaky bucket:** After idle, capacity is immediately available up to `limit` (no gradual refill curve).
- **Denied requests leave no trace:** Correct for quota semantics; abuse patterns cannot be inferred from ZSET after deny.
- **EXPIRE only on allow:** Denied-only traffic eventually relies on key TTL or later allowed writes to refresh expiry.

## Failure modes

1. **Redis/Lua failure:** Limiter returns **503** with `"Rate limiter unavailable"` — not 429.
2. **expireSec < 1:** Go clamps to 1 before sending to Lua.
3. **Hot key saturation:** `benchmarks/hot-key/hot-key-test.js` at 5,000 target → **99.9% 429** — correct enforcement, elevated p99.
4. **Member collision (theoretical):** UUID suffix makes same-ms collisions negligible (`redis_sliding_window_test.go`, same-ms uniqueness suite).

## Operational concerns

| Variable | Default | Role |
|----------|---------|------|
| `ALGORITHM` | `sliding` | Compose default |
| `CAPACITY` | `10` | `limit` in script |
| `WINDOW_SEC` | `60` | Window duration |

- Multi-replica quota proof uses sliding window + `ZCARD` verification (`docs/benchmarks/final-benchmark-report.md` §10).
- Compare `handler="check"` metrics when switching algorithms — denial rate semantics differ from token bucket.
- Enforcement test (`benchmarks/enforcement/enforcement-test.js`): ~8 actual RPS, **98% 429** for 500/min target — ~10 allowed per 60 s window.

## Performance implications

Benchmark commit `a1de9ec`, direct limiter `/check` (sliding):

| Target RPS | Actual RPS | p99 |
|------------|------------|-----|
| 1,000 | **871** | 7.98 ms |
| 5,000 | **285–1,504** (saturated) | 382 ms–51 s |

Sidecar e2e sustainable ceiling ~**872 RPS**, p99 **11 ms**, 0% non-429 errors.

**Test-proven invariants** (`redis_sliding_window_test.go`):

- Exactly `limit` allowed, `M-limit` denied under `M ≫ limit` concurrent goroutines; final `ZCARD == limit` (atomicity suite).
- First `limit` requests allowed, `limit+1` denied (exact exhaustion).
- Key A exhaustion does not affect key B (isolation).

## Lessons learned

Sliding window is the right default for demo compose because "10 per minute" is easy to explain in docs and runbooks. The ZSET cost is real — at saturation, Redis CPU and memory dominate before correctness breaks.

Keeping `sw:` separate from `rate:` prevents algorithm mix-ups in Redis CLI debugging. I always verify with `ZCARD sw:{user}` during incident response, not `HGETALL rate:{user}`.
