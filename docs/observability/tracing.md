# Distributed Tracing (OpenTelemetry)

**Sources:** `internal/telemetry/`, `cmd/limiter/main.go`, `cmd/sidecar/main.go`

---

## Enablement

| Env var | Default | Purpose |
|---------|---------|---------|
| `OTEL_ENABLED` | `false` | Master switch |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4318` | OTLP HTTP collector (Jaeger-compatible) |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` (unless `"false"`) | TLS skip for local Jaeger |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` | Ratio for `TraceIDRatioBased` sampler |
| `OTEL_SERVICE_NAME` | per-binary default | Overrides default service name |

**Service names:**

- Limiter: `rate-limiter` (`telemetry.LoadConfigFromEnv("rate-limiter")`)
- Sidecar: `rate-sidecar` (`telemetry.LoadConfigFromEnv("rate-sidecar")`)

`telemetry.Init` configures OTLP HTTP export, `ParentBased(TraceIDRatioBased(ratio))` sampling, and composite W3C propagators (`TraceContext` + `Baggage`).

Shutdown: returned `Shutdown` func must run after HTTP drain — 10s flush timeout (`defaultShutdownTimeout` in `provider.go`).

---

## HTTP instrumentation

`telemetry.WrapHandler` stack (`middleware.go`):

1. **Request ID** — read `X-Request-ID` or generate UUID; stored in context.
2. **`otelhttp.NewHandler`** — span per request; name format `METHOD path`.
3. **Filter** — `SkipHealthMetrics`: `/health` and `/metrics` are **not** traced (reduces scraper/probe noise).
4. **Response headers** — `X-Request-ID`, `X-Trace-ID`, `X-Span-ID` when span context is valid.

Both binaries wrap their mux with `telemetry.WrapHandler(mux, otelCfg.ServiceName)`.

---

## Manual spans

| Location | Span name |
|----------|-----------|
| `cmd/limiter/main.go` | `limiter.check`, `limiter.check_hierarchical` |
| `cmd/sidecar/main.go` | `sidecar.proxy`, `sidecar.rate_limit_check`, `sidecar.idempotency`, `sidecar.upstream_proxy` |
| `internal/idempotency/store.go` | `idempotency.claim`, `idempotency.complete`, `idempotency.fail` |
| `internal/routing/router.go` | `sidecar.intelligent_route` |
| Outbound HTTP (`http_transport.go`) | `HTTP GET`, `HTTP POST`, … (client kind) |

Helpers: `telemetry.StartSpan`, `telemetry.RecordError`, `telemetry.SetHTTPStatus`.

---

## Propagation

**Inbound:** W3C `traceparent` parsed by `otelhttp` handler.

**Outbound:**

- Sidecar → limiter: `telemetry.NewHTTPTransport` on limiter HTTP client (`cmd/sidecar/limiter_http.go`).
- Sidecar → upstream: reverse proxy transport wrapped with `NewHTTPTransport`.
- Router gateway calls: same client transport.

**429 vs 5xx on client spans:** `setClientHTTPStatus` leaves 429 unset (expected quota denial); 5xx marks span error — matches limiter check semantics.

---

## Redis tracing

When `OTEL_ENABLED=true`, both binaries call `telemetry.InstrumentRedis(rdb)` (`redis.go`):

- Tracing via `redisotel.InstrumentTracing`
- Metrics via `redisotel.InstrumentMetrics`

Redis commands only appear in traces when the call passes a traced `context.Context`.

---

## Jaeger

Compose exposes Jaeger UI (typically `:16686`). Collector OTLP HTTP endpoint `:4318` matches default `OTEL_EXPORTER_OTLP_ENDPOINT`.

Trace a request:

1. Send `X-Request-ID` (optional) from client.
2. Read `X-Trace-ID` from sidecar/limiter response.
3. Search Jaeger by trace ID or service name (`rate-limiter`, `rate-sidecar`).

---

## Shutdown order (traces)

Limiter: HTTP shutdown → audit drain → `otelShutdown` → Redis close.  
Sidecar: HTTP shutdown → `otelShutdown` → Redis close.

Flushing spans after HTTP drain avoids losing in-flight request spans.
