# Observability Architecture

> Engineering journal. How I wired traces, metrics, and correlation IDs across this fleet.

## Problem Statement

A request in the distributed rate limiter flows **client to sidecar to central limiter to Redis** (and optional upstream or routing). For production debugging I needed end-to-end latency breakdown, Redis and command visibility, rate-limit decision attribution, and **stable request correlation** for support. I also needed to keep `/health` and `/metrics` from flooding the trace backend.

## Why the problem exists

Without observability, answering "is the sidecar slow, the limiter, or Redis?" is guesswork. Multiple binaries (`rate-limiter`, `rate-sidecar`) and shared Redis mean logs alone are insufficient. The same user retry can land on different pod logs. Prometheus without careful label discipline can OOM the TSDB (for example per-user labels). In production incidents, missing W3C trace propagation leaves upstream and downstream disconnected.

## Design goals

1. **OpenTelemetry traces** via OTLP HTTP exporter (Jaeger-compatible).
2. **Prometheus metrics** from `internal/metrics`. Low-cardinality labels only.
3. **Correlation IDs**. `X-Request-ID` generation and propagation. Trace IDs in response headers.
4. **Redis instrumentation**. `redisotel` tracing and metrics on commands with context.
5. **Outbound HTTP propagation**. Sidecar to limiter to gateway calls carry trace context.
6. **Health and metrics trace filter**. Scrapers do not pollute the trace backend.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Logs-only with JSON | No automatic distributed trace linking. |
| Jaeger native agent UDP | OTLP HTTP is simpler in Docker Compose. Jaeger all-in-one accepts OTLP. |
| Datadog or commercial APM | I wanted an OSS stack for the demo and repo. |
| Per-user Prometheus labels | Explicitly rejected. TSDB cardinality explosion. |
| 100% trace sampling always | Cost and latency. I made the ratio configurable. |

## Final architecture

### OpenTelemetry provider (`internal/telemetry/provider.go`)

Activation: `OTEL_ENABLED=true`

```
Init(ctx, cfg):
  otlptracehttp.New → OTLP HTTP exporter
  TracerProvider with Batcher
  ParentBased(TraceIDRatioBased(sample_ratio))
  Propagators: W3C TraceContext + Baggage
```

**Config** (`telemetry.LoadConfigFromEnv(serviceName)`):

| Env | Purpose | Default |
|-----|---------|---------|
| `OTEL_ENABLED` | Master switch | false |
| `OTEL_SERVICE_NAME` | Override service name | arg to LoadConfigFromEnv |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Collector URL | `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_INSECURE` | TLS skip | true (not `false`) |
| `OTEL_TRACES_SAMPLER_ARG` | Sample ratio 0 to 1 | 1.0 |

Docker Compose (`docker-compose.yml`) ships Jaeger but sets `OTEL_ENABLED=false` on limiter and sidecar by default (SDK conflict workaround per audit §7). Enable tracing explicitly:

```yaml
OTEL_ENABLED=true
OTEL_SERVICE_NAME=rate-limiter | rate-sidecar
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
OTEL_EXPORTER_OTLP_INSECURE=true
```

Jaeger service: `jaegertracing/all-in-one:1.58`. UI and OTLP receiver on 4318.

### HTTP middleware (`internal/telemetry/middleware.go`)

`WrapHandler(mux, serviceName)` chain:

1. **`requestIDMiddleware`**. `X-Request-ID` from header or `uuid.New()`. Stored in context (`RequestIDFromContext`).
2. **`otelhttp.NewHandler`**. Span per request. `SkipHealthMetrics` filter excludes `/health` and `/metrics` from tracing.
3. **Response headers**. Echo `X-Request-ID`, `X-Trace-ID`, `X-Span-ID`.

Span naming: `METHOD path` (for example `GET /check`).

### Outbound propagation

`telemetry.NewHTTPTransport(nil)` wraps the sidecar HTTP client. W3C `traceparent` is injected on limiter checks and gateway forwards.

Manual spans via `telemetry.StartSpan(ctx, name, attrs...)`:

| Service | Span examples |
|---------|---------------|
| Limiter | `limiter.check`, `limiter.check_hierarchical` |
| Sidecar | `sidecar.proxy`, `sidecar.idempotency`, `sidecar.rate_limit_check`, `sidecar.upstream_proxy`, `sidecar.intelligent_route` |
| Idempotency | `idempotency.claim`, `idempotency.complete`, `idempotency.fail` |

Errors: `telemetry.RecordError(span, err)`. HTTP status: `telemetry.SetHTTPStatus(span, code)`.

### Redis OTel (`internal/telemetry/redis.go`)

When `OTEL_ENABLED`:

```go
redisotel.InstrumentTracing(rdb)
redisotel.InstrumentMetrics(rdb)
```

Applied in limiter `main` and sidecar `connectSidecarRedis` after ping. Redis commands with `context` get child spans.

### Correlation ID flow

```text
Client (optional X-Request-ID)
  → Sidecar WrapHandler: ensure ID in context and response
  → Limiter WrapHandler: same
  → recordAudit: RequestIDFromContext → audit event
  → Idempotency replay headers: x-request-id, x-correlation-id whitelisted
```

Both the audit trail and idempotency replay respect `X-Request-ID`. Support can search audit from a ticket reference.

### Prometheus metrics (`internal/metrics/metrics.go`)

**Design rule** (package comment): labels are intentionally low-cardinality. **Never per-user**.

**Core limiter**:

- `rate_limiter_requests_total{handler, allowed}`
- `rate_limiter_requests_duration_seconds{handler}`
- `rate_limiter_redis_duration_seconds`

**Sidecar cache**:

- `rate_limiter_sidecar_cache_hits_total`
- `rate_limiter_sidecar_cache_misses_total`

**Idempotency**:

- `idempotency_claims_total{result}` (claimed, replay, in_progress, hash_mismatch, failed, error)
- `idempotency_completes_total`
- `idempotency_redis_duration_seconds`

**Circuit breaker**:

- `circuit_breaker_state{target}` (0, 1, 2)
- `circuit_breaker_transitions_total{target, from, to}`
- `circuit_breaker_rejections_total{target, state}`
- `circuit_breaker_outcomes_total{target, outcome}`
- `circuit_breaker_failure_rate{target}`
- `circuit_breaker_latency_ema_ms{target}`
- `circuit_breaker_redis_duration_seconds`

**Routing**:

- `routing_decisions_total{gateway, failover}`
- `routing_outcomes_total{gateway, result}`
- `routing_failovers_total{gateway}`
- `routing_gateway_health_score{gateway}`
- `routing_gateway_latency_seconds{gateway}`
- `routing_redis_duration_seconds`
- `routing_circuit_open{gateway}` (legacy; mirrors breaker open state)

**Audit**:

- `audit_events_total{decision, handler}`
- `audit_append_duration_seconds`
- `audit_search_duration_seconds`
- `audit_dropped_total`

**Redis HA**:

- Failover drills: use `/health` `redis.role` and `circuit_breaker_transitions_total{target="redis"}`.

### Prometheus scraping (`deploy/prometheus/prometheus.yml`)

```yaml
scrape_configs:
  - job_name: rate-limiter
    targets: ['limiter:8080']
  - job_name: sidecar
    targets: ['sidecar:9090']
```

Metrics endpoints: optional `METRICS_REQUIRE_AUTH=true` with `METRICS_API_KEY` (header `X-API-Key` or `X-Internal-API-Key`) on both services.

### Trace diagram reference

See `docs/diagrams/tracing-flow.md` for a visual request flow.

## Tradeoffs

- **OTLP over HTTP**. Simpler than gRPC in compose. Slightly more overhead.
- **ParentBased sampling**. Child spans follow parent. Root sampling ratio applies.
- **Health and metrics excluded from traces**. Correct for noise. Harder to debug scraper issues.
- **No custom exemplars**. Histograms do not link to trace IDs yet.
- **Sentinel failover signals**: `/health` `redis.role` and `circuit_breaker_transitions_total{target="redis"}` (no dedicated failover reconnect counter).
- **Baggage enabled**. Unused by default. Potential PII risk if misused later.
- **Structured logging absent**. Stdlib `log` only; no `trace_id` in log lines (audit §8).

## Failure modes

| Scenario | Effect |
|----------|--------|
| Jaeger down | Batcher drops or buffers spans. Process continues. |
| OTEL init failure | `log.Fatalf` on startup. Fail fast. |
| Missing context on Redis call | Span not attached to that command. |
| High sample ratio plus traffic | Jaeger storage pressure. |
| Cardinality mistake in new metric | TSDB or memory issues. Code review guardrail. |

## Operational concerns

- Jaeger UI: typically port 16686 in the all-in-one image.
- Tune `OTEL_TRACES_SAMPLER_ARG` in prod (for example 0.1).
- Dashboards: correlate `rate_limiter_requests_total{allowed="false"}` spike with `circuit_breaker_state` and `audit_events_total{decision="denied"}`.
- `X-Trace-ID` response header lets clients include it in support tickets.
- Shutdown: `otelShutdown` with 5s timeout flushes the batcher on SIGTERM path (defer in main).
- Grafana dashboards are provisioned from `deploy/grafana/` in default compose (`:3000`).

## Performance implications

- **otelhttp handler**: roughly microseconds overhead per request. Excluded paths have zero trace cost.
- **Batcher async export**: minimal hot path blocking.
- **Redis redisotel**: small per-command overhead. Use context everywhere.
- **Prometheus promauto**: in-process registry. `/metrics` scrape parses all counters. Keep label cardinality low.
- **Manual spans**: limit nesting depth on `/check`. Only one child span per handler.

## Lessons learned

I wrote the **label cardinality comment** in the metrics package first. That discipline saved me when adding routing and gateway metrics later. The `SkipHealthMetrics` filter was essential. Early on, Prometheus scrape every 5s created thousands of useless spans. I placed request ID middleware **outside** trace middleware so correlation still works when tracing is disabled. The OTLP endpoint default `localhost:4318` is local dev friendly. Compose overrides with service name `jaeger`. Whitelisting `x-correlation-id` in idempotency replay aligns observability with the API contract.
