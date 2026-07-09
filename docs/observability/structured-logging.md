# Structured Logging (slog)

**Source:** `internal/logging/logger.go`, `internal/telemetry/middleware.go`

---

## Configuration

| Env var | Default | Values |
|---------|---------|--------|
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`/`warning`, `error` |
| `LOG_FORMAT` | `json` | `json`, `text` |

`logging.Init()` runs once at process start (`sync.Once`) and sets the process-wide `slog` default.

Invalid `LOG_LEVEL` falls back to `info` with a warning log.

---

## Correlation fields

Every `logging.Debug/Info/Warn/Error(ctx, ...)` call merges context attributes via `attrsFromContext`:

| Field | Source |
|-------|--------|
| `request_id` | `telemetry.RequestIDFromContext(ctx)` — from `X-Request-ID` or generated UUID |
| `trace_id` | OpenTelemetry span context (when valid) |
| `span_id` | OpenTelemetry span context (when valid) |

Example JSON log shape:

```json
{
  "time": "...",
  "level": "ERROR",
  "msg": "rate limit check failed",
  "request_id": "550e8400-e29b-41d4-a716-446655440000",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "component": "sidecar",
  "operation": "rate_limit_check",
  "error": "..."
}
```

Additional structured keys (`component`, `operation`, `fail_open`, etc.) are added at call sites in `cmd/limiter` and `cmd/sidecar`.

---

## Request ID lifecycle

1. Client may send `X-Request-ID`.
2. `requestIDMiddleware` (`telemetry/middleware.go`) copies or generates UUID into context.
3. Response echoes `X-Request-ID`.
4. Logs on that request include `request_id`.
5. Audit records store `RequestID` from the same context chain when handlers pass it.

---

## Trace ↔ log linking

Trace and span IDs come from the active OTEL span in context — set by `otelhttp` or manual `telemetry.StartSpan`. Enable tracing (`OTEL_ENABLED=true`) for trace IDs in logs.

Without OTEL, only `request_id` is guaranteed when requests pass through `WrapHandler`.

---

## Fatal and startup logs

`logging.Fatal(msg, args...)` logs at error level and `os.Exit(1)`. Used for config validation (`STRICT_SECURITY`), Redis ping failure at startup, and listen errors.

Startup warnings (dev mode):

- Missing `INTERNAL_API_KEY` — `/check` reachable without auth.
- Default `ADMIN_API_KEY` placeholder.
- Sidecar: empty `ALLOWED_PATHS` (all paths proxied).
- `FAIL_OPEN=true` on sidecar.

These use `logging.Warn(nil, ...)` without request context.

---

## Testing

`logging.InitWith(w, level, format)` and `logging.CorrelationAttrs(ctx)` are exported for tests (`logger_test.go`).
