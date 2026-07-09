# Graceful Shutdown

**Sources:** `cmd/limiter/main.go`, `cmd/sidecar/main.go`, `internal/audit/shutdown.go`, `internal/telemetry/provider.go`

Both binaries handle `SIGINT` and `SIGTERM`. Shutdown timeout: **5 seconds** for HTTP servers (limiter main + admin; sidecar).

---

## Limiter shutdown order

```
Signal (SIGINT/SIGTERM)
  → adminSrv.Shutdown(5s)     # Admin API :8082
  → srv.Shutdown(5s)          # Main HTTP :8080 — drains in-flight /check
  → auditStore.Shutdown(ctx)  # If audit enabled + async — drain queue
  → otelShutdown(ctx)         # Flush spans (10s internal budget)
  → redisclient.Close(rdb)    # Only if audit RedisCloseSafe()
  → exit
```

From `cmd/limiter/main.go`:

> Graceful shutdown drains in-flight checks before exit — important during rolling deploys.

### Audit drain

When `ENABLE_AUDIT_TRAIL=true` and `AUDIT_ASYNC=true`:

1. `auditStore.Shutdown(ctx)` closes the bounded queue and waits for workers (`internal/audit/shutdown.go`).
2. On context deadline, returns `ctx.Err()` — workers may still run.
3. **Redis must not close** until `auditStore.RedisCloseSafe()` is true (`state == stateStopped`).

If shutdown times out:

```text
Skipping Redis close while audit workers are still active
```

A later `Shutdown` with fresh context can resume; process exit may still force-close.

Events recorded after shutdown begins are dropped (`audit_dropped_total`).

### OpenTelemetry

`otelShutdown` runs **after** HTTP drain so in-flight request spans complete. Uses 10s `ForceFlush` + `Shutdown` (`provider.go`).

---

## Sidecar shutdown order

```
Signal
  → sweeperCancel()           # Cache TTL sweeper
  → probeCancel()             # Routing health probes (if enabled)
  → srv.Shutdown(5s)          # Drains proxied requests
  → otelShutdown(5s ctx)
  → redisclient.Close(sharedRdb)  # If idempotency/routing Redis was opened
  → exit
```

Sidecar has **no audit store** — simpler Redis close (no worker drain gate).

Background goroutines (cache sweeper, routing probes) are cancelled before HTTP shutdown.

---

## HTTP server timeouts (steady state)

Both servers configure:

| Setting | Value |
|---------|-------|
| `ReadTimeout` | 5s |
| `WriteTimeout` | 10s |
| `IdleTimeout` | 120s |

`Shutdown` stops accepting new connections and waits for active handlers up to the shutdown context deadline.

---

## Rolling deploy guidance

1. **Remove from LB** — wait for `/health` to fail or use preStop hook.
2. **Send SIGTERM** — allow 5s HTTP + audit drain.
3. **Do not kill Redis** before limiter processes exit (audit workers may still append).
4. **OTEL** — ensure collector reachable for final flush.

Limiter is the critical path for audit ordering; sidecar can restart independently but will 503 while limiter is unavailable unless `FAIL_OPEN=true`.

---

## Tests

- `internal/audit/shutdown_test.go` — drain, timeout, Redis close ordering, idempotent shutdown.
- `cmd/sidecar/shutdown_test.go` — sidecar graceful behavior.

See also `docs/architecture/shutdown-lifecycle.md` if present in repo map.
