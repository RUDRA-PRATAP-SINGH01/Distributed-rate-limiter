# Test Strategy

**Sources:** `.github/workflows/ci.yml`, `scripts/qa.ps1`, `go test` layout across repo, `benchmarks/`, `chaos/`

---

## Pyramid

```
                    ┌─────────────────┐
                    │ exploratory     │  Session charters (manual)
                    ├─────────────────┤
                    │ chaos / k6 e2e  │  Manual + benchmarks/
                    ├─────────────────┤
                    │ live smoke/sanity│  tests/smoke + tests/sanity
                    ├─────────────────┤
                    │ integration     │  Redis service in CI
                    ├─────────────────┤
                    │ cmd/* tests     │  HTTP handlers + sidecar
                    ├─────────────────┤
                    │ internal/* unit │  Lua stores, algorithms
                    └─────────────────┘
```

Quality process (gates, owners, severity): [quality-management.md](quality-management.md).
Black-box vs white-box map: [blackbox-whitebox.md](blackbox-whitebox.md).
Exploratory charters: [exploratory-charters.md](exploratory-charters.md).

**Runner (test automation framework):** `scripts/qa.ps1` / `scripts/qa.sh`.

---

## CI jobs (see [continuous-integration.md](../ci/continuous-integration.md))

| Job | Command | Purpose |
|-----|---------|---------|
| `static-build` | `go vet`, `go build ./...` | Compile all binaries |
| `lint` | `golangci-lint run ./...` | Static analysis beyond vet (`.golangci.yml`) |
| `vuln` | `govulncheck ./...` | Reachable CVEs in deps and stdlib |
| `process-smoke` | `-run` health + `/check` + sidecar health | Fast critical-path smoke |
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

```powershell
.\scripts\qa.ps1 quality-gate     # vet + process-smoke + unit
.\scripts\qa.ps1 process-smoke    # no Docker
.\scripts\qa.ps1 smoke            # live compose /health + /check
.\scripts\qa.ps1 sanity -Changed  # live happy path + touched packages
.\scripts\qa.ps1 exploratory      # print session charters
```

```bash
./scripts/qa.sh quality-gate
go test ./...
go test -race ./...
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
golangci-lint run ./...
govulncheck ./...

# Same suites against a real server instead of miniredis.
docker run -d --name drl-redis-test -p 6399:6379 redis:7-alpine
REDIS_TEST_ADDR=127.0.0.1:6399 ./scripts/qa.sh integration
```

Test harnesses get their instance from `internal/redistest`: a real client when
`REDIS_TEST_ADDR` is set, miniredis otherwise. Tests that need to kill the
server mid-run call `SkipIfReal` and only run in the miniredis pass.

---

## Beyond unit tests

| Suite | Location | Proves |
|-------|----------|--------|
| Process smoke | `scripts/qa.ps1 process-smoke` | Critical handlers still compile-and-pass |
| Deploy smoke | `tests/smoke/` (`-tags=smoke`) | Compose stack answers HTTP |
| Sanity | `tests/sanity/` (`-tags=sanity`) | Auth, allow, deny after a change |
| Exploratory | [exploratory-charters.md](exploratory-charters.md) | Unknown failures around known paths |
| k6 benchmarks | `benchmarks/scripts/` | Throughput, p99, multi-replica |
| Chaos | `chaos/chaos_test.ps1` | Redis kill → 503 → recovery |
| Idempotency race | `benchmarks/scripts/idempotency-race.js` | Single upstream execution |

Document results in `docs/benchmarks/final-benchmark-report.md`.

---

## What CI does not run

- Live smoke / sanity (`-tags=smoke` / `-tags=sanity`) — need a running stack
- Exploratory sessions (manual charters)
- k6 load tests (manual / release gate)
- Docker compose e2e (local `docker compose up`)
- Sentinel failover drills (`docker-compose.ha.yml`)
- Chaos script (operator runbook)

Add live smoke + sanity + these to the pre-release checklist (`docs/operations/runbooks.md` RB-7).

---

## Related

- [quality-management.md](quality-management.md)
- [blackbox-whitebox.md](blackbox-whitebox.md)
- [exploratory-charters.md](exploratory-charters.md)
- [concurrency-and-race-testing.md](concurrency-and-race-testing.md)
- [failure-testing.md](failure-testing.md)
- [multi-replica-testing.md](multi-replica-testing.md)
