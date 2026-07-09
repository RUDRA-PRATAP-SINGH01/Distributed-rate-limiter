# Prometheus Metrics

**Source:** `internal/metrics/metrics.go`  
**Scrape:** `GET /metrics` on limiter (`:8080`) and sidecar (`:9090`). Optional auth via `METRICS_REQUIRE_AUTH` + `METRICS_API_KEY` (falls back to `INTERNAL_API_KEY`).

---

## Cardinality policy

Package comment in `metrics.go`:

> Labels are intentionally low-cardinality (handler + allowed) — never per-user — because unbounded label values would OOM the Prometheus TSDB under real traffic.

**Safe label dimensions:** fixed handler names, boolean `allowed`, gateway IDs from config, circuit `target` names, idempotency `result` enums.

**Never label by:** `user_id`, `tenant_id`, request path variants, IP addresses, idempotency keys.

---

## Core rate limiting

| Metric | Type | Labels | Help |
|--------|------|--------|------|
| `rate_limiter_requests_total` | Counter | `handler`, `allowed` | Total requests processed |
| `rate_limiter_requests_duration_seconds` | Histogram | `handler` | Request latency (buckets: 0.5ms–1s) |
| `rate_limiter_redis_duration_seconds` | Histogram | — | Redis operation latency |

**Handlers** (from limiter routes): `check`, `check_hierarchical`, admin handlers as recorded.

**Sidecar cache:**

| Metric | Type | Labels |
|--------|------|--------|
| `rate_limiter_sidecar_cache_hits_total` | Counter | — |
| `rate_limiter_sidecar_cache_misses_total` | Counter | — |

---

## Idempotency

| Metric | Type | Labels |
|--------|------|--------|
| `idempotency_claims_total` | Counter | `result` |
| `idempotency_completes_total` | Counter | — |
| `idempotency_redis_duration_seconds` | Histogram | — |

---

## Intelligent routing

| Metric | Type | Labels |
|--------|------|--------|
| `routing_decisions_total` | Counter | `gateway`, `failover` |
| `routing_outcomes_total` | Counter | `gateway`, `result` |
| `routing_failovers_total` | Counter | `gateway` |
| `routing_gateway_health_score` | Gauge | `gateway` |
| `routing_gateway_latency_seconds` | Histogram | `gateway` |
| `routing_redis_duration_seconds` | Histogram | — |
| `routing_circuit_open` | Gauge | `gateway` | Legacy; prefer `circuit_breaker_state` |

---

## Circuit breaker

| Metric | Type | Labels | Notes |
|--------|------|--------|-------|
| `circuit_breaker_state` | Gauge | `target` | 0=closed, 1=open, 2=half_open |
| `circuit_breaker_transitions_total` | Counter | `target`, `from`, `to` | |
| `circuit_breaker_rejections_total` | Counter | `target`, `state` | Open or exhausted half-open |
| `circuit_breaker_outcomes_total` | Counter | `target`, `outcome` | |
| `circuit_breaker_failure_rate` | Gauge | `target` | Rolling failure rate |
| `circuit_breaker_latency_ema_ms` | Gauge | `target` | EMA latency in ms |
| `circuit_breaker_redis_duration_seconds` | Histogram | — | Lua allow/record latency |

**Known targets:** `redis`, `central-limiter`, `gateway-{id}` (`internal/circuitbreaker/types.go`).

`RecordCircuitState` also updates legacy `routing_circuit_open` for routing gateways.

---

## Audit trail

| Metric | Type | Labels |
|--------|------|--------|
| `audit_events_total` | Counter | `decision`, `handler` |
| `audit_append_duration_seconds` | Histogram | — |
| `audit_search_duration_seconds` | Histogram | — |
| `audit_dropped_total` | Counter | — | Queue full or shutdown begun |

Append failures increment `audit_events_total{decision="error",handler="append"}` via `RecordAuditAppend`.

---

## Prometheus scrape config

`deploy/prometheus/prometheus.yml` scrapes:

- `limiter:8080` (job `rate-limiter`)
- `sidecar:9090` (job `sidecar`)
- `redis-exporter:9121` (job `redis-exporter`)

Scrape interval: 5s.

---

## Recording helpers

Use the `Record*` functions in `metrics.go` rather than touching vec types directly — they normalize label values (e.g. `RecordRequest` maps `allowed` bool → `"true"`/`"false"`).
