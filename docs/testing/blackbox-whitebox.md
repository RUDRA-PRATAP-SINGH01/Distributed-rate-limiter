# Black-box and White-box Testing

**Sources:** `internal/**/*_test.go`, `cmd/**/*_test.go`, `tests/smoke/`, `tests/sanity/`, `chaos/`, `benchmarks/`

Both styles already exist in this repo. This page names them so a PR can say which kind of evidence it added.

---

## White-box

Tester **knows internals**: Redis key names, Lua return codes, in-process caches, circuit state machines.

| Area | File examples | What the test is allowed to see |
|------|---------------|----------------------------------|
| Token / sliding Lua | `internal/limiter/*_test.go` | Key shape, remaining tokens, TTL |
| Hierarchical merge | `internal/limiter/hierarchical_test.go` | Level order, override apply |
| Circuit store | `internal/circuitbreaker/*_test.go` | State transitions, Redis keys |
| Idempotency claim | `internal/idempotency/store_test.go` | Fence tokens, TTL |
| Routing score | `internal/routing/scorer_test.go` | Formula inputs |
| URL allowlist | `internal/routing/urlguard_test.go` | Parsed host / scheme |
| Identity templates | `internal/identity/*_test.go` | Header → principal |

White-box tests may call unexported helpers in the same package (`package limiter`, not `package limiter_test`) and may assert Redis keys such as `rate:<user>`.

**Process smoke** is a *thin* white-box gate: it re-runs a few existing handler tests through `httptest` / miniredis without Docker.

```powershell
.\scripts\qa.ps1 process-smoke
```

---

## Black-box

Tester **only sees HTTP** (and health JSON). No Redis keys, no Lua, no sidecar cache internals.

| Suite | Location | Oracle |
|-------|----------|--------|
| Handler contracts | `cmd/limiter/*_test.go`, `cmd/sidecar/*_test.go` | Status, headers, body fields |
| Deploy smoke | `tests/smoke/` (`-tags=smoke`) | Process answers 200 / 429, not 5xx |
| Change sanity | `tests/sanity/` (`-tags=sanity`) | Auth 401, allow, deny + `Retry-After` |
| Chaos R1 | `chaos/` (`-tags=chaos`) | Redis down → 503, then recover |
| k6 load | `benchmarks/scripts/` | RPS, p99, allow/deny counts |

Black-box tests use production headers: `X-User-ID`, `X-API-Key` / `X-Internal-API-Key`. They must not depend on `?user_id=` (`ALLOW_QUERY_USER_ID=false` in compose).

```powershell
.\scripts\qa.ps1 smoke
.\scripts\qa.ps1 sanity
```

Shared client: `tests/qa/client.go` (same header rules as `chaos/client.go`).

---

## How to add a new test

| If you changed… | Prefer | Example assertion |
|-----------------|--------|-------------------|
| Lua script or key layout | White-box in `internal/...` | Remaining tokens, key exists |
| HTTP status / header contract | Black-box in `cmd/...` | 429 + `Retry-After` |
| "Does compose still boot?" | Smoke in `tests/smoke/` | `/health` status=healthy |
| "Did my PR break allow/deny?" | Sanity in `tests/sanity/` | Fresh user 200, burst 429 |
| Fail-closed behavior | Chaos contract | 503, no secret leak |

If a bug was found in an exploratory session, encode the **smallest** oracle: black-box if the customer can see it, white-box if only Redis/Lua can prove it.

---

## Grey-box (already used, do not over-name it)

Some `cmd/limiter` tests are grey-box: they hit HTTP **and** then `fixture.rdb.Exists(...)`. That is fine for proving the algorithm key, but do not put Redis key checks in `tests/smoke` or `tests/sanity` — those suites stay operator-runnable against any environment that only exposes HTTP.
