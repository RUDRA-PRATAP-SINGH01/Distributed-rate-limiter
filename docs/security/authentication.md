# Authentication

**Source:** `internal/auth/middleware.go`, `cmd/limiter/config.go`, `cmd/sidecar/main.go`

---

## Mechanism

Shared-secret **API key** in HTTP headers. No OAuth/JWT inside limiter or sidecar — identity for quota is separate (`X-User-ID`).

---

## Headers

| Header | Used for |
|--------|----------|
| `X-API-Key` | Primary API key header |
| `X-Internal-API-Key` | Alternate (sidecar → limiter) |

`RequireAPIKey` accepts either header. Comparison uses `auth.SecureCompare` (SHA-256 digest pre-hashing followed by `crypto/subtle.ConstantTimeCompare`) to prevent both byte-value timing and length oracle side-channels (M-05).

Empty expected key → **auth disabled** (dev Prometheus scrape, open `/check`).

---

## Protected endpoints

### Limiter main port (`PORT`, default 8080)

| Route | Key env | Notes |
|-------|---------|-------|
| `/check`, `/check_hierarchical` | `INTERNAL_API_KEY` | Sidecar sends `X-Internal-API-Key` |
| `/metrics` | `METRICS_API_KEY` or `INTERNAL_API_KEY` if `METRICS_REQUIRE_AUTH=true` | Open by default |
| `/health` | None | Public readiness |

### Limiter admin port (`ADMIN_PORT`, default 8082)

| Route | Key env |
|-------|---------|
| `/admin/*` | `ADMIN_API_KEY` |

Separate port allows network isolation from hot path.

### Sidecar (`PORT`, default 9090)

| Route | Key env |
|-------|---------|
| `/metrics` | Optional via `METRICS_REQUIRE_AUTH` |
| `/health` | None |
| Proxied app routes | **No API key** — relies on edge gateway + path allowlist |

Sidecar attaches `INTERNAL_API_KEY` to outbound limiter requests only.

---

## Configuration matrix

| Variable | Component | Purpose |
|----------|-----------|---------|
| `INTERNAL_API_KEY` | Limiter + sidecar | Protect quota endpoints |
| `ADMIN_API_KEY` | Limiter admin | Overrides, audit, circuit admin |
| `METRICS_API_KEY` | Both | Override for scrape auth |
| `METRICS_REQUIRE_AUTH` | Both | Require key on `/metrics` |
| `STRICT_SECURITY` | Limiter | Fatal if keys missing/weak |

---

## Production checklist

1. Set strong random keys — not `dev-key-change-in-prod` / `dev-internal-key-change-in-prod`.
2. `STRICT_SECURITY=true` in CI/staging to catch misconfig.
3. Rotate keys via env reload / redeploy (no hot reload in code).
4. Bind admin `:8082` to internal network only.
5. Do not expose limiter `:8080` publicly without mTLS or private network.

---

## Failure responses

| Condition | HTTP |
|-----------|------|
| Missing/wrong API key | **401** `unauthorized` |
| Valid key, Redis down | **503** (not 401) |

Tests: `cmd/limiter/admin_auth_test.go`, `cmd/limiter/route_security_test.go`.

---

## Identity vs authentication

**Authentication** (API key): proves caller is trusted sidecar/service.  
**Identity** (`X-User-ID`): which end-user bucket to debit — see [sensitive-data-policy.md](sensitive-data-policy.md).
