# Redis Design

When I chose Redis as the coordination layer, I was not picking a cache — I was picking a **single-threaded per shard execution model** where Lua scripts give me compare-and-swap semantics without inventing my own distributed lock service. Every invariant that must survive concurrent sidecars, limiter replicas, and Sentinel failover lives in a namespaced key with an embedded script.

The layout is documented visually in [../diagrams/redis-layout.mmd](../diagrams/redis-layout.mmd). This document explains **why** each namespace exists and **why** I used HASH vs ZSET vs STRING.

---

## Problem Statement

I need a shared data store that supports:

- **Atomic quota checks** across N limiter processes (though I run one logical owner, HA matters).
- **Idempotency locks** with fencing tokens and response storage.
- **Circuit breaker state** shared between limiter (Redis target) and sidecar (central-limiter + gateway targets).
- **Gateway metrics** for weighted routing.
- **Audit events** with time-range search and retention caps.
- **Runtime configuration** overrides without redeploy.

All operations must be **O(1) or O(log N)** per request — no `KEYS *` on hot paths.

---

## Why the problem exists

Go mutexes do not cross processes. Two limiter pods calling `GET` then `SET` on token count would race. Redis `INCR` alone is insufficient for token bucket refill math — I need read-modify-write in one atomic step.

Similarly, idempotency **claim** and **complete** must be one transaction; otherwise two POST retries both see "no key" and double-execute upstream.

I ruled out PostgreSQL for hot-path quota: higher latency, connection overhead, and I'd still want advisory locks or stored procedures — reinventing Lua in SQL.

---

## Design goals

| Goal | Approach |
|------|----------|
| Atomicity | Lua via `redis.NewScript` + `EVALSHA` |
| Namespace isolation | Prefix per domain: `rate:`, `idem:`, `cb:`, `route:`, `audit:`, `config:`, `sw:` |
| TTL hygiene | `EXPIRE` on rate keys (3600 s); `PEXPIRE` on idempotency; audit trim in script |
| Sentinel HA | `UniversalClient` + `FailoverClient` — scripts unchanged after promotion |
| Observability | OpenTelemetry Redis instrumentation when `OTEL_ENABLED` |
| Memory bounds | Counter halving in circuit/routing scripts; audit `max_events` trim |

---

## Alternative approaches considered

### Redis transactions (MULTI/EXEC) without Lua

WATCH/MULTI works for optimistic locking but fails open under contention — retries add latency. Lua is one round-trip with server-side logic.

### Separate Redis databases per concern

DB 0 vs DB 1 simplifies mental model but not operations — same memory pressure, same failover. I use key prefixes instead.

### RediSearch / RedisJSON for audit

Powerful query UX; I chose HASH + ZSET indexes because append.lua already maintains retention atomically without another module dependency.

### ZSET for token bucket timestamps

That's my **sliding window** algorithm (`sw:{userID}`), not token bucket. Sliding window needs ordered event times; token bucket needs only two numbers (tokens, last_refill).

### Storing full HTTP bodies only in HASH

Large responses bloat HASH memory. I split `idem:body:{scope}:{key}` STRING when body exceeds `InlineThreshold` (default 64 KiB).

---

## Final architecture

### Key namespace catalog

#### Rate limiting — HASH (token bucket)

| Key pattern | Fields | Algorithm |
|-------------|--------|-----------|
| `rate:{userID}` | `tokens`, `last_refill` | Flat `/check` atomic token bucket |
| `rate:global` | same | Hierarchical level 0 |
| `rate:tenant:{tenantID}` | same | Hierarchical level 1 |
| `rate:user:{userID}` | same | Hierarchical level 2 |
| `rate:endpoint:{tenantID}:{path}` | same | Hierarchical level 3 |

Scripts: `internal/limiter/lua/token_bucket.lua`, `hierarchical.lua`

**Why HASH:** Two numeric fields update together every request. HMGET/HMSET in Lua is natural. No ordering semantics needed.

**TTL:** `EXPIRE key 3600` — cold keys evict after idle hour; active users refresh continuously.

#### Rate limiting — ZSET (sliding window)

| Key pattern | Members | Algorithm |
|-------------|---------|-----------|
| `sw:{userID}` | score = timestamp ms, member = unique id | `sliding_window.lua` |

**Why ZSET:** Window enforcement requires evicting events older than `windowStart` via `ZREMRANGEBYSCORE`, then counting with `ZCARD`. HASH cannot express time-ordered pruning.

**Selection:** `ALGORITHM=sliding` in limiter env switches implementation; compose demo uses sliding with `CAPACITY=10`, `WINDOW_SEC=60`.

#### Configuration overrides — HASH

| Key pattern | Fields |
|-------------|--------|
| `config:global:default` | `capacity`, `refill_rate` |
| `config:tenant:{id}` | same |
| `config:user:{id}` | same |
| `config:endpoint:{tenant\|path}` | same |

Read through `override.Store` with local `sync.Map` cache (`OVERRIDE_CACHE_TTL_MS`, default 5000 ms). Admin API on `:8082` writes; limiter merges on `/check_hierarchical`.

#### Idempotency — HASH + STRING

| Key pattern | Type | Purpose |
|-------------|------|---------|
| `idem:{scope}:{key}` | HASH | `status`, `request_hash`, `fence_token`, `lock_until`, `http_status`, `resp_headers`, `resp_body` or `body_ref` |
| `idem:body:{scope}:{key}` | STRING | External storage for large response bodies |

Scope = first 16 bytes of `SHA256(tenant|user)` as hex (32 chars).

Scripts: `claim.lua`, `complete.lua`, `fail.lua`

**Fence token flow:**

1. Go generates UUID per claim attempt.
2. `claim.lua` stores `fence_token` on new or reclaimed lock.
3. `complete.lua` / `fail.lua` require `fence_token` match and `status == processing`.
4. Stale completers get `{0}` → `ErrStaleFence` in Go.

**Statuses:** `processing` → `completed` | `failed`

#### Circuit breaker — HASH

| Key pattern | Fields (representative) |
|-------------|-------------------------|
| `cb:{target}` | `state`, `opened_at`, `half_open_at`, `half_open_calls`, `half_open_successes`, `success_count`, `failure_count`, `timeout_count`, `latency_spike_count`, `total_count`, `consecutive_failures`, `latency_ema_ms`, `updated_at` |

Well-known targets:

- `cb:redis` — limiter guards Redis health before quota Lua
- `cb:central-limiter` — sidecar guards HTTP to limiter
- `cb:{gatewayID}` — per-gateway during routing

Scripts: `allow.lua` (pre-call), `record.lua` (post-call)

States: `closed`, `open`, `half_open`

#### Routing — HASH + SET

| Key pattern | Type | Purpose |
|-------------|------|---------|
| `route:gw:{id}` | HASH | `url`, `weight`, `enabled`, `latency_ema_ms`, `success_count`, `error_count`, `total_requests`, `health_score`, `updated_at` |
| `route:index` | SET | All gateway IDs |

Script: `record_outcome.lua` — updates EMA, counters, health score; circuit `record.lua` called from Go afterward.

**Counter halving:** When `success_count + error_count > 1000`, script divides counts by 2 — prevents unbounded integer growth on long-lived gateways.

#### Audit — HASH + ZSET + STRING

| Key pattern | Type | Purpose |
|-------------|------|---------|
| `audit:event:{uuid}` | HASH | Full event record |
| `audit:idx:ts` | ZSET | Global time index (score = timestamp ms) |
| `audit:idx:tenant:{tenant}` | ZSET | Per-tenant index |
| `audit:idx:user:{user}` | ZSET | Per-user index |
| `audit:idx:req:{requestID}` | STRING | Points to latest event ID for request |

Script: `append.lua` — HSET event, ZADD indexes, retention purge, max_events trim with `purge_event` helper.

---

## Lua atomicity pattern

Every hot-path mutation follows the same template I copied across packages:

```
1. KEYS[1..N] declared — all Redis keys touched in one script
2. Read state with HGET/HMGET/ZCARD
3. Compute decision in Lua (no cross-request state in Go)
4. Write state with HSET/HINCRBY/ZADD in same script
5. Return compact numeric tuple — Go parses via luaInt/luaString helpers
```

**Why scripts, not pipelines:** Pipelines batch network but do not atomicize across commands from other clients.

**Script loading:** Go 1.16+ `//go:embed lua/*.lua` → `redis.NewScript` caches SHA on first `EVAL`.

### Token bucket script (excerpt logic)

1. HMGET `tokens`, `last_refill`
2. Initialize to capacity if nil
3. Refill: `tokens + elapsed * refill_rate`, cap at capacity
4. If `tokens >= requested`, decrement
5. HMSET + EXPIRE
6. Return `{allowed, remaining}`

### Hierarchical script (two-phase)

1. Loop 4 keys: refill each, track `min_remaining`, fail if any level `< 1`
2. If all pass, decrement each by 1
3. Return `{allowed, remaining}` where remaining reflects tightest bucket

**Design note:** Failed requests still write refilled token counts but do not decrement — user sees 0 remaining when any level blocks.

### Idempotency claim outcomes

| Return code | Meaning |
|-------------|---------|
| `{1, fence}` | Claimed |
| `{2, status, headers, body}` | Replay |
| `{3, retry_ms}` | In progress |
| `{0}` | Hash mismatch |

### Circuit allow.lua

- Missing key → initialize closed
- Open + cooldown elapsed → transition half_open, allow probe
- Half_open + probes exhausted → reopen
- Half_open + under probe budget → increment `half_open_calls`, allow

### Audit append.lua retention

1. Write event HASH + EXPIRE
2. ZADD to ts/tenant/user indexes
3. SET request index if request_id present
4. `ZRANGEBYSCORE` delete stale by retention window
5. While `ZCARD > max_events`, purge oldest from ts index

---

## Tradeoffs

**Single Redis cluster is a blast radius.** Sentinel mitigates node failure, not regional loss. Multi-region would need CRDT or divided keyspaces — out of scope.

**Lua CPU on hot keys** — one shard handles all `rate:global` traffic in hierarchical mode. I set generous global capacity so the key is rarely the bottleneck; tenant/user keys shard naturally.

**KEYS in admin only** — `circuitbreaker.ListTargets` uses `SCAN cb:*` for admin API, not request path.

**No Redis Cluster hash-tagging yet** — all keys could land on one slot in cluster mode. For cluster deployment I'd tag `{tenant}:rate:...`.

**Script versioning** — changing Lua without flush can leave mixed SHA behavior during deploy. I deploy limiter + sidecar together.

---

## Failure modes

| Event | Impact |
|-------|--------|
| Master failover | Sub-second errors; circuits may open; scripts retry on new master |
| Replication lag | Read-your-writes not guaranteed on replicas — I only write to master via client |
| MEMORY max | Redis evicts per policy — volatile keys with TTL safer than unbounded indexes |
| Script error | Request fails; Go returns 503/500; circuit records failure |
| Fence stale complete | Idempotent retry after reclaim cannot poison completed entry |
| Audit queue drop | Event lost; indexes not updated — enforcement unaffected |
| Halving counters | Sudden error rate appearance changes — smoothed EMA compensates |

---

## Operational concerns

- **Password:** `REDIS_PASSWORD` in compose; Sentinel uses `masterauth` in `deploy/redis/*.conf`.
- **Memory estimate:** Audit `AUDIT_MAX_EVENTS=100000` caps ts index; idempotency `IDEMPOTENCY_COMPLETED_TTL_MS` default 24 h.
- **Monitoring:** `redis.connected` via `/health`; track `circuit_redis_duration`, `idempotency_redis_duration` histograms.
- **Backup:** RDB/AOF for audit compliance — rate keys are ephemeral by design.
- **KEYS to inspect:** Documented prefixes above — use `SCAN` with pattern in ops runbooks.

### Sentinel deployment

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build
```

Env on limiter/sidecar:

```
REDIS_MODE=sentinel
REDIS_MASTER_NAME=mymaster
REDIS_SENTINEL_ADDRS=redis-sentinel-1:26379,...
```

`internal/redis.New` → `FailoverClient` — application code identical.

---

## Performance implications

| Operation | Round-trips | Notes |
|-----------|-------------|-------|
| Flat allow | 1 × EVALSHA | Dominant prod cost |
| Hierarchical allow | 1 × EVALSHA | 4× logic, same RTT |
| Sliding window | 1 × EVALSHA | ZSET trim + add |
| Idempotency claim | 1 × EVALSHA | Replay avoids downstream |
| Circuit allow + record | 2 × EVALSHA | allow before, record after |
| Audit (async) | 1 × EVALSHA | Off hot path in worker |

Pool tuning: `PoolSize=100`, `MinIdleConns=10` per process.

Halving at 1000 samples keeps HASH fields small — O(1) HGETALL on circuit snapshots.

---

## Lessons learned

1. **Pick the Redis type from the question.** "How many events in the last 60 seconds?" → ZSET. "How many tokens remain?" → HASH.

2. **Return tuples, not JSON from Lua** — less parsing CPU in Go and stable across redis-py/go-redis type coercion.

3. **Initialize-on-miss inside scripts** — first request for a user does not need a separate `SETNX` in Go.

4. **External body STRING saved my MEMORY dashboards** — 1 MB JSON responses in HASH were killing `MEMORY USAGE`.

5. **Audit trim in the same script as append** — async workers racing separate trim jobs caused index/event orphans.

6. **Fence tokens are not optional** for lock reclaim — without them I reproduced double-complete in `benchmarks/idempotency/idempotency-race.js`.

---

## Related documents

- [overview.md](./overview.md) — system context
- [request-flow.md](./request-flow.md) — when each script fires
- [routing-architecture.md](./routing-architecture.md) — `route:gw:*` in selection loop
