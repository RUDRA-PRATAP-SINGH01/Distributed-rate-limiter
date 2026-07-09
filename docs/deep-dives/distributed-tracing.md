# Distributed Tracing

## Problem Statement

A single API request crosses rate limit check, idempotency claim, routing decision, circuit breaker gate, upstream gateway, and audit append. When p99 spikes or errors cluster, logs alone cannot answer **which hop consumed the budget** or whether retries duplicated work.

I needed **distributed tracing** with W3C trace context propagation, request correlation IDs, Redis command spans, and OTLP export compatible with Jaeger.

## Why the problem exists

Microservice-style sidecars inherit the observability gap of microservices without being full services:

- Multiple Redis round-trips per HTTP request (limiter + idempotency + circuit + routing + audit).
- Outbound proxy to gateway is a separate HTTP client span.
- Retries and failover create sibling spans that logs flatten into noise.

Without trace IDs, support cannot link client `X-Request-ID` to upstream gateway logs. Without Redis instrumentation, "Redis slow" is a guess.

## Design goals

1. OTLP HTTP exporter: `telemetry.Init` in `provider.go`.
2. Parent-based sampling: `TraceIDRatioBased` respects upstream decision.
3. Request ID middleware: Generate or propagate `X-Request-ID`.
4. Response trace headers: `X-Trace-ID`, `X-Span-ID` for client support.
5. Handler wrapping: `otelhttp` with health/metrics filter.
6. Outbound propagation: `NewHTTPTransport` for router upstream calls.
7. Redis tracing: `InstrumentRedis` via redisotel.
8. Idempotency, routing, audit sub-operations.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Logs only with correlation ID | No latency breakdown per hop |
| Jaeger agent UDP | OTLP is modern default; HTTP works in k8s |
| 100% trace sampling always | Cost prohibitive at 1k RPS |
| Zipkin separate stack | Jaeger accepts OTLP |
| No Redis spans | Blind to EVAL latency |

OpenTelemetry SDK with OTLP HTTP won.

## Final architecture

**Init** (`internal/telemetry/provider.go`):

```go
exporter, _ := otlptracehttp.New(ctx, opts...)
sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))
tp := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(exporter),
    sdktrace.WithResource(res), // service.name
)
otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
    propagation.TraceContext{}, propagation.Baggage{},
))
```

Returns shutdown func. must call on process exit to flush spans.

**HTTP middleware** (`internal/telemetry/middleware.go`):

1. `requestIDMiddleware`. read or generate UUID, store in context
2. `otelhttp.NewHandler`. span per request, name `METHOD path`
3. `SkipHealthMetrics`. still traces `/health` and `/metrics` (filter returns false for those paths. they get spans; naming avoids metric double-count confusion in comments)
4. Inner handler sets response headers from span context

**Manual spans:**

| Location | Span name |
|----------|-----------|
| `idempotency/store.go` | `idempotency.claim`, `.complete`, `.fail` |
| `routing/router.go` | `sidecar.intelligent_route` |

Attributes: `idempotency.key`, `gateway.id`, `gateway.score`, `gateway.failover`, `http.status_code`.

**Error recording**. `telemetry.RecordError(span, err)` sets status + `RecordError`.

**Redis** (`internal/telemetry/redis.go`):

```go
redisotel.InstrumentTracing(rdb)
redisotel.InstrumentMetrics(rdb)
```

Requires passing `context.Context` into Redis commands. already done in store implementations.

**Config** (`internal/telemetry/config.go`). `Enabled`, `ServiceName`, `OTLPEndpoint`, `Insecure`, `SampleRatio`.

## Tradeoffs

- Sampling: Rare production bugs may lack traces; increase sample ratio temporarily during incidents.
- Batch export delay: `WithBatcher` adds seconds before spans appear. bad for live debugging, good for overhead.
- Baggage propagation: Enabled but unused. future tenant baggage must be PII-reviewed.
- Double instrumentation: Otelhttp + manual spans require consistent context passing.
- Health trace noise: K8s probes generate spans unless filtered. `SkipHealthMetrics` naming is subtle.

## Failure modes

1. OTLP endpoint down: Exporter buffers then drops; no crash; blind window.
2. Async audit workers use `context.Background()`. orphan spans.
3. Shutdown skip: Lost tail spans on SIGKILL.
4. High cardinality attributes: Putting raw user IDs on every span. cost; we use request/idempotency keys selectively.
5. Trace context not forwarded by client: New root trace per request. acceptable.

## Operational concerns

- Env: `OTEL_ENABLED` (default `false` in `docker-compose.yml`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, sample ratio.
- Jaeger UI: search by `X-Trace-ID` response header support teams already collect.
- Link traces to audit via `RequestIDFromContext` matching audit `request_id`.
- Disable in local dev (`Enabled=false`). logs confirm in `Init`.
- Correlate with Prometheus metrics. same request should show Redis duration metrics + Redis spans.

## Performance implications

Sampling at 0.1 means 90% requests have zero trace export overhead.

Unsampled trace adds: span creation, attribute serialization, batch append. typically < 1% CPU at 1k RPS.

Redis redisotel adds per-command hook. measurable in `go test` micro-bench; acceptable vs one network RTT.

Benchmarks (`benchmarks/metrics/collect-metrics.ps1`) should capture sidecar CPU with tracing on/off for regression.

## Lessons learned

**Context is the API**. any new Redis call without `ctx` breaks the chain; code review checklist item.

Response trace headers (`X-Trace-ID`) improved support more than internal Jaeger access. clients paste ID into tickets.

Parent-based sampling respects upstream mesh traces if we later sit behind traced ingress. forward-compatible.

Async audit workers losing trace context is a known gap. if audit latency matters in traces, pass ctx into queue (not done yet).

Init shutdown hook is easy to forget in `main`. document in README runbook; lost spans on deploy look like "gap in graph."

Instrument Redis once at client creation in `main`, not per package. `InstrumentRedis` is global hook on the shared client.
