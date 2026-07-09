# Idempotency Architecture

Sidecar layer mutating requests (`POST`, `PUT`, `PATCH`, `DELETE` + `Idempotency-Key`) के लिए **distributed claim → execute → complete** flow। Guarantee: **duplicate suppression + cached replay + fencing** — **NOT exactly-once** upstream execution।

---

## State machine

```mermaid
stateDiagram-v2
  [*] --> Absent: first request
  Absent --> Processing: claim.lua (new)
  Processing --> Completed: complete.lua + valid fence
  Processing --> Failed: fail.lua + valid fence
  Processing --> Processing: duplicate in-flight (409)
  Processing --> Processing: lock expired → reclaim + new fence
  Completed --> Completed: replay cached response (200)
  Failed --> Failed: replay cached error response
  Absent --> HashMismatch: same key, different body hash (422)
  Completed --> [*]: TTL expiry
  Failed --> [*]: TTL expiry
```

### Redis record (`idem:{scope}:{key}`)

| Field | Purpose |
|-------|---------|
| `status` | `processing` \| `completed` \| `failed` |
| `request_hash` | Body fingerprint — mismatch → 422 |
| `fence_token` | UUID per claim/reclaim owner |
| `lock_until` | Processing lease (`IDEMPOTENCY_LOCK_TTL_MS`) |
| `http_status`, `resp_headers`, `resp_body` / `body_ref` | Cached response |

Large bodies: `idem:body:{scope}:{key}` STRING when > inline threshold (default 64 KiB)।

---

## Request flow

```mermaid
sequenceDiagram
  participant C as Client
  participant S as Sidecar
  participant R as Redis
  participant U as Upstream

  C->>S: POST + Idempotency-Key
  S->>R: claim.lua
  alt claimed (new)
    R-->>S: {1, fence_token}
    S->>U: forward request
    U-->>S: response
    S->>R: complete.lua (fence must match)
    S-->>C: 200 + X-Idempotency-Status: created
  else replay
    R-->>S: {2, status, headers, body}
    S-->>C: cached response (replayed)
  else in progress
    R-->>S: {3, retry_after_ms}
    S-->>C: 409 Conflict
  end
```

Scope = `SHA256(tenant|user)` prefix (32 hex chars) — cross-tenant key collision नहीं।

---

## Fencing tokens

1. Go `Claim()` पर fresh UUID generate।
2. `claim.lua` stores `fence_token` on new claim या expired-lock reclaim।
3. `complete.lua` / `fail.lua`:
   - `status == processing` required
   - `fence_token` must match ARGV
   - mismatch → `{0}` → Go `ErrStaleFence`
4. Stale holder (crashed worker, reclaimed lock) cannot complete old execution।

**Purpose:** reclaim के बाद पुराना completer silent success नहीं कर सकता।

---

## NOT exactly-once

| Scenario | Behavior |
|----------|----------|
| Happy path | One upstream call, one completion |
| Concurrent duplicates | 1× claim winner, N×409 in-progress |
| Crash **after** upstream, **before** `Complete` | Lock expires → reclaim → **second upstream possible** |
| Replay after complete | Cached response, zero upstream |

**Crash window:** `processing` lease TTL के दौरान owner dead + upstream already mutated → at-least-once upstream side effects possible। Documented limitation, not hidden。

---

## HTTP outcomes

| Code | Meaning |
|------|---------|
| 200 + `X-Idempotency-Status: created` | Fresh execution |
| 200 + `replayed` | Cached success |
| 409 + `in_progress` | Another holder active |
| 422 | Key reused with different request hash |
| 503 | Redis unavailable |

Key validation: `ValidateKey()` — empty / too long → error before Lua (k6 script issues)।

---

## Benchmark evidence

### k6 `idempotency-race` (`bench-progress.log`) — **invalid**

```
total=100  rps=3.3  200=10  errors=90 (422)
```

Script ने invalid idempotency key format भेजा — **422 validation failures**, architecture test नहीं।

### Runtime proof — **valid**

40 parallel POST, same GUID key, 2 sidecars:

| Result | Count |
|--------|-------|
| 200 (upstream executed) | **1** |
| 409 (in-progress / lost race) | **39** |

Evidence: `benchmarks/testing/concurrency-and-race-testing.md`, RUNTIME-PROVEN।

Unit: `TestClaimSingleWinnerUnderConcurrency` — 1 claim, 99 in_progress (TEST-PROVEN)।

---

## Circuit breaker coupling

Idempotency enabled → sidecar `cb:central-limiter` guard on limiter HTTP; optional per-gateway CB when routing on।

---

## Source references

| File | Role |
|------|------|
| `internal/idempotency/store.go` | Claim/Complete/Fail |
| `internal/idempotency/lua/*.lua` | Atomic semantics |
| `cmd/sidecar/main.go` | `serveIdempotent`, `forwardIdempotent` |
| `internal/idempotency/store.go` | Claim/Complete/Fail |
| `docs/limitations.md` | Explicit non-guarantees |
