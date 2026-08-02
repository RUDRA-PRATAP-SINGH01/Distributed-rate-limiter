# Hierarchical Rate Limiting

## Problem Statement

Multi-tenant APIs need **stacked quotas**: global → tenant → user → endpoint. A request is allowed only if **every** level has capacity. Partial deduction (pass global, fail tenant, global already charged) is a correctness bug.

I enforce all four levels in one Lua script (`hierarchical.lua`) via `HierarchicalLimiter` (`internal/limiter/hierarchical.go`). Exposed on `GET /check_hierarchical` with admin override merge (`effectiveHierarchicalLimits` in `cmd/limiter/admin_api.go`).

## Why the problem exists

Flat per-user limits cannot protect shared infrastructure or commercial tenant tiers. Naive sequential checks in Go:

```
if !global.Allow() { deny }
if !tenant.Allow() { deny }  // global already deducted — bug
```

Under concurrency, interleaved sequential scripts produce partial commits and quota overruns. This is a distributed transaction problem; Lua provides fixed ordering with all-or-nothing deduct.

## Design goals

| Goal | Implementation |
|------|----------------|
| All-or-nothing | Refill all 4 → check all 4 → deduct all 4 or none |
| One RTT | Single `hierarchical.lua` EVAL |
| Dynamic caps | `AllowWithParams` accepts runtime capacities from overrides |
| Bottleneck visibility | `remaining = floor(min_remaining - 1)` on success |
| Key namespacing | Four distinct `rate:*` keys per request path |
| Strict arity | `len(keys)==len(capacities)==len(refillRates)==4` or error |

## Alternative approaches considered

| Approach | Issue |
|----------|-------|
| Four sequential Lua scripts | Four RTTs; partial deduct if later level fails |
| Check in Go, deduct in pipeline | Pipeline not atomic across competing clients |
| Four INCR with rollback | Rollback races |
| Mixed algorithms per level | Operational complexity; uniform token math chosen |

One four-key script is the production design.

## Final architecture

**Four Redis keys** (HASH, same schema as flat token bucket):

| Level | Key pattern | Example |
|-------|-------------|---------|
| Global | `rate:global` | Shared across all traffic |
| Tenant | `rate:tenant:{tenantID}` | Per-tenant bucket |
| User | `rate:user:{userID}` | Per-user within tenant |
| Endpoint | `rate:endpoint:{tenantID}:{path}` | Per-route cap |

Constructed in `cmd/limiter/main.go`:

```go
globalKey := "rate:global"
tenantKey := fmt.Sprintf("rate:tenant:%s", tenantID)
userKey := fmt.Sprintf("rate:user:%s", userID)
endpointKey := fmt.Sprintf("rate:endpoint:%s:%s", tenantID, endpoint)
```

**Script contract** (`internal/limiter/lua/hierarchical.lua`):

```
KEYS[1..4]   = global, tenant, user, endpoint
ARGV[1..4]   = capacities
ARGV[5..8]   = refill_rates
ARGV[9]      = client now (ms) — unused; Lua uses redis.call('TIME')
ARGV[10]     = requested (1)
```

**Phase 1 — refill and check (lines 15–53):**

For each level `i`:

1. `HMGET tokens,last_refill`; initialize to `capacity` if missing.
2. Refill: `new_tokens = min(capacity, tokens + elapsed * refill_rate)`.
3. If `floor(new_tokens) < requested` → `allowed = 0`.
4. Track `min_remaining = min(floor(new_tokens))`.

**Phase 2 — write back (lines 55–72):**

- If `allowed == 1`: decrement each level by `requested`; `remaining = floor(min_remaining - 1)`.
- If `allowed == 0`: write refilled state **without deduct**; `remaining = 0`.

Return `{allowed, remaining}`.

**Override merge** (`effectiveHierarchicalLimits`):

1. `store.RefreshGeneration(ctx)` — invalidate local cache if `config:generation` advanced.
2. Merge `config:global:default`, `config:tenant:{id}`, `config:user:{id}`, `config:endpoint:{tenant|path}` over env defaults.
3. Pass merged capacities/rates as ARGV into Lua.

## Tradeoffs

- **Refill-on-deny writes:** Phase 1 updates all four hashes even when one level is empty — advances `last_refill` on denied requests for script simplicity.
- **Four keys, one shard:** Redis Cluster would need hash tags to co-locate keys; default Compose/Sentinel assumes single primary (SOURCE-PROVEN limitation).
- **Uniform token math:** All levels use token bucket, not sliding window — endpoint-hard windows would need a different script.
- **Telemetry gap:** Audit records `handler=hierarchical` but not **which** level denied.
- **Flat `/check` ignores overrides:** Only hierarchical path reads `config:*` overrides (SOURCE-PROVEN).

## Failure modes

1. **Wrong key count:** `AllowWithParams` returns error if arity ≠ 4.
2. **Global exhaustion:** User bucket may have tokens but request still denied — correct behavior.
3. **Override staleness:** If `GET config:generation` fails, local cache may persist until `OVERRIDE_CACHE_TTL_MS` (default 5000 ms).
4. **Idle key TTL:** `EXPIRE 3600` per level; expired keys re-init to full capacity on next hit.
5. **Redis error:** **503** `"Hierarchical rate limiter unavailable"` — distinct from quota **429**.

## Operational concerns

| Variable | Default (compose) | Level |
|----------|-------------------|-------|
| `ENABLE_HIERARCHICAL` | `true` | Feature flag |
| `GLOBAL_CAPACITY` | `1000000` | Level 0 |
| `TENANT_CAPACITY` | `100000` | Level 1 |
| `USER_CAPACITY` | `100` | Level 2 |
| `ENDPOINT_CAPACITY` | `10` | Level 3 (often tightest) |

- Admin writes bump `config:generation` atomically with `HSET` (`override.SetOverride` pipeline).
- Cross-replica visibility: next `/check_hierarchical` after successful `RefreshGeneration` — not fixed TTL wait.
- Debug unfair 429s: `HGETALL` each of the four keys for the request path.
- Sidecar hierarchical mode calls `/check_hierarchical?endpoint={path}` (`cmd/sidecar/main.go`).

## Performance implications

Benchmark `a1de9ec`, hierarchical `/check_hierarchical` at 1,000 target:

| Metric | Value |
|--------|-------|
| Actual RPS | **870** |
| p99 | 34 ms |
| 429 rate | High (endpoint cap=10 in test config) |

One RTT vs four sequential scripts saves ~3 ms at p99. Lua CPU is O(4) hash ops — negligible until Redis saturates.

**Test-proven invariants** (`hierarchical_test.go`):

- `TestHierarchical_AllOrNothing_NoPartialDeductions` — reject at any level → zero deduction at all levels.
- `TestHierarchical_GlobalContention` — 50 paths, global cap 20 → exactly 20 allowed globally.
- `TestHierarchical_SamePathContention_BottleneckMatches` — user cap 5, 50 goroutines → exactly 5 allowed.
- `TestHierarchical_MultiBottleneck` — endpoint cap 7 is tightest → exactly 7 allowed.

## Lessons learned

Hierarchical limiting is a **distributed transaction** with trivial rollback: "don't decrement." Lua makes that obvious in code review.

I initially wanted different algorithms per level. Uniform token math reduced SRE cognitive load — one knob pair (`capacity`, `refill_rate`) at each tier.

**Never deduct outside Lua.** The comment on `HierarchicalLimiter` — "partial commits are impossible" — is the invariant I defend in every review.

## Topology constraints & Redis Cluster (H-01)

Because `hierarchical.lua` atomically checks and deducts four distinct keys (`rate:global`, `rate:tenant:...`, `rate:user:...`, `rate:endpoint:...`), Redis Cluster produces a `CROSSSLOT` error unless all four keys share an identical hash slot. 

Therefore:
- **Hierarchical limiting requires Standalone Redis or Sentinel-managed failover** (`REDIS_MODE=standalone` or `REDIS_MODE=sentinel`).
- **Cluster fail-fast:** `cmd/limiter` validates this at startup. If `REDIS_MODE=cluster` and `ENABLE_HIERARCHICAL=true`, the process terminates immediately at boot with a fatal error.
- **Cluster flat mode:** To run against a Redis Cluster, set `ENABLE_HIERARCHICAL=false`; single-key `/check` operates safely across cluster shards.
