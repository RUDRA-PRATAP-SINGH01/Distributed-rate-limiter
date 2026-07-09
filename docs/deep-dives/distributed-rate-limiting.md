# Distributed Rate Limiting

## Problem Statement

I needed a rate limiter that works correctly when multiple sidecar instances sit in front of the same API fleet. A single-process token counter in memory is useless the moment you scale horizontally: each replica maintains its own count, so effective limits multiply by instance count. Worse, without atomicity, two sidecars can both read "one token left" and both allow a request that should have been denied.

The core problem is **shared, consistent quota state** across N stateless proxies, with sub-millisecond decision latency and predictable rejection semantics (HTTP 429, not silent over-admission).

## Why the problem exists

Distributed systems split traffic across replicas for availability and throughput. Rate limiting is inherently **stateful**. you must remember how many requests a tenant or user has consumed. That state cannot live only in process memory without either:

1. Sticky sessions (fragile, uneven load), or
2. A shared store every instance consults before forwarding.

Redis became the natural choice because it is already our coordination layer for idempotency, circuit breakers, and audit. The hard part is not "store a number in Redis" but doing **read-modify-write atomically** under concurrent load from dozens of sidecars.

I initially prototyped a Go-side GET → compute refill → SET flow in `internal/limiter/redis_token_bucket.go` (non-atomic path). Load tests at ~1,000 RPS showed occasional over-admission. exactly the race the comment in `redis_atomic_token_bucket.go` warns about.

## Design goals

1. One Lua `EVAL` per check; no split-brain between refill and deduct.
2. Multiple algorithms: Token bucket for smooth refill, sliding window for hard "N per minute" semantics, hierarchical for stacked quotas.
3. If we cannot verify quota, deny (503/429), never guess.
4. Observable: Every Redis round-trip records duration via `metrics.RecordRedisDuration` in the limiter packages.
5. Key isolation: Per-user keys like `rate:{userID}` and `sw:{userID}` to avoid cross-tenant bleed.

## Alternative approaches considered

| Approach | Why I rejected it |
|----------|-------------------|
| In-memory per sidecar + gossip | Eventually consistent; limits drift for minutes after scale events |
| Redis INCR without refill logic | Fixed windows only; burst at window boundaries |
| Separate rate-limit service (gRPC) | Extra hop, SPOF, harder to colocate with sidecar |
| Go-side transactions (WATCH/MULTI) | Contention under hot keys; Lua is simpler on Redis primary |
| Envoy/local rate limit only | No shared global/tenant caps across the mesh |

I kept the non-atomic `RedisTokenBucket` as a reference implementation but production paths use embedded Lua scripts.

## Final architecture

```
Client → Sidecar → [Lua EVAL on Redis primary] → allow/deny + remaining
                         ↓
              metrics.RecordRedisDuration
```

**Production limiters** (`internal/limiter/`):

- `RedisAtomicTokenBucket`. `lua/token_bucket.lua`, key prefix `rate:`
- `RedisSlidingWindow`. `lua/sliding_window.lua`, key prefix `sw:`, ZSET-based window
- `HierarchicalLimiter`. `lua/hierarchical.lua`, four stacked buckets in one script

Each limiter embeds its Lua via `//go:embed` and wraps `redis.NewScript(...).Run()`. The token bucket script reads `HMGET tokens,last_refill`, refills based on elapsed seconds, deducts if allowed, and returns `{allowed, remaining}`.

`HierarchicalLimiter.AllowWithParams` accepts dynamic capacities from admin overrides (`internal/override/override.go`) while preserving the same four-key atomic evaluation. partial commits across global/tenant/user/endpoint levels are impossible because decrement happens only after all four levels pass.

Sliding window uses `ZREMRANGEBYSCORE` + `ZCARD` + conditional `ZADD` with a unique member (`{now}:{nanos}`) to avoid ZADD score collisions under burst traffic.

## Tradeoffs

- Redis is the SPOF for admission, but that is acceptable because we fail-closed; chaos tests in `chaos/chaos_test.ps1` verify 503 behavior when Redis dies.
- Lua on primary only: replicas are not used for writes; Sentinel failover adds brief unavailability (see `sentinel-failover.md`).
- Second-granularity refill in token bucket: `time.Now().Unix()` means sub-second bursts are smoothed by capacity, not micro-bucketed.
- The sliding window is fixed-window-ish: the ZSET implementation trims by score; a true sliding log is more expensive in memory.

## Failure modes

1. Redis timeout: Sidecar returns error; metrics spike on `RecordRedisDuration`; clients see 503.
2. One `rate:power_user` becomes a single-shard bottleneck; `benchmarks/hot-key/hot-key-test.js` shows 99.9% rejection at 5,000 target RPS (correct behavior, but latency rises).
3. Clock skew: Sidecars pass `now` into Lua; skew across nodes can cause minor refill discrepancies (seconds-level).
4. Lua script cache miss: First call after deploy pays `SCRIPT LOAD` cost; negligible at steady state.
5. Over-admission if non-atomic path used: Operational risk if someone wires `RedisTokenBucket` instead of `RedisAtomicTokenBucket`.

## Operational concerns

- Monitor `rate_limiter_redis_duration_seconds` and limiter allow/deny ratio per `handler` label (`check` or `hierarchical`).
- Set `EXPIRE` on bucket keys (3600s in token bucket Lua) so abandoned users do not leak memory.
- Sliding window `expireSec` must be ≥ 1. Redis rejects sub-second TTL on some configs; `redis_sliding_window.go` enforces this.
- Capacity overrides via admin API must propagate to `AllowWithParams` atomically. never split override read from Lua call across requests.
- Run `benchmarks/enforcement/enforcement-test.js` after config changes to verify 500/min windows.

## Performance implications

From `benchmarks/summary.md`:

- We sustain ~1,000 actual RPS with p99 < 100 ms and 0% errors. That is our practical ceiling per sidecar+Redis pair.
- Beyond 5,000 target RPS, actual throughput plateaus (~1,353 RPS) with p99 at 3.5s and 10% errors. Redis single-threaded execution becomes the knee.
- Each allow costs one round-trip; hierarchical costs one script touching four keys. still one RTT but longer Lua CPU time on primary.

`internal/limiter/redis_atomic_token_bucket.go` normalizes Lua return types via `luaInt()` because RESP encodings differ by Redis version and driver. avoiding parse panics under load.

## Lessons learned

I underestimated how much **algorithm choice matters for product semantics**. Token bucket feels fair to users (smooth refill); sliding window is easier to explain in SLAs ("500 per minute"). Building both taught me to keep them as separate embeds rather than one mega-script.

The race in non-atomic GET/SET is not theoretical. I saw it in k6 runs. **If you need correctness, pay for Lua.** The ~0.1ms script cost is cheaper than explaining quota overruns to enterprise tenants.

Hierarchical limiting was the right place to spend atomicity budget: without `hierarchical.lua`, I'd need four sequential Redis calls and accept partial deduction bugs. One script, four keys, one decision. that invariant is worth the Lua complexity.

Finally, fail-closed is a product decision, not just an engineering default. Chaos tests (`chaos/chaos_test.ps1`, `chaos/network_partition.py`) are part of the contract. I run them before releases.
