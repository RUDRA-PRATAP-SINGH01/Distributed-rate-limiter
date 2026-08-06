# Open production-readiness findings

**Audience:** a subsequent AI agent (or engineer) that must fix these without rediscovering the audit.

**Source of truth for status:** this file for *open* work. The live visual register is a Cursor canvas (`production-readiness-audit.canvas.tsx` in the workspace `canvases/` directory, not committed). IDs (`C-01`, `H-08`, `N-01`, …) are stable across both.

**Date of this dump:** 2026-08-19. Line numbers match the working tree at that date. Re-read the cited files before editing; do not trust a line number if the surrounding code no longer matches the snippet described here.

**Verdict of the audit:** do not ship to other teams as a production dependency until the six Critical items (`C-01`–`C-06`) are closed. `N-01`–`N-07` are closed. `N-08`–`N-17` remain open Medium items. Previously closed High/Medium items must not be re-implemented.

---

## How to use this document

1. Fix in the order in [Recommended implementation order](#recommended-implementation-order). Several findings share files; doing them independently will fight.
2. Do **not** re-implement anything in [Already fixed — do not redo](#already-fixed--do-not-redo). Those are closed with tests and CI gates.
3. Do **not** “add `go test -race`” or “upgrade off Go 1.25 because it does not exist”. Those peer-audit claims were wrong (`X-01`, `X-02`).
4. Preserve invariants that already work: Lua-atomic token bucket / sliding-window log / hierarchical EVAL; idempotency fencing tokens; fail-closed default; local Redis circuit breaker (`LocalStore`); CI jobs `lint`, `vuln`, `race`, `chaos`, widened `redis-integration`.
5. After a fix: `gofmt -w` the touched files, `golangci-lint run ./...`, `go test -count=1 -race` on the affected packages, and if you touched Lua or a Redis store, run the real-Redis pass (`REDIS_TEST_ADDR` + `-p 1`) described in `docs/ci/continuous-integration.md`.
6. `.golangci.yml` deliberately does **not** enable `contextcheck`. M-02 is closed by threading `context.Context` through the override store. Do not re-enable that linter: it false-positives on handlers that already pass `r.Context()`.
7. Respond to the user in whatever language they asked; this file is English so agents parse it.

### Product shape (one paragraph)

Two binaries share Redis: `cmd/limiter` (HTTP `/check` and `/check_hierarchical`, Admin API on a second port) and `cmd/sidecar` (in-process proxy that asks the limiter, then forwards to an upstream or to `internal/routing`). Quota is Redis-authoritative and Lua-atomic. Sidecar denial cache and `singleflight` are **process-local optimizations** and must not weaken global quota. Identity is supposed to come from a trusted gateway via `X-User-ID`. The code currently trusts that header with no cryptographic check.

---

## Already fixed — do not redo

| ID | What landed | Where to look instead of re-fixing |
|----|-------------|--------------------------------------|
| L-03 + H-04 | Redis-dependency circuit breaker is in-process `LocalStore`, not Redis-backed | `internal/circuitbreaker/local_store.go`, `cmd/limiter/main.go` wires `NewLocalStore` for `TargetRedis` |
| L-01 + M-03 | CI-gated chaos contracts; clients send headers, not `?user_id=` | `chaos/` (`//go:build chaos`), `docker-compose.chaos.yml`, CI `chaos` job |
| L-02 | Bucket TTL = `ceil(capacity/refill_rate)` clamped `[1, 86400]` | `internal/limiter/lua/token_bucket.lua`, `hierarchical.lua`, `internal/limiter/ttl.go` |
| M-04 | Sliding window returns measured `retry_after_ms`; HTTP `Retry-After` uses it | `sliding_window.lua`, `RetryAfterLimiter` in `cmd/limiter/limiter.go` |
| M-07 | `golangci-lint`, `govulncheck`, real-Redis matrix for limiter/CB/idempotency/audit/routing | `.golangci.yml`, `.github/workflows/ci.yml`, `internal/redistest/` |
| M-08 | Routing `Selector` uses `math/rand/v2.Float64`; concurrency test is the regression guard | `internal/routing/selector.go`, `selector_concurrency_test.go` |
| M-01 | Rate limiters use Redis server time (`redis.call('TIME')`); unused timestamp ARGVs kept for rolling deploys. Sliding window derives duration from ARGV[1]-ARGV[2], not a sixth ARGV | `token_bucket.lua`, `hierarchical.lua`, `sliding_window.lua` |
| M-02 | Runtime limit overrides thread `context.Context` from HTTP handlers to Redis | `internal/override/override.go`, `cmd/limiter/admin_api.go` |
| M-05 | Constant-time API key verification with SHA-256 pre-hashing to prevent timing and length oracles | `internal/auth/middleware.go`, `cmd/limiter/admin_*.go` |
| M-06 | Audit index ZSETs auto-expire matching retention TTL; empty indexes DEL'd in Lua; user index capped at 1000; event hashes use ttl+60s so trim can still HGET; cap loop always ZREMs ghost members | `internal/audit/lua/append.lua`, `internal/audit/store_test.go` |
| H-01 | Pre-boot topology check refuses `ENABLE_HIERARCHICAL=true` with `REDIS_MODE=cluster`; cluster client factory provided for flat check | `cmd/limiter/main.go`, `internal/redis/client.go` |
| H-02 | Denial cache stores strictly denials (allows never enter the map), insert-time + sweeper cap (default 10k), sweeper 500ms | `cmd/sidecar/main.go`, `cmd/sidecar/cache_test.go` |
| H-03 | Deleted un-synchronized `RedisTokenBucket` prototype; production strictly uses `RedisAtomicTokenBucket` | `internal/limiter/redis_atomic_token_bucket.go` |
| H-05 | Rate-limit 429 denials and transient 503s expire with transient `LockTTL` instead of poisoning idempotency keys with 24h `CompletedTTL`; `claim.lua` reclaims expired failures | `internal/idempotency/lua/claim.lua`, `internal/idempotency/store.go`, `cmd/sidecar/main.go` |
| H-06 | Strict path allowlist: `path.Clean` then exact match or `prefix+"/"`; `ALLOWED_PATHS=/` remains match-all | `cmd/sidecar/main.go`, `cmd/sidecar/sidecar_test.go` |
| H-07 | Redis circuit breaker keys (`cb:{target}`) expire after 24h idle TTL (`86400s`) on both initial creation and update | `internal/circuitbreaker/lua/allow.lua`, `record.lua`, `store_test.go` |
| H-08 | Half-open probe in-flight preservation: probe budget exhaustion preserves `StateHalfOpen` and rejects excess calls until in-flight probes record outcomes or cooldown deadline expires | `internal/circuitbreaker/lua/allow.lua`, `internal/circuitbreaker/local_store.go`, `store_test.go` |
| H-09 | Admin API defaults to loopback `127.0.0.1`, optional `ListenAndServeTLS`, Compose maps `ADMIN_HOST=0.0.0.0`, Terraform binds loopback and does not open SG :8082 | `cmd/limiter/config.go`, `cmd/limiter/admin_api.go`, `docker-compose*.yml`, `deploy/terraform/` |
| H-10 | Routing request body bounded with `http.MaxBytesReader` (default 1MB); returns 413 on overflow without forwarding empty body; idempotent routing reuses buffered body | `cmd/sidecar/main.go` |
| H-11 | Malformed JSON on 200 OK from limiter sets `callErr = err` and records telemetry error, properly tripping central limiter circuit breaker | `cmd/sidecar/main.go`, `cmd/sidecar/circuit_breaker_test.go` |
| N-01 | Audit 5-key EVAL hash-tagged `audit:{audit}:…`; Lua purge uses the same prefix; `TestAuditEvalKeysShareClusterSlot` | `internal/audit/store.go`, `lua/append.lua`, `store_test.go` |
| N-02 | Idempotency meta+body keys `idem:{scope}:meta:` / `idem:{scope}:body:`; `SanitizeHashTag`; slot test | `internal/idempotency/store.go`, Lua comments, `persist_test.go` |
| N-03 | `skipRecord` when `Allow` rejects; half-open excess cannot Record success | `cmd/sidecar/main.go`, `circuit_breaker_test.go` |
| N-04 | `PersistAsComplete`: 5xx/0/408/425 → Fail+LockTTL; 429 stays Complete | `internal/idempotency/persist.go`, sidecar `forwardIdempotent` |
| N-05 | Gateway URL guard + guarded dial/redirect; IMDS always blocked; `ROUTING_ALLOW_PRIVATE` for Compose | `internal/routing/urlguard.go`, compose GATEWAYS |
| N-06 | Terraform/Compose `ALLOW_QUERY_USER_ID=false`; template regression test; demo traffic uses `X-User-ID` | `ecs.tf`, `docker-compose*.yml`, `internal/identity/prod_templates_test.go` |
| N-07 | `ResolveTenantID` gated by `ALLOW_QUERY_USER_ID`; charset/length; limiter + sidecar wired | `internal/identity/user.go`, hierarchical handler, sidecar `tenantID` |

Toolchain pin `go 1.25.0` + `toolchain go1.26.6` in `go.mod` is load-bearing for the vuln job (stdlib CVEs). Bump the toolchain when `govulncheck` reports a new stdlib advisory. Do not drop it.

---

## Open findings

Counts: **6 Critical, 0 High, 10 Medium** still open. `N-01`–`N-07` are closed. H-01–H-12 and M-01–M-08 remain closed (no regression). Remaining new IDs are `N-08`–`N-17`. Two correction rows (`X-01`, `X-02`) are not defects.

---

### C-01 — `X-User-ID` is fully client-controllable (Critical, Security)

**Files:** `internal/identity/user.go` (`ResolveUserID`), `cmd/sidecar/main.go` `ServeHTTP` (~line 196), `cmd/limiter/main.go` `/check` and `/check_hierarchical` (~lines 161, 252).

**What the code does today**

```go
func ResolveUserID(r *http.Request, allowQuery bool) (string, error) {
	if userID := strings.TrimSpace(r.Header.Get("X-User-ID")); userID != "" {
		return userID, nil
	}
	if allowQuery { /* ?user_id= */ }
	return "", fmt.Errorf("missing trusted user identity: ...")
}
```

The docstring says “trusted” and “auth gateway (JWT validated upstream)”. Nothing in this repo validates a JWT, HMAC, mTLS client cert, or internal-network binding. Whatever string arrives in `X-User-ID` becomes the Redis quota key (`rate:<userID>` or `rate:user:<userID>`).

`ALLOW_QUERY_USER_ID` defaults to `false` (good). Chaos and tests already send the header. That does **not** close C-01: a browser or untrusted client that can reach the sidecar (or a mis-exposed limiter) still sets the header.

**Why it breaks production**

- Attacker sets `X-User-ID` to a victim → drains their quota (DoS of that user).
- Attacker rotates UUIDs every request → unlimited fresh buckets (bypass).
- Attacker targets a shared cap (`rate:global` is separate; user-level still explodes cardinality).

This is the identity trust boundary. A rate limiter that takes identity from the client is not a rate limiter.

**Constraints for a real fix**

- Do **not** “document that an API gateway must sit in front” and leave the code unchanged. The product is sold as a sidecar other teams deploy; many will expose it.
- Acceptable designs (pick one, make it the default, fail closed if misconfigured):
  1. Sidecar (or limiter) validates a JWT/HMAC whose subject is the quota key. Secret/JWKS from env. Reject unsigned/forged tokens.
  2. mTLS between edge and sidecar; identity from the client cert SAN, not a header.
  3. Internal-only header that the **edge proxy strips and rewrites** after its own auth, plus a startup check that the listen address is not public **and** a shared secret still required (`C-02`). Still weaker than 1 or 2.
- Keep `ALLOW_QUERY_USER_ID=false` as default. Query identity is demo-only.
- Tests: a request with a spoofed `X-User-ID` and no valid token must **not** consume another principal’s bucket. Today’s tests that only set the header will need a trusted-identity helper.

**Do not** conflate this with `C-02`. `C-02` is “who may call `/check`”. `C-01` is “whose quota is consumed”. Both must be closed.

---

### C-02 — empty `INTERNAL_API_KEY` leaves `/check` unauthenticated (Critical, Security)

**Files:** `cmd/limiter/config.go` (`LoadConfig`, ~62–108), `internal/auth/middleware.go` (`RequireAPIKey`).

**What the code does today**

```go
func RequireAPIKey(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
	if expectedKey == "" {
		return next // bypass
	}
	// ...
}
```

`INTERNAL_API_KEY` defaults to `""`. `STRICT_SECURITY` defaults to `"false"`. Only when `STRICT_SECURITY=true` does startup `Fatal` on a missing key. Otherwise it logs a warning and serves `/check` and `/check_hierarchical` to the world.

The comment on `RequireAPIKey` says the empty bypass is “useful for local Prometheus scraping”. That is the wrong layer: `/metrics` has its own `MetricsRequireAuth` path. Enforcement endpoints must not inherit a scrape convenience.

**Why it breaks production**

Anyone who can hit the limiter port can:

- Consume quota for any `X-User-ID` (`C-01` compounds this).
- Probe remaining counts.
- Combined with `C-05`, skip Lua entirely.

Compose/dev defaults will be copied into “just get it running” production.

**Constraints for a real fix**

- Fail startup if `INTERNAL_API_KEY` is empty **unconditionally** for the limiter binary (or at least for `/check*`). Do not hide this behind `STRICT_SECURITY`.
- Remove the empty-string bypass in `RequireAPIKey` **or** split two helpers: `RequireAPIKey` (never empty) vs `OptionalAPIKey` (metrics only).
- Sidecar already sends `X-Internal-API-Key` when configured (`cmd/sidecar/main.go` ~535–537). Sidecar must also fail startup if the key is empty when talking to a locked limiter.
- Tests: `cmd/limiter/config_test.go` currently tests `STRICT_SECURITY` behavior — extend so the default (non-strict) path also refuses to boot without a key. `route_security_test.go` already uses a real key; keep that.

**Related:** `C-03` (admin key), `C-05` (bypass is worse if `/check` is open), `M-05` (how the key is compared once it exists).

---

### C-03 — Admin API ships with a public default key (Critical, Security)

**Files:** `cmd/limiter/config.go` ~74–77, 101–114; `cmd/limiter/admin_api.go` (auth ~85, `ListenAndServe` ~47); `ENABLE_ADMIN_API` default `true`.

**What the code does today**

```go
AdminAPIKey: getEnv("ADMIN_API_KEY", "dev-key-change-in-prod"),
EnableAdminAPI: getEnv("ENABLE_ADMIN_API", "true") == "true",
```

That placeholder string is in the public GitHub repo. Admin can GET/PUT/DELETE hierarchical overrides (global/tenant/user/endpoint). `STRICT_SECURITY=true` refuses the placeholder; the default is not strict. Startup only **warns**.

**Why it breaks production**

Anyone who cloned the repo can:

- Set a tenant’s capacity to 0 (DoS).
- Set it to a huge number (bypass).
- Combined with `H-09`: the admin server is cleartext `ListenAndServe` on `ADMIN_PORT` (default 8082), so the key also travels in the clear on the network.

**Constraints for a real fix**

- No default value. Missing `ADMIN_API_KEY` while admin is enabled → `Fatal`.
- If the value equals `dev-key-change-in-prod` (or any known placeholder) → `Fatal`.
- Consider defaulting `ENABLE_ADMIN_API` to `false` for production profiles; if you keep it on, the key check must still be hard.
- Tests already exist in `config_test.go` for the strict path. Make the insecure default unbootable even without `STRICT_SECURITY`.

**Related:** `H-09` (TLS + constant-time compare on the same surface). Do both in one pass if you touch admin auth.

---

### C-04 — singleflight shares one token across concurrent requests (Critical, Concurrency)

**Files:** `cmd/sidecar/main.go` `serveNormal` ~411–451; tests `cmd/sidecar/concurrency_test.go` ~76–79 (and similar suites).

**What the code does today**

```go
resultAny, err, _ := s.limitFlight.Do(cacheKey, func() (interface{}, error) {
    return s.checkRateLimit(ctx, r, userID, false)
})
// every waiter uses the same result
result := resultAny.(limitResult)
if result.allowed { s.forwardRequest(w, r) }
```

`cacheKey` is `tenant|user|path`. A burst of N concurrent requests for that key becomes **one** HTTP `/check` (one Redis EVAL, one token deducted). All N waiters receive `allowed=true` and all N are forwarded upstream.

The test suite treats this as success:

```go
// Singleflight collapse guarantee: exactly 1 limiter call should be performed
if fixture.limiterHandler.callCount != 1 {
    t.Errorf("expected exactly 1 rate limiter call, got %d", ...)
}
```

That test encodes the bug. A mock limiter that always returns allowed + `callCount==1` cannot detect over-admission.

**Why it breaks production**

The sidecar exists to enforce the limit **under concurrency**, which is exactly when `singleflight` coalesces. Effective throughput becomes `N × configured_rate` during simultaneous bursts (thundering herd after a deploy, retry storms, multi-tab clients). Redis Lua is correct; the sidecar throws that correctness away in-process.

Denial coalescing **is** a valid optimization: if the leader is denied, waiters can share the 429. Admission must not be coalesced.

**Constraints for a real fix**

- **Per-request consume** for allows. Typical pattern: singleflight only around a *negative* cache / in-flight denial; each request that might be allowed calls `/check` itself (or a batch consume API that takes `n=`).
- Do **not** “fix” this by documenting that clients should not burst.
- Do **not** add a mutex around the whole `ServeHTTP` — that serializes the sidecar.
- Tests must assert **admitted count ≤ capacity** against a **real** limiter (miniredis or `internal/redistest`), not `callCount==1` on a stub that always allows. Keep a separate test that concurrent *denials* still collapse to one limiter call if you retain that optimization.
- `H-12` is the same `Do` callback: it uses the **leader’s** `r.Context()`. Fix both together (see H-12).

**Related:** `H-02` (the result of this flight is then stored in an unbounded `sync.Map`). `H-12` (cancellation).

---

### C-05 — `idempotent_replay=true` skips Lua and always allows (Critical, Security)

**Files:** `cmd/limiter/main.go` `/check` ~170–181 and `/check_hierarchical` ~271–289; sidecar helper `checkRateLimit(..., idempotentReplay bool)` ~504–525 **can** append the query param. Current `serveIdempotent` calls `checkRateLimit(..., false)` (~280), so the **sidecar does not use this bypass today**. The HTTP API still has it. Test helpers duplicate the same branch (`cmd/limiter/test_helpers_test.go`).

**What the code does today**

On `/check`:

```go
if r.URL.Query().Get("idempotent_replay") == "true" {
    setRateLimitHeaders(...)
    json.Encode({allowed: true, remaining: cfg.Capacity, replay: true})
    return // never touches Redis
}
```

Hierarchical path is the same idea: returns `allowed: true` with remaining derived from capacities, still no EVAL.

This looks like a leftover so that an idempotent *replay* of a cached 200 would not deduct a second token. Real idempotency already lives in `internal/idempotency` on the sidecar (claim/complete/fail with fencing). Replays should not hit `/check` at all, or should use a **server-issued** capability, not a client query string.

**Why it breaks production**

Anyone who can call `/check` (see `C-02`) adds `?idempotent_replay=true` and gets unlimited `allowed:true`. Combined with empty `INTERNAL_API_KEY`, this is unauthenticated unlimited quota.

**Constraints for a real fix**

- **Delete the query parameter** from limiter handlers and from `checkRateLimit`’s `idempotentReplay` flag. Sidecar idempotent replays already short-circuit inside `claim.lua` (`ResultReplay`) before `checkRateLimit`.
- If you believe a skip-quota path is still needed, it must be a server-side nonce/HMAC bound to a prior Claim, not a public query flag.
- Grep the repo for `idempotent_replay` after deletion (`main.go`, `test_helpers_test.go`, sidecar, docs). Tests that set the flag to skip Redis must be rewritten to use a real allow or a fake limiter interface.

---

### C-06 — ignored `Complete` errors → double-execute after success (Critical, Correctness)

**Files:** `cmd/sidecar/main.go` `forwardIdempotent` ~339–340; also `_ = completeIdempotent` / `_ = failIdempotent` at ~292, 301, 328.

**What the code does today**

```go
captured := capturer.Commit() // response already on the wire to the client
_ = s.completeIdempotent(r.Context(), scope, idemKey, fenceToken, captured.StatusCode, ...)
```

`Commit()` writes the captured upstream response to the client. Then Complete is best-effort. Redis errors, fence mismatches, and body-write failures are discarded.

Idempotency contract (see `docs/architecture/idempotency-architecture.md` and `claim.lua`): after a successful upstream call the key must remain `status=completed` until `CompletedTTL` (default **24h**, `internal/idempotency/types.go`). If Complete never lands, the key stays `processing` until `LockTTL` expires, then another replica **reclaims** (`claim.lua` expired-lock branch) and runs upstream **again**.

The architecture doc already admits “not exactly-once” for crash-between-upstream-and-complete. This finding is worse: even when Redis is up, the sidecar **does not try** to surface or retry Complete failure.

**Why it breaks production**

Payments, POSTs, anything with `Idempotency-Key`: client got 200, retry with the same key after lock expiry hits upstream a second time. Fencing (`ErrStaleFence`) only stops the *stale* worker from completing; it does not stop the *new* owner from executing.

**Constraints for a real fix**

- After a successful upstream response, Complete failure is an **incident**, not a log-and-forget:
  - Metric + log at error with scope/key (not the raw key if you consider it sensitive — at least a hash).
  - Retry Complete with backoff on a detached context with a budget (request ctx may already be done — `Commit` happened). Use the fence token you still hold.
  - If retries exhaust: never silently return; at minimum a metric that pages. Some designs nack to the client **only if** the body has not been committed; here it has, so the remaining lever is durable retry + alert.
- Same for `failIdempotent` after 503/429 (`H-05` interacts: even a successful Fail currently stores 503 as terminal for 24h).
- Tests: inject a Complete error (miniredis `Close` after capture, or a fake store) and assert a metric/retry, not a clean 200-and-forget. `internal/idempotency/store_tracing_test.go` already covers Fail-when-Redis-dead at the store layer; the **sidecar** still swallows the error.

**Related:** `H-05` (what gets persisted). `docs/limitations.md` “Not exactly-once” stays true for process crash; this finding is about **not even attempting** Complete.

---

## New from re-audit (2026-08-19)

These IDs were not in the original C/H/M register. **`N-01`–`N-07` are closed** (see [Already fixed](#already-fixed--do-not-redo)). **`N-08`–`N-17` are still open** — full briefs below. Do not confuse them with the historical H-02+ writeups after this section, which stay as closed-issue context.

| ID | Sev | Status | Title |
|----|-----|--------|--------|
| N-01 | High | **Fixed** | Audit 5-key EVAL hash-tagged `{audit}` |
| N-02 | High | **Fixed** | Idempotency meta+body EVAL hash-tagged `{scope}` |
| N-03 | High | **Fixed** | Sidecar does not Record on local Allow reject |
| N-04 | High | **Fixed** | Complete only authoritative &lt;500 (not 408/425); 5xx Fail+LockTTL |
| N-05 | High | **Fixed** | Gateway URL SSRF guard + guarded dial/redirect |
| N-06 | High | **Fixed** | Terraform/Compose `ALLOW_QUERY_USER_ID=false` + template test |
| N-07 | High | **Fixed** | `ResolveTenantID` query-gated + charset/length (header still spoofable: C-01) |
| N-08 | Medium | Open | `/metrics` unauthenticated unless `METRICS_REQUIRE_AUTH=true` |
| N-09 | Medium | Open | `METRICS_REQUIRE_AUTH=true` + empty key still opens metrics (`RequireAPIKey` empty bypass) |
| N-10 | Medium | Open | Public `/health` returns Redis `mode` / `master_addr` |
| N-11 | Medium | Open | Terraform plaintext env secrets + `allowed_cidr=0.0.0.0/0` |
| N-12 | Medium | Open | Default reverse-proxy path has no `MaxBytesReader` (H-10 only covered routing/idempotent) |
| N-13 | Medium | Open | Idempotent Fail/Complete still use `r.Context()` (H-12 only detached `serveNormal`) |
| N-14 | Medium | Open | Audit tenant ZSET has no ZCARD cap (user index capped at 1000) |
| N-15 | Medium | Open | User/endpoint strings unsanitized before Redis keys (tenant charset landed in N-07) |
| N-16 | Medium | Open | Hierarchical `Retry-After` is `1/min(refill)` not a measured wait |
| N-17 | Medium | Open | CI `redis-integration` skips `cmd/limiter` and `cmd/sidecar` |

Live visual: `production-readiness-reaudit.canvas.tsx` in the workspace `canvases/` directory.

---

### N-08 — `/metrics` is unauthenticated by default (Medium, Security)

**Files:** `cmd/limiter/config.go` `MetricsRequireAuth` default `"false"` (~88); `cmd/limiter/main.go` ~140–148; `cmd/sidecar/main.go` ~780–785, ~887.

**What the code does today**

```go
metricsKey := ""
if cfg.MetricsRequireAuth {
    metricsKey = cfg.MetricsAuthKey()
}
mux.Handle("/metrics", auth.RequireAPIKey(metricsKey, promhttp.Handler().ServeHTTP))
```

Comment in `main.go`: “Metrics stay open by default so Prometheus can scrape without bearer tokens.” Sidecar copies the same pattern: `METRICS_REQUIRE_AUTH` must be the string `"true"` or `metricsKey` stays `""` and `RequireAPIKey` is a no-op (`N-09`).

**Why it breaks production**

Prometheus text is reconnaissance: request rates by handler, Redis errors, circuit-breaker state, audit drops. Compose publishes limiter `:8080` and sidecar `:9090`. Terraform `allowed_cidr` defaults to `0.0.0.0/0` (`N-11`), so a “production” ECS task can expose scrape to the internet.

**Constraints for a real fix**

- Default `METRICS_REQUIRE_AUTH=true` for Terraform and any profile that is not explicitly local-dev. Keep Compose scrape working by giving Prometheus the same key (or bind metrics to loopback / an internal network).
- Do not “document that scrape must be on a private net” and leave the default open.
- Tests: with the production-shaped default, `/metrics` without a key is 401; with the key it is 200. Chaos/Grafana jobs must send the header if you flip the default.
- Fix together with `N-09` (empty-key bypass) or operators will set `METRICS_REQUIRE_AUTH=true` and still be open.

**Related:** `C-02` (same `RequireAPIKey` empty bypass), `N-09`, `N-11`.

---

### N-09 — `METRICS_REQUIRE_AUTH=true` with empty key still opens `/metrics` (Medium, Security)

**Files:** `cmd/limiter/main.go` ~140–148; `cmd/limiter/config.go` `MetricsAuthKey()` ~59–64; `internal/auth/middleware.go` `RequireAPIKey` ~31–34; `cmd/sidecar/main.go` `sidecarMetricsAuthKey` ~737–785.

**What the code does today**

```go
func RequireAPIKey(expectedKey string, next http.HandlerFunc) http.HandlerFunc {
    if expectedKey == "" {
        return next // bypass
    }
```

`MetricsAuthKey()` returns `METRICS_API_KEY` if set, else `INTERNAL_API_KEY`. If the operator sets `METRICS_REQUIRE_AUTH=true` but both keys are empty (limiter default `INTERNAL_API_KEY=""` — `C-02`), `metricsKey` is `""` and the wrapper disappears. Sidecar: `metricsAPIKey := sidecarMetricsAuthKey(internalKey, METRICS_API_KEY)` then the same `if metricsRequireAuth { metricsKey = metricsAPIKey }`.

**Why it breaks production**

The flag looks like a lock. Misconfig (empty internal key, “I’ll add Prometheus auth later”) leaves scrape world-readable. This is C-02 applied to `/metrics`.

**Constraints for a real fix**

- If `MetricsRequireAuth` is true and the resolved key is empty → `Fatal` at startup (limiter and sidecar).
- Prefer splitting `RequireAPIKey` (never empty) vs `OptionalAPIKey` (metrics when auth is off). Do not keep the empty bypass on enforcement paths (`C-02`).
- Tests: `METRICS_REQUIRE_AUTH=true` + empty keys must not boot (or must 401 every scrape — fail-closed is better). Current helpers set a real `MetricsAPIKey` and will not catch this.

**Related:** `C-02`, `N-08`. Same middleware; one pass.

---

### N-10 — public `/health` leaks Redis topology (Medium, Security)

**Files:** `cmd/limiter/main.go` `/health` ~150–167; `internal/redis/health.go` `Health` (`mode`, `role`, `replication`, `master_addr`); sidecar `evaluateSidecarHealth` ~31–57 embeds the same `Health` when Redis is configured. Sidecar also **GETs** limiter `/health` (`cmd/sidecar/health.go` ~64) but only uses the status code for its own limiter check; the limiter’s JSON is still public to anyone who hits `:8080/health`.

**What the code does today**

```go
json.NewEncoder(w).Encode(map[string]interface{}{
    "status": "healthy",
    "redis":  h, // Mode, Role, Replication, MasterAddr
})
```

Unauthenticated. `cmd/limiter/route_security_test.go` asserts public `/health` is 200. Terraform README tells operators to `curl :8080/health` and expect `{"status":"healthy","redis":{...}}`.

**Why it breaks production**

`master_addr`, Sentinel role, and replication summary are recon for targeting the Redis control plane. Combined with `N-11` (open CIDR) this is internet-visible topology.

**Constraints for a real fix**

- Public body is `{"status":"healthy"}` / `{"status":"unhealthy"}` only (HTTP 200 vs 503 can stay).
- Put `mode` / `master_addr` / replication behind admin auth (or a separate `/health/detail`).
- Sidecar public `/health` must not echo limiter Redis fields; keep limiter probe internal.
- Update Terraform README and health tests that assert the fat JSON. Load-balancer health checks must keep working on status-only.

**Related:** `N-11`, `H-09` (admin surface already exists for privileged ops).

---

### N-11 — Terraform plaintext env secrets + `allowed_cidr=0.0.0.0/0` (Medium, Security)

**Files:** `deploy/terraform/variables.tf` ~19–43; `deploy/terraform/ecs.tf` task `environment` block (`REDIS_PASSWORD`, `ADMIN_API_KEY`, `INTERNAL_API_KEY` as `value = var.*`); `deploy/terraform/network.tf` SG ingress `cidr_blocks = [var.allowed_cidr]`; `terraform.tfvars.example` sets `allowed_cidr = "0.0.0.0/0"`.

**What the code does today**

Secrets have **defaults** (`change-me-redis-password`, `change-me-internal-key`, `change-me-admin-key`) and are injected as plaintext container environment. They appear in the ECS task definition (console, APIs, anyone with `ecs:DescribeTaskDefinition`). Security group default allows the world to `:8080` (and the task still maps host 8082 even though `ADMIN_HOST=127.0.0.1` — `H-09`).

`ALLOW_QUERY_USER_ID=false` already landed (`N-06`). This finding is secrets + network, not query identity.

**Why it breaks production**

Copy-paste “free-tier AWS” is an internet-facing limiter with guessable keys in the task def. `/check` + `C-01`/`C-02`/`C-05` become remote.

**Constraints for a real fix**

- No secret defaults. `redis_password` / `internal_api_key` / `admin_api_key` are `nullable = false` with no default (required tfvars).
- ECS `secrets { valueFrom = aws_ssm_parameter... }` (or Secrets Manager), not `environment`.
- Default `allowed_cidr` must not be `0.0.0.0/0`. Example file should show `x.x.x.x/32` first; `0.0.0.0/0` if kept must be an explicit, warned override.
- Tests: none today (Terraform is not in CI). At least document in `deploy/terraform/README.md` and fail `terraform validate` if you add a check/precondition. Do not put real secrets in git.

**Related:** `C-02`, `C-03`, `N-08`, `N-10`. `H-09` already bound admin to loopback.

---

### N-12 — default reverse-proxy path has no `MaxBytesReader` (Medium, Resilience)

**Files:** `cmd/sidecar/main.go` `forwardRequest` ~666–703. Routing branch uses `readRequestBody` + `http.MaxBytesReader` (`H-10`). Idempotent path uses `idempotency.ReadBody` with `MaxBodyBytes`. The **default** path is:

```go
target, _ := url.Parse(s.upstreamURL)
r.Host = target.Host
s.proxy.ServeHTTP(w, r) // httputil.ReverseProxy streams r.Body unbounded
```

Compose `ENABLE_ROUTING=true` hides this on the demo stack. Any deploy with `ENABLE_ROUTING=false` (or routing init failure falling back — there is no fallback; routing is all-or-nothing) hits this. Unit tests that do not `SetRouter` exercise this path (`cmd/sidecar/sidecar_test.go`).

**Why it breaks production**

Public sidecar + slow/malicious client POST → sidecar buffers or streams unbounded body while waiting on upstream. H-10 only closed the intelligent-routing and idempotent readers.

**Constraints for a real fix**

- Wrap `r.Body` with `http.MaxBytesReader` (same cap as `idempotency.MaxBodyBytes`, default 1MB) **before** `proxy.ServeHTTP` on mutating methods. Return 413 on overflow without forwarding.
- GET/HEAD can stay uncapped if you want; POST/PUT/PATCH must not.
- Tests: large POST on the non-router fixture is 413 and `upstreamHandler.callCount` stays 0. Do not weaken H-10 routing tests.

**Related:** `H-10` (routing). Same cap constant; do not invent a second limit.

---

### N-13 — idempotent Fail/Complete still use `r.Context()` (Medium, Correctness)

**Files:** `cmd/sidecar/main.go` `serveIdempotent` / `forwardIdempotent`. `serveNormal` limiter flight uses `context.WithoutCancel` + timeout (`H-12`, ~480). Idempotent path still:

```go
result, err := s.checkRateLimit(ctx, r, userID, false) // ctx is r.Context()
...
_ = s.failIdempotent(r.Context(), ...)
captured := capturer.Commit()
_ = s.completeIdempotent(r.Context(), ...) // N-04 branches Complete vs Fail; still this ctx
```

**What the code does today**

`Commit()` already wrote the body. If the client disconnects, `r.Context()` is done. Redis `Complete`/`Fail` then fails or is skipped. Combined with `_ =` (`C-06`), the key stays `processing` until `LockTTL`, then another owner reclaims and hits upstream again.

Limiter `/check` on the idempotent path also dies with the client, so a disconnect mid-check can 503 a mutation that should have waited for Redis.

**Why it breaks production**

Same double-execute as `C-06`, triggered by a cancelled request ctx rather than a Redis error. Payments/POSTs with `Idempotency-Key`.

**Constraints for a real fix**

- `Fail` / `Complete` after the client has an answer: `context.WithoutCancel` + a short timeout budget (Redis client already times out). Keep the fence token.
- Idempotent `checkRateLimit` should use the same detached budget as `serveNormal` (`H-12`), not the leader/client ctx.
- Tests: cancel the request context after `Commit` (or inject a cancelled ctx into `completeIdempotent`) and assert Redis still reaches `completed`/`failed`. Do not only test the happy path.
- Do **not** treat this as closing `C-06`: swallowed errors still need retry + metric.

**Related:** `C-06`, `H-12`, `N-04`.

---

### N-14 — audit tenant ZSET has no ZCARD cap (Medium, Distributed)

**Files:** `internal/audit/lua/append.lua`. User index: `user_index_cap = 1000` and a trim loop (~114–126). Tenant index: `ZADD` + `EXPIRE` only (~84, 92). Global `max_events` trims `idx:ts` (~104–110), which **does** `purge_event` (and thus `ZREM` tenant members for those event IDs), but a **single tenant** can still hold up to `max_events` (default 100_000) members, and **many tenants** each get their own ZSET until TTL.

**What the code does today after N-07:** `ResolveTenantID` rejects oversized/illegal charset, so `{`/` ` injection is gone. Header `X-Tenant-ID` is still client-set (`C-01`). Rotating valid slugs (`t1`, `t2`, …) still creates `audit:{audit}:idx:tenant:{id}` per tenant for `AUDIT_RETENTION_HOURS` (default 168h).

**Why it breaks production**

High-cardinality tenants → Redis memory ≈ distinct_tenants × index overhead until expire. M-06 closed the **user** index bomb; tenant is the remaining axis.

**Constraints for a real fix**

- Mirror the user cap: `tenant_index_cap` (1000 or `max_events` clamped) with the same ZREM/purge loop so ZCARD cannot grow without bound **per tenant**.
- Do not rely on N-07 charset alone.
- Tests: seed &gt; cap members on one tenant index (same pattern as `TestRecord...dangling` user cap in `store_test.go`); after `record`, `ZCARD(tenantIndexKey)` ≤ cap.

**Related:** `M-06` (user cap — copy the loop, don’t rewrite TTL), `C-01`/`N-07` (who can mint tenant IDs).

---

### N-15 — user/endpoint strings become Redis keys unsanitized (Medium, Resilience)

**Files:** `internal/identity/user.go` `ResolveUserID` (no `ValidateID`); `cmd/limiter/main.go` hierarchical `userKey` / `endpointKey` ~320–322 (`endpoint := r.URL.Query().Get("endpoint")`); audit `RecordInput.UserID`; sidecar cache key `userID` + path.

**What the code does today after N-07:** tenants go through `ValidateID` (max 128, letters/digits/`-_. :`). **Users do not.** Endpoint is a raw query string (paths can be long, include `..`, spaces, Unicode). Those strings are interpolated into `rate:user:%s`, `rate:endpoint:%s:%s`, `audit:{audit}:idx:user:%s`.

**Why it breaks production**

- C-01 already allows rotating `X-User-ID` → unbounded `rate:user:*` hashes (TTL helps but cardinality during the window is the attack).
- Oversized / weird keys: Redis memory, slow CLUSTER slot calc, log injection, hash-tag braces in user IDs (`SanitizeHashTag` is only on idempotency scope today).
- Endpoint query is another unbounded dimension even with a valid tenant.

**Constraints for a real fix**

- Call `ValidateID` (or a slightly wider alphabet if you must allow emails — then cap length hard) in `ResolveUserID`. Fail-closed 400.
- Cap and normalize `endpoint` the same way as sidecar `normalizeHTTPPath` (and reject `..`).
- Tests: 129-char user ID → 400; `{inject}` user ID → 400; huge `endpoint` → 400. Existing IDs like `alice-hier` must still pass.
- This does **not** close C-01: a valid 16-char random ID still mints a fresh bucket.

**Related:** `C-01`, `N-07`, `N-14`.

---

### N-16 — hierarchical `Retry-After` is a config estimate, not the blocking wait (Medium, Correctness)

**Files:** `cmd/limiter/main.go` denial path ~364 `retryAfterForHierarchical(cfg)`; `cmd/limiter/ratelimit_http.go` ~41–58; `internal/limiter/lua/hierarchical.lua` returns `{allowed, remaining}` only (~96). Flat token/sliding paths use measured `retry_after` (`M-04`, `retryAfterHeader`).

**What the code does today**

```go
func retryAfterForHierarchical(cfg Config) string {
    minRate := min(Endpoint, User, Tenant, Global refill)
    seconds := ceil(1.0 / minRate)
}
```

That is “time to refill one token at the slowest **configured** rate”, not “when the bucket that actually blocked this request will allow again” (overrides, remaining=0 on tenant vs endpoint, Lua TIME).

**Why it breaks production**

Clients retry too soon (thundering herd) or wait too long (under-utilization). Hierarchical is the advertised multi-tenant path; Retry-After is part of the HTTP contract.

**Constraints for a real fix**

- Return the blocking level’s wait from Lua (or `max` of per-level waits computed from remaining + refill + Redis TIME). Same units as sliding-window `retry_after_ms`.
- HTTP layer: `retryAfterHeader(cfg, measured)` like the flat path. Do not leave a second formula in `test_helpers_test.go`.
- Tests: exhaust endpoint capacity with a fast endpoint refill and a slow tenant refill — `Retry-After` must match the bucket that denied, not blindly `1/min(all rates)`.
- Do not change Lua allow/deny semantics; only the hint.

**Related:** `M-04` (the pattern to copy).

---

### N-17 — CI `redis-integration` skips `cmd/limiter` and `cmd/sidecar` (Medium, Testing)

**Files:** `.github/workflows/ci.yml` job `redis-integration` ~121–158.

**What the code does today**

```yaml
go test -count=1 -p 1 -v \
  ./internal/limiter/... \
  ./internal/circuitbreaker/... \
  ./internal/idempotency/... \
  ./internal/audit/... \
  ./internal/routing/...
```

`REDIS_TEST_ADDR` is set. `cmd/limiter` and `cmd/sidecar` are **not** in the list. Those packages still run in the default `go test ./...` job against **miniredis**. C-05 (`idempotent_replay`), hierarchical HTTP, sidecar CB/idempotency wiring never hit real Redis in CI.

**Why it breaks production**

M-07 exists because gopher-lua ≠ Redis Lua. Handler bugs (replay bypass, header identity, N-04 persist branch) can pass unit tests and fail only on a real server — or never fail CI at all.

**Constraints for a real fix**

- Add `./cmd/limiter` and `./cmd/sidecar` to the redis-integration command **or** a follow-on step with the same `REDIS_TEST_ADDR` and `-p 1`.
- Tests that need miniredis-only features (`Close` mid-request) should skip when `REDIS_TEST_ADDR` is set (pattern already used in store tests).
- Do not drop the internal Lua packages from the job.
- Keep runtime reasonable; `-p 1` + flush-on-setup is load-bearing.

**Related:** `M-07`, `C-05`. Closing N-17 does not close C-05; it makes C-05 harder to miss.

---

The H-02–H-12 sections below are **closed-issue context** (already in [Already fixed](#already-fixed--do-not-redo)). Do not re-implement them.

### H-02 — denial cache `sync.Map` unbounded vs 10s sweeper (High, Resilience)

**Files:** `cmd/sidecar/main.go` `cache.Store` ~437–443; `StartCacheSweeper` ~112+; `main` sets TTL default **30ms** (`CACHE_TTL_MS`) and sweeper **10s** (~672, 823).

**What the code does today**

Every flight result is stored, including **allows**. On the read path, allowed entries are loaded then **ignored** (“allowed cache entry ignored”). So allows occupy memory until the sweeper deletes them.

Sweeper interval is 10 seconds; TTL is 30ms. Between sweeps, dead entries accumulate. Cardinality is `tenant|user|path` — attacker-controlled if `C-01` is open.

**Why it breaks production**

Memory ≈ arrival_rate × sweeper_interval for distinct keys, not × TTL. A 30ms cache with a 10s sweeper holds ~300× more garbage than the TTL suggests. High-cardinality identity → sidecar OOM.

**Constraints for a real fix**

- Store **denials only**. Allows must not enter the map (they are already ignored).
- Sweeper interval should be on the order of `TTL/2`, not 10s vs 30ms. Or use a bounded LRU / singleflight-only coalescing with no map.
- Cap map size; on overflow drop inserts (fail open on cache, never on quota).
- Tests: generate many distinct user IDs, assert the map does not grow without bound (or that allows are never stored). Existing cache tests in `cmd/sidecar/cache_test.go` check isolation, not size.

---

### H-05 — idempotency replays `failed` / 429 as terminal for 24h (High, Correctness)

**Files:** `internal/idempotency/lua/claim.lua` ~42–52; sidecar `serveIdempotent` ~292–304; `Fail`/`Complete` both use `CompletedTTL` (default 86_400_000 ms) in `internal/idempotency/store.go`.

**What the code does today**

```lua
if status == 'completed' or status == 'failed' then
  -- replay cached http_status / body for CompletedTTL
  return {2, http_status, headers, body}
end
```

Sidecar on limiter 503:

```go
_ = s.failIdempotent(..., http.StatusServiceUnavailable, ..., body)
http.Error(w, "Rate limiter unavailable", 503)
```

On 429 it calls **`completeIdempotent`** with 429 (~299–304), which is even more terminal: a rate-limit denial is stored as `completed` for 24h.

**Why it breaks production**

Client sends `Idempotency-Key: K`, limiter is briefly down → 503 persisted as `failed` → every retry of `K` for 24h gets 503 even after Redis is healthy. Same for 429: the client is stuck denied for a day for that key, even when quota has refilled.

**Constraints for a real fix**

- Replay **only** authoritative upstream successes (2xx/4xx that came from upstream after a real attempt), not local 503s and not limiter 429s.
- Transient outcomes (`processing` timeout, 503, 429 from limiter) should expire with **lock TTL** (or a short failed TTL), and `claim.lua` should treat expired `failed` like an expired lock (reclaim), not like `completed`.
- 429 from the **upstream** (if you proxy a 429) is a product decision; 429 from **your** rate limiter must not poison the key.
- Tests: `store_test.go` covers fence/reclaim; add a case that `Fail(503)` then wait/expire then `Claim` is `ResultClaimed` again. Sidecar tests should not expect a 503 to be replayed for 24h.

**Related:** `C-06` (Complete/Fail errors swallowed). Fix persistence policy (`H-05`) and error handling (`C-06`) in one sidecar/idempotency pass.

---

### H-06 — `pathAllowed` third clause bypasses the allowlist (High, Security)

**Files:** `cmd/sidecar/main.go` ~173–182; `ALLOWED_PATHS` env parsed in `main`.

**What the code does today**

```go
if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix) {
    return true
}
```

The third clause makes the second redundant and wrong: `ALLOWED_PATHS=/api` matches `/api`, `/api/foo`, **and** `/apiEvil`, `/api-admin`, `/apitoken`.

Empty `allowedPaths` allows everything (with a startup warning). That is a separate footgun; this finding is the prefix bug when the list **is** set.

**Why it breaks production**

Operators think they locked the proxy to `/api/...`. Adjacent paths still go through rate limiting **and** to upstream.

**Constraints for a real fix**

- Match only exact `path == prefix` or `strings.HasPrefix(path, prefix+"/")`. Normalize trailing slashes so `/api` and `/api/` are equivalent if you want that.
- Tests: `/api` allowed, `/api/users` allowed, `/apiEvil` **rejected**. There may be no test for this today.

---

### H-07 — circuit-breaker Redis keys never `EXPIRE` (High, Distributed)

**Files:** `internal/circuitbreaker/lua/allow.lua` (HSET, no EXPIRE); `record.lua` (same); `internal/circuitbreaker/store.go` `ListTargets` SCANs `cb:*` (~162–175). **Does not apply to `LocalStore`** (in-process, limiter’s Redis target). **Does apply** to the Redis-backed store used for **gateways / central-limiter target** from the sidecar.

**What the code does today**

First `Allow` on a target `HSET`s `cb:{target}` forever. Dynamic gateway IDs (intelligent routing) create one hash per gateway for the life of the Redis. `ListTargets` (admin) `SCAN`s all of them.

**Why it breaks production**

Unbounded Redis memory as gateways come and go (k8s pod names, ephemeral IDs). SCAN cost grows.

**Constraints for a real fix**

- `PEXPIRE` on allow and record to an idle TTL >> cooldown (e.g. 24h of no traffic → key may vanish, which is equivalent to a fresh closed circuit — acceptable).
- Document that expiry resets consecutive-failure counts (correct: idle target should not stay open forever anyway).
- Optional GC in `ListTargets` for keys with `total_count=0` and old `updated_at`.
- Tests: after Allow+Record, `TTL` is > 0. Use `internal/redistest`.

**Do not** add EXPIRE to `LocalStore`; it is memory with a map you can bound separately if needed.

---

### H-08 — half-open probe budget reopens the circuit before in-flight probes record (High, Resilience)

**Files:** `internal/circuitbreaker/lua/allow.lua` ~33–42; `internal/circuitbreaker/local_store.go` ~82–95 (intentionally mirrored). Tests `TestHalfOpenProbeExhaustionTransitionsToOpen` **encode this behavior**.

**What the code does today**

Half-open admits up to `max_probes` (default 3). The **next** `Allow` after `half_open_calls >= max_probes` immediately sets state `open` **without waiting** for those probes’ `Record` to arrive. If probes are slow but successful, their success records can hit an already-reopened circuit (or be counted in the wrong state). Recovery is discarded; the breaker stays in an open ↔ half-open loop under load.

`LocalStore` comment: “Probe budget exhausted without recovery — reopen (matches allow.lua).” Parity was the goal of L-03; both copies of the bug remain.

**Why it breaks production**

A healthy dependency that is merely slow to answer the probes never closes. Sidecar/limiter stick on 503. Chaos recovery already has to wait cooldown (`CB_OPEN_COOLDOWN_MS`); this makes recovery racy under concurrency.

**Constraints for a real fix**

- Once `max_probes` are **in flight**, further `Allow`s should **reject** (stay half-open, do not increment, do not reopen) until those probes `Record`. Reopen only if recorded failures/timeouts fail the half-open success threshold, or if a deadline since `half_open_at` expires with no successes.
- Change **Lua and LocalStore together** plus tests. If you only fix one, H-04’s parity guarantee breaks and Redis vs in-process breakers diverge.
- Rewrite `TestHalfOpenProbeExhaustionTransitionsToOpen` / local equivalent: exhausting Allow without Record must **not** force open; recording failures should.

---

### H-09 — Admin API cleartext (High, Security)

**Files:** `cmd/limiter/admin_api.go` `ListenAndServe` ~47 (no TLS). `ENABLE_ADMIN_API` default true. Default key `dev-key-change-in-prod` (`C-03`).

**What the code does today**

Admin is a second HTTP server, default on. No `ListenAndServeTLS`. Key **comparison** is no longer `!=`: handlers call `auth.SecureCompare` (M-05). The remaining hole is the transport and bind, not the compare.

**Why it breaks production**

Override/circuit/audit APIs still travel in the clear. Anyone on the path can read `X-API-Key`. Default key (`C-03`) compounds this.

**Constraints for a real fix**

- TLS: require `TLS_CERT_FILE`/`TLS_KEY_FILE` when admin is enabled in production, **or** bind `127.0.0.1` only unless an explicit `ADMIN_BIND` is set. Limiter already has TLS env for the main server (`config.go` ~86–119).
- Default admin off, or keep it on only with C-03’s hard key check.
- Prefer wrapping admin routes with `auth.RequireAPIKey` after C-02 removes the empty bypass, instead of per-handler `SecureCompare`. Compare itself is already constant-time.

---

### H-10 — routing reads unbounded body; errors ignored (High, Resilience)

**Files:** `cmd/sidecar/main.go` `readRequestBody` ~620–629; callers `forwardRequest` ~602 and `forwardIdempotent` ~321 (`body, _ :=`). `internal/routing/router.go` `Forward` correctly takes `[]byte` and replays per gateway (`execute`).

**What the code does today**

```go
func readRequestBody(r *http.Request) ([]byte, error) {
    body, err := io.ReadAll(r.Body) // no LimitReader
    ...
}
body, _ := readRequestBody(r) // error ignored
s.router.Forward(..., body)
```

Idempotent path uses `idempotency.ReadBody` with `MaxBodyBytes` **before** claim; later `forwardIdempotent` may call `readRequestBody` again without a limit if routing is on.

**Why it breaks production**

- Memory DoS: unbounded POST through the sidecar.
- Failover: if read failed, `body` is nil/empty; `Forward` still “succeeds” in sending empty POSTs to gateways. Mutations silently lose the body.

**Constraints for a real fix**

- `http.MaxBytesReader` (or the same cap as `idempotency.MaxBodyBytes`) in `readRequestBody`. Return 413 on overflow.
- **Never** ignore the error. If read fails, do not Forward.
- Idempotent path should reuse the bytes already read by `ReadBody`, not read twice.
- Tests: huge body rejected; read error does not call upstream; failover still sends the **same** non-empty body (router already can — sidecar must pass it).

---

### H-11 — limiter JSON decode error recorded as circuit-breaker success (High, Resilience)

**Files:** `cmd/sidecar/main.go` `checkRateLimit` ~473–484 (defer `ClassifyHTTP(callErr, statusCode, ...)`), ~552 `statusCode = resp.StatusCode`, ~587–588 decode.

**What the code does today**

```go
statusCode = resp.StatusCode // 200
...
if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
    return limitResult{}, err // callErr still nil
}
```

Defer runs with `callErr == nil` and `statusCode == 200`. `ClassifyHTTP` treats that as **success**. A limiter returning 200 with a truncated/HTML/empty body never trips the central-limiter circuit. Traffic keeps hitting a sick dependency.

Non-200 paths **do** set `callErr` (~577–581). Transport errors set `callErr` (~546). Only the decode path is wrong.

**Constraints for a real fix**

```go
if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
    callErr = err
    return limitResult{}, err
}
```

Add a test: limiter returns 200 + invalid JSON; after enough samples the sidecar circuit for `TargetCentralLimiter` opens (or `callErr` is classified as failure in a unit test of this function). Do not only test the happy path.

---

### H-12 — singleflight leader cancellation cascades to waiters (High, Concurrency)

**Files:** same `Do` as `C-04`: `cmd/sidecar/main.go` ~411–413.

**What the code does today**

```go
resultAny, err, _ := s.limitFlight.Do(cacheKey, func() (interface{}, error) {
    return s.checkRateLimit(ctx, r, userID, false) // ctx is the leader's request
})
```

`singleflight` runs one function. That function uses the **leader** request’s `ctx`. If the leader disconnects/cancels, `httpClient.Do` abort applies to the shared call. All waiters see that error → fail-closed 503 for everyone, or fail-open forward for everyone (`FAIL_OPEN`).

**Why it breaks production**

One impatient client (or LB idle timeout on that connection) mass-503s or mass-bypasses the same `tenant|user|path`.

**Constraints for a real fix**

- Inside `Do`, use `context.WithoutCancel(ctx)` plus a **timeout budget** (limiter HTTP client already has timeouts in `cmd/sidecar/limiter_http.go`). Do not inherit the leader’s cancel.
- Still pass a context that dies if the **process** is shutting down.
- Fix together with `C-04` (same callback). Tests: cancel leader mid-flight; waiters with live ctx should still get a limiter result (or their own timeout), not the leader’s cancel.

---

## Correction rows (not defects)

### X-01

Peer audit claimed `go test -race` is missing from CI. False. `.github/workflows/ci.yml` has a dedicated `race` job. Do not add a second one. Race coverage does **not** mean `C-04` tests are correct.

### X-02

Peer audit claimed `go 1.25.0` is not a real language version. False as of 2026. Language version stays `1.25`; **toolchain** is pinned to `go1.26.6` because M-07’s `govulncheck` found reachable stdlib CVEs on 1.26.1. Bump toolchain on new advisories.

---

## Recommended implementation order

Work in **passes that share files**, not strictly by ID.

| Pass | IDs | Why together |
|------|-----|----------------|
| **P0 identity + auth** | C-01, C-02, C-03 | Same trust boundary. Empty key, default admin key, spoofable `X-User-ID` (N-06/N-07 query gates landed; header spoof remains C-01). H-09 bind/TLS already landed. |
| **P0 limiter API** | C-05 | Delete `idempotent_replay` while touching `cmd/limiter/main.go`. |
| **P1 sidecar admission** | C-04, N-12 | Per-request consume; body cap on the default proxy. N-03 Record-on-reject closed. H-02/H-11/H-12 already landed. |
| **P1 idempotency** | C-06, N-13 | Complete/Fail retry + detached ctx. N-02 hash tags and N-04 5xx Fail landed. H-05 LockTTL Fail already landed. |
| **P2 routing / deploy** | N-11 | Terraform secrets/CIDR. N-05 gateway URL guard landed. |
| **P3 hygiene** | N-08–N-10, N-14–N-17 | Metrics/health, tenant index cap, remaining ID sanitization, hierarchical Retry-After, CI `cmd/` matrix. |

Do not start with remaining Medium items while C-01–C-06 are open. N-07 did **not** close C-01: `X-Tenant-ID` / `X-User-ID` headers are still client-set.

---

## Files you will almost certainly edit

| Area | Paths |
|------|--------|
| Identity | `internal/identity/user.go`, sidecar + limiter handlers that call it |
| Auth | `internal/auth/middleware.go`, `cmd/limiter/config.go`, `cmd/limiter/admin_api.go` |
| Limiter HTTP | `cmd/limiter/main.go`, `cmd/limiter/test_helpers_test.go` |
| Sidecar | `cmd/sidecar/main.go`, `cmd/sidecar/concurrency_test.go`, `cmd/sidecar/cache_test.go` |
| Idempotency | `internal/idempotency/lua/claim.lua`, `store.go`, sidecar complete/fail |
| Hierarchical keys | `cmd/limiter/main.go`, `internal/limiter/lua/hierarchical.lua` |
| Circuit breaker | `lua/allow.lua`, `lua/record.lua`, `local_store.go`, tests |
| Override | `internal/override/override.go` |
| Audit | `internal/audit/lua/append.lua` (N-14 tenant ZCARD still open) |
| Token bucket clock | `lua/token_bucket.lua`, `redis_atomic_token_bucket.go` |
| Routing / default proxy body | `cmd/sidecar/main.go` `forwardRequest` (N-12 non-router path) |
| Metrics / health | `cmd/limiter/main.go`, `internal/auth/middleware.go`, `internal/redis/health.go` |
| Terraform | `deploy/terraform/variables.tf`, `ecs.tf`, `network.tf` |
| CI | `.github/workflows/ci.yml` `redis-integration` |

---

## Residual risk not in the register

The audit noted but did not ticket: audit-trail async shutdown under Redis unavailability (there are tests in `internal/audit/shutdown_test.go` — re-read before claiming a gap); Sentinel failover under write load; k6 automation not CI-gated. Do not confuse those with the IDs above.
