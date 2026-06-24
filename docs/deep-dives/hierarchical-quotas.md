# Hierarchical Quotas — Engineering Journal

## Problem Statement

Flat per-user rate limits are insufficient for multi-tenant APIs. A single abusive user must not exhaust the entire platform capacity, and a single tenant must not starve others — yet each user still needs a personal budget, and sensitive endpoints may need tighter caps than the tenant default.

I needed **stacked quotas**: global → tenant → user → endpoint, where a request is allowed only if **every** level has capacity. Partial approval (deduct at user level but fail at global) is unacceptable.

## Why the problem exists

Quota hierarchies mirror organizational reality:

- **Global** protects shared infrastructure (Redis, upstream gateways).
- **Tenant** enforces commercial plan limits.
- **User** prevents one API key from monopolizing a tenant.
- **Endpoint** applies fine-grained policy (`/export` vs `/health`).

Naive implementations check levels sequentially in Go:

```
if !global.Allow() { deny }
if !tenant.Allow() { deny }  // global already deducted — bug
```

Any multi-step deduction without rollback creates **partial commit** bugs. Under concurrency, two requests can pass global and tenant checks interleaved, exceeding tenant cap even when global has room.

## Design goals

1. **All-or-nothing atomicity** — refill, check, and deduct all four levels in one Redis script.
2. **Dynamic overrides** — admin can change tenant/user caps without redeploy; `AllowWithParams` accepts runtime capacities.
3. **Single RTT** — one `EVAL` per hierarchical check, same as flat token bucket.
4. **Meaningful `remaining` header** — return the tightest bottleneck (minimum remaining across levels).
5. **Consistent algorithm** — token bucket math at each level (capacity + refill rate).

## Alternative approaches considered

| Approach | Issue |
|----------|-------|
| Four sequential Lua scripts | Four RTTs; partial deduct if later level fails |
| Four INCR keys with rollback script | Rollback races under failure |
| Nested keys (tenant embeds users) | Redis has no native hierarchy; awkward eviction |
| Check all in Go, deduct in pipeline | Pipeline is not atomic across competing clients |
| Separate services per level | Latency stack; operational nightmare |

I chose one Lua script (`internal/limiter/lua/hierarchical.lua`) invoked by `HierarchicalLimiter` in `internal/limiter/hierarchical.go`.

## Final architecture

```
Allow(globalKey, tenantKey, userKey, endpointKey)
        │
        ▼
HierarchicalLimiter.AllowWithParams
        │
        ▼
EVAL hierarchical.lua
  KEYS[1..4] = global, tenant, user, endpoint
  ARGV[1..4] = capacities
  ARGV[5..8] = refill_rates
  ARGV[9] = now (unix sec)
  ARGV[10] = tokens requested (1)
```

**Phase 1 (lines 12–49):** For each level, `HMGET tokens,last_refill`, compute refill, floor tokens, check `>= 1`, track `min_remaining`, write refilled state with `HMSET` + `EXPIRE 3600`.

**Phase 2 (lines 51–63):** If `allowed == 1`, decrement each level by 1; else `remaining = 0`.

Return `{allowed, remaining}` where `remaining = floor(min_remaining - 1)` on success.

Key construction happens in the sidecar handler layer (not in the limiter package) — typically scoped Redis keys per dimension. Overrides from `internal/override/override.go` feed into `AllowWithParams` so a support engineer can bump `tenantCap` without restarting pods.

## Tradeoffs

- **Refill-then-check-then-deduct writes even on deny** — Phase 1 updates all bucket hashes even when one level is empty. This refreshes `last_refill` on denied requests, which slightly advances refill clocks. I accepted this for script simplicity; alternative "dry run" paths doubled script size.
- **Four keys on one shard** — Redis Cluster requires hash-tag alignment (`{tenant}:global` patterns) if levels must co-locate; standalone/Sentinel mode has no issue.
- **Uniform token bucket at all levels** — sliding window at endpoint-only granularity would need a different script; not mixed today.
- **Integer flooring** — `math.floor(new_tokens)` means fractional refill tokens accumulate until whole.

## Failure modes

1. **Wrong key count** — `AllowWithParams` returns error if `len(keys) != 4`; prevents silent misconfiguration.
2. **Override race** — admin changes tenant cap between read and Lua call is fine (cap is passed in ARGV); stale override cache in sidecar is the real risk — mitigate with short TTL on override reads.
3. **Global exhaustion with healthy user buckets** — correct behavior; users see 429 with `remaining: 0` even if their personal bucket has tokens.
4. **Lua timeout** — four HMGET/HMSET loops on a slow primary block other commands; watch `slowlog`.
5. **Key TTL expiry mid-request** — `EXPIRE 3600` on each level; idle keys disappear; next request re-initializes to full capacity (burst on return).

## Operational concerns

- Log which level denied when possible — `audit` store records handler `hierarchical` and `remaining` (see `internal/audit/store.go`).
- Document override precedence: endpoint-specific overrides should map to `endpointCap` in `AllowWithParams`, not a fifth level.
- When debugging "unfair" 429s, dump all four key states in Redis CLI: `HGETALL` each key.
- Run `benchmarks/enforcement/enforcement-test.js` for flat limits; hierarchical needs dedicated test vectors per level.

## Performance implications

One Lua invocation vs four sequential calls saves ~3 RTTs (~3ms at p99). Script CPU is O(4) hash operations — negligible compared to network until Redis saturates.

`benchmarks/summary.md` knee at ~1,000 RPS applies to hierarchical checks too; each request still hits primary once but with ~4× the Lua work of flat bucket. Expect slightly lower max RPS for hierarchical endpoints vs simple `/check`.

`metrics.RecordRedisDuration` in `hierarchical.go` captures end-to-end script latency — compare `handler=hierarchical` vs `handler=token_bucket` in dashboards.

## Lessons learned

The hierarchical problem is really a **distributed transaction** problem with a trivial isolation model: all keys are quota keys, ordering is fixed, rollback is "don't decrement." Lua made that transaction obvious.

I initially wanted different algorithms per level (global leaky bucket, user token bucket). Uniform math in `hierarchical.lua` reduced cognitive load for SRE and support — one set of knobs: `capacity` and `refill_rate`.

The `min_remaining` header was a product win. Users stop asking "which limit hit me?" when support can read the smallest remaining value.

Most importantly: **never deduct outside Lua.** The comment on `HierarchicalLimiter` — "partial commits are impossible" — is the invariant I defend in code review.
