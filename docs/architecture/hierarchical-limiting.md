# Hierarchical Limiting Architecture

Hierarchical rate limiting evaluates **four stacked token buckets** in a single Redis Lua round-trip. Tokens are deducted from a level only if all levels approve — **partial commit impossible**.

---

## Four-level merge

```mermaid
flowchart TB
  REQ["/check_hierarchical"] --> MERGE["effectiveHierarchicalLimits()"]
  MERGE --> L0["L0: global — rate:global"]
  MERGE --> L1["L1: tenant — rate:tenant:{tenantID}"]
  MERGE --> L2["L2: user — rate:user:{userID}"]
  MERGE --> L3["L3: endpoint — rate:endpoint:{tenant}:{path}"]
  L0 & L1 & L2 & L3 --> LUA["hierarchical.lua — single EVALSHA"]
  LUA -->|allowed=1| OK[200 + remaining]
  LUA -->|allowed=0| DENY[429]
```

### Level semantics

| Level | Redis key | Default source | Override key |
|-------|-----------|----------------|--------------|
| 0 Global | `rate:global` | `GLOBAL_CAPACITY`, `GLOBAL_REFILL_RATE` | `config:global:default` |
| 1 Tenant | `rate:tenant:{tenantID}` | `TENANT_*` env | `config:tenant:{tenantID}` |
| 2 User | `rate:user:{userID}` | `USER_*` env | `config:user:{userID}` |
| 3 Endpoint | `rate:endpoint:{tenantID}:{endpoint}` | `ENDPOINT_*` env | `config:endpoint:{tenant\|endpoint}` |

`tenantID` / `userID` come from headers (`identity` package); endpoint is the query param `?endpoint=/api/v1/foo`.

---

## Endpoint keys — tenant isolation

The endpoint bucket is **tenant-scoped**:

```
rate:endpoint:{tenantID}:{path}
```

Two tenants with the same path (`/api/login`) do **not** share — override ID is also `tenantID + "|" + endpoint` (`override.EndpointOverrideID`).

Admin path example: `POST /admin/limits/endpoint/default%7C%2Fapi%2Fv1%2Fresource`

In sidecar hierarchical mode, the denial cache key is also scoped to `tenant + user + path` (`cmd/sidecar/main.go` — `cacheKey`).

---

## Lua merge algorithm (`hierarchical.lua`)

1. **Read phase:** On each level, `HMGET tokens, last_refill` → refill from elapsed time.
2. **Check phase:** If any level has `floor(tokens) < requested` → `allowed=0`.
3. **Write phase:**
   - Allowed → deduct `requested` from all levels + `EXPIRE 3600`.
   - Denied → persist refill state (no deduct).
4. **Return:** `{allowed, remaining}` — `remaining` = tightest level's floor minus request.

```go
// internal/limiter/hierarchical.go — AllowWithParams
keys := []string{globalKey, tenantKey, userKey, endpointKey}
capacities, refillRates := effectiveHierarchicalLimits(...)
```

---

## Override merge (`effectiveHierarchicalLimits`)

On every `/check_hierarchical` call:

1. `store.RefreshGeneration(ctx)` — check `config:generation`.
2. Per-level: env default → Redis override (if exists).
3. Pass capacities + refill rates to Lua.

`X-RateLimit-Limit` header = **smallest capacity** across levels (to show the bottleneck).

---

## Request flow

```
Client → Sidecar (optional) → GET /check_hierarchical?endpoint=...
  → auth (INTERNAL_API_KEY)
  → RefreshGeneration + override merge
  → hierarchical.lua (4 KEYS, 10 ARGV)
  → audit record (async)
  → JSON {allowed, remaining}
```

Flat `/check` does **not** use the hierarchical path — separate algorithm/env.

---

## Benchmark note (`bench-progress.log`)

`hierarchical-1000` @ target 1000:

| Metric | Value |
|--------|-------|
| Actual RPS | **870.2** |
| p99 | **34.17 ms** |
| 200 / 429 | 440 / 60474 |

High 429 rate is **by design** — benchmark endpoint capacity configured so the majority of requests deny at the endpoint level; latency path still healthy.

---

## Correctness guarantees

| Property | Mechanism |
|----------|-----------|
| No partial deduct | Single Lua script |
| Cross-replica consistency | Shared Redis keys |
| Override visibility | Generation-validated cache (see `override-consistency.md`) |
| Fail-closed on Redis error | HTTP 503, no silent allow |

---

## Source references

| File | Role |
|------|------|
| `internal/limiter/hierarchical.go` | Go wrapper |
| `internal/limiter/lua/hierarchical.lua` | Atomic 4-level logic |
| `cmd/limiter/main.go` | `/check_hierarchical` handler |
| `cmd/limiter/admin_api.go` | `effectiveHierarchicalLimits` |
| `docs/algorithms/hierarchical-rate-limiting.md` | Algorithm deep dive |
