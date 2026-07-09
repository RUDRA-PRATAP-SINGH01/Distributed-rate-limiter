# Sensitive Data Policy

**Sources:** `cmd/limiter/redis_failure_test.go`, `internal/logging/logger.go`, `internal/audit/types.go`, `cmd/limiter/config.go`

---

## Principles

1. **Error responses** — generic messages to clients; no Redis addresses, stack traces, or internal hostnames.
2. **Logs** — structured fields only; no raw API keys or passwords in log calls.
3. **Metrics** — no PII in label values (see [metrics.md](../observability/metrics.md) cardinality policy).
4. **Audit** — stores decision metadata for forensics; treat audit Redis as sensitive storage.

---

## Client-visible errors

Redis failure on `/check` returns:

```json
{"error":"Rate limiter unavailable"}
```

`TestRedisFailure_Handling` verifies bodies do **not** contain `REDIS_ADDR` or low-level Redis errors.

Sidecar fail-closed:

```text
Rate limiter unavailable
```

Circuit responses may include `circuit_state` enum — not infrastructure details.

---

## Identity data

| Field | Storage | Logged? |
|-------|---------|---------|
| `X-User-ID` | Redis quota keys, audit indexes | Only in debug/audit context at call sites — not in metric labels |
| `X-Tenant-ID` | Hierarchical keys, audit | Same |
| `X-Request-ID` | Audit index, logs | Yes — correlation, not PII by itself |
| Query `user_id` | Opt-in dev only | Disable `ALLOW_QUERY_USER_ID` in prod |

Production: identity must come from trusted gateway header, not client-supplied query params.

---

## Secrets in configuration

| Secret | Env var | Must not |
|--------|---------|----------|
| Limiter auth | `INTERNAL_API_KEY` | Commit to git, log at startup |
| Admin | `ADMIN_API_KEY` | Use default in prod (`STRICT_SECURITY` guards) |
| Redis | `REDIS_PASSWORD`, `REDIS_SENTINEL_PASSWORD` | Appear in `/health` JSON |
| Metrics | `METRICS_API_KEY` | Expose in public scrape without TLS |

`.gitignore` should exclude local `.env` files with secrets.

---

## Audit trail content

Audit events (`internal/audit/types.go`) may include:

- `user_id`, `tenant_id`, `request_id`
- Decision, handler name, reason string
- Remaining quota snapshot

**Retention:** `AUDIT_RETENTION_HOURS`, `AUDIT_MAX_EVENTS` — oldest trimmed in Lua.

Admin search on `:8082` requires `ADMIN_API_KEY` — restrict network access.

Idempotency stores **response bodies** for replay — may contain PII from upstream; scope Redis access accordingly.

---

## Tracing

Trace spans include operational attributes (`http.path`, `gateway.id`, `idempotency.key` presence) — avoid attaching full request bodies to spans.

OTEL export goes to collector/Jaeger — treat trace backend as sensitive if paths or keys appear in attributes.

---

## TLS

Optional mutual TLS not implemented in-app. Use `TLS_CERT_FILE` + `TLS_KEY_FILE` on limiter/sidecar for edge TLS, or terminate at load balancer.

---

## Compliance operations

- Monitor `audit_dropped_total` — silent loss of compliance records.
- For strict retention requirements, document `AUDIT_MAX_EVENTS` trim behavior with legal/compliance.
- Redis backups contain quota + audit + idempotency data — encrypt at rest per org policy.
