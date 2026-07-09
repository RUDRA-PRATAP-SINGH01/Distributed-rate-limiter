# Audit Trail

## Problem Statement

Rate limiting decisions are opaque by default. clients see 429 without knowing which hierarchical level blocked them. The audit store records `handler` as `check` or `hierarchical` only (not algorithm name) and does not record which quota level denied (audit §9, questions 20–23). Compliance and support need a **durable, searchable audit trail** of allow/deny decisions tied to request ID, tenant, and user.

I needed append-only events with retention, index queries, and optional async ingestion so hot paths don't block on Redis index maintenance.

## Why the problem exists

Regulated tenants ask: "Prove you enforced our contracted limit on date X." Without audit:

- Support guesses from logs (if they exist).
- Disputes become he-said-she-said.
- Incident replay ("what would happen now?") lacks historical input.

Audit is distinct from metrics. Prometheus counters show volume, not per-request context. Distinct from tracing. traces expire quickly; audit retention is business-driven (days/weeks).

## Design goals

1. Atomic append + index: `append.lua` writes event HASH and ZSET indexes in one script.
2. Multi-dimensional search: By time, tenant, user, request ID (`store.go` Search).
3. Retention enforcement: Purge stale events by score cutoff and `max_events` cap in Lua.
4. Async mode: Bounded worker pool + queue; drop with metric when full.
5. Replay hints: `Replay()` documents how to re-evaluate a past decision.
6. Decision helpers: `DecisionFromAllowed`, `ReasonFor`, `NormalizeTenant`.

## Alternative approaches considered

| Approach | Issue |
|----------|-------|
| Structured logs only | Hard to query; no unified API |
| PostgreSQL audit table | Extra DB; slower append under spike |
| Kafka event stream | Ops overhead for our scale |
| Sync audit blocking response | Adds p99 latency on every check |
| No retention cap | Redis memory unbounded |

Redis ZSET indexes + HASH events matched existing infra.

## Final architecture

**Package** (`internal/audit/`):

| File | Role |
|------|------|
| `store.go` | Record, Search, Get, Replay, Stats, PurgeTenant |
| `types.go` | `Event`, `Decision`, `Query`, `RecordInput` |
| `config.go` | Enabled, Async, Workers, QueueSize, Retention, MaxEvents |
| `lua/append.lua` | Atomic append + trim |

**Key layout:**

- `audit:event:{uuid}`. HASH with id, request_id, tenant_id, user_id, decision, reason, handler, remaining, timestamp_ms
- `audit:idx:ts`. global time index (ZSET score = timestamp_ms)
- `audit:idx:tenant:{tenant}`. per-tenant index
- `audit:idx:user:{user}`. per-user index
- `audit:idx:req:{request_id}`. STRING → event id (latest)

**Record path:**

```go
if cfg.Async {
    select {
    case queue <- in:
        return // fast path
    default:
        metrics.RecordAuditDropped()
    }
}
return s.record(ctx, in) // sync fallback
```

**append.lua highlights:**

- `HSET` event + `EXPIRE` with retention-derived TTL (min 60s)
- `ZADD` to all indexes
- `ZRANGEBYSCORE` purge stale events with `purge_event()` helper removing cross-index entries
- `while ZCARD > max_events` purge oldest

**Search**. picks index by filter (request ID direct lookup vs tenant vs user vs global ts), `ZRevRangeByScore`, hydrate events, `Query.matches` post-filter.

## Tradeoffs

- Async drop on full queue: Prefer dropping audit over slowing 429/200 responses; monitor `audit_dropped_total`.
- Eventual consistency in async mode: Search may lag milliseconds; acceptable for support UI.
- Request ID index overwrites: `SET req_idx` keeps latest only; multiple decisions per request ID lose history. we assume one limit check per request ID.
- Redis memory: `max_events` cap is global on ts index; large tenants can crowd others. consider per-tenant caps future work.
- Ops can DEL keys; not a compliance ledger blockchain.

## Failure modes

1. Audit dropped; metric fires; decision still enforced.
2. Lua purge loop slow: Huge stale backlog blocks Redis; tune retention job frequency.
3. Clock skew: Timestamp_ms from Go `time.Now()`; search windows use same clock.
4. Empty tenant normalization: `default` tenant bucket; document for queries.
5. append failed: Returns error on sync path; handler should not fail request if audit optional. wiring decision in sidecar matters.

## Operational concerns

- Toggle `Enabled` in config for dev environments.
- `Stats()` → `events_indexed` for capacity planning.
- `PurgeTenant` for GDPR deletes. ops API.
- Benchmark: `go test -bench=. ./internal/audit/...` and `benchmarks/audit/summary.md`.
- Correlate audit `request_id` with `X-Request-ID` from `telemetry/middleware.go`.

## Performance implications

Sync append = 1 Lua RTT touching 5 keys. heavier than flat rate limit.

Async mode amortizes to channel send (~nanoseconds) when queue healthy.

`benchmark_test.go` measures append/search paths. compare sync vs async configs before production defaults.

Search reads `limit * 3` ids then filters. over-fetch handles post-filter mismatch; tune for large tenants.

## Lessons learned

Audit belongs **off the critical path** by default. async with explicit drop metric is honest backpressure.

Atomic append+index in Lua prevented orphaned HASHes without ZSET entries. I tried pipeline first, lost events on partial failure.

`ReasonFor(allowed, handler)` standardizes support language. "hierarchical: rate limit exceeded" beats raw 429.

Replay hints in `Replay()` reduced "how do I reproduce?" slack traffic. cheap UX win.

Retention in Lua on every append spreads GC cost. acceptable at our event rate; would move to nightly cron if append became hot.

Pair audit records with limiter `remaining` field. support can see "0 remaining at user level" without Redis CLI.
