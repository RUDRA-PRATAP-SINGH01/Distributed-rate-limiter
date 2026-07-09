# Multi-Replica Testing

**Sources:** `docker-compose.scale.yml`, `benchmarks/scripts/multi-replica-e2e.js`, `docs/README.md`, `cmd/sidecar/concurrency_test.go`

---

## Goal

Prove that multiple limiter and sidecar replicas sharing one Redis enforce **global** quota — not per-process counters.

---

## Docker scale profile

```bash
docker compose -f docker-compose.yml -f docker-compose.scale.yml --profile scale up --build
```

| Instance | Host URL |
|----------|----------|
| Sidecar A | `http://localhost:9090` |
| Sidecar B | `http://localhost:9092` (9091 = Prometheus) |
| Limiter A | `http://localhost:8080` |
| Limiter B | `http://localhost:8083` |

Both sidecars default to `RATE_LIMITER_URL=http://limiter:8080` — tests hit shared central limiter unless reconfigured.

---

## k6 multi-replica script

`benchmarks/scripts/multi-replica-e2e.js`:

- Targets both sidecar ports.
- Shared user/tenant against capacity **10**.
- **Verified:** 60 concurrent → **10 allowed / 50 denied** (`docs/README.md`, RUNTIME-PROVEN).

Run:

```bash
k6 run benchmarks/scripts/multi-replica-e2e.js
```

---

## Idempotency across replicas

`benchmarks/scripts/idempotency-race.js` with 2 sidecars:

- 40 parallel mutating requests with same idempotency key.
- **1×200**, 39×409 in_progress or replay.

Requires `ENABLE_IDEMPOTENCY=true` and shared Redis on both sidecars.

---

## Unit-level replica semantics

| Test | File | Invariant |
|------|------|-----------|
| Singleflight | `cmd/sidecar/concurrency_test.go` | 1 limiter call per cache key burst |
| Override restart | `internal/override/override_test.go` | New limiter process reads Redis overrides |
| Hierarchical | `internal/limiter/hierarchical_test.go` | Multi-level keys in Redis |

---

## What to watch

| Metric / signal | Healthy multi-replica |
|-----------------|----------------------|
| Denied count at cap | ~`(requests - capacity)` |
| `rate_limiter_requests_total` | Sums across limiter pods |
| Per-sidecar cache | Independent — denials may cache locally |
| Redis key contention | p99 rises before RPS ceiling |

---

## Failure scenarios

- **Split-brain Redis** — not supported; single primary required.
- **Sidecar A cache, Sidecar B miss** — allowed requests always re-check; denials may differ briefly until TTL expires (`CACHE_TTL_MS`, default 30ms).
- **Limiter B unused** — scale test still valid if all traffic via limiter A + Redis.

---

## CI gap

Multi-replica k6 is **not** in `.github/workflows/ci.yml`. Run before release (RB-7) or after changing Lua keys, hierarchical logic, or sidecar cache.

---

## Related

- [operations/scaling.md](../operations/scaling.md)
- `docs/correctness/multi-replica-correctness.md`
