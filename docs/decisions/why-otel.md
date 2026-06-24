# Why OpenTelemetry

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** `internal/telemetry`, Jaeger export, Redis instrumentation, HTTP propagation

---

## Problem Statement

A single user request crosses sidecar → limiter → Redis → optional gateway → upstream. When latency spikes or 503s appear, grep-ing logs per service is insufficient. I needed correlated traces across processes with optional sampling so I could debug production without drowning in spans.

## Why the problem exists

Distributed rate limiting **is** a distributed systems problem. Failures surface as "sidecar slow" when the root cause is Redis circuit open or gateway failover. Without trace context propagation, `X-Request-ID` alone cannot show nested span timing or which `gateway.id` was selected.

## Design goals

- Opt-in via env: `OTEL_ENABLED=true`. zero overhead path when disabled.
- OTLP HTTP export: `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`) for Jaeger/collector compatibility.
- Service identity: `OTEL_SERVICE_NAME` overrides default (`rate-limiter`, `rate-sidecar` from `LoadConfigFromEnv(serviceName)`).
- Sampling control: `OTEL_TRACES_SAMPLER_ARG` (0.0 to 1.0 ratio).
- Redis hooks: `telemetry.InstrumentRedis(rdb)` via `redisotel` when enabled in both binaries.
- HTTP middleware: `telemetry.WrapHandler` on sidecar mux; `telemetry.NewHTTPTransport` on outbound clients.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **Logs only** | No automatic parent/child timing across hops. |
| **Jaeger agent UDP only** | OTLP is the modern collector standard; one exporter config. |
| **Custom trace IDs in headers** | Reinvents W3C tracecontext; poor tooling integration. |
| **100% trace sampling always** | Prohibitive at high RPS; ratio sampler is default-friendly. |
| **Metrics-only (Prometheus)** | Histograms lack per-request causality for failover chains. |

## Final architecture

Initialization in `cmd/limiter/main.go` and `cmd/sidecar/main.go`:

```go
otelCfg := telemetry.LoadConfigFromEnv("rate-limiter") // or rate-sidecar
otelShutdown, err := telemetry.Init(context.Background(), otelCfg)
```

Key spans (from code):

| Span name | Location | Attributes |
|-----------|----------|------------|
| `sidecar.proxy` | `serveNormal` | `user.id`, `http.path` |
| `sidecar.idempotency` | `serveIdempotent` | `idempotency.key` |
| `sidecar.rate_limit_check` | `checkRateLimit` | `hierarchical`, `rate_limit.allowed` |
| `sidecar.intelligent_route` | `routing.Router.Forward` | `gateway.id`, `gateway.score`, `gateway.failover` |
| `idempotency.claim` / `complete` | `idempotency.RedisStore` | key, status |
| Limiter handlers | `telemetry.StartSpan` on `/check` paths | user, tenant, algorithm |

Redis commands receive context-carried spans when `InstrumentRedis` is active (`internal/telemetry/redis.go`).

Env reference:

| Variable | Default | Role |
|----------|---------|------|
| `OTEL_ENABLED` | `false` | Master switch |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4318` | Collector URL |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` (not `false`) | TLS off for local Jaeger |
| `OTEL_SERVICE_NAME` | per-binary default | Service label |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | Trace sampling ratio |

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| Opt-in OTEL | No prod surprise overhead | Easy to forget enabling in staging |
| OTLP HTTP | Simple docker-compose wiring | Slightly higher latency than gRPC exporter |
| Redis auto-instrumentation | Every EVAL visible | More spans per request |
| Context propagation on outbound HTTP | End-to-end traces | All clients must use instrumented transport |

## Failure modes

- Collector down: Exporter buffers then drops; core limiting still works. tracing is best-effort.
- Init failure: Both binaries `log.Fatalf` on `telemetry.Init` error. misconfiguration fails startup (intentional when OTEL miswired).
- Missing context in goroutines: Background routing probes use `context.Background()`. intentionally not tied to user traces.
- High cardinality attributes: I avoid per-user span attributes on Redis spans; user.id on sidecar spans only.

## Operational concerns

- Deploy Jaeger or Grafana Tempo behind `OTEL_EXPORTER_OTLP_ENDPOINT`.
- Lower `OTEL_TRACES_SAMPLER_ARG` in production (e.g. 0.01) after validating pipelines.
- Cross-reference traces with Prometheus: same request should show span `sidecar.intelligent_route` + metric `routing_failovers_total` increment on failover.
- Diagram: `docs/diagrams/tracing-flow.md`.

## Performance implications

Disabled OTEL path is a boolean check per request. Enabled path adds propagation header parsing, span creation, and async export. typically <1% CPU at 0.1 sampling. `InstrumentRedis` adds hook per command; visible in `idempotency_redis_duration_seconds` vs trace span duration comparison during tuning.

## Lessons learned

I added OTEL after routing failover bugs that logs could not explain. two gateways, one trace ID, and I could see `gateway.failover=true` on the span. The tradeoff I accepted: **startup fatality on OTEL init failure** only when enabled, so misconfigured collectors are caught in CI/staging, not silently ignored. If I extended this, I would add exemplars linking `rate_limiter_requests_duration_seconds` to trace IDs.

**References:** `internal/telemetry/config.go`, `internal/telemetry/middleware.go`, `deploy/prometheus.yml`, README Observability section
