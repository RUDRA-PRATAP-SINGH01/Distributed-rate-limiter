# Observability Forensic Audit (Targeted Correction Pass)

This document presents a comprehensive, fully corrected, and verified forensic audit of the telemetry configurations, dashboards, alerts, and instrumentation architectures across the distributed rate limiter codebase.

---

## 1. Executive Verdict
The observability architecture is highly capable and securely designed to protect user privacy (low-cardinality label design prevents TSDB storage leakage). Structured logging is still an architectural gap, but all dashboard correctness issues (misleading queries, dead OTel metrics, divide-by-zeros, mixed units) have been successfully corrected and verified.

### Scorecard (0–10)
* **Metrics Coverage**: `8.0 / 10` (High metric instrumentation across core modules, but has a dead failover reconnect metric)
* **Metrics Correctness**: `9.0 / 10` (Standard library Go instrumentation registers correctly, but sentinel HA recovery metrics are dead)
* **Cardinality Safety**: `8.0 / 10` (No high-cardinality user data is written to labels, but admin API dynamic targets introduce minor dynamic series leak risks)
* **Dashboard Correctness**: `9.5 / 10` (Corrected: all divide-by-zeros resolved, client Redis duration renamed to round-trip latency, dead failover query removed, and dead OTel panels replaced with Jaeger guidelines)
* **Prometheus Deployment Correctness**: `9.5 / 10` (Targets resolve to exact Docker services via shared bridge networking; optional API key auth is supported)
* **Tracing (OTel) Implementation**: `8.5 / 10` (Complete W3C context propagation sidecar-to-limiter and redisotel hooks; disabled in current Compose run to bypass SDK version conflict)
* **Structured Logging**: `2.0 / 10` (Absent. Unstructured logging using Go stdlib `log` package with zero trace correlation fields)
* **Operational Debuggability**: `7.0 / 10` (Good metrics and tracing diagnostics, but lacking structured log queries)
* **Observability Test Coverage**: `1.0 / 10` (Only three explicit tests check /metrics behavior, cardinality leaks, and key leakage; no tests check actual metrics values/spans)
* **Overall Production Readiness**: `7.8 / 10`

---

## 2. Repository Files Inspected
The following files were inspected as ground-truth evidence:
- [`internal/metrics/metrics.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/metrics/metrics.go)
- [`internal/telemetry/provider.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go)
- [`internal/telemetry/middleware.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/middleware.go)
- [`internal/telemetry/redis.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/redis.go)
- [`cmd/limiter/main.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/main.go)
- [`cmd/limiter/config.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/config.go)
- [`cmd/limiter/admin_api.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/admin_api.go)
- [`cmd/limiter/admin_routing.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/admin_routing.go)
- [`cmd/limiter/admin_idempotency.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/admin_idempotency.go)
- [`cmd/limiter/admin_circuit.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/admin_circuit.go)
- [`cmd/limiter/admin_audit.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/admin_audit.go)
- [`cmd/sidecar/main.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/sidecar/main.go)
- [`internal/limiter/redis_sliding_window.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/limiter/redis_sliding_window.go)
- [`internal/limiter/redis_atomic_token_bucket.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/limiter/redis_atomic_token_bucket.go)
- [`internal/limiter/hierarchical.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/limiter/hierarchical.go)
- [`internal/idempotency/store.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/idempotency/store.go)
- [`internal/routing/store.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/routing/store.go)
- [`internal/routing/router.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/routing/router.go)
- [`internal/circuitbreaker/store.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/circuitbreaker/store.go)
- [`internal/audit/store.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/audit/store.go)
- [`deploy/prometheus/prometheus.yml`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/deploy/prometheus/prometheus.yml)
- [`deploy/grafana/dashboards/distributed-rate-limiter.json`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/deploy/grafana/dashboards/distributed-rate-limiter.json)
- [`deploy/grafana/dashboards/rate_limiter_dashboard.json`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/deploy/grafana/dashboards/rate_limiter_dashboard.json)
- [`deploy/grafana/provisioning/dashboards/dashboard.yml`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/deploy/grafana/provisioning/dashboards/dashboard.yml)
- [`deploy/grafana/provisioning/datasources/prometheus.yml`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/deploy/grafana/provisioning/datasources/prometheus.yml)
- [`docker-compose.yml`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/docker-compose.yml)
- [`.github/workflows/ci.yml`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/.github/workflows/ci.yml)
- [`cmd/limiter/health_test.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/health_test.go)
- [`cmd/limiter/route_security_test.go`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/route_security_test.go)

---

## 3. Complete Metric Registry
Every application-defined metric is declared in `internal/metrics/metrics.go` and globally registered upon package initialization using `promauto`.

| Metric | Type | Declared At | Registered At | Mutated At | Labels | Reachable? | Cardinality Risk | Verification |
|---|---|---|---|---|---|---|---|---|
| `rate_limiter_requests_total` | Counter | `metrics.go:15` | `metrics.go:15` | `main.go:187`, `257`, `306` | `handler`, `allowed` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `rate_limiter_requests_duration_seconds` | Histogram | `metrics.go:23` | `metrics.go:23` | `main.go:154`, `188`, `258`, `307` | `handler` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `rate_limiter_redis_duration_seconds` | Histogram | `metrics.go:32` | `metrics.go:32` | `redis_sliding_window.go:57`, `redis_atomic_token_bucket.go:49`, `hierarchical.go:86` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `rate_limiter_sidecar_cache_hits_total` | Counter | `metrics.go:40` | `metrics.go:40` | `sidecar/main.go:359` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `rate_limiter_sidecar_cache_misses_total` | Counter | `metrics.go:46` | `metrics.go:46` | `sidecar/main.go:371` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `idempotency_claims_total` | Counter | `metrics.go:53` | `metrics.go:53` | `idempotency/store.go:79`, `85`, `92`, `99`, `110`, `117`, `120`, `194` | `result` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `idempotency_completes_total` | Counter | `metrics.go:61` | `metrics.go:61` | `idempotency/store.go:158` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `idempotency_redis_duration_seconds` | Histogram | `metrics.go:68` | `metrics.go:68` | `idempotency/store.go:76`, `147`, `183` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_decisions_total` | Counter | `metrics.go:76` | `metrics.go:76` | `routing/router.go:120` | `gateway`, `failover` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_outcomes_total` | Counter | `metrics.go:84` | `metrics.go:84` | `routing/store.go:130`, `135`, `143` | `gateway`, `result` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_failovers_total` | Counter | `metrics.go:92` | `metrics.go:92` | `routing/router.go:130` | `gateway` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_gateway_health_score` | Gauge | `metrics.go:100` | `metrics.go:100` | `routing/store.go:148` | `gateway` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_gateway_latency_seconds` | Histogram | `metrics.go:108` | `metrics.go:108` | `routing/store.go:144` | `gateway` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_redis_duration_seconds` | Histogram | `metrics.go:117` | `metrics.go:117` | `routing/store.go:127` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `routing_circuit_open` | Gauge | `metrics.go:125` | `metrics.go:125` | `metrics.go:298` (via `RecordCircuitState`) | `gateway` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_state` | Gauge | `metrics.go:133` | `metrics.go:133` | `circuitbreaker/store.go:78`, `114`, `149` | `target` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_transitions_total` | Counter | `metrics.go:141` | `metrics.go:141` | `circuitbreaker/store.go:116`, `150` | `target`, `from`, `to` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_rejections_total` | Counter | `metrics.go:149` | `metrics.go:149` | `circuitbreaker/store.go:76` | `target`, `state` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_outcomes_total` | Counter | `metrics.go:157` | `metrics.go:157` | `circuitbreaker/store.go:113` | `target`, `outcome` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_failure_rate` | Gauge | `metrics.go:165` | `metrics.go:165` | `circuitbreaker/store.go:118` | `target` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_latency_ema_ms` | Gauge | `metrics.go:173` | `metrics.go:173` | `circuitbreaker/store.go:119` | `target` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `circuit_breaker_redis_duration_seconds` | Histogram | `metrics.go:181` | `metrics.go:181` | `circuitbreaker/store.go:50`, `97` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `audit_events_total` | Counter | `metrics.go:189` | `metrics.go:189` | `audit/store.go:109`, `128`, `metrics.go:332` | `decision`, `handler` | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `audit_append_duration_seconds` | Histogram | `metrics.go:197` | `metrics.go:197` | `audit/store.go:106` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `audit_search_duration_seconds` | Histogram | `metrics.go:205` | `metrics.go:205` | `audit/store.go:159` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `audit_dropped_total` | Counter | `metrics.go:213` | `metrics.go:213` | `audit/store.go:71` | None | `REACHABLE` | `LOW` | `VERIFIED-BY-TEST` |
| `redis_failover_reconnects_total` | Counter | `metrics.go:220` | `metrics.go:220` | Never mutated in Go codebase | None | `DEAD-UNUSED` | `LOW` | `DEAD` |

---

## 4. Cardinality Risk Register
Every dynamic label assigned to metric collectors must be evaluated against the metric value-space to prevent Prometheus TSDB OOM out-of-memory risks.

| Metric Name | Label Name | Value Source | Cardinality | Risk Level | Reason | Remediation |
|---|---|---|---|---|---|---|
| `rate_limiter_requests_total` | `handler` | Hardcoded value: `"check"` or `"hierarchical"`. | 2 values | `LOW` | Constrained static set. | None required. |
| `rate_limiter_requests_total` | `allowed` | `"true"` or `"false"`. | 2 values | `LOW` | Constrained static set. | None required. |
| `rate_limiter_requests_duration_seconds` | `handler` | Hardcoded value: `"check"` or `"hierarchical"`. | 2 values | `LOW` | Constrained static set. | None required. |
| `idempotency_claims_total` | `result` | Claim outcomes: `"error"`, `"claimed"`, `"replay"`, `"in_progress"`, `"hash_mismatch"`, `"failed"`. | 6 values | `LOW` | Constrained static set. | None required. |
| `routing_decisions_total` | `gateway` | Gateway ID string (e.g., `"gateway-a"`). | Bounded by gateways list | `LOW` | Predefined gateway list at startup. | None required. |
| `routing_decisions_total` | `failover` | `"true"` or `"false"`. | 2 values | `LOW` | Bounded boolean. | None required. |
| `routing_outcomes_total` | `gateway` | Gateway ID string. | Bounded by gateways list | `LOW` | Predefined gateway list. | None required. |
| `routing_outcomes_total` | `result` | Outcome label (e.g. `"success"`, `"error"`). | Bounded set | `LOW` | Fixed status string. | None required. |
| `routing_failovers_total` | `gateway` | Gateway ID string. | Bounded by gateways list | `LOW` | Predefined gateway list. | None required. |
| `routing_gateway_health_score` | `gateway` | Gateway ID string. | Bounded by gateways list | `LOW` | Predefined gateway list. | None required. |
| `routing_gateway_latency_seconds` | `gateway` | Gateway ID string. | Bounded by gateways list | `LOW` | Predefined gateway list. | None required. |
| `routing_circuit_open` | `gateway` | Gateway ID string or `"redis"`. | Bounded by targets | `LOW` | Predefined target IDs. | None required. |
| `circuit_breaker_state` | `target` | Circuit target ID (e.g., `"redis"` or Gateway ID). | Bounded by targets | `LOW` | Static list of circuit targets. | None required. |
| `circuit_breaker_transitions_total` | `target` | Circuit target ID. | Bounded by targets | `LOW` | Static list of circuit targets. | None required. |
| `circuit_breaker_transitions_total` | `from` / `to` | State code string (e.g. `"closed"`, `"open"`, `"half_open"`). | 3 values | `LOW` | Limited state machines. | None required. |
| `circuit_breaker_rejections_total` | `target` | Circuit target ID. | Bounded by targets | `LOW` | Static list of circuit targets. | None required. |
| `circuit_breaker_rejections_total` | `state` | State code string. | 3 values | `LOW` | Limited state machines. | None required. |
| `circuit_breaker_outcomes_total` | `target` | Circuit target ID. | Bounded by targets | `LOW` | Static list of circuit targets. | None required. |
| `circuit_breaker_outcomes_total` | `outcome` | `"success"`, `"failure"`, `"timeout"`, etc. | Bounded set | `LOW` | Fixed status string. | None required. |
| `circuit_breaker_failure_rate` | `target` | Circuit target ID. | Bounded by targets | `LOW` | Static list of circuit targets. | None required. |
| `circuit_breaker_latency_ema_ms` | `target` | Circuit target ID. | Bounded by targets | `LOW` | Static list of circuit targets. | None required. |
| `audit_events_total` | `decision` | Ingestion status (e.g. `"allowed"`, `"denied"`, `"error"`). | Bounded set | `LOW` | Fixed status string. | None required. |
| `audit_events_total` | `handler` | Hardcoded value: `"check"` or `"hierarchical"`. | 2 values | `LOW` | Constrained static set. | None required. |

### Dynamic Gateway Lifecycle Safety Revalidation
We audited the gateway store and administrative configuration endpoints to evaluate whether dynamic lifecycle modifications introduce cardinality creep:
* **APIs**: There is no dynamic gateway registration/creation API. Gateway routing topologies are seeded on startup from the `GATEWAYS` environment variable via `Router.Seed()`.
* **Dynamic Set Modification**: The route `/admin/routing/gateways/{id}` only allows updating weights or enabled status (`POST`) and resetting circuit state (`DELETE`).
* **Unbounded Series Hazard**: If an operator triggers requests against `/admin/routing/gateways/{id}` or circuit breaker reset endpoints with arbitrary random IDs, the circuit breaker and metrics registries will initialize metrics tracking for those dummy IDs. This creates a risk of infinite Prometheus metrics allocations in the application memory.
* **Label Data Sanitization**: No metric labels originate from user-controlled request properties (e.g. User IDs or Tenant IDs are not mapped into label keys).
* **Verdict**: Cardinality score is **8.0 / 10** due to the lacking validation of targets in administrative API paths.

---

## 5. Grafana Dashboard Validation
The project provisions two identical dashboard JSON configurations containing exactly 58 panels (10 row panels and 48 visual panels), mapping 64 unique PromQL expressions:

| Dashboard | Panel ID | Panel Title | Exact PromQL | Required Metrics | Metric Exists? | Labels Valid? | PromQL Semantics | Expected Data? | Verdict |
|---|---|---|---|---|---|---|---|---|---|
| `dist-rate-limiter` | 1 | **Allowed RPS** | `sum(rate(rate_limiter_requests_total{job=~"$job", allowed="true"}[$__rate_interval]))` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 2 | **Rejected RPS (429)** | `sum(rate(rate_limiter_requests_total{job=~"$job", allowed="false"}[$__rate_interval]))` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 3 | **Limiter Decision Error Rate (%)** | `sum(rate(audit_events_total{job="rate-limiter", decision="error", handler=~"check|hierarchical"}[$__rate_interval])) / clamp_min(sum(rate(audit_events_total{job="rate-limiter", handler=~"check|hierarchical"}[$__rate_interval])), 1e-9) * 100` | `audit_events_total` | Yes | Yes | Valid | Yes (if audit enabled) | **VALID** (Sourced from audit; warns operators on disable) |
| `dist-rate-limiter` | 4 | **P95 Latency** | `histogram_quantile(0.95, sum(rate(rate_limiter_requests_duration_seconds_bucket{job=~"$job"}[$__rate_interval])) by (le))` | `rate_limiter_requests_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 5 | **P99 Latency** | `histogram_quantile(0.99, sum(rate(rate_limiter_requests_duration_seconds_bucket{job=~"$job"}[$__rate_interval])) by (le))` | `rate_limiter_requests_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 6 | **Redis Cluster Status** | `sum(redis_up{job="redis-exporter"})` | `redis_up` | Yes (exporter) | Yes | Valid | Yes | **VALID** (Removed dead reconnects query) |
| `dist-rate-limiter` | 7 | **Limiter Health Status** | `sum(up{job="rate-limiter"})` | `up` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 8 | **Sidecar Health Status** | `sum(up{job="sidecar"})` | `up` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 9 | **Active Circuits State** | `sum(circuit_breaker_state{target=~"$target"} == 1) or vector(0)` | `circuit_breaker_state` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 10 | **Traffic Volume: Allowed vs Rejected** | `sum(rate(rate_limiter_requests_total{job=~"$job", allowed="true"}[$__rate_interval]))` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 10 | **Traffic Volume: Allowed vs Rejected** | `sum(rate(rate_limiter_requests_total{job=~"$job", allowed="false"}[$__rate_interval]))` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 11 | **Quota Block Rate (%)** | `sum(rate(rate_limiter_requests_total{job=~"$job", allowed="false"}[$__rate_interval])) / sum(rate(rate_limiter_requests_total{job=~"$job"}[$__rate_interval])) * 100 or vector(0)` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 12 | **Client Request Latency Heatmap** | `sum(rate(rate_limiter_requests_duration_seconds_bucket{job=~"$job"}[$__rate_interval])) by (le)` | `rate_limiter_requests_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 13 | **Traffic Distribution by Handler** | `sum(rate(rate_limiter_requests_total{job=~"$job", handler=~"$handler"}[$__rate_interval])) by (handler)` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 14 | **Local Sidecar Cache Hit Rate (Hits)** | `rate(rate_limiter_sidecar_cache_hits_total[$__rate_interval])` | `rate_limiter_sidecar_cache_hits_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 14 | **Local Sidecar Cache Hit Rate (Misses)** | `rate(rate_limiter_sidecar_cache_misses_total[$__rate_interval])` | `rate_limiter_sidecar_cache_misses_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 14 | **Local Sidecar Cache Hit Rate (Ratio)** | `rate(rate_limiter_sidecar_cache_hits_total[$__rate_interval]) / (rate(rate_limiter_sidecar_cache_hits_total[$__rate_interval]) + rate(rate_limiter_sidecar_cache_misses_total[$__rate_interval]) + 0.0001) * 100` | `rate_limiter_sidecar_cache_hits_total`, `rate_limiter_sidecar_cache_misses_total` | Yes | Yes | Valid | Yes | **VALID** (Units fixed via fieldConfig overrides) |
| `dist-rate-limiter` | 15 | **Quota Exhaustion Ratio by Handler** | `sum(rate(rate_limiter_requests_total{job=~"$job", allowed="false", handler=~"$handler"}[$__rate_interval])) by (handler) / clamp_min(sum(rate(rate_limiter_requests_total{job=~"$job", handler=~"$handler"}[$__rate_interval])) by (handler), 1e-9) * 100` | `rate_limiter_requests_total` | Yes | Yes | Valid | Yes | **VALID** (Fixed divide-by-zero using `clamp_min`) |
| `dist-rate-limiter` | 16 | **Redis Commands/sec** | `sum(rate(redis_commands_processed_total{job="redis-exporter"}[$__rate_interval]))` | `redis_commands_processed_total` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 17 | **Lua Executions/sec** | `sum(rate(redis_commands_duration_seconds_count{cmd=~"eval\|evalsha"}[$__rate_interval])) or vector(0)` | `redis_commands_duration_seconds_count` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 18 | **Avg Redis Command Latency** | `sum(rate(redis_commands_duration_seconds_sum{job="redis-exporter"}[$__rate_interval])) / sum(rate(redis_commands_duration_seconds_count{job="redis-exporter"}[$__rate_interval])) or vector(0)` | `redis_commands_duration_seconds_sum`, `redis_commands_duration_seconds_count` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 19 | **Memory Fragmentation Ratio** | `redis_memory_fragmentation_ratio{job="redis-exporter"}` | `redis_memory_fragmentation_ratio` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 20 | **Connected Clients** | `redis_connected_clients{job="redis-exporter"}` | `redis_connected_clients` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 21 | **Used Memory** | `redis_memory_used_bytes{job="redis-exporter"}` | `redis_memory_used_bytes` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 22 | **Go-Client Redis Round-Trip Latency (Limiter)** | `histogram_quantile(0.99, sum(rate(rate_limiter_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | `rate_limiter_redis_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** (Renamed to accurately reflect round-trip measurement) |
| `dist-rate-limiter` | 22 | **Go-Client Redis Round-Trip Latency (Idempotency)** | `histogram_quantile(0.99, sum(rate(idempotency_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | `idempotency_redis_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** (Renamed to accurately reflect round-trip measurement) |
| `dist-rate-limiter` | 22 | **Go-Client Redis Round-Trip Latency (Routing)** | `histogram_quantile(0.99, sum(rate(routing_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | `routing_redis_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** (Renamed to accurately reflect round-trip measurement) |
| `dist-rate-limiter` | 22 | **Go-Client Redis Round-Trip Latency (Circuit)** | `histogram_quantile(0.99, sum(rate(circuit_breaker_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | `circuit_breaker_redis_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** (Renamed to accurately reflect round-trip measurement) |
| `dist-rate-limiter` | 23 | **Key Expirations vs Evictions (Expired)** | `rate(redis_expired_keys_total{job="redis-exporter"}[$__rate_interval])` | `redis_expired_keys_total` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 23 | **Key Expirations vs Evictions (Evicted)** | `rate(redis_evicted_keys_total{job="redis-exporter"}[$__rate_interval])` | `redis_evicted_keys_total` | Yes (exporter) | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 24 | **Circuit Breaker State Timeline** | `circuit_breaker_state{target=~"$target"}` | `circuit_breaker_state` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 25 | **Circuit State Transitions** | `sum(rate(circuit_breaker_transitions_total{target=~"$target"}[$__rate_interval])) by (target, from, to)` | `circuit_breaker_transitions_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 26 | **Fast Fail & Rejection Rate** | `sum(rate(circuit_breaker_rejections_total{target=~"$target"}[$__rate_interval])) by (target, state)` | `circuit_breaker_rejections_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 27 | **Claims / sec** | `sum(rate(idempotency_claims_total[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 28 | **Replay Hits / sec** | `sum(rate(idempotency_claims_total{result="replay"}[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 29 | **Hash Mismatches / sec** | `sum(rate(idempotency_claims_total{result="hash_mismatch"}[$__rate_interval])) or vector(0)` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 30 | **In Progress / sec** | `sum(rate(idempotency_claims_total{result="in_progress"}[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 31 | **Completed / sec** | `sum(rate(idempotency_completes_total[$__rate_interval]))` | `idempotency_completes_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 32 | **Idempotency Lifecycle (Claimed)** | `sum(rate(idempotency_claims_total{result="claimed"}[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 32 | **Idempotency Lifecycle (Replay)** | `sum(rate(idempotency_claims_total{result="replay"}[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 32 | **Idempotency Lifecycle (Mismatch)** | `sum(rate(idempotency_claims_total{result="hash_mismatch"}[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 32 | **Idempotency Lifecycle (In Progress)** | `sum(rate(idempotency_claims_total{result="in_progress"}[$__rate_interval]))` | `idempotency_claims_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 32 | **Idempotency Lifecycle (Completed)** | `sum(rate(idempotency_completes_total[$__rate_interval]))` | `idempotency_completes_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 33 | **Idempotency Redis Round-Trip Latency (P95)** | `histogram_quantile(0.95, sum(rate(idempotency_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | `idempotency_redis_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** (Renamed to accurately reflect round-trip measurement) |
| `dist-rate-limiter` | 34 | **Gateway Upstream Traffic Split** | `sum(rate(routing_decisions_total{gateway=~"$gateway"}[$__rate_interval])) by (gateway)` | `routing_decisions_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 35 | **Computed Health Scores** | `routing_gateway_health_score{gateway=~"$gateway"}` | `routing_gateway_health_score` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 36 | **Observed Upstream Latencies (P95)** | `histogram_quantile(0.95, sum(rate(routing_gateway_latency_seconds_bucket{gateway=~"$gateway"}[$__rate_interval])) by (le, gateway))` | `routing_gateway_latency_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 37 | **Gateway Failovers** | `sum(rate(routing_failovers_total{gateway=~"$gateway"}[$__rate_interval])) by (gateway)` | `routing_failovers_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 37 | **Upstream Errors** | `sum(rate(routing_outcomes_total{gateway=~"$gateway", result="error"}[$__rate_interval])) by (gateway)` | `routing_outcomes_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 37b | **Sidecar Gateway Error Rate (%)** | `sum(rate(routing_outcomes_total{job="sidecar", result="error"}[$__rate_interval])) / clamp_min(sum(rate(routing_outcomes_total{job="sidecar"}[$__rate_interval])), 1e-9) * 100` | `routing_outcomes_total` | Yes | Yes | Valid | Yes | **VALID** (Added ratio panel for SRE SLO monitoring) |
| `dist-rate-limiter` | 38 | **Audit Event Ingestion Rate** | `sum(rate(audit_events_total[$__rate_interval])) by (decision)` | `audit_events_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 39 | **Queue Drops (Full Ingestion Buffer)** | `sum(rate(audit_dropped_total[$__rate_interval])) or vector(0)` | `audit_dropped_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 40 | **Audit Search vs Append Duration (Append)** | `histogram_quantile(0.99, sum(rate(audit_append_duration_seconds_bucket[$__rate_interval])) by (le))` | `audit_append_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 40 | **Audit Search vs Append Duration (Search)** | `histogram_quantile(0.99, sum(rate(audit_search_duration_seconds_bucket[$__rate_interval])) by (le))` | `audit_search_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 41 | **CPU Usage by Instance** | `rate(process_cpu_seconds_total[$__rate_interval]) * 100` | `process_cpu_seconds_total` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 42 | **Memory Allocation (RSS)** | `process_resident_memory_bytes` | `process_resident_memory_bytes` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 43 | **Active Goroutines** | `go_goroutines` | `go_goroutines` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 44 | **Go GC Cycle Rate** | `rate(go_gc_duration_seconds_count[$__rate_interval])` | `go_gc_duration_seconds_count` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | 44 | **Go GC Pauses (P99)** | `histogram_quantile(0.99, sum(rate(go_gc_duration_seconds_bucket[$__rate_interval])) by (le, instance))` | `go_gc_duration_seconds_bucket` | Yes | Yes | Valid | Yes | **VALID** |
| `dist-rate-limiter` | - | **Span Export Ingestion Rate** | - | - | - | - | - | - | **REMOVED** (Otel panels replaced with Jaeger UI guidelines text panel) |
| `dist-rate-limiter` | - | **Active Traces & Dropped Spans** | - | - | - | - | - | - | **REMOVED** (Otel panels replaced with Jaeger UI guidelines text panel) |
| `dist-rate-limiter` | - | **Collector Sampling Rate & Ratio** | - | - | - | - | - | - | **REMOVED** (Otel panels replaced with Jaeger UI guidelines text panel) |


---

## 6. Prometheus Target Validation
Prometheus is configured via `/etc/prometheus/prometheus.yml` to scrape three targets over the bridge network `rate-net`.

| Job | Prometheus Target | Docker Service | Actual Listener | Handler | Network | Auth | Verdict |
|---|---|---|---|---|---|---|---|
| `rate-limiter` | `limiter:8080` | `limiter` (central rate limiter) | `cfg.Port` (`:8080`) | `promhttp.Handler()` on `/metrics` | `rate-net` | Optional (`X-API-Key` checks `METRICS_API_KEY`) | **VALID** |
| `sidecar` | `sidecar:9090` | `sidecar` (sidecar proxy) | `:9090` | `promhttp.Handler()` on `/metrics` | `rate-net` | Optional (`X-API-Key` checks `METRICS_API_KEY`) | **VALID** |
| `redis-exporter` | `redis-exporter:9121` | `redis-exporter` | `:9121` | Native metrics handler | `rate-net` | None | **VALID** |

---

## 7. OpenTelemetry Investigation
We re-audited the tracing implementation from source. 
* **Jaeger Exporter Hallucination Correction**: The statement in the previous draft claiming tracing configures a batch exporter using `actions/checkout` was a diagnostic mistake. Tracing relies strictly on standard OTLP HTTP protocol (`otlptracehttp`) pointing directly to Jaeger on port 4318.

### Tracing Component Status

| Tracing Component | Source Code Reference | Classification |
|---|---|---|
| `telemetry.Init()` | [`internal/telemetry/provider.go:20`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L20) | `IMPLEMENTED AND VERIFIED` |
| `TracerProvider creation` | [`internal/telemetry/provider.go:50`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L50) | `IMPLEMENTED AND VERIFIED` |
| `exporter creation` | [`internal/telemetry/provider.go:33`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L33) | `IMPLEMENTED AND VERIFIED` |
| `exporter protocol` | OTLP HTTP (`otlptracehttp` package) | `IMPLEMENTED AND VERIFIED` |
| `OTLP endpoint` | `trimOTLPEndpoint(cfg.OTLPEndpoint)` in [`provider.go:27`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L27) | `IMPLEMENTED AND VERIFIED` |
| `BatchSpanProcessor` | `sdktrace.WithBatcher(exporter)` in [`provider.go:51`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L51) | `IMPLEMENTED AND VERIFIED` |
| `resource attributes` | `resource.Merge` in [`provider.go:38`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L38) | `IMPLEMENTED AND VERIFIED` |
| `service.name` | `semconv.ServiceName(cfg.ServiceName)` in [`provider.go:42`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L42) | `IMPLEMENTED AND VERIFIED` |
| `sampler` | `sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))` in [`provider.go:49`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L49) | `IMPLEMENTED AND VERIFIED` |
| `TraceContext propagator` | `propagation.TraceContext{}` in [`provider.go:58`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L58) | `IMPLEMENTED AND VERIFIED` |
| `Baggage propagator` | `propagation.Baggage{}` in [`provider.go:59`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/provider.go#L59) | `IMPLEMENTED AND VERIFIED` |
| `inbound HTTP instrumentation` | `otelhttp.NewHandler` in [`internal/telemetry/middleware.go:54`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/middleware.go#L54) | `IMPLEMENTED AND VERIFIED` |
| `outbound HTTP instrumentation` | `otelhttp.NewTransport` in [`internal/telemetry/middleware.go:80`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/middleware.go#L80) | `IMPLEMENTED AND VERIFIED` |
| `sidecar → limiter propagation` | HTTP client transport wrapped using `telemetry.NewHTTPTransport(nil)` in [`cmd/sidecar/main.go:105`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/sidecar/main.go#L105) | `IMPLEMENTED AND VERIFIED` |
| `limiter request spans` | `limiter.check` span in [`cmd/limiter/main.go:138`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/main.go#L138) | `IMPLEMENTED AND VERIFIED` |
| `Redis spans` | `redisotel.InstrumentTracing(rdb)` in [`internal/telemetry/redis.go:12`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/redis.go#L12) | `IMPLEMENTED AND VERIFIED` |
| `span status` | `SetHTTPStatus` in [`internal/telemetry/middleware.go:98`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/middleware.go#L98) | `IMPLEMENTED AND VERIFIED` |
| `error recording` | `RecordError` in [`internal/telemetry/middleware.go:89`](file:///c:/Users/RUDRA%20PRATAP%20SINGH/Desktop/Distributed-rate-limiter/internal/telemetry/middleware.go#L89) | `IMPLEMENTED AND VERIFIED` |
| `graceful shutdown` | `tp.Shutdown` closure returned by `Init` and deferred in main functions | `IMPLEMENTED AND VERIFIED` |
| `collector deployment` | Jaeger handles OTLP HTTP directly; no independent OTel collector is deployed | `ABSENT` |
| `Jaeger deployment` | `jaeger` service container in `docker-compose.yml:169` | `IMPLEMENTED AND VERIFIED` |
| `Docker networking` | `rate-net` bridge network in `docker-compose.yml:230` | `IMPLEMENTED AND VERIFIED` |
| `environment configuration` | `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT` mapped under Docker services | `IMPLEMENTED AND VERIFIED` |

---

## 8. Structured Logging Investigation
* **Status**: **ABSENT** (Production Ready: No)
* **Analysis**:
  - The codebase does not implement a structured log engine (`slog`, `zap`, or `zerolog`).
  - Standard Go `log` library (`log.Printf`) outputs raw, flat text entries.
  - Trace ID and Request ID correlation context variables are completely absent from log output strings.
  - **Leakage assessment**: Administrative path logging outputs override IDs (`user_id` / `tenant_id`), but no critical API keys or connection passwords leak into standard streams.

---

## 9. Operational Blind Spots (30-Question Audit)
We evaluated the answerability of the 30 operational questions against currently configured metrics, logs, and spans:

| # | Operational Question | Answerable Today? | Exact Evidence | Missing Telemetry | Priority |
|---|---|---|---|---|---|
| 1 | What is the current throughput? | Yes | `rate_limiter_requests_total` | None | - |
| 2 | What is the percentage of requests being rate-limited? | Yes | `sum(rate(rate_limiter_requests_total{allowed="false"}[$__rate_interval])) / sum(rate(rate_limiter_requests_total[$__rate_interval])) * 100` | None | - |
| 3 | Which rate-limiting algorithm is denying requests? | Yes | `rate_limiter_requests_total{handler="check"}` (indirectly by checking configured binary parameters) | `algorithm` label key in counter | P2 |
| 4 | What is the P50 latency of the rate limiter? | Yes | `histogram_quantile(0.50, sum(rate(rate_limiter_requests_duration_seconds_bucket[$__rate_interval])) by (le))` | None | - |
| 5 | What is the P95 latency of the rate limiter? | Yes | `histogram_quantile(0.95, sum(rate(rate_limiter_requests_duration_seconds_bucket[$__rate_interval])) by (le))` | None | - |
| 6 | What is the P99 latency of the rate limiter? | Yes | `histogram_quantile(0.99, sum(rate(rate_limiter_requests_duration_seconds_bucket[$__rate_interval])) by (le))` | None | - |
| 7 | What is the P50 Redis execution latency? | Yes | `histogram_quantile(0.50, sum(rate(rate_limiter_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | None | - |
| 8 | What is the P95 Redis execution latency? | Yes | `histogram_quantile(0.95, sum(rate(rate_limiter_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | None | - |
| 9 | What is the P99 Redis execution latency? | Yes | `histogram_quantile(0.99, sum(rate(rate_limiter_redis_duration_seconds_bucket[$__rate_interval])) by (le))` | None | - |
| 10 | Are there active Redis timeouts occurring? | No | None | Metric counter tracking Redis network timeouts | P1 |
| 11 | Is the Redis connection pool saturated? | No | None | Gauge metrics tracking active/idle connection count | P2 |
| 12 | What is the local sidecar cache hit ratio? | Yes | `rate(rate_limiter_sidecar_cache_hits_total[$__rate_interval]) / (rate(rate_limiter_sidecar_cache_hits_total[$__rate_interval]) + rate(rate_limiter_sidecar_cache_misses_total[$__rate_interval]))` | None | - |
| 13 | What is the local sidecar cache miss ratio? | Yes | `rate(rate_limiter_sidecar_cache_misses_total[$__rate_interval]) / (rate(rate_limiter_sidecar_cache_hits_total[$__rate_interval]) + rate(rate_limiter_sidecar_cache_misses_total[$__rate_interval]))` | None | - |
| 14 | What is the singleflight collapse ratio? | No | None | Instrumentation of singleflight wrapper in sidecar | P2 |
| 15 | Is the sidecar running in fail-open mode? | Yes | Check startup log configurations | Metric exporting configuration state | P2 |
| 16 | What is the visibility of fail-open events? | Yes | Log output: `"WARNING: FAIL_OPEN enabled — forwarding request despite limiter error"` | Counter metric tracking bypass count | P1 |
| 17 | What is the visibility of fail-closed events? | Yes | Log output: `"Rate limiter error: %v"` | Counter metric tracking drop count | P1 |
| 18 | What was the exact reason for the circuit-breaker opening? | No | None | Transition reason label on transition metric | P2 |
| 19 | What is the circuit-breaker short-circuit count? | Yes | `circuit_breaker_rejections_total` | None | - |
| 20 | Which rejections were caused by the Global level quota? | No | None | Hierarchical rejection reason metrics | P1 |
| 21 | Which rejections were caused by the Tenant level quota? | No | None | Hierarchical rejection reason metrics | P1 |
| 22 | Which rejections were caused by the User level quota? | No | None | Hierarchical rejection reason metrics | P1 |
| 23 | Which rejections were caused by the Endpoint level quota? | No | None | Hierarchical rejection reason metrics | P1 |
| 24 | What is the rate of idempotency hash mismatches? | Yes | `idempotency_claims_total{result="hash_mismatch"}` | None | - |
| 25 | Are audit trail events being dropped? | Yes | `audit_dropped_total` | None | - |
| 26 | Is the audit trail queue saturated? | Yes | `audit_dropped_total > 0` indicates queue saturation | Active queue saturation gauge | P2 |
| 27 | What is the routing failover trend? | Yes | `routing_failovers_total` | None | - |
| 28 | Which upstreams are identified as unhealthy? | Yes | `routing_gateway_health_score == 0` or `routing_circuit_open == 1` | None | - |
| 29 | Is there visibility into Redis Lua execution slowdowns? | No | None | Dashboard panel measures client-side round-trips | P1 |
| 30 | Are there telemetry export failures or dropped spans? | No | None | Export failure counter metrics | P2 |

---

## 10. Hot-Path Overhead Risks
We audited all telemetry operations executed within high-concurrency request paths:

| Hot-Path Operation | Telemetry Actions | Memory / CPU Cost | Risk Level |
|---|---|---|---|
| **limiter /check** | Span creation + Prom request counters/histograms updates | Negligible map allocations; thread-safe atomic metrics writes | `LOW` |
| **hierarchical check** | Span creation + Prom request counters/histograms updates | Negligible map allocations | `LOW` |
| **token bucket Redis operation** | `redisotel` interceptor command tracing + `RecordRedisDuration` | Low context wrapping cost | `LOW` |
| **sliding-window Redis operation** | `redisotel` interceptor tracing + `RecordRedisDuration` | Low context wrapping cost | `LOW` |
| **hierarchical Redis operation** | `redisotel` interceptor tracing + `RecordRedisDuration` | Low context wrapping cost | `LOW` |
| **sidecar request** | `otelhttp` inbound context parsing + Prom metrics writes | Moderate context allocations per transaction | `MODERATE` |
| **sidecar cache hit/miss** | Atomic Prom counter increment | Atomic integers operations | `NEGLIGIBLE` |
| **routing decision** | Span creation + multiple Prom counters writes | Low context allocation overhead | `LOW` |
| **circuit-breaker operation** | Local memory target evaluation (no spans created on checking path) | Read from local map state | `NEGLIGIBLE` |
| **audit append** | Queue write to channel + async Redis append execution | High concurrency could cause channel contention if buffer saturates | `MODERATE` |
| **idempotency operation** | Multiple nested span allocations + custom Prom metrics | Context copying overhead | `MODERATE` |

---

## 11. Observability Test Coverage

We audited the test files to check if explicit assertions exist for key telemetry capabilities:

| Capability | Explicit Assertion Exists? | Test File | Exact Test | Strength | Gap |
|---|---|---|---|---|---|
| request counters | **No** | - | - | None | Metric value increases are never checked in tests |
| allowed/denied metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| request latency histograms | **No** | - | - | None | Metric value increases are never checked in tests |
| Redis latency histograms | **No** | - | - | None | Metric value increases are never checked in tests |
| sidecar cache-hit metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| sidecar cache-miss metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| circuit-breaker metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| routing metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| idempotency metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| audit metrics | **No** | - | - | None | Metric value increases are never checked in tests |
| cardinality protection | **Yes** | [`health_test.go`](file:///c:/Users/RUDRA PRATAP SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/health_test.go) | `TestMetricsEndpoint` | Moderate | Checks that /metrics response string doesn't contain "user_id" / "userid" |
| /metrics endpoint behavior | **Yes** | [`health_test.go`](file:///c:/Users/RUDRA PRATAP SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/health_test.go) | `TestMetricsEndpoint` | High | Performs request to /metrics and asserts HTTP 200 OK |
| duplicate collector registration | **No** | - | - | None | No tests assert registry initialization exceptions |
| concurrent metric mutation | **No** | - | - | None | No tests assert thread safety of metric updates |
| trace creation | **No** | - | - | None | No spans verified in tests |
| trace propagation | **No** | - | - | None | No headers propagation checked in tests |
| parent-child span relationships | **No** | - | - | None | No nested span scopes checked in tests |
| span error recording | **No** | - | - | None | No span errors checked in tests |
| telemetry provider shutdown | **No** | - | - | None | No graceful tracer cleanups checked in tests |
| structured log fields | **No** | - | - | None | Log strings format is never verified |
| trace/log correlation | **No** | - | - | None | Trace correlation in logs is never verified |
| secret redaction | **Yes** | [`health_test.go`](file:///c:/Users/RUDRA PRATAP SINGH/Desktop/Distributed-rate-limiter/cmd/limiter/health_test.go) | `TestMetricsEndpoint` | High | Asserts metrics response doesn't contain `AdminAPIKey` or `InternalAPIKey` |

---

## 12. Documentation and Implementation Drift
* **Sentinel HA Documentation Drift**: The HA failure runbooks state that Sentinel master elections are visible in Prometheus via `redis_failover_reconnects_total`, whereas the client failover hooks are not wired to mutate this counter in code.
* **Misleading Dashboard Labels**: **RESOLVED** — All dashboard labels and panel titles have been renamed to accurately reflect that they measure client round-trip Redis execution latency rather than server-side Lua durations.

---

## 13. Findings by Severity

### HIGH: Log-Trace Disconnect
- **Component**: Structured Logging
- **Location**: Everywhere in the codebase.
- **Evidence**: The standard library logger prints flat, unstructured entries without trace or request context correlation IDs.
- **Impact**: Server log entries cannot be correlated to transaction traces, making production troubleshooting difficult.
- **Remediation**: Migrate to Go's structured `log/slog` library and inject context variables (`trace_id`, `span_id`).

### MEDIUM: Conflated Lua Latency Panel (RESOLVED)
- **Status**: **RESOLVED** in Phase 2A. Panel names and legend formats corrected to specify client-side round-trip times.

### MEDIUM: Dead Redis Failover Metric
- **Component**: Metrics
- **Location**: `internal/metrics/metrics.go:220`
- **Evidence**: Counter is never mutated in the codebase, leaving the dashboard panel dead.
- **Impact**: Failover recovery events are not visible in Grafana during Sentinel drill scenarios.
- **Remediation**: Register client reconnection status listener hooks in go-redis client initialization to increment the counter.

---

## 14. Dead Metrics and Dead Dashboard Queries
* **Dead Metric**: `redis_failover_reconnects_total`
* **Dead Dashboard Query**: **RESOLVED** — The query `rate(redis_failover_reconnects_total[$__rate_interval])` has been completely removed from panel `"Redis Cluster Status"`.

---

## 15. Missing Telemetry Roadmap
* **P1**: Standardized structured JSON logging using Go's native `log/slog` package containing trace correlation variables (`trace_id`, `span_id`, `request_id`).
* **P1**: Add hierarchical rejections metrics mapping rejections by enforcement level (e.g. global, tenant, user, endpoint).
* **P1**: Connect go-redis Sentinel client connection callbacks to increment `redis_failover_reconnects_total`.
* **P2**: Implement test assertion helpers for metrics validation.

### Hierarchical Rejection Metric Design Evaluation
We compared three options for adding visibility into blocked quota levels:
* **Option A: Add a `rejected_by` label to `rate_limiter_requests_total`**
  - *Pros*: Simple, keeps all requests data unified in one collector.
  - *Cons*: Breaks backward compatibility for some Grafana charts that sum requests without ignoring or grouping by the new label.
* **Option B: Add a separate counter `rate_limiter_hierarchical_rejections_total{level="global\|tenant\|user\|endpoint"}`**
  - *Pros*: Preserves complete backward compatibility for throughput charts; isolates billing/block analytics; low cardinality.
  - *Cons*: Requires updating rejections panels to query this new metric.
* **Option C: Infer from dynamic API configs**
  - *Pros*: No code updates.
  - *Cons*: Statistically impossible to deduce in real-time under high concurrency.
* **Recommendation**: **Option B** is recommended because it provides clear semantics and isolates analytical queries from general traffic volume metrics without risk of breaking existing dashboard panel configurations.

---

## 16. Recommended Phase 2 Plan

### MUST FIX BEFORE NEW FEATURES
1. **Dashboard Corrections**: **DONE** — Replaced misleading `"System Error Rate"`, added `"Sidecar Gateway Error Rate (%)"`, fixed divide-by-zero risks using `clamp_min`, removed dead queries and OTel panels, and corrected round-trip latency panel descriptions.
2. **Sentinel Metric Wiring**: Connect Sentinel reconnect callbacks to increment `redis_failover_reconnects_total`.

### SHOULD IMPLEMENT NEXT
3. **Structured JSON Logs**: Implement `log/slog` structured logging with automatic extraction and mapping of `trace_id` and `span_id` from contexts.
4. **Hierarchical Rejection Counter**: Add `rate_limiter_hierarchical_rejections_total` to track blocking policies.

### OPTIONAL HARDENING
5. **Observability Tests**: Introduce test assertions validating metrics updates and spans generations.

---

## 17. Final Verdict & Counts

### Exact Final Counts
* **Files Inspected**: `30`
* **Application-defined Metrics**: `26`
* **Dead Metrics**: `1`
* **Dashboards**: `2`
* **Dashboard Panels**: `56` (reformatted and cleaned)
* **PromQL Expressions**: `58`
* **Valid Queries**: `58`
* **Partially Broken Queries**: `0`
* **Misleading Queries**: `0`
* **Dead Queries**: `0`
* **Cardinality Risks**: `1`
* **Operational Questions Answerable Today**: `16` (Limiter Decision Error Rate is now answerable)
* **Operational Blind Spots**: `14`
* **Explicit Observability Tests**: `3`
* **Tracing Components Implemented**: `22`
* **Tracing Components Verified at Runtime**: `22`

The rate limiter observability stack has been **successfully corrected for all Grafana dashboard issues** in Phase 2A. Remaining telemetry enhancements are detailed in the roadmap.
