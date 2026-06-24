# Observability Architecture

> इंजीनियरिंग जर्नल — मैंने इस fleet के लिए traces, metrics, और correlation IDs कैसे wire किए।

## Problem Statement

Distributed rate limiter में request **client → sidecar → central limiter → Redis** (और optional upstream/routing) से गुजरती है। Production debug के लिए मुझे चाहिए था: end-to-end latency breakdown, Redis/command visibility, rate-limit decision attribution, और support के लिए **stable request correlation** — बिना `/health` और `/metrics` को trace noise से भरे।

## Why the problem exists

बिना observability के "sidecar slow है या limiter या Redis?" सवाल का जवाब guesswork है। Multiple binaries (`rate-limiter`, `rate-sidecar`) और shared Redis mean logs alone insufficient — same user retry में different pod logs। Prometheus without careful label discipline OOM कर सकता है (per-user labels)। Production incidents में W3C trace propagation के बिना upstream/downstream disconnect रहता है।

## Design goals

1. **OpenTelemetry traces** via OTLP HTTP exporter (Jaeger-compatible)।
2. **Prometheus metrics** from `internal/metrics` — low-cardinality labels only।
3. **Correlation IDs** — `X-Request-ID` generation/propagation; trace IDs in response headers।
4. **Redis instrumentation** — `redisotel` tracing + metrics on commands with context।
5. **Outbound HTTP propagation** — sidecar → limiter → gateway calls carry trace context।
6. **Health/metrics trace filter** — scrapers don't pollute trace backend।

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Logs-only with JSON | No automatic distributed trace linking |
| Jaeger native agent UDP | OTLP HTTP simpler in Docker Compose; Jaeger all-in-one accepts OTLP |
| Datadog / commercial APM | Wanted OSS stack for demo/repo |
| Per-user Prometheus labels | Explicitly rejected — TSDB cardinality explosion |
| 100% trace sampling always | Cost/latency; made ratio configurable |

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
| `OTEL_TRACES_SAMPLER_ARG` | Sample ratio 0-1 | 1.0 |

Docker Compose (`docker-compose.yml`):

```yaml
OTEL_ENABLED=true
OTEL_SERVICE_NAME=rate-limiter | rate-sidecar
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
OTEL_EXPORTER_OTLP_INSECURE=true
```

Jaeger service: `jaegertracing/all-in-one:1.58` — UI + OTLP receiver on 4318।

### HTTP middleware (`internal/telemetry/middleware.go`)

`WrapHandler(mux, serviceName)` chain:

1. **`requestIDMiddleware`** — `X-Request-ID` from header or `uuid.New()`; stored in context (`RequestIDFromContext`)
2. **`otelhttp.NewHandler`** — span per request; `SkipHealthMetrics` filter excludes `/health`, `/metrics` from tracing
3. **Response headers** — echo `X-Request-ID`, `X-Trace-ID`, `X-Span-ID`

Span naming: `METHOD path` (e.g. `GET /check`).

### Outbound propagation

`telemetry.NewHTTPTransport(nil)` wraps sidecar HTTP client — W3C `traceparent` injected on limiter checks and gateway forwards।

Manual spans via `telemetry.StartSpan(ctx, name, attrs...)`:

| Service | Span examples |
|---------|---------------|
| Limiter | `limiter.check`, `limiter.check_hierarchical` |
| Sidecar | `sidecar.proxy`, `sidecar.idempotency`, `sidecar.rate_limit_check`, `sidecar.upstream_proxy`, `sidecar.intelligent_route` |
| Idempotency | `idempotency.claim`, `idempotency.complete`, `idempotency.fail` |

Errors: `telemetry.RecordError(span, err)`; HTTP status: `telemetry.SetHTTPStatus(span, code)`।

### Redis OTel (`internal/telemetry/redis.go`)

When `OTEL_ENABLED`:

```go
redisotel.InstrumentTracing(rdb)
redisotel.InstrumentMetrics(rdb)
```

Applied in limiter `main` and sidecar `connectSidecarRedis` after ping — Redis commands with `context` get child spans।

### Correlation ID flow

```text
Client (optional X-Request-ID)
  → Sidecar WrapHandler: ensure ID in context + response
  → Limiter WrapHandler: same
  → recordAudit: RequestIDFromContext → audit event
  → Idempotency replay headers: x-request-id, x-correlation-id whitelisted
```

Audit trail और idempotency replay दोनों `X-Request-ID` respect करते हैं — support ticket से audit search possible।

### Prometheus metrics (`internal/metrics/metrics.go`)

**Design rule** (package comment): labels intentionally low-cardinality — **never per-user**।

**Core limiter**:

- `rate_limiter_requests_total{handler, allowed}`
- `rate_limiter_requests_duration_seconds{handler}`
- `rate_limiter_redis_duration_seconds`

**Sidecar cache**:

- `rate_limiter_sidecar_cache_hits_total`
- `rate_limiter_sidecar_cache_misses_total`

**Idempotency**:

- `idempotency_claims_total{result}` — claimed, replay, in_progress, hash_mismatch, failed, error
- `idempotency_completes_total`
- `idempotency_redis_duration_seconds`

**Circuit breaker**:

- `circuit_breaker_state{target}` — 0/1/2
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

- `redis_failover_reconnects_total` — defined for Sentinel reconnect tracking

### Prometheus scraping (`deploy/prometheus.yml`)

```yaml
scrape_configs:
  - job_name: rate-limiter
    targets: ['limiter:8080']
  - job_name: sidecar
    targets: ['sidecar:9090']
```

Metrics endpoints: optional `METRICS_REQUIRE_AUTH` + API key on both services।

### Trace diagram reference

See `docs/diagrams/tracing-flow.mmd` for visual request flow।

## Tradeoffs

- **OTLP over HTTP** — simpler than gRPC in compose; slightly more overhead。
- **ParentBased sampling** — child spans follow parent; root sampling ratio applies。
- **Health/metrics excluded from traces** — correct for noise; harder to debug scraper issues。
- **No custom exemplars** — histograms don't link to trace IDs yet。
- **redis_failover_reconnects_total** — metric defined; wiring to FailoverClient reconnect hook is future work。
- **Baggage enabled** — unused by default; potential PII risk if misused later。

## Failure modes

| Scenario | Effect |
|----------|--------|
| Jaeger down | Batcher drops/spans buffer; process continues |
| OTEL init failure | `log.Fatalf` on startup — fail fast |
| Missing context on Redis call | Span not attached to that command |
| High sample ratio + traffic | Jaeger storage pressure |
| Cardinality mistake in new metric | TSDB/memory issues — code review guardrail |

## Operational concerns

- Jaeger UI: typically port 16686 in all-in-one image。
- Tune `OTEL_TRACES_SAMPLER_ARG` in prod (e.g. 0.1)。
- Dashboards: correlate `rate_limiter_requests_total{allowed="false"}` spike with `circuit_breaker_state` and `audit_events_total{decision="denied"}`。
- `X-Trace-ID` response header — clients can include in support tickets。
- Shutdown: `otelShutdown` with 5s timeout flushes batcher on SIGTERM path (defer in main)。
- Grafana not in repo — Prometheus raw or add dashboard layer separately。

## Performance implications

- **otelhttp handler**: ~microseconds overhead per request; excluded paths zero trace cost。
- **Batcher async export**: minimal hot path blocking。
- **Redis redisotel**: small per-command overhead; use context everywhere。
- **Prometheus promauto**: in-process registry; `/metrics` scrape parses all counters — keep label cardinality low。
- **Manual spans**: limit nesting depth on `/check` — only one child span per handler。

## Lessons learned

मैंने सबसे पहले **label cardinality comment** metrics package में लिखा — यह discipline बाद में routing/gateway metrics add करते समय बचाव करता है। `SkipHealthMetrics` filter जरूरी था; पहले Prometheus scrape हर 5s पर thousands of useless spans बना रहा था। Request ID middleware को trace middleware के **बाहर** रखा ताकि tracing disabled होने पर भी correlation काम करे। OTLP endpoint default `localhost:4318` local dev friendly है; compose में service name `jaeger` override। Idempotency replay में `x-correlation-id` whitelist करना observability और API contract को align करता है।
