# Continuous Integration

**Source:** `.github/workflows/ci.yml`

---

## Triggers

```yaml
on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]
```

**Concurrency:** one run per workflow + ref; `cancel-in-progress: true` — newer commits cancel stale runs.

**Permissions:** `contents: read` only.

---

## Jobs overview

| Job | Runner | Timeout | Steps |
|-----|--------|---------|-------|
| `static-build` | `ubuntu-latest` | 10 min | checkout, setup-go, mod download/verify, vet, build |
| `lint` | `ubuntu-latest` | 15 min | checkout, setup-go, `golangci-lint` v2 |
| `vuln` | `ubuntu-latest` | 10 min | checkout, setup-go, `govulncheck ./...` |
| `test` | `ubuntu-latest` | 10 min | checkout, setup-go, `go test -count=1 ./...` |
| `race` | `ubuntu-latest` | 15 min | checkout, setup-go, `go test -count=1 -race ./...` |
| `redis-integration` | `ubuntu-latest` | 15 min | checkout, setup-go, Redis service, all Lua-bearing packages |
| `coverage` | `ubuntu-latest` | 10 min | checkout, setup-go, coverprofile, upload artifact |
| `chaos` | `ubuntu-latest` | 30 min | checkout, setup-go, buildx, resilience contracts |

Jobs run **in parallel** (no inter-job dependencies in workflow file).

---

## Go toolchain

All jobs use:

```yaml
uses: actions/setup-go@v5
with:
  go-version-file: go.mod
  cache: true
```

`go.mod` carries a `toolchain` directive pinning a patched Go release. That
pin is load-bearing rather than cosmetic: the `vuln` job fails on any known CVE
reachable from our call graph, and most of those live in the standard library,
so the fix is a patched toolchain. Bump the directive when `govulncheck`
reports a new standard-library advisory.

---

## static-build

1. `go mod download`
2. `go mod verify`
3. `go vet ./...`
4. `go build ./...`

Ensures all packages (`cmd/limiter`, `cmd/sidecar`, `internal/...`) compile.

---

## test

```bash
go test -count=1 ./...
```

Full unit + handler test suite. `-count=1` disables cached pass results.

---

## race

```bash
go test -count=1 -race ./...
```

Race detector on entire module. Longer timeout (15 min) for slower execution.

---

## redis-integration

**Service container:**

```yaml
redis:
  image: redis:7-alpine
  ports: 6379:6379
  options: >-
    --entrypoint redis-server
    --health-cmd "redis-cli ping"
    --health-interval 5s
    --health-timeout 3s
    --health-retries 5
```

**Test command:**

```bash
go test -count=1 -p 1 -v \
  ./internal/limiter/... \
  ./internal/circuitbreaker/... \
  ./internal/idempotency/... \
  ./internal/audit/... \
  ./internal/routing/...
```

**Env:** `REDIS_TEST_ADDR=127.0.0.1:6379`

Every package that executes Lua against Redis runs a second time here. miniredis
interprets Lua on gopher-lua and keeps its own clock, so a script can pass
in-memory and still break against a real server — the double is not a substitute
for the thing.

Test harnesses obtain their instance from `internal/redistest`, which returns a
real client when `REDIS_TEST_ADDR` is set and miniredis otherwise. The suites
share one database, so `-p 1` keeps packages serial; each harness flushes on
setup. A few tests kill the server mid-run to force an outage — those are
miniredis-only and skip here with a logged reason, still covered by the
`test` and `race` jobs.

This does not exercise the full compose stack; the `chaos` job does that.

---

## coverage

```bash
go test -count=1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

**Artifact:** `coverage-report` uploads `coverage.out`, retention 14 days (`actions/upload-artifact@v4`).

---

## lint

```bash
golangci-lint run ./...
```

Configuration is `.golangci.yml`, which documents why each linter is enabled and
why each exclusion exists. Beyond the standard set it adds `bodyclose`, `noctx`,
`errorlint`, `gosec`, `nilerr`, `durationcheck`, `makezero`, `copyloopvar`,
`unconvert`, and `wastedassign`.

---

## vuln

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

Reports only vulnerabilities that are actually reachable from our code, so a
finding here is a real exposure rather than a dependency-graph warning. Fixes
are either a dependency bump or a `toolchain` bump in `go.mod`.

---

## chaos

Resilience contracts against a minimal compose stack. See `chaos/README.md`.

---

## Not in CI

| Check | Run locally / release |
|-------|----------------------|
| k6 benchmarks | `benchmarks/run-all.ps1` |
| Manual chaos demos | `chaos/chaos_test.ps1`, `chaos/network_partition.py` |
| Docker compose e2e | `docker compose up` |
| Sentinel HA drill | `docker-compose.ha.yml --profile ha` |

---

## Branch protection recommendation

Require passing: `static-build`, `lint`, `vuln`, `test`, `race`,
`redis-integration`, `chaos` before merge. Coverage artifact optional for review.
