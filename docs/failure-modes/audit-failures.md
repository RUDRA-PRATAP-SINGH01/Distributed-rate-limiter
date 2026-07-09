# Failure Mode: Audit Trail Failures

**Sources:** `internal/audit/store.go`, `internal/audit/shutdown.go`, `internal/audit/config.go`, `internal/metrics/metrics.go`

**Severity:** Low–Medium (compliance gap; **does not block** `/check`)  
**Components:** Async worker pool, `append.lua`, admin search API

---

## Design principle

Audit failures must **not** block rate-limit decisions. Enforcement returns allowed/denied/503 independently of audit success.

---

## Async path (default)

Config defaults (`internal/audit/config.go`):

| Setting | Default |
|---------|---------|
| `ENABLE_AUDIT_TRAIL` | enabled (set `false` to disable) |
| `AUDIT_ASYNC` | `true` |
| `AUDIT_WORKERS` | `4` |
| `AUDIT_QUEUE_SIZE` | `4096` |
| `AUDIT_RETENTION_HOURS` | `168` |
| `AUDIT_MAX_EVENTS` | `100000` |

`Store.Record` flow:

1. If async: try non-blocking send to bounded queue.
2. Queue full → `metrics.RecordAuditDropped()` (`audit_dropped_total++`); request still proceeds.
3. Shutdown begun → drop + `audit_dropped_total`.
4. Sync fallback when async disabled or direct `record()` on overflow path.

---

## Redis keys (`append.lua`)

```
audit:event:{id}           HASH
audit:idx:ts               ZSET
audit:idx:tenant:{tenant}  ZSET
audit:idx:user:{user}      ZSET
audit:idx:req:{request_id} STRING → latest event id
```

Append error → `audit_events_total{decision="error",handler="append_failed"}`.

---

## Metrics

| Metric | Meaning |
|--------|---------|
| `audit_events_total{decision,handler}` | Successful / error recordings |
| `audit_dropped_total` | Queue full or shutdown drops |
| `audit_append_duration_seconds` | Lua latency |
| `audit_search_duration_seconds` | Admin query latency |

**Alert:** `rate(audit_dropped_total[5m]) > 0`.

---

## Failure modes

| Failure | User impact | Detection |
|---------|-------------|-----------|
| Queue full | Check succeeds; event lost | `audit_dropped_total` |
| Redis append error | Check succeeds | `audit_events_total{decision="error"}` |
| `ENABLE_AUDIT_TRAIL=false` | No records | Config |
| Search overload | Slow admin only | `audit_search_duration_seconds` |
| Retention trim | Old events removed | By design (`AUDIT_MAX_EVENTS`) |
| Shutdown mid-queue | Pending events drained or timeout | `Shutdown()` + logs |

---

## Shutdown interaction

Graceful limiter shutdown calls `auditStore.Shutdown(ctx)` **before** Redis close. If workers still active, Redis close is skipped (`RedisCloseSafe()` false). See [graceful-shutdown.md](../operations/graceful-shutdown.md).

---

## Mitigation

```bash
# Increase capacity
AUDIT_WORKERS=8
AUDIT_QUEUE_SIZE=8192

# Strict durability (latency cost)
AUDIT_ASYNC=false
```

Admin forensics:

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" \
  "http://localhost:8082/admin/audit?tenant_id=acme&decision=denied&limit=50"
```

---

## Tests

- `internal/audit/store_test.go`
- `internal/audit/shutdown_test.go`

Audit is **eventually consistent** with enforcement — size queue for peak × safety factor if compliance requires near-zero drops.
