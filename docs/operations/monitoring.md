# Monitoring

## Problem Statement

Debugging a distributed rate limiter without observability is blind work. Is that 429 quota enforcement or 503 because Redis is down? Which gateway got the route? Is the circuit open or half-open? I wired OpenTelemetry traces (Jaeger), Prometheus-style metrics (`internal/metrics`), and admin inspection APIs into one monitoring story. This doc is what I actually look at in production, and how those signals correlate with benchmark numbers like **~1,000 RPS** sustainable load.

## Why the problem exists

Failures in rate limiting systems are subtle:

- A 429 storm is correct behavior, but SREs page anyway.
- Without metrics, a Redis outage looks like "rate limited" to clients when they see 503 vs 429 confusion.
- Routing drift can send traffic to gateway-c silently.
- Idempotency keys stuck in `in_progress` are invisible without the admin API.
- Failover reconnects are not exported to Prometheus today: `redis_failover_reconnects_total` is declared but never incremented (`docs/OBSERVABILITY_FORENSIC_AUDIT.md` §14). Use `/health` and circuit metrics instead.

## Design goals

1. OTEL traces from sidecar and limiter spans to Jaeger (`:16686`).
2. RED metrics: rate, errors, duration per component.
3. Domain metrics for routing, circuit, idempotency, and audit.
4. Health endpoints at `/health` with Redis role and replication info.
5. Admin read APIs for runtime state without Redis CLI.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Logs only | High cardinality; hard to correlate |
| Custom dashboard only | Metrics source still needed |
| No tracing | Miss cross-service latency |
| Redis MONITOR | Too heavy for prod |
| Sidecar statsd only | OTEL standard won |

I use OTEL plus Prometheus metrics plus admin API as a trinity.

## Final architecture

**OTEL env** (limiter + sidecar; default compose sets `OTEL_ENABLED=false` — set `true` to export traces):

```
OTEL_ENABLED=true
OTEL_SERVICE_NAME=rate-limiter  # or rate-sidecar
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
OTEL_EXPORTER_OTLP_INSECURE=true
```

**Jaeger UI:** `http://localhost:16686`

**Key metrics:**

| Metric | Component | Meaning |
|--------|-----------|---------|
| `rate_limiter_redis_duration_seconds` | limiter | Client round-trip to Redis (Lua path) |
| `circuit_breaker_state{target}` | limiter / sidecar | 0=closed, 1=open, 2=half_open |
| `circuit_breaker_rejections_total` | limiter / sidecar | Fast-fail while open |
| `circuit_breaker_failure_rate` | limiter / sidecar | Rolling failure ratio |
| `routing_decisions_total{gateway,failover}` | sidecar | Route choices |
| `routing_gateway_health_score{gateway}` | sidecar | 0 to 100 health |

**Health:**

```bash
curl http://localhost:8080/health | jq .
curl http://localhost:9090/health | jq .
```

**Admin inspection** (`:8082`, header `X-API-Key: $ADMIN_API_KEY`):

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/routing/gateways
curl -H "X-API-Key: $ADMIN_API_KEY" "http://localhost:8082/admin/audit?tenant_id=default&decision=denied&limit=20"
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/audit/stats
```

**Benchmark correlation:** `run-all.ps1` plus `metrics/collect-metrics.ps1` produces `docker stats` JSON for CPU and memory vs actual RPS.

## Tradeoffs

Prometheus (`:9091`) and Grafana (`:3000`) run in default compose with provisioned dashboards under `deploy/grafana/`. Jaeger is always on for OTLP when `OTEL_ENABLED=true`. Per-gateway metric labels are bounded by the `GATEWAYS` seed list but arbitrary admin target IDs can leak series (audit §4). OTEL overhead is ~1 to 2% at **1,000 RPS** when enabled; sample at higher load. The admin API is not a metrics source: polling admin in a loop is an anti-pattern; I use it for incidents. Audit search via `admin/audit` does Redis scans and is not sub-ms at 100k events. Application logging is unstructured stdlib `log` with no trace correlation (audit §8).

## Failure modes

Silent fail-open is dangerous: if `FAIL_OPEN=true`, 429 and 503 drop and I should watch for an allow rate spike. A circuit stuck open shows `circuit_breaker_state=1` forever; I check Redis connectivity. Trace dropout happens when Jaeger OOMs or OTEL buffers overflow (only when `OTEL_ENABLED=true`). `/health` can look OK while Redis is slow; I watch `rate_limiter_redis_duration_seconds` p99. Sentinel failover is visible via `/health` `redis.role` changes and `circuit_breaker_transitions_total{target="redis"}` — not via `redis_failover_reconnects_total` (dead metric per audit §14).

## Operational concerns

I plot actual RPS vs p99 using `benchmarks/graphs/latency-vs-rps.png` as a template. Alerts I care about: Redis error rate, circuit open longer than 60s, 503 rate above 1%, `/health` redis role change during HA drills. During benchmarks I save `metrics/results/{test-name}.json` for post-hoc CPU correlation. For idempotency I monitor 409 in_progress rate vs 201/200 completion ratio. I run `chaos_test.ps1` quarterly to verify 503 metric spike and recovery drop.

## Performance implications

Sustainable load is **~1,000 actual RPS** with p99 **3.2 ms** on the limiter path (`summary.md`).

The collapse signature is p99 **3.5 s** and **10% errors** at **1,353 actual RPS** (5,000 target). I monitor for that knee in prod, scaled proportionally.

Circuit `Allow` is ~**120µs** on miniredis, negligible vs Redis quota RTT (**2 to 10 ms** local).

Idempotency replay is **p95 5.7 ms**, a separate SLI from the mutating path **p95 14.9 ms** claim.

Audit append is ~**300µs** in bench. I enable `ENABLE_AUDIT_TRAIL=true` with retention `AUDIT_RETENTION_HOURS=168` and `AUDIT_MAX_EVENTS=100000`.

## Lessons learned

I used to monitor only 429 count. During a Redis outage we got 503s and on-call assumed users were hitting quota. Separate metric labels saved hours.

A Jaeger trace with a `routing.select` span helped me debug a gateway-c leak that was diluted on the metrics dashboard.

Admin `/admin/circuit` was faster than Redis `HGETALL` during an incident, but I still need metrics export for automation.
