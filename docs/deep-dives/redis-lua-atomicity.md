# Redis Lua Atomicity

## Problem Statement

Nearly every correctness-critical path in this system. rate limiting, idempotency claims, circuit breaker transitions, audit appends, routing outcome recording. depends on **atomic read-modify-write** in Redis. Without atomicity, concurrent sidecars produce duplicate upstream calls, quota overruns, double circuit opens, and torn audit indexes.

I needed a single pattern: embed Lua, ship via `//go:embed`, execute with `redis.NewScript`, parse heterogeneous return values safely.

## Why the problem exists

Redis commands are individually atomic, but business operations are multi-step:

```
tokens = GET key
tokens = refill(tokens)
if tokens >= 1: SET key tokens-1
```

Two clients interleaving this sequence both observe `tokens == 1` and both deduct. Redis offers three realistic fixes:

1. WATCH/MULTI/EXEC: optimistic locking. retries under contention.
2. Lua scripting: Server-side atomicity, no retry loop.
3. Redis Functions: (7+). module-like, heavier ops story.

At our scale (single primary, Sentinel HA), Lua on the primary is the sweet spot. Every subsystem hit the same wall independently, which is why `internal/limiter/`, `internal/idempotency/`, `internal/circuitbreaker/`, `internal/audit/`, and `internal/routing/` all embed `.lua` files.

## Design goals

1. No splitting claim vs complete across round trips without fences.
2. Embedded scripts: Versioned with Go binary; no drift between deployed code and Redis `SCRIPT LOAD` cache.
3. Defensive parsing: `luaInt()` / `luaString()` helpers normalize RESP types across redis-go versions.
4. KEYS vs ARGV discipline: keys for shard routing in Cluster; all tunables in ARGV.
5. Instrumented latency: Each store records Redis duration to subsystem metrics.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Redlock + Go logic | Complex; Martin Kleppmann's critique applies; still need atomic write |
| MULTI/EXEC without WATCH | Last writer wins. same race |
| Separate Redis per concern | Ops burden; doesn't fix per-key races |
| CRDT counters | Overkill; eventual consistency wrong for hard limits |
| PostgreSQL advisory locks | Wrong store; adds latency |

Lua won consistently.

## Final architecture

**Script inventory:**

| Package | Script | Purpose |
|---------|--------|---------|
| `internal/limiter/lua/token_bucket.lua` | Refill + deduct one bucket |
| `internal/limiter/lua/sliding_window.lua` | ZSET trim + count + add |
| `internal/limiter/lua/hierarchical.lua` | Four-level token buckets |
| `internal/idempotency/lua/claim.lua` | Claim / replay / in-progress |
| `internal/idempotency/lua/complete.lua` | Store response with fence check |
| `internal/idempotency/lua/fail.lua` | Mark failed with fence check |
| `internal/circuitbreaker/lua/allow.lua` | Open/half-open gating |
| `internal/circuitbreaker/lua/record.lua` | EMA + state transitions |
| `internal/audit/lua/append.lua` | Event + indexes + retention trim |
| `internal/routing/lua/record_outcome.lua` | Gateway EMA/latency update |

**Go invocation pattern** (example from `redis_atomic_token_bucket.go`):

```go
//go:embed lua/token_bucket.lua
var luaScript string

script := redis.NewScript(luaScript)
result, err := script.Run(ctx, rdb, []string{key}, args...).Result()
```

`redis.NewScript` handles `EVALSHA` with fallback to `EVAL`. scripts are cached on the server by SHA.

**Atomicity guarantee:** Redis executes the entire Lua body without interleaving other commands. Replication sends the script effect as one atomic unit on the primary (replica lag is a separate concern. we never read quota from replicas).

## Tradeoffs

- Primary CPU concentrates on hot keys: one thread serializes Lua execution, so script length matters.
- If KEYS span slots, `CROSSSLOT` errors occur; we assume standalone or correctly tagged keys.
- Debugging: Stack traces are Lua, not Go; logging must happen in Go around `Run()`.
- Script evolution: Changing Lua without deploy coordination leaves old SHA on long-lived Redis; embed ensures binary/script match after deploy.
- Blocking: Long loops in Lua block Redis; `append.lua` retention purge loops are bounded by `max_events`.

## Failure modes

1. NOSCRIPT: Rare after `SCRIPT FLUSH`; go-redis retries with full script body.
2. Lua runtime error: Bad ARGV types; Go returns wrapped error; idempotency maps to `ErrStoreUnavailable`.
3. OOM on large ZSET purge: `append.lua` purges stale events in-loop; huge retention backlog can spike latency.
4. Fence stale complete: `complete.lua` returns `{0}` if fence mismatch; Go returns `ErrStaleFence`. correct, not a Redis failure.
5. Wrong integer parsing: Without `luaInt()`, `float64` vs `int64` mismatches cause silent `allowed=false`.

## Operational concerns

- Watch Redis `slowlog-get` for scripts > 10ms.
- After Lua changes, deploy sidecars before relying on new return codes. old binaries + new manual script upload is unsupported (we don't upload out-of-band).
- `SCRIPT EXISTS` in staging to verify SHA loaded after deploy.
- Pool sizing in `internal/redis/client.go` (`PoolSize` default 100). each blocked Lua call holds a connection.
- Failover during `EVAL`. command may fail; client retries; idempotency claim may return `in_progress` or reclaim. safe by design.

## Performance implications

Each atomic op is **one round-trip**. Hierarchical limiter trades script CPU for 3 saved RTTs vs naive sequential scripts.

Benchmarks (`benchmarks/summary.md`): system knee ~1,000 RPS. Lua is not free but RTT dominates until saturation.

`go test -bench=. ./internal/audit/...` and `./internal/circuitbreaker/...` micro-benchmark script paths in isolation.

Idempotency race test (`benchmarks/idempotency/idempotency-race.js`): 100 VUs, 1 key → **1 upstream execution**. proof Lua claim atomicity works under contention.

## Lessons learned

Copy-paste `luaInt()` into every package felt wrong, but a shared `internal/lua` import would create coupling. I kept helpers local. duplication beats import cycles.

The idempotency trilogy (`claim`, `complete`, `fail`) taught me **fence tokens must be checked inside Lua**, not in Go between calls. Any check outside the script is a race window.

`audit/lua/append.lua` is the most complex script. multi-key index maintenance + retention purge. I would split purge to async workers if `max_events` trim becomes hot, but then atomicity of "append + index" breaks unless I use a stream consumer pattern.

**Rule I enforce in review:** if you have `if redis.call` followed by another `redis.call` that depends on the first, it belongs in Lua.
