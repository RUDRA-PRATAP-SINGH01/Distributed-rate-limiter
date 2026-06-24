# Idempotency Architecture

> इंजीनियरिंग जर्नल — मैंने sidecar में distributed idempotency layer क्यों और कैसे बनाया।

## Problem Statement

मुझे POST/PUT/PATCH जैसे mutating HTTP requests के लिए **exactly-once client semantics** चाहिए थीं — client retry करे तो upstream दोबारा execute न हो, बल्कि पहली बार store हुआ response replay हो। साथ ही same `Idempotency-Key` को अलग payload के साथ reuse करने पर reject करना था, और slow/crashed worker के बाद lock reclaim हो सके।

## Why the problem exists

Distributed rate limiter के साथ sidecar pattern में client → sidecar → central limiter → upstream chain है। Network timeout, 502, या client library की automatic retry policy के कारण **duplicate side effects** आम हैं — payment, inventory, या कोई भी non-idempotent upstream call दो बार चल सकती है। Pure in-memory dedup fleet-wide काम नहीं करता; हर sidecar instance अलग state रखता है। Redis atomic scripts के बिना claim और complete के बीच race हो जाती है।

## Design goals

1. **Atomic claim** — एक Lua script में new / replay / in-progress / hash-mismatch / expired-reclaim सब handle हो।
2. **Request fingerprinting** — method + path + sorted query + body का SHA-256 hash; same key, different body = `422 Unprocessable Entity`।
3. **Tenant-scoped keys** — `BuildScope(tenantID, userID)` से cross-tenant collision न हो।
4. **Fencing tokens** — reclaim के बाद stale worker पुराने token से complete/fail न कर सके।
5. **Sidecar integration** — rate limit check के बाद upstream forward; response capture करके Redis में store।
6. **Fail-closed by default** — Redis down हो तो `503`; optional `IDEMPOTENCY_FAIL_OPEN=true`।

## Alternative approaches considered

| Approach | क्यों reject किया |
|----------|-------------------|
| Database unique constraint on key | Latency और schema coupling; response body storage awkward |
| Kafka / event log dedup | Overkill for HTTP replay; operational burden |
| Optimistic locking without fence token | Reclaim के बाद दो workers दोनों complete कर सकते थे |
| Client-only idempotency | Malicious या buggy clients bypass कर सकते हैं |
| Separate idempotency microservice | Extra hop; sidecar already Redis से जुड़ा है |

## Final architecture

### Redis key layout

```
idem:{scope}:{key}           → HASH (metadata: status, request_hash, fence_token, …)
idem:body:{scope}:{key}      → STRING (large response bodies, >64 KB)
```

`scope` = first 16 bytes of `SHA256(tenantID + "|" + userID)` as hex (32 chars).

### Lua lifecycle

**`claim.lua`** — atomic entry point:

| Return code | Meaning |
|-------------|---------|
| `{1, fence_token}` | Claimed (new or reclaimed after lock expiry) |
| `{2, status, headers, body}` | Replay cached completed/failed response |
| `{3, retry_after_ms}` | Another holder processing; client gets `409 Conflict` |
| `{0}` | Hash mismatch |

**`complete.lua`** — `processing` + matching `fence_token` हो तभी `completed` set करता है; body ≤64 KB inline HASH में, बड़ा body अलग STRING key में।

**`fail.lua`** — transient upstream errors के लिए `failed` state; retryable, replay पर same error response मिलता है।

### Fingerprint (`internal/idempotency/fingerprint.go`)

```text
SHA256(UPPER(method) + "\n" + path + "\n" + sortedQuery(rawQuery) + "\n" + body)
```

Mutating methods: POST, PUT, PATCH only.

### Fencing flow

1. `Claim` पर `uuid.New().String()` fence token generate होता है।
2. Lock expire होने पर reclaim नया fence token लिखता है।
3. `Complete` / `Fail` पर token match नहीं → `ErrStaleFence` (silent no-op in Lua returns `{0}`).

### Sidecar integration (`cmd/sidecar/main.go`)

```
Client → Sidecar.ServeHTTP
  ├─ Idempotency-Key absent → serveNormal
  └─ Idempotency-Key + mutating method → serveIdempotent
        ├─ ReadBody + Fingerprint + BuildScope
        ├─ Claim
        ├─ replay / in_progress / hash_mismatch → immediate response
        ├─ claimed → checkRateLimit → forwardIdempotent
        │     ├─ ResponseCapturer wraps ResponseWriter
        │     ├─ router.Forward OR reverse proxy
        │     └─ Complete with captured status/headers/body
        └─ limiter error → Fail or fail-open path
```

`ENABLE_IDEMPOTENCY=true` पर sidecar Redis connect करता है; `X-Idempotency-Status` header: `created`, `replayed`, `in_progress`, `hash_mismatch`।

Replay headers whitelist: `content-type`, `x-request-id`, `x-correlation-id`।

### Config defaults (`idempotency.DefaultConfig`)

- Lock TTL: 60s
- Completed TTL: 24h
- Max body: 1 MB
- Inline threshold: 64 KB

## Tradeoffs

- **Redis dependency** — idempotency और rate limiting दोनों same Redis; outage पर sidecar fail-closed (unless fail-open)।
- **24h retention** — Stripe-style; long-running workflows के लिए TTL extend करना पड़ सकता है।
- **Failed state replay** — client को same error मिलता है; intentional retry के लिए नया key चाहिए या TTL wait।
- **ResponseCapturer** — streaming / SSE responses buffer में जाते हैं; very large responses `MaxBodyBytes` से reject।

## Failure modes

| Scenario | Behavior |
|----------|----------|
| Redis unavailable | `503 Idempotency store unavailable` (or fail-open bypass) |
| Worker crash after claim | Lock expires → reclaim → new worker proceeds |
| Stale worker completes after reclaim | `complete.lua` returns `{0}`; client may retry |
| Hash mismatch | `422` — client bug or key reuse abuse |
| Body too large | `413 Request Entity Too Large` before claim |
| Rate limit denial on claimed request | `429` stored via Complete — replay भी 429 देगा |

## Operational concerns

- Admin API: `cmd/limiter/admin_idempotency.go` — scope/key से record inspect/delete।
- Metrics: `idempotency_claims_total{result}`, `idempotency_completes_total`, `idempotency_redis_duration_seconds`।
- Traces: spans `idempotency.claim`, `idempotency.complete`, `idempotency.fail`, `sidecar.idempotency`।
- Env: `IDEMPOTENCY_LOCK_TTL_MS`, `IDEMPOTENCY_COMPLETED_TTL_MS`, `IDEMPOTENCY_MAX_BODY_BYTES`, `IDEMPOTENCY_FAIL_OPEN`।
- Monitor `hash_mismatch` और `in_progress` spike — client misconfiguration या thundering herd।

## Performance implications

- हर mutating idempotent request पर **कम से कम एक Redis round-trip** (claim); success path पर complete भी।
- Replay path upstream skip करता है — significant win on retries।
- Lua scripts single-key atomic; large bodies external STRING से HASH size bounded रहता है।
- `ReadBody` request body को memory में hold करता है — `MaxBodyBytes` cap जरूरी है।

## Lessons learned

मैंने सीखा कि **fencing token के बिना lock reclaim dangerous है** — expired lease पर दो workers race कर सकते हैं। Fingerprint में sorted query string include करना जरूरी था; बिना इसके `?a=1&b=2` vs `?b=2&a=1` अलग requests मानी जातीं। Sidecar में rate-limit denial को भी complete करना controversial लगा, पर Stripe model follow किया: replay पर consistent 429 better than double-charging upstream। `ResponseCapturer` का `Hijack`/`Flush` support रखा ताकि websocket paths crash न करें, भले idempotency उन पर apply न हो।
