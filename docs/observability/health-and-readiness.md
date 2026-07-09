# Health and Readiness

**Sources:** `cmd/limiter/main.go`, `cmd/sidecar/health.go`, `internal/redis/health.go`

Both services expose `GET /health` with JSON bodies. Neither exposes a separate `/ready` or `/live` endpoint — `/health` serves as readiness for orchestrators.

---

## Central limiter (`:8080/health`)

**Checks:** Redis connectivity only.

```json
{
  "status": "healthy",
  "redis": {
    "mode": "standalone",
    "connected": true,
    "role": "master",
    "replication": "role=master slaves=0"
  }
}
```

- **200** — `redis.connected == true` (`redisclient.CheckHealth`).
- **503** — Redis ping fails; `status: "unhealthy"`, `redis.error` set.

Sentinel mode adds `role`, `master_addr`, and replication summary from `INFO replication`.

Startup **fatals** if initial Redis ping fails — the process never serves false healthy on boot.

**Not checked on `/health`:** circuit breaker state, audit queue depth, admin API.

---

## Sidecar (`:9090/health`)

**Checks (in order):**

1. **Central limiter** — `GET {RATE_LIMITER_URL}/health` must return 200.
2. **Redis** (only if `ENABLE_IDEMPOTENCY=true` or `ENABLE_ROUTING=true`) — same `CheckHealth` as limiter.

```json
{ "status": "healthy" }
```

With Redis required:

```json
{
  "status": "healthy",
  "redis": { "mode": "standalone", "connected": true, ... }
}
```

**503 when:**

- Limiter health probe fails (network, limiter down, limiter Redis unhealthy).
- Redis required but client nil or ping fails.

Sidecar does **not** verify upstream (`UPSTREAM_URL`) or individual gateways on `/health` — only limiter (+ optional Redis).

---

## Differences summary

| Aspect | Limiter | Sidecar |
|--------|---------|---------|
| Redis check | Always (authoritative store) | Only if idempotency or routing enabled |
| Limiter check | N/A | Required |
| Upstream check | N/A | Not on `/health` |
| Used by | Direct limiter clients, sidecar probe | Edge load balancers, k8s probes |

---

## Application routes vs health

Sidecar `ServeHTTP` returns **404** for `/health` and `/metrics` on the catch-all handler — those paths are registered on the mux **before** the sidecar handler. Health is not rate-limited.

---

## Operational use

- **Readiness gate:** Remove sidecar from LB when `/health` ≠ 200.
- **Limiter rolling deploy:** Sidecar `/health` fails when limiter is draining/unhealthy — edge stops sending traffic.
- **Sentinel failover:** Watch `redis.role` flips in limiter `/health` during promotion drills.

See `docs/operations/runbooks.md` RB-1 and RB-2 for incident flows.
