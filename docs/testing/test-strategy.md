# Test Strategy

**Sources:** `.github/workflows/ci.yml`, `go test` layout across repo, `benchmarks/`, `chaos/`

---

## Pyramid

```
                    ┌─────────────────┐
                    │ chaos / k6 e2e  │  Manual + benchmarks/
                    ├─────────────────┤
                    │ integration     │  Redis service in CI
                    ├─────────────────┤
                    │ cmd/* tests     │  HTTP handlers + sidecar
                    ├─────────────────┤
                    │ internal/* unit │  Lua stores, algorithms
                    └─────────────────┘
```

---

## CI jobs (see [continuous-integration.md](../ci/continuous-integration.md))

| Job | Command | Purpose |
|-----|---------|---------|
| `static-build` | `go vet`, `go build ./...` | Compile all binaries |
| `test` | `go test -count=1 ./...` | Unit + handler tests |
| `race` | `go test -race ./...` | Data race detection |
| `redis-integration` | `go test ./internal/limiter/...` | Real Redis (`REDIS_TEST_ADDR`) |
| `coverage` | `-coverprofile=coverage.out` | Artifact upload |

All jobs: Go version from `go.mod`, module cache enabled.

---

## Package layout

| Area | Test location | Focus |
|------|---------------|-------|
| Token/sliding algorithms | `internal/limiter/*_test.go` | Lua correctness |
| HTTP limiter | `cmd/limiter/*_test.go` | Handlers, admin, security |
| Sidecar proxy | `cmd/sidecar/*_test.go` | Cache, fail-open, CB |
| Circuit breaker | `internal/circuitbreaker/*_test.go` | State machine |
| Audit | `internal/audit/*_test.go` | Async, shutdown |
| Idempotency | `internal/idempotency/*_test.go` | Claim/replay |
| Redis client | `internal/redis/*_test.go` | Timeouts, health |
| Telemetry | `internal/telemetry/*_test.go` | Propagation, 503 span status |
| Logging | `internal/logging/logger_test.go` | Correlation attrs |

`-count=1` in CI disables test cache for reproducibility.

---

## Local commands

```bash
go test ./...
go test -race ./...
go test -v ./internal/limiter/...   # needs Redis on 6379 or REDIS_TEST_ADDR
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

---

## Beyond unit tests

| Suite | Location | Proves |
|-------|----------|--------|
| k6 benchmarks | `benchmarks/scripts/` | Throughput, p99, multi-replica |
| Chaos | `chaos/chaos_test.ps1` | Redis kill → 503 → recovery |
| Idempotency race | `benchmarks/scripts/idempotency-race.js` | Single upstream execution |

Document results in `docs/benchmarks/final-benchmark-report.md`.

---

## What CI does not run

- k6 load tests (manual / release gate)
- Docker compose e2e (local `docker compose up`)
- Sentinel failover drills (`docker-compose.ha.yml`)
- Chaos script (operator runbook)

Add these to pre-release checklist (`docs/operations/runbooks.md` RB-7).

---

## Related

- [concurrency-and-race-testing.md](concurrency-and-race-testing.md)
- [failure-testing.md](failure-testing.md)
- [multi-replica-testing.md](multi-replica-testing.md)
