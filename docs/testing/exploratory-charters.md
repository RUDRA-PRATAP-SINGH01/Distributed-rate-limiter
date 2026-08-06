# Exploratory Testing Charters

Session-based exploratory testing (SBTM). Automated suites lock known contracts. These sessions hunt **unknown** failures around the compose stack.

Print the list:

```powershell
.\scripts\qa.ps1 exploratory
```

Timebox **45–60 minutes** per charter. One charter per session. Notes go at the bottom of this file or in the PR.

---

## Session template

```
Charter: ET-?
Date / timebox:
Build / commit:
Stack: docker compose / HA / scale
Mission:
What I tried:
Bugs / surprises:
Oracles used (status, header, Grafana, log):
Follow-up (new test / runbook / won't-fix):
```

---

## ET-1 — Quota allow / deny / headers

**Mission:** A unique user is allowed, then denied, with the headers a gateway would cache.

**Try:**

- `GET /check` with `X-User-ID` + `X-API-Key` until 429 (compose `CAPACITY=10`).
- Same user through sidecar `GET /`.
- Confirm `X-RateLimit-Remaining` on 200 and `Retry-After` on 429.

**Oracles:** 200 vs 429 only — never 503 for a healthy Redis. Body `allowed` matches status.

---

## ET-2 — Auth hardening

**Mission:** Untrusted identity cannot consume quota.

**Try:**

- `/check` with no API key → 401.
- `/check?user_id=alice` without headers (query identity is off).
- Admin `/admin/limits` without `ADMIN_API_KEY` → 401.
- Prefix/suffix/case mutations of the key (see `cmd/limiter/route_security_test.go`).

**Oracles:** 401/400, no Redis key for the spoofed user, no secret in body.

---

## ET-3 — Redis down → fail-closed

**Mission:** Outage is 503, recovery is automatic.

**Try:**

- `.\scripts\demo\redis-down.ps1` or `docker stop rate-redis`.
- Hit `/check` and sidecar `/`.
- `docker start rate-redis`, wait for `/health`, hit again.

**Oracles:** 503 while down; no upstream work on sidecar; 200 after health is green. Scripted twin: `chaos` R1.

---

## ET-4 — Hierarchical quotas

**Mission:** Platform / tenant / user / endpoint limits interact without double-charging confusion.

**Try:**

- `GET /check_hierarchical?endpoint=/api/v1/resource` for one user.
- Change endpoint, confirm a different bucket.
- Apply an admin override and re-check.

**Oracles:** First deny names the level that exhausted. Docs: [hierarchical-rate-limiting.md](../algorithms/hierarchical-rate-limiting.md).

---

## ET-5 — Idempotency first write vs replay

**Mission:** Same `Idempotency-Key` does not execute upstream twice.

**Try:**

- `POST` sidecar `/api/orders` with a new key (may need path allowlist — compose `ALLOWED_PATHS=/` so use a local override or the demo script).
- Replay immediately; replay after 2s.
- Two sidecars (`docker-compose.scale.yml`) racing the same key.

**Oracles:** One 200, rest 409 or replay of the first body. Scripted twin: `benchmarks/scripts/idempotency-race.js`.

---

## ET-6 — Routing failover and circuit

**Mission:** Unhealthy gateway-c loses traffic; circuit does not flap forever.

**Try:**

- `.\scripts\demo\routing.ps1` / watch Grafana `routing_gateway_health_score`.
- Force errors on one gateway if a demo hook exists; otherwise soak and watch weights.

**Oracles:** Scores move; 503 when circuit open; no request to a host outside the allowlist (`urlguard`).

---

## ET-7 — Admin override visibility

**Mission:** A runtime limit change is visible on the next check, not after restart.

**Try:**

- PUT/POST override via admin API with `ADMIN_API_KEY`.
- Immediate `/check_hierarchical` for that tenant/user.
- Delete override; confirm revert within `OVERRIDE_CACHE_TTL_MS` (5s in compose).

**Oracles:** Audit event exists; old process memory is not the source of truth.

---

## ET-8 — Observability after `start.ps1`

**Mission:** A human can see live decisions without reading code.

**Try:**

- `.\scripts\start.ps1`, open Grafana / Prometheus / Jaeger.
- Generate 429s (`.\scripts\demo\hotkey.ps1`) and 503s (Redis down).
- Confirm `/metrics` rejects a missing metrics key.

**Oracles:** Panels update; traces show `sidecar.proxy` → limiter; no API keys in log lines.

---

## Turning a session into automation

| Finding | Encode as |
|---------|-----------|
| Status/header contract | `cmd/...` black-box test or `tests/sanity` |
| Lua / key bug | `internal/...` white-box test |
| "Stack won't boot" | `tests/smoke` |
| Fail-closed regression | `chaos` contract |
| Load-only | k6 script + note in `docs/benchmarks/` |
