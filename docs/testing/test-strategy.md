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
| `lint` | `golangci-lint run ./...` | Static analysis beyond vet (`.golangci.yml`) |
| `vuln` | `govulncheck ./...` | Reachable CVEs in deps and stdlib |
| `test` | `go test -count=1 ./...` | Unit + handler tests |
| `race` | `go test -race ./...` | Data race detection |
| `redis-integration` | `go test -p 1` on all Lua-bearing packages | Real Redis (`REDIS_TEST_ADDR`) |
| `coverage` | `-coverprofile=coverage.out` | Artifact upload |
| `chaos` | `go test -tags=chaos ./chaos/...` | Fail-closed resilience contracts |

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
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
golangci-lint run ./...
govulncheck ./...

# Same suites against a real server instead of miniredis.
docker run -d --name drl-redis-test -p 6399:6379 redis:7-alpine
REDIS_TEST_ADDR=127.0.0.1:6399 go test -p 1 ./internal/limiter/... \
  ./internal/circuitbreaker/... ./internal/idempotency/... \
  ./internal/audit/... ./internal/routing/...
```

Test harnesses get their instance from `internal/redistest`: a real client when
`REDIS_TEST_ADDR` is set, miniredis otherwise. Tests that need to kill the
server mid-run call `SkipIfReal` and only run in the miniredis pass.

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
