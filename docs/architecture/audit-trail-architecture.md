# Audit Trail Architecture

> Engineering journal. Why I store an immutable audit trail of rate-limit decisions in Redis.

## Problem Statement

In production I needed a **forensic record** of every allow and deny decision: which user and tenant, which handler, which request ID, and how much quota remained. Support tickets ("why did I get a 429?") and compliance reviews need searchable history. I had to do this without blocking the primary limiter hot path.

## Why the problem exists

Rate limiting state lives in Redis counters, but counters show *current quota*, not *why this request was denied at 14:32*. Logs are ephemeral and hard to correlate. Without a dedicated store, decision logging on central limiter `/check` and `/check_hierarchical` paths was asymmetric. In multi-tenant SaaS, tenant-scoped audit queries are a common operational need.

## Design goals

1. **Append-only events**. Immutable decision records with TTL retention.
2. **Searchable indexes**. By timestamp, tenant, user, and request ID.
3. **Non-blocking hot path**. Async worker pool with a bounded queue.
4. **Atomic append and trim**. Single Lua script writes the event, updates indexes, and purges stale entries.
5. **Admin search API**. Authenticated query for ops and support.
6. **`purge_event` helper**. Consistent index cleanup when trimming.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Structured logs only | Hard to query. No tenant index. |
| PostgreSQL audit table | Extra dependency. Write latency on the hot path. |
| Redis LIST append | No efficient time-range or tenant queries. |
| Synchronous Redis write per check | Adds milliseconds to every request under load. |
| External SIEM only | Overkill for dev and demo. I wanted a first-class admin API. |

## Final architecture

### Event storage

```
audit:event:{uuid}     → HASH (id, request_id, tenant_id, user_id, decision, reason, handler, remaining, timestamp_ms)
audit:idx:ts           → ZSET (score=timestamp_ms, member=event_id)
audit:idx:tenant:{id}  → ZSET
audit:idx:user:{id}    → ZSET
audit:idx:req:{req_id} → STRING → event_id (latest for that request)
```

### `append.lua`. Atomic write, indexes, and retention

1. `HSET` event hash and `EXPIRE` (retention-based TTL, minimum 60s)
2. `ZADD` to ts, tenant, and user indexes
3. `SET` request index with TTL when `request_id` is non-empty
4. **Retention trim**: `ZRANGEBYSCORE` on the ts index for events older than `retention_ms`, then **`purge_event(eid)`** for each:
   - `ZREM` from tenant index
   - `ZREM` from user index
   - `DEL` request index key
   - `DEL` event hash
5. **Capacity trim**: while `ZCARD(ts) > max_events`, remove oldest via `purge_event`

The `purge_event` Lua local function keeps indexes consistent. Orphaned ZSET members do not remain.

### Worker pool (`AUDIT_QUEUE_SIZE` / `AUDIT_WORKERS`)

`audit.Store.Record()`:

```text
if async (default):
  startWorkers() once → worker goroutines drain queue
  select:
    queue <- input  → return immediately (fire-and-forget)
    default (queue full) → RecordAuditDropped(), fall through to sync record
else:
  synchronous record()
```

Defaults (`audit.DefaultConfig`):

| Setting | Default | Env |
|---------|---------|-----|
| Enabled | true | `ENABLE_AUDIT_TRAIL=false` disables |
| Retention | 7 days | `AUDIT_RETENTION_HOURS` |
| Max events | 100,000 | `AUDIT_MAX_EVENTS` |
| Async | true | `AUDIT_ASYNC=false` |
| Queue size | 4096 | `AUDIT_QUEUE_SIZE` |
| Workers | 4 | `AUDIT_WORKERS` |

### Limiter integration (`cmd/limiter/main.go`, `audit_record.go`)

Every `/check` and `/check_hierarchical` handler on success, deny, and error paths:

```go
recordAudit(ctx, auditStore, audit.RecordInput{
    RequestID: telemetry.RequestIDFromContext(ctx),  // auto-filled if empty
    TenantID:  audit.NormalizeTenant(tenantID),
    UserID:    userID,
    Decision:  audit.DecisionFromAllowed(allowed),
    Reason:    audit.ReasonFor(allowed, handlerName),
    Handler:   "check" | "check_hierarchical",
    Remaining: remaining,
})
```

`RequestID` comes from `X-Request-ID` via the telemetry middleware correlation chain.

### Admin search (`cmd/limiter/admin_api.go`)

Authenticated via `X-API-Key` = `ADMIN_API_KEY`:

| Endpoint | Purpose |
|----------|---------|
| `GET /admin/audit` | Search with query params |
| `GET /admin/audit/{id}` | Single event |
| `GET /admin/audit/replay?id=` | Replay payload and hint |
| `GET /admin/audit/{id}/replay` | Same replay |
| `GET /admin/audit/stats` | `events_indexed` count |

**Search query params**: `request_id`, `tenant_id`, `user_id`, `decision`, `handler`, `from_ms`, `to_ms`, `limit` (default 50, max 500).

**Search strategy** (`audit.Store.Search`):

- `request_id` set: direct lookup via `audit:idx:req:{id}`
- `tenant_id`: `ZREVRANGEBYSCORE` on tenant index
- `user_id`: user index
- else: global ts index
- Post-filter: `matches()` for decision, handler, and time range
- Fetch up to `limit * 3` IDs then trim (over-fetch for filter loss)

### Replay

`Replay(ctx, id)` returns the event plus a `ReplayHint`. For denied events, the hint notes that quota must refill before re-allow.

### PurgeTenant (ops)

`Store.PurgeTenant(ctx, tenantID)`. `ZREMRANGEBYSCORE` on the tenant index by retention cutoff (index entries only; full `purge_event` path is not invoked. Partial ops cleanup).

## Tradeoffs

- **Redis memory**. 100k events at roughly 500 bytes each plus index overhead. The `max_events` cap is essential.
- **Async drops**. Queue full increments `audit_dropped_total`. Sync fallback runs when the select default branch fires.
- **Request index is latest only**. Duplicate request IDs overwrite the pointer.
- **No cross-region replication**. Audit is tied to Redis durability (AOF in HA compose).
- **Search is O(n) on the index slice**. Fine for ops limits at or below 500, not an analytics warehouse.

## Failure modes

| Scenario | Effect |
|----------|--------|
| Queue saturated | Events dropped (`audit_dropped_total`). Hot path unaffected. |
| Redis append fails | `audit_events_total{decision="error"}`. Limiter check still succeeds. |
| `purge_event` mid-search | Rare race. Get returns nil and the event is skipped in results. |
| Retention much shorter than traffic | Constant churn. Oldest events evicted aggressively. |
| Audit disabled | `Record` is a no-op. Admin API returns empty. |

## Operational concerns

- Monitor: `audit_events_total`, `audit_dropped_total`, `audit_append_duration_seconds`, `audit_search_duration_seconds`.
- Alert on sustained `audit_dropped_total` increase. Increase `AUDIT_WORKERS` or `AUDIT_QUEUE_SIZE`.
- `GET /admin/audit/stats` for index cardinality vs `AUDIT_MAX_EVENTS`.
- HA compose sets `AUDIT_RETENTION_HOURS=168` (7 days) on the limiter.
- Admin port is separate (`ADMIN_PORT=8082`) from the public limiter port.

## Performance implications

- **Hot path (async)**: channel send only (nanoseconds) unless the queue is full.
- **Worker path**: one Lua script per event. Roughly 1 to 2ms Redis. Four workers with a queue of 4096 buffer bursts.
- **append.lua trim loop**: under heavy write plus old data, trim can add latency to the worker. Cost is amortized across writes.
- **Search**: `ZREVRANGEBYSCORE` plus N times `HGETALL`. Acceptable for admin, not for high-QPS automation.

## Lessons learned

I kept the hot path on a **never block** principle with an async queue. The rate limit decision is authoritative. Audit is best-effort. `purge_event` as a Lua local function avoids duplicate cleanup logic. In an early prototype I found orphaned index entries. Auto-filling request ID from telemetry middleware simplifies support workflows. On a full queue I chose drop plus metric over silent loss without signal, so a Redis spike does not kill the limiter. Admin search over-fetch (`limit * 3`) is simple. Composite indexes (tenant plus decision) could be a future optimization.
