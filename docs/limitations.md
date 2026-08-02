# Known Limitations

Authoritative list of what this system **does not** guarantee.

---

## Scalability

| Limitation | Detail | Evidence |
|------------|--------|----------|
| Single Redis master | All quota, idempotency, CB, routing, audit indexes share one Redis process in default Compose | SOURCE-PROVEN |
| Throughput knee | ~870–872 actual RPS sustainable end-to-end on tested laptop (i9-14900HX, Docker) | BENCHMARK-PROVEN |
| Redis Cluster | Multi-key hierarchical Lua is unsupported on Redis Cluster (CROSSSLOT); refused fast at boot when `ENABLE_HIERARCHICAL=true`. Flat `/check` is Cluster-safe (audit multi-key index appends also require single-slot). | SOURCE-PROVEN |
| Linear multi-replica scale | Two replicas proven for correctness, not throughput linearity | RUNTIME-PROVEN |

---

## Idempotency

| Limitation | Detail | Evidence |
|------------|--------|----------|
| Not exactly-once | Duplicate upstream execution possible after Claim → upstream success → crash before Complete → lease expiry → reclaim | SOURCE-PROVEN |
| Not at-most-once side effects | Fencing prevents stale Complete/Fail, not duplicate execution | TEST-PROVEN |
| Lease sizing | `IDEMPOTENCY_LOCK_TTL_MS` must exceed p99 upstream + margin | DOCUMENTED LIMITATION |

---

## Overrides

| Limitation | Detail | Evidence |
|------------|--------|----------|
| Hierarchical path only | `/check` flat path ignores Redis overrides | SOURCE-PROVEN |
| Generation refresh failure | If `GET config:generation` fails, local cache may remain until TTL (`OVERRIDE_CACHE_TTL_MS`) | SOURCE-PROVEN |
| Not instant global | Cross-replica visibility on **next successful** `RefreshGeneration` after admin write | TEST + RUNTIME |

---

## Audit trail

| Limitation | Detail | Evidence |
|------------|--------|----------|
| Best-effort async | Queue full or shutdown → events dropped (`audit_dropped_total`) | SOURCE + TEST |
| Not guaranteed delivery | No durable outbox to external SIEM | SOURCE-PROVEN |

---

## Observability

| Limitation | Detail | Evidence |
|------------|--------|----------|
| `/health` and `/metrics` not traced | `SkipHealthMetrics` filter | SOURCE-PROVEN |
| Admin API not for metrics polling | Redis scan on audit search | SOURCE-PROVEN |

---

## Benchmarks & soak

| Limitation | Detail | Evidence |
|------------|--------|----------|
| Localhost Docker only | Windows 11 + Docker Desktop; not cloud production | BENCHMARK-PROVEN |
| 15 min soak | No months-long stability proof | BENCHMARK-PROVEN |
| k6 arrival-rate ceiling | Several 1000-target workloads cluster ~869–872 actual RPS — likely executor/backpressure, not hard Redis cap at exactly 872 | DOCUMENTED UNCERTAINTY |

---

## Deployment defaults

| Limitation | Detail | Evidence |
|------------|--------|----------|
| Dev secrets in Compose | `dev-key-change-in-prod` etc. | SOURCE-PROVEN |
| `ALLOW_QUERY_USER_ID=true` in Compose | Convenient for dev; prod should trust headers only | SOURCE-PROVEN |
| HA is opt-in | `docker-compose.ha.yml` profile `ha` — default is standalone Redis | SOURCE-PROVEN |

---

## Explicitly unsupported

- Exactly-once / at-most-once **upstream side effects**
- Redis Cluster for hierarchical quota without redesign
- Guaranteed audit durability
- Universal "1000 RPS" on arbitrary hardware without re-benchmarking
