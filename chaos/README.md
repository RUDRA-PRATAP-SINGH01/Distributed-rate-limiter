# Chaos / Resilience Contracts (L-01)

CI-gated **behavior contracts** for production resilience. These are the
authoritative proof behind any "chaos-tested" claim — not the manual scripts.

## Contracts

| ID | Contract | Assertion |
|----|----------|-----------|
| **R1** | Fail-closed on Redis loss | HTTP **503**, no secret/backend leak, recovery after Redis returns |
| **R3** | (covered by unit tests) Local Redis circuit trips & fail-fast | See `cmd/limiter` + `internal/circuitbreaker` LocalStore tests |

Requests use production-shaped headers (`X-User-ID`, `X-Internal-API-Key`) —
never `?user_id=` — so contracts survive auth hardening.

## Run in CI / locally

```bash
# Starts minimal stack (redis + limiter + demo + sidecar), runs R1, tears down.
go test -count=1 -tags=chaos -timeout 20m ./chaos/...
```

Or drive Compose yourself:

```bash
docker compose -f docker-compose.chaos.yml -p rate-chaos up -d --build
export CHAOS_EXTERNAL_STACK=true
export CHAOS_LIMITER_URL=http://127.0.0.1:8080
export CHAOS_SIDECAR_URL=http://127.0.0.1:9090
export INTERNAL_API_KEY=dev-internal-key-change-in-prod
go test -count=1 -tags=chaos -timeout 10m ./chaos/...
docker compose -f docker-compose.chaos.yml -p rate-chaos down -v
```

Default `go test ./...` **skips** these tests (build tag `chaos`) so unit CI stays fast.

## Adding scenarios (do not redesign)

1. Add `chaos/<name>_test.go` with `//go:build chaos`.
2. Reuse `Client` / `Harness` — send headers via `client.go`.
3. Assert HTTP contracts only (status + safe body), not log lines.
4. Wire nothing extra unless the new contract needs another fault injector.

Examples for upcoming work:

- Auth mandatory → unauthenticated `/check` returns 401
- Fail-open matrix → `FAIL_OPEN=true` allows traffic when Redis is down
- Idempotency → Redis blip must not poison keys for 24h

## Manual demos (not CI proof)

| Script | Role |
|--------|------|
| `chaos_test.ps1` | Local Windows demo of R1 (uses headers) |
| `network_partition.py` | Manual partition experiment |
| `high_latency.py` | Linux/`tc` only — experimental, not a PR gate |

If CI `chaos` is green on `main`, you may say the product is **chaos-tested (R1)**.
