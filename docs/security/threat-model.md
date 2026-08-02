# Threat Model

**Sources:** `cmd/limiter/config.go`, `cmd/sidecar/main.go`, `internal/auth/middleware.go`, `internal/identity/user.go`

---

## Assets

| Asset | Location | Impact if compromised |
|-------|----------|------------------------|
| Quota state | Redis | Unlimited API abuse, revenue loss |
| `INTERNAL_API_KEY` | Sidecar → limiter | Arbitrary quota consumption |
| `ADMIN_API_KEY` | Admin `:8082` | Override limits, circuit reset, audit access |
| `REDIS_PASSWORD` | All Redis clients | Full data read/write |
| User identity | `X-User-ID` header | Wrong bucket, bypass if spoofed |
| Idempotency records | Redis | Duplicate charges if broken |
| Audit trail | Redis | Compliance / forensics loss |

---

## Trust boundaries

```
[Untrusted client] → [Sidecar edge] → [Central limiter + Redis]
                          ↓
                    [Upstream app]
```

**Trusted:** Sidecar, limiter, Redis network path.  
**Untrusted by default:** Browser/query params, public internet clients.

---

## Threats and mitigations

| Threat | Mitigation in code |
|--------|-------------------|
| Anonymous quota consumption | `auth.RequireAPIKey` on `/check` (`INTERNAL_API_KEY`) |
| Admin API abuse | `ADMIN_API_KEY` on admin routes; separate port `:8082` |
| User ID spoofing via query | `ALLOW_QUERY_USER_ID=false` in prod; header-only identity |
| Timing & length attack on API keys | `auth.SecureCompare` (SHA-256 pre-hashed constant-time compare) in `internal/auth/middleware.go` |
| Metrics scraping / recon | Optional `METRICS_REQUIRE_AUTH` |
| Path traversal to internal routes | Sidecar `ALLOWED_PATHS` prefix allowlist |
| Redis outage → traffic flood | Fail-closed default (`FAIL_OPEN=false`) |
| Duplicate mutating requests | Idempotency + fencing tokens (when enabled) |
| Gateway cascade failure | Per-gateway circuit breakers + routing scores |

---

## Dev-mode warnings (logged at startup)

- Missing `INTERNAL_API_KEY` — limiter `/check` open.
- Default `ADMIN_API_KEY` placeholder.
- Empty `ALLOWED_PATHS` — all paths proxied on sidecar.
- `FAIL_OPEN=true` — forwards despite limiter/Redis errors.

`STRICT_SECURITY=true` fatals on weak keys when admin API enabled.

---

## Out of scope (operator responsibility)

- TLS termination at ingress (optional `TLS_CERT_FILE` on binaries).
- Network policies (admin port not on public internet).
- Redis ACLs beyond password.
- JWT validation — expected **upstream** of sidecar; sidecar trusts `X-User-ID` from gateway.

---

## Related

- [authentication.md](authentication.md)
- [sensitive-data-policy.md](sensitive-data-policy.md)
- `docs/operations/production-hardening.md`
