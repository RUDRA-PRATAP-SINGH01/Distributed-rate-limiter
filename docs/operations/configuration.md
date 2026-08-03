# Configuration

**Sources:** `cmd/limiter/config.go`, `cmd/sidecar/main.go`, `internal/redis/config.go`, `internal/telemetry/config.go`, `internal/audit/config.go`, `internal/circuitbreaker/config.go`, `internal/routing/config.go`

All binaries are env-driven — same image for local, Docker, and production.

---

## Limiter (`cmd/limiter/config.go`)

### Server and Redis

| Variable | Default | Notes |
|----------|---------|-------|
| `PORT` | `8080` | Main HTTP |
| `REDIS_ADDR` | `localhost:6379` | See shared Redis section |
| `REDIS_PASSWORD` | `""` | |
| `ALGORITHM` | `token` | `token` or `sliding` |
| `CAPACITY` | `10` | Flat limit bucket/window |
| `REFILL_RATE` | `1.0` | Token bucket only |
| `WINDOW_SEC` | `60` | Sliding window only |

### Hierarchical quotas

| Variable | Default |
|----------|---------|
| `ENABLE_HIERARCHICAL` | `true` |
| `GLOBAL_CAPACITY` / `GLOBAL_REFILL_RATE` | `1000000` / `10000.0` |
| `TENANT_CAPACITY` / `TENANT_REFILL_RATE` | `100000` / `1000.0` |
| `USER_CAPACITY` / `USER_REFILL_RATE` | `100` / `1.0` |
| `ENDPOINT_CAPACITY` / `ENDPOINT_REFILL_RATE` | `10` / `0.5` |

### Admin API (`:8082`)

| Variable | Default | Notes |
|----------|---------|-------|
| `ENABLE_ADMIN_API` | `true` | |
| `ADMIN_HOST` | `127.0.0.1` | Loopback default. Compose sets `0.0.0.0` for port-map. Terraform keeps loopback and does not expose `:8082` on the instance SG. Binding `0.0.0.0` without TLS logs a warning. |
| `ADMIN_PORT` | `8082` | |
| `ADMIN_API_KEY` | `dev-key-change-in-prod` | |
| `OVERRIDE_CACHE_TTL_MS` | `5000` | |

### Security

| Variable | Default | Notes |
|----------|---------|-------|
| `INTERNAL_API_KEY` | `""` | Protects `/check`, `/check_hierarchical` |
| `METRICS_API_KEY` | `""` | Falls back to internal key |
| `METRICS_REQUIRE_AUTH` | `false` | |
| `ALLOW_QUERY_USER_ID` | `false` | Dev/demo query param |
| `STRICT_CONFIG` | `false` | Invalid ints/floats fatal |
| `STRICT_SECURITY` | `false` | Requires non-empty internal + non-default admin keys |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | `""` | Both required to enable TLS |

### Audit (`internal/audit/config.go`)

| Variable | Default |
|----------|---------|
| `ENABLE_AUDIT_TRAIL` | `true` (set `false` to disable) |
| `AUDIT_RETENTION_HOURS` | `168` |
| `AUDIT_MAX_EVENTS` | `100000` |
| `AUDIT_ASYNC` | `true` |
| `AUDIT_QUEUE_SIZE` | `4096` |
| `AUDIT_WORKERS` | `4` |

### Circuit breaker (`CB_*` in `internal/circuitbreaker/config.go`)

Limiter always enables Redis circuit breaker at startup. Key vars: `CB_FAILURE_RATE`, `CB_MIN_SAMPLES`, `CB_CONSECUTIVE_FAILURES`, `CB_LATENCY_THRESHOLD_MS`, `CB_OPEN_COOLDOWN_MS`, `CB_HALF_OPEN_MAX_PROBES`, `CB_HALF_OPEN_SUCCESS_REQUIRED`, `CIRCUIT_FAIL_OPEN`.

---

## Sidecar (`cmd/sidecar/main.go`)

### Required

| Variable | Notes |
|----------|-------|
| `RATE_LIMITER_URL` | **Required** — base URL of central limiter |
| `UPSTREAM_URL` | Required unless `ENABLE_ROUTING=true` |

### Proxy and limits

| Variable | Default |
|----------|---------|
| `PORT` | `9090` |
| `CACHE_TTL_MS` | `30` (denial cache TTL) |
| `FAIL_OPEN` | `false` |
| `USE_HIERARCHICAL` | `false` |
| `RATE_LIMIT` | `10` (header fallback) |
| `ALLOWED_PATHS` | `""` (empty = all paths) |
| `ALLOW_QUERY_USER_ID` | `false` |
| `INTERNAL_API_KEY` | Sent to limiter as `X-Internal-API-Key` |
| `METRICS_REQUIRE_AUTH` / `METRICS_API_KEY` | Same pattern as limiter |

### Idempotency

| Variable | Default |
|----------|---------|
| `ENABLE_IDEMPOTENCY` | `false` |
| `IDEMPOTENCY_FAIL_OPEN` | `false` |
| `IDEMPOTENCY_LOCK_TTL_MS` | from `idempotency.DefaultConfig()` |
| `IDEMPOTENCY_COMPLETED_TTL_MS` | from default |
| `IDEMPOTENCY_MAX_BODY_BYTES` | from default |
| `ENABLE_CIRCUIT_BREAKER` | `true` when idempotency on (set `false` to disable limiter CB) |

### Routing

| Variable | Default |
|----------|---------|
| `ENABLE_ROUTING` | `false` |
| `GATEWAYS` | `id\|url\|weight,...` — required when routing on |
| `ROUTING_TARGET_LATENCY_MS` | `100` |
| `ROUTING_CIRCUIT_ERROR_RATE` | `0.5` |
| `ROUTING_PROBE_INTERVAL_SEC` | `15` |

See `internal/routing/config.go` for full routing tunables.

### Limiter HTTP client (`cmd/sidecar/limiter_http.go`)

| Variable | Default |
|----------|---------|
| `SIDECAR_LIMITER_HTTP_TIMEOUT_MS` | `1500` |
| `SIDECAR_LIMITER_DIAL_TIMEOUT_MS` | `500` |
| `SIDECAR_LIMITER_HEADER_TIMEOUT_MS` | `1000` |
| `SIDECAR_LIMITER_TLS_TIMEOUT_MS` | `1000` |

---

## Shared Redis (`internal/redis/config.go`)

| Variable | Default | Notes |
|----------|---------|-------|
| `REDIS_MODE` | `standalone` | `standalone`, `sentinel`, or `cluster` (fails fast on invalid modes) |
| `REDIS_ADDR` | `localhost:6379` | Standalone host:port or cluster seed address |
| `REDIS_SENTINEL_ADDRS` | — | Comma-separated Sentinel endpoints (`s1:26379,s2:26379`) |
| `REDIS_CLUSTER_ADDRS` | — | Comma-separated Cluster endpoints (`c1:6379,c2:6379`). Note: Cluster requires `ENABLE_HIERARCHICAL=false` |
| `REDIS_MASTER_NAME` | `mymaster` | Sentinel master group name |
| `REDIS_DB` | `0` | Database index (ignored in Cluster mode; always DB 0) |
| `REDIS_DIAL_TIMEOUT_MS` | `500` | |
| `REDIS_READ_TIMEOUT_MS` | `500` | |
| `REDIS_WRITE_TIMEOUT_MS` | `500` | |
| `REDIS_POOL_TIMEOUT_MS` | `1000` | |
| `REDIS_DIALER_RETRIES` | `1` | |
| `REDIS_MAX_RETRIES` | `0` (disabled) | |

---

## OpenTelemetry (`internal/telemetry/config.go`)

| Variable | Default |
|----------|---------|
| `OTEL_ENABLED` | `false` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_INSECURE` | `true` |
| `OTEL_TRACES_SAMPLER_ARG` | `1.0` |
| `OTEL_SERVICE_NAME` | overrides per-binary default |

---

## Logging

| Variable | Default |
|----------|---------|
| `LOG_LEVEL` | `info` |
| `LOG_FORMAT` | `json` |

See [structured-logging.md](../observability/structured-logging.md).
