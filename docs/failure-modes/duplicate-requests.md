# Failure Mode: Duplicate Requests

**Status:** Documented  
**Severity:** High (financial correctness)  
**Components:** Sidecar idempotency middleware, `claim.lua`, client retries

---

## 1. Problem Statement

Clients send the same `Idempotency-Key` multiple times — retries after 409, network duplicates, or mobile SDK auto-retry. Without server-side deduplication, POST `/api/orders` executes multiple times. I needed exactly-once **upstream side effect** with many-safe **client retries**.

## 2. Why the problem exists

HTTP retries are idiomatic (Stripe, PayPal, etc.). Load balancers may deliver the same request twice. Concurrent duplicates from 100 threads (see `benchmarks/idempotency/idempotency-race.js`) race at the sidecar before any application mutex exists.

## 3. Design goals

- Atomic claim in `claim.lua` — one winner, others get `in_progress` or `replay`.
- Fingerprint mismatch detection — same key, different body → `ResultHashMismatch`.
- Cached replay returns stored `http_status`, headers, body without upstream call.
- Metrics per outcome: `idempotency_claims_total{result="claimed|replay|in_progress|hash_mismatch|error"}`.

## 4. Alternative approaches considered

| Approach | Gap |
|----------|-----|
| Rate limit only | Different keys or windows — no dedup |
| App DB unique index | Not fleet-wide at sidecar; slower |
| At-least-once messaging | Requires compensating logic downstream |

## 5. Final architecture

**Happy path (duplicate after completion):**

1. Client A POST with `Idempotency-Key: pay-001` → claim code `1` (claimed).
2. Upstream executes once → `complete.lua` sets `status=completed`.
3. Client B retry same key + same body → claim code `2` (replay) → cached 201, **no upstream**.

**Concurrent path (100 duplicates):**

1. One claim wins code `1`.
2. Others get code `3` (`in_progress`) → 409 + `Retry-After: {lock_until - now}`.
3. After completion, retries get code `2` (replay).

**Hash mismatch:**

- Same key, `{"amount":1000}` vs `{"amount":2000}` → code `0` → reject (conflict).

Scope isolation: `idem:{scope}:{key}` where `scope = SHA256(tenant|user)[:32]` — keys do not cross tenants.

Sidecar gate: only when `ENABLE_IDEMPOTENCY=true` and mutating method with non-empty header.

## 6. Tradeoffs

| Benefit | Risk |
|---------|------|
| Upstream executes once | Redis memory for cached bodies |
| 409 under contention | Clients must backoff |
| 24h replay window | Stale keys block intentional reuse |

## 7. Failure modes

| Failure | Mitigation |
|---------|------------|
| `IDEMPOTENCY_FAIL_OPEN=true` during Redis outage | Duplicates reach upstream — never prod |
| Lease too short + slow upstream | Second worker reclaims — see lease-expiration.md |
| `ErrStaleFence` on late complete | First completion wins; monitor logs |
| Body > `IDEMPOTENCY_MAX_BODY_BYTES` | 413 before claim |
| Missing idempotency header | Request bypasses dedup — normal proxy path |

## 8. Operational concerns

- Verify upstream execution count stays at 1 during race tests (`curl .../api/orders/count` in README).
- Alert on `idempotency_claims_total{result="hash_mismatch"}` — possible client bug or attack.
- `idempotency_completes_total` should track `claimed` over time.
- Env: `ENABLE_IDEMPOTENCY`, `IDEMPOTENCY_LOCK_TTL_MS`, `IDEMPOTENCY_COMPLETED_TTL_MS`, `IDEMPOTENCY_FAIL_OPEN`.

## 9. Performance implications

Replay path skips limiter upstream and gateway — fastest response in system. Contention path generates 409 storms — clients should exponential backoff using `Retry-After`. First claim still pays full rate limit + upstream cost.

## 10. Lessons learned

The 100-way race benchmark was my acceptance test for payment-grade claims. Before fencing tokens, a reclaimed lease plus late complete could corrupt state — duplicates looked "fixed" until delay injection. Now duplicates surface as replay or `ErrStaleFence`, not silent double billing. **Idempotency-Key is a contract** — document required headers for API consumers.

**References:** `internal/idempotency/store.go`, `internal/idempotency/lua/claim.lua`, `benchmarks/idempotency/`, `docs/decisions/why-idempotency.md`
