# Failure Mode: Audit Trail Failures

**Status:** Documented  
**Severity:** Low–Medium (compliance gap, not request blocking)  
**Components:** `internal/audit`, limiter `recordAudit`, admin search API

---

## 1. Problem Statement

The audit trail records every rate-limit decision for compliance and forensics. Audit failures must **not** block `/check` responses — but silent loss is unacceptable for regulated tenants. I needed bounded async writes, drop metrics, and searchable indexes with retention caps.

## 2. Why the problem exists

Synchronous Redis append on every `/check` would add latency to the hot path. Unbounded async goroutines would OOM under spike. Full queue blocking would couple audit health to enforcement. The compromise is a **bounded queue with explicit drops** counted by `audit_dropped_total`.

## 3. Design goals

- Non-blocking record path from handlers (`cmd/limiter/audit_record.go`).
- Async worker pool when `AUDIT_ASYNC=true` (default): `AUDIT_WORKERS=4`, `AUDIT_QUEUE_SIZE=4096`.
- Atomic append + index + trim in `append.lua`.
- Retention: `AUDIT_RETENTION_HOURS` (default 168), `AUDIT_MAX_EVENTS` (default 100000).
- Disable entirely: `ENABLE_AUDIT_TRAIL=false`.
- Admin search/replay on `:8082` — `/admin/audit`, `/admin/audit/replay`.

## 4. Alternative approaches considered

| Alternative | Why rejected |
|-------------|--------------|
| Sync append only | Too slow on hot path |
| Unbounded channel | Memory risk |
| Block when queue full | Audit outage denies API traffic |
| External Kafka only | Extra infra for MVP; Redis sufficient |

## 5. Final architecture

`audit.Store.Record` (`internal/audit/store.go`):

```go
if s.cfg.Async {
    s.startWorkers()
    select {
    case s.queue <- in:
        return Event{...}, nil  // accepted async
    default:
        metrics.RecordAuditDropped()  // audit_dropped_total++
    }
}
return s.record(ctx, in)  // sync fallback when queue full or async off
```

Sync `record` runs `append.lua` on keys:

```
audit:event:{id}           HASH
audit:idx:ts               ZSET
audit:idx:tenant:{tenant}  ZSET
audit:idx:user:{user}      ZSET
audit:idx:req:{request_id} STRING → latest event id
```

Limiter handlers call `recordAudit` with `DecisionAllowed`, `DecisionDenied`, or `DecisionError` after each check.

**Metrics:**

| Metric | Meaning |
|--------|---------|
| `audit_events_total{decision,handler}` | Successful writes |
| `audit_dropped_total` | Queue full drops |
| `audit_append_duration_seconds` | Lua latency |
| `audit_search_duration_seconds` | Admin query latency |

## 6. Tradeoffs

| Async + drop | Sync on overflow |
|--------------|------------------|
| Protects latency | May block under spike if `AUDIT_ASYNC=false` |
| Possible compliance gap | Stronger durability |

Retention trim in Lua may delete oldest events under `AUDIT_MAX_EVENTS` — historical gap by design.

## 7. Failure modes

| Failure | User impact | Detection |
|---------|-------------|-----------|
| Queue full | Request still allowed/denied; no audit row | `audit_dropped_total` increase |
| Redis append error | Same — check succeeds | `audit_events_total{decision="error"}` |
| `ENABLE_AUDIT_TRAIL=false` | No records | Config audit |
| Search overload | Slow admin queries | `audit_search_duration_seconds` tail |
| Retention trim | Old events gone | Expected; document for compliance |
| Worker panic | Lost queued events | Queue drain stops — restart limiter |

## 8. Operational concerns

**Alert:** `rate(audit_dropped_total[5m]) > 0` — scale workers or queue:

```
AUDIT_WORKERS=8
AUDIT_QUEUE_SIZE=8192
```

**Compliance:** If drops unacceptable, set `AUDIT_ASYNC=false` — accept latency tradeoff or shard audit to dedicated Redis.

**Forensics:**

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" \
  "http://localhost:8082/admin/audit?tenant_id=acme&decision=denied&limit=50"

curl -H "X-API-Key: $ADMIN_API_KEY" \
  "http://localhost:8082/admin/audit/replay?id={event-id}"
```

Replay returns hint only — does not re-execute Lua (`audit.Store.Replay`).

## 9. Performance implications

Async path: O(1) channel send on hot path. Workers batch Redis appends — tune `AUDIT_WORKERS` to append throughput. Search uses ZREVRANGEBYSCORE — O(log N + M); `limit` caps result size. High-cardinality tenant indexes grow with tenants — README notes shard-by-tenant at scale.

## 10. Lessons learned

I added `audit_dropped_total` after a load test silently lost 12k events — the queue defaulted to full and I had no metric. **Drops must be visible** even when requests succeed. For interviews I say: audit is **eventually consistent with enforcement** — if you need strict audit-every-request, disable async or size queue for peak × safety factor. The limiter correctness path never waits on audit — intentional separation.

**References:** `internal/audit/store.go`, `internal/audit/lua/append.lua`, `cmd/limiter/audit_record.go`, `internal/metrics/metrics.go` (`AuditDropped`)
