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
| `test` | `ubuntu-latest` | 10 min | checkout, setup-go, `go test -count=1 ./...` |
| `race` | `ubuntu-latest` | 15 min | checkout, setup-go, `go test -count=1 -race ./...` |
| `redis-integration` | `ubuntu-latest` | 15 min | checkout, setup-go, Redis service, limiter integration tests |
| `coverage` | `ubuntu-latest` | 10 min | checkout, setup-go, coverprofile, upload artifact |

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
go test -count=1 -v ./internal/limiter/...
```

**Env:** `REDIS_TEST_ADDR=127.0.0.1:6379`

Only `internal/limiter` package runs against real Redis in CI — not full docker compose stack.

---

## coverage

```bash
go test -count=1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

**Artifact:** `coverage-report` uploads `coverage.out`, retention 14 days (`actions/upload-artifact@v4`).

---

## Not in CI

| Check | Run locally / release |
|-------|----------------------|
| k6 benchmarks | `benchmarks/run-all.ps1` |
| Chaos script | `chaos/chaos_test.ps1` |
| Docker compose e2e | `docker compose up` |
| Sentinel HA drill | `docker-compose.ha.yml --profile ha` |
| golangci-lint | Not configured in workflow |

---

## Branch protection recommendation

Require passing: `static-build`, `test`, `race`, `redis-integration` before merge. Coverage artifact optional for review.
