# Audit Trail Architecture

> इंजीनियरिंग जर्नल — मैंने rate-limit decisions का immutable audit trail Redis में क्यों रखा।

## Problem Statement

Production rate limiter में मुझे हर allow/deny decision का **forensic record** चाहिए था: किस user/tenant को, किस handler से, किस request ID के साथ, कितना quota बचा था। Support tickets ("मुझे 429 क्यों मिला?") और compliance reviews के लिए searchable history जरूरी थी — बिना primary limiter hot path को block किए।

## Why the problem exists

Rate limiting state Redis counters में है, पर counters बताते हैं *current quota*, न कि *why this request was denied at 14:32*. Logs ephemeral हैं और correlate करना मुश्किल। Central limiter `/check` और `/check_hierarchical` दोनों paths पर decision logging asymmetric थी बिना dedicated store के। Multi-tenant SaaS में tenant-scoped audit query common operational need है।

## Design goals

1. **Append-only events** — immutable decision records with TTL retention।
2. **Searchable indexes** — by timestamp, tenant, user, request ID।
3. **Non-blocking hot path** — async worker pool with bounded queue।
4. **Atomic append + trim** — single Lua script: write event, update indexes, purge stale।
5. **Admin search API** — authenticated query for ops/support।
6. **`purge_event` helper** — consistent index cleanup when trimming।

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Structured logs only | Hard to query; no tenant index |
| PostgreSQL audit table | Extra dependency; write latency on hot path |
| Redis LIST append | No efficient time-range or tenant queries |
| Synchronous Redis write per check | Adds ms to every request under load |
| External SIEM only | Overkill for dev/demo; wanted first-class admin API |

## Final architecture

### Event storage

```
audit:event:{uuid}     → HASH (id, request_id, tenant_id, user_id, decision, reason, handler, remaining, timestamp_ms)
audit:idx:ts           → ZSET (score=timestamp_ms, member=event_id)
audit:idx:tenant:{id}  → ZSET
audit:idx:user:{id}    → ZSET
audit:idx:req:{req_id} → STRING → event_id (latest for that request)
```

### `append.lua` — atomic write + indexes + retention

1. `HSET` event hash + `EXPIRE` (retention-based TTL, min 60s)
2. `ZADD` to ts, tenant, user indexes
3. `SET` request index with TTL (if request_id non-empty)
4. **Retention trim**: `ZRANGEBYSCORE` on ts index for events older than `retention_ms` → **`purge_event(eid)`** for each:
   - `ZREM` from tenant index
   - `ZREM` from user index
   - `DEL` request index key
   - `DEL` event hash
5. **Capacity trim**: while `ZCARD(ts) > max_events`, remove oldest via `purge_event`

`purge_event` Lua local function ensures indexes stay consistent — orphaned ZSET members नहीं बचते।

### Worker pool (`AUDIT_QUEUE_SIZE` / `AUDIT_WORKERS`)

`audit.Store.Record()`:

```text
if async (default):
  startWorkers() once → Workers goroutines drain queue
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

हर `/check` और `/check_hierarchical` handler success/deny/error path पर:

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

`RequestID` = `X-Request-ID` from telemetry middleware correlation chain।

### Admin search (`cmd/limiter/admin_api.go`)

Authenticated via `X-API-Key` = `ADMIN_API_KEY`:

| Endpoint | Purpose |
|----------|---------|
| `GET /admin/audit` | Search with query params |
| `GET /admin/audit/{id}` | Single event |
| `GET /admin/audit/replay?id=` | Replay payload + hint |
| `GET /admin/audit/{id}/replay` | Same replay |
| `GET /admin/audit/stats` | `events_indexed` count |

**Search query params**: `request_id`, `tenant_id`, `user_id`, `decision`, `handler`, `from_ms`, `to_ms`, `limit` (default 50, max 500).

**Search strategy** (`audit.Store.Search`):

- `request_id` set → direct lookup via `audit:idx:req:{id}`
- `tenant_id` → `ZREVRANGEBYSCORE` on tenant index
- `user_id` → user index
- else → global ts index
- Post-filter: `matches()` for decision, handler, time range
- Fetch up to `limit * 3` IDs then trim (over-fetch for filter loss)

### Replay

`Replay(ctx, id)` returns event + `ReplayHint` — e.g. denied events hint that quota must refill before re-allow。

### PurgeTenant (ops)

`Store.PurgeTenant(ctx, tenantID)` — `ZREMRANGEBYSCORE` on tenant index by retention cutoff (index entries only; full `purge_event` path not invoked — ops partial cleanup)।

## Tradeoffs

- **Redis memory** — 100k events × ~500 bytes + index overhead; `max_events` cap essential।
- **Async drops** — queue full → `audit_dropped_total` increment; sync fallback or silent drop on full queue select default branch।
- **Request index = latest only** — duplicate request IDs overwrite pointer।
- **No cross-region replication** — audit tied to Redis durability (AOF in HA compose)।
- **Search is O(n) on index slice** — fine for ops limits ≤500; not analytics warehouse।

## Failure modes

| Scenario | Effect |
|----------|--------|
| Queue saturated | Events dropped (`audit_dropped_total`); hot path unaffected |
| Redis append fails | `audit_events_total{decision="error"}`; limiter check still succeeds |
| `purge_event` mid-search | Rare race; Get returns nil, skipped in results |
| Retention << traffic | Constant churn; oldest events evicted aggressively |
| Audit disabled | `Record` no-op; admin API empty |

## Operational concerns

- Monitor: `audit_events_total`, `audit_dropped_total`, `audit_append_duration_seconds`, `audit_search_duration_seconds`।
- Alert on sustained `audit_dropped_total` increase — increase `AUDIT_WORKERS` or `AUDIT_QUEUE_SIZE`।
- `GET /admin/audit/stats` for index cardinality vs `AUDIT_MAX_EVENTS`।
- HA compose sets `AUDIT_RETENTION_HOURS=168` (7 days) on limiter।
- Admin port separate (`ADMIN_PORT=8082`) from public limiter port।

## Performance implications

- **Hot path (async)**: channel send only (~nanoseconds) unless queue full।
- **Worker path**: one Lua script per event — ~1-2ms Redis; 4 workers × queue 4096 buffers bursts।
- **append.lua trim loop**: under heavy write + old data, trim can add latency to worker — amortized across writes।
- **Search**: `ZREVRANGEBYSCORE` + N× `HGETALL` — acceptable for admin, not for high-QPS automation।

## Lessons learned

मैंने hot path को **never block** principle पर async queue रखा — rate limit decision authoritative है, audit best-effort। `purge_event` as Lua local function duplicate cleanup logic से बचाता है; पहले prototype में orphaned index entries मिले थे। Request ID correlation telemetry middleware से auto-fill करना support workflow simplify करता है। Queue full पर silent drop vs sync fallback — मैंने drop + metric choose किया ताकि Redis spike limiter को न मारे। Admin search over-fetch (`limit * 3`) simple है; composite indexes (tenant+decision) future optimization हो सकती है।
