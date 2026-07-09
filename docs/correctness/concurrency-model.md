# Concurrency Model

## Problem Statement

The system runs highly concurrent HTTP handlers across multiple processes. I need a clear model of **what serializes where**, which races are impossible by design, and which optimizations only reduce load without changing semantics.

This document covers Lua atomicity, sidecar singleflight, `sync.Map` caches, and audit mutex boundaries.

## Why the problem exists

Go's memory model gives goroutine safety inside one process, but sidecars and limiters are separate OS processes. Concurrent requests for the same user can arrive at:

- Different goroutines on the same sidecar
- Different sidecar replicas
- Different limiter replicas

Without explicit serialization at the shared store, quota races return.

## Design goals

| Layer | Serialization mechanism | Scope |
|-------|------------------------|-------|
| Quota state | Redis Lua (single-threaded primary) | Global |
| Concurrent limiter misses (same key) | `singleflight.Group` | Per sidecar process |
| Denial / override cache | `sync.Map` | Per process |
| Audit async lifecycle | `sync.Mutex` + `sync.WaitGroup` | Per limiter process |
| HTTP handler safety | Standard Go net/http per-connection goroutines | Per process |

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| `sync.Mutex` around Redis calls in sidecar | Does not coordinate across replicas |
| Channel per user in sidecar | Memory leak; doesn't cross processes |
| Redis WATCH/MULTI | Retry storms under hot keys |
| Global lock service | Extra dependency; latency |

Lua + selective process-local dedup won.

## Final architecture

### Redis Lua atomicity

Redis executes each script atomically — no interleaving of other commands during `EVAL`.

**Quota scripts:**

| Script | Keys touched | Atomic unit |
|--------|--------------|-------------|
| `token_bucket.lua` | 1 × `rate:{id}` | Refill + check + deduct |
| `sliding_window.lua` | 1 × `sw:{id}` | Prune + count + conditional add |
| `hierarchical.lua` | 4 × `rate:*` | Refill/check all → deduct all or none |

**Other atomic scripts:** `claim.lua`, `complete.lua`, `allow.lua` (circuit breaker), `append.lua` (audit).

Go pattern:

```go
//go:embed lua/token_bucket.lua
var luaScript string
script := redis.NewScript(luaScript)
result, err := script.Run(ctx, rdb, keys, args...)
```

`redis.NewScript` uses `EVALSHA` with `EVAL` fallback. Parsing via `luautil.LuaInt` avoids RESP type mismatches.

**Proof:** `TestTokenBucket_Atomicity_30Goroutines` — 30 goroutines, cap=10 → exactly 10 wins (TEST-PROVEN). Same pattern for sliding window ZCARD and hierarchical all-or-nothing suites.

### Sidecar singleflight

```go
type Sidecar struct {
    cache       sync.Map
    limitFlight singleflight.Group
}
```

On cache miss (`serveNormal`):

```go
resultAny, err, _ := s.limitFlight.Do(cacheKey, func() (interface{}, error) {
    return s.checkRateLimit(ctx, r, userID, false)
})
```

**Semantics:**

- Concurrent requests sharing `cacheKey` execute **one** limiter HTTP call.
- All waiters receive the same `limitResult`.
- Different keys run in parallel — `TestSidecar_SingleflightKeyIsolation`: 100×user-A + 100×user-B → exactly **2** limiter calls.

**Not a correctness requirement for quota:** Even without singleflight, Lua would still admit ≤cap. Singleflight reduces Redis/HTTP amplification.

**Tests:**

- `TestSidecar_SingleflightCollapse` — 100 concurrent → 1 limiter call (TEST-PROVEN).
- `TestSidecar_ConcurrentDenialCacheMiss` — 50 concurrent denials → 1 limiter call, 0 upstream (TEST-PROVEN).

### sync.Map caches

**Sidecar denial cache** (`cache sync.Map`):

- Stores `CacheEntry{Allowed, Remaining, Limit, RetryAfter, ExpiresAt}`.
- Concurrent `Load`/`Store` safe without external mutex.
- Only **denials** served from cache; allows ignored on hit.
- Sweeper goroutine `Range`+`Delete` for TTL eviction.

**Override store** (`internal/override/override.go`):

```go
type Store struct {
    cache sync.Map // keyed by config:{level}:{id}
    localGeneration atomic.Int64
}
```

- `RefreshGeneration` may `Range`+`Delete` entire cache on generation bump.
- `getOverride` uses load-or-fetch from Redis; concurrent readers safe.
- Generation stored in `atomic.Int64` — lock-free read on hot path.

**Cache key isolation** (hierarchical sidecar):

```go
return tenantID + "|" + userID + "|" + r.URL.Path
```

Pipe separator prevents `tenant="ab", user="c"` colliding with `tenant="a", user="bc"` (`TestSidecar_CacheIsolation`).

### Audit mutex model

`audit.Store` (`internal/audit/store.go`):

```go
type Store struct {
    mu     sync.Mutex      // state + queue send
    shutMu sync.Mutex      // Shutdown serialization
    wg     sync.WaitGroup  // worker drain
    state  storeState      // running | shuttingDown | stopped
}
```

**Async Record path:**

1. `mu.Lock` → check `state == stateRunning`.
2. Non-blocking `select` on `queue` → drop if full (`audit_dropped_total`).
3. Worker goroutines read queue, call `record()` → `append.lua`.

**Shutdown path** (`shutdown.go`):

- `shutMu` ensures one shutdown waiter.
- `shutdownBeginOnce` closes queue exactly once.
- `waitWorkers` blocks on `wg.Wait()` or context deadline.
- `RedisCloseSafe()` reads `state` under `mu`.

**Tests:** `TestShutdown_ConcurrentRecordNoPanic`, `TestShutdown_Idempotent` (4 parallel `Shutdown` calls), `TestShutdown_HighContentionRaceStress` (TEST-PROVEN, `-race`).

## Tradeoffs

- **singleflight thundering herd on different keys:** Only per-key collapse — 10,000 unique users → 10,000 flights.
- **sync.Map vs RWMutex:** Chosen for read-heavy cache; no strong size bounds — sweeper mitigates.
- **Audit drops under load:** `select default` on full queue — quota path unaffected, audit loss possible.
- **Lua blocks Redis:** Long scripts (audit append + trim) delay other keys on same primary.

## Failure modes

1. **singleflight shared panic:** Panic propagates to all waiters — rare; limiter errors return normally.
2. **Stale denial cache entry:** TTL-bound; may extend 429 briefly after refill — under-admit only.
3. **Generation refresh during Range delete:** Brief window where cache empty → extra Redis reads, not wrong values.
4. **Shutdown timeout:** Workers may survive past deadline; Redis close skipped (`RedisCloseSafe` false).
5. **Hot key Lua queueing:** Latency spikes, not quota violations.

## Operational concerns

- Run `go test -race ./cmd/sidecar/... ./internal/audit/... ./internal/circuitbreaker/...` in CI.
- `singleflight.js` k6 burst — functional load, not instrumented for call count at runtime.
- Monitor `rate_limiter_cache_hits_total` vs misses on sidecar.
- Do not replace `sync.Map` denial cache with a mutex map without reviewing sweeper concurrency.

## Performance implications

| Mechanism | Effect |
|-----------|--------|
| Lua atomicity | Correctness baseline; ~1 RTT per admit |
| singleflight | 100 concurrent same user → 1 limiter RTT |
| Denial cache | 618k cached 429s at p99 7 ms (hammer test) |
| Override sync.Map | Avoids Redis `HGETALL` on every hierarchical read when generation unchanged |

Redis single-threaded execution is the global ceiling (~870 RPS sustainable in benchmarks).

## Lessons learned

I initially thought singleflight was a **correctness** feature. It is a **load** feature. Lua provides correctness; singleflight saves money.

`sync.Map` for denial cache was controversial in review — a regular map+RWMutex is easier to reason about. `sync.Map` won for read-heavy abuse patterns without manual shard locking.

Audit mutex complexity (`mu` + `shutMu` + `workersOnce`) looks heavy, but `TestShutdown_RedisCloseOrdering` proved why: closing Redis early is worse than the mutex cost.
