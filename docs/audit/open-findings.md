# Open production-readiness findings

**Audience:** a subsequent AI agent (or engineer) that must fix these without rediscovering the audit.

**Source of truth for status:** this file for *open* work. The live visual register is a Cursor canvas (`production-readiness-audit.canvas.tsx` in the workspace `canvases/` directory, not committed). IDs (`C-01`, `H-08`, …) are stable across both.

**Date of this dump:** 2026-08-19. Line numbers match the working tree at that date. Re-read the cited files before editing; do not trust a line number if the surrounding code no longer matches the snippet described here.

**Verdict of the audit:** do not ship to other teams as a production dependency until the six Critical items (`C-01`–`C-06`) are closed. High items are the next wave. Medium items are real defects, not nits.

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
| H-12 | Singleflight callback decouples from leader's context cancellation via `context.WithoutCancel(ctx)` with 1.5s timeout budget to prevent cascading 503s on client disconnects | `cmd/sidecar/main.go`, `cmd/sidecar/sidecar_test.go` |

Toolchain pin `go 1.25.0` + `toolchain go1.26.6` in `go.mod` is load-bearing for the vuln job (stdlib CVEs). Bump the toolchain when `govulncheck` reports a new stdlib advisory. Do not drop it.

---

## Open findings

Counts: **6 Critical, 0 High, 0 Medium** still open. Two correction rows (`X-01`, `X-02`) are not defects. (All High findings H-01 through H-12 are fully resolved).

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
| **P0 identity + auth** | C-01, C-02, C-03, H-09 | Same trust boundary. Empty key, default admin key, spoofable user, admin TLS/bind. Compare is M-05. |
| **P0 limiter API** | C-05 | Delete `idempotent_replay` while touching `cmd/limiter/main.go`. |
| **P1 sidecar admission** | C-04, H-12, H-02, H-11 | One `checkRateLimit` / `serveNormal` rewrite: per-request consume, detached ctx, denial-only cache, decode `callErr`. |
| **P1 idempotency** | C-06, H-05 | Complete/Fail policy + error handling. |
| **P1 proxy hygiene** | H-06, H-10 | Allowlist + body limits. |
| **P2 Redis topology** | H-07 | CB key TTLs. |
| **P2 CB recovery** | H-08 | Half-open grace in Lua **and** LocalStore. |

Do not start with remaining High items while C-01–C-06 are open. A Cluster refuse on hierarchical is not a ship gate.

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
| Audit | `internal/audit/lua/append.lua` |
| Token bucket clock | `lua/token_bucket.lua`, `redis_atomic_token_bucket.go` |
| Routing body | `cmd/sidecar/main.go` `readRequestBody` |

---

## Residual risk not in the register

The audit noted but did not ticket: audit-trail async shutdown under Redis unavailability (there are tests in `internal/audit/shutdown_test.go` — re-read before claiming a gap); Sentinel failover under write load; k6 automation not CI-gated. Do not confuse those with the IDs above.
