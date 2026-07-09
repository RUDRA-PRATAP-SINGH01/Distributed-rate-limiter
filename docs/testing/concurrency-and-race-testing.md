# Concurrency and Race Testing

**Sources:** `.github/workflows/ci.yml` (`race` job), `cmd/limiter/concurrency_test.go`, `cmd/sidecar/concurrency_test.go`, `internal/audit/shutdown_test.go`

---

## Race detector (CI)

Job **`race`** runs on every push/PR to `main`:

```bash
go test -count=1 -race ./...
```

Timeout: 15 minutes. Catches data races in cache, audit workers, circuit state, and HTTP handlers.

---

## Limiter concurrency

`cmd/limiter/concurrency_test.go` — **`TestConcurrency_RateChecking`**:

- Capacity 50, 150 concurrent goroutines via barrier release.
- Same user `concur-user` hits `/check`.
- Asserts: allowed + denied + errors == total; allowed ≤ capacity; no unexpected status codes.

Validates Redis Lua atomicity under burst — no over-admission beyond bucket capacity.

---

## Sidecar concurrency

### Singleflight collapse

`cmd/sidecar/concurrency_test.go` — **`TestSidecar_SingleflightCollapse`**:

- 100 concurrent requests, same user, blocked limiter handler.
- **Exactly 1** limiter HTTP call (`limitFlight.Do`).
- All clients receive 200 or 429 — zero errors.

### Cache concurrency

`cmd/sidecar/cache_test.go` — parallel cache read/write under TTL.

---

## Audit shutdown races

`internal/audit/shutdown_test.go`:

| Test | Proves |
|------|--------|
| `TestShutdown_ConcurrentRecordNoPanic` | Record during shutdown — no panic |
| `TestShutdown_HighContentionRaceStress` | Many goroutines + shutdown |
| `TestShutdown_Idempotent` | Parallel Shutdown calls safe |
| `TestShutdown_RedisCloseOrdering` | Redis close after workers stop |

---

## Idempotency races

`benchmarks/scripts/idempotency-race.js` — 40 parallel requests across 2 sidecars → **1×200**, 39×409.

Unit tests: `internal/idempotency/store_test.go` for claim races with miniredis/Redis.

---

## Running locally

```bash
go test -race -count=1 ./cmd/limiter/ -run Concurrency
go test -race -count=1 ./cmd/sidecar/ -run Singleflight
go test -race -count=1 ./internal/audit/ -run Shutdown
```

Full race pass (slow):

```bash
go test -race -count=1 ./...
```

---

## Known non-goals

Race tests use in-process HTTP test servers — not multi-process Redis contention at production scale. Supplement with k6 multi-replica scripts for fleet behavior.
