# Production Hardening

## Problem Statement

Local `docker compose` defaults (`dev-key-change-in-prod`, `FAIL_OPEN=false` but `ALLOW_QUERY_USER_ID=true`) are not production-ready. I needed an explicit hardening checklist: secrets, network policy, HA, observability, chaos validation, benchmark gates. Deploy confidence should rest on both `benchmarks/summary.md` (**~1,000 RPS** tested) and `chaos/` (fail-closed proven).

## Why the problem exists

Sidecar rate limiters bring unique risks in production:

- Fail-open misconfig allows unlimited traffic during a Redis blip.
- Admin API exposure on `:8082` enables unauthenticated quota change if misconfigured.
- Query param `user_id` enables spoofing in prod.
- Redis SPOF when running standalone without Sentinel.
- Benchmarks alone do not prove 503 fail-closed behavior without chaos.
- Circuit and env drift when `CB_*` defaults are wrong for payment gateways.

## Design goals

1. Fail-closed everywhere: limiter, idempotency, circuit (`FAIL_OPEN=false`, `IDEMPOTENCY_FAIL_OPEN=false`).
2. Secret rotation for `REDIS_PASSWORD`, `ADMIN_API_KEY`, `INTERNAL_API_KEY`.
3. HA Redis via Sentinel overlay for production (`docker-compose.ha.yml`).
4. Observability on: `OTEL_ENABLED=true`, with Prometheus enablement planned.
5. Validated resilience via chaos, Sentinel drill, and benchmark gate.
6. Audit enabled: `ENABLE_AUDIT_TRAIL=true` with retention limits.

## Alternative approaches considered

| Control | Verdict |
|---------|---------|
| Fail-open for availability | Rejected. over-admission worse than 503 |
| No admin API in prod | Too rigid; secure with key + network policy |
| Client-side rate limit only | Doesn't scale horizontally |
| Skip benchmarks in CI | Regression undetected |
| Manual pen test only | Chaos scripts cheaper, repeatable |

I use defense in depth: config plus network plus HA plus automated validation.

## Final architecture

### Security hardening

| Setting | Dev default | Production |
|---------|-------------|------------|
| `ADMIN_API_KEY` | `dev-key-change-in-prod` | Strong random, secrets manager |
| `INTERNAL_API_KEY` | `dev-internal-key-change-in-prod` | Rotated, sidecar to limiter only |
| `REDIS_PASSWORD` | `dev-redis-password` | Strong, Redis ACLs |
| `ALLOW_QUERY_USER_ID` | `true` | **`false`**. use trusted header |
| `ENABLE_ADMIN_API` | `true` | `true` but bind internal network only |
| Admin port `:8082` | Exposed | Firewall / internal LB only |

### Resilience hardening

```
FAIL_OPEN=false
IDEMPOTENCY_FAIL_OPEN=false
REDIS_MODE=sentinel  # production HA
REDIS_SENTINEL_ADDRS=...
REDIS_MASTER_NAME=mymaster
```

**Chaos validation (pre-prod gate):**

```powershell
.\chaos\chaos_test.ps1          # expect 503 fail-closed, recovery
python chaos/network_partition.py  # expect 503/timeout, recovery
```

**Sentinel drill:**

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build -d
k6 run benchmarks/load-test.js
docker stop redis-master
# verify 5-30s recovery per sentinel/summary.md
docker start redis-master
```

### Circuit breaker tuning (`CB_*` env)

| Variable | Default | Production note |
|----------|---------|-----------------|
| `CB_FAILURE_RATE` | 0.5 | Lower for critical gateways |
| `CB_MIN_SAMPLES` | 10 | Prevent premature open |
| `CB_OPEN_COOLDOWN_MS` | 30000 | Avoid flap after failover |
| `CB_HALF_OPEN_SUCCESS_REQUIRED` | 2 | Gradual recovery |
| `CB_LATENCY_THRESHOLD_MS` | 500 | Trip slow gateways |

Admin reset: `DELETE /admin/circuit/{target}`

### Observability hardening

```
OTEL_ENABLED=true
OTEL_EXPORTER_OTLP_ENDPOINT=<collector>
ENABLE_AUDIT_TRAIL=true
AUDIT_RETENTION_HOURS=168
AUDIT_MAX_EVENTS=100000
```

Uncomment Prometheus and Grafana in `docker-compose.yml` for prod-like monitoring.

### Performance gate

```powershell
.\benchmarks\run-all.ps1
```

**Pass criteria** (`benchmarks/summary.md`):

| Target RPS | Actual RPS | p99 | Errors |
|------------|------------|-----|--------|
| 1,000 | 1,000 | 3.2 ms | 0% |

Do not deploy perf regressions past saturation knee without a capacity plan.

### Idempotency hardening

```
ENABLE_IDEMPOTENCY=true
IDEMPOTENCY_LOCK_TTL_MS=60000
IDEMPOTENCY_COMPLETED_TTL_MS=86400000
IDEMPOTENCY_FAIL_OPEN=false
```

**Validation:**

```bash
k6 run benchmarks/idempotency/idempotency-race.js   # 1 upstream
k6 run benchmarks/idempotency/idempotency-replay.js  # ~942 RPS, 0% errors
go test ./internal/idempotency/... -v
```

## Tradeoffs

Fail-closed trades availability: 503 during Redis outage means clients must retry with backoff. Sentinel adds ops overhead vs standalone. Audit storage lives in Redis memory; `AUDIT_MAX_EVENTS` cap is required. Stricter circuit breakers mean more false-positive opens on flaky networks. Benchmark on laptop is not prod; I use it for regression, not SLA.

## Failure modes

Fail-open leak: one replica with `FAIL_OPEN=true` gives partial unlimited traffic. Admin key in git is a risk when compose defaults are committed; I override via env files not in repo. Sentinel quorum loss when 2/3 sentinels are down means no failover. Idempotency TTL too short lets client retries after 24h duplicate execution. Circuit never closes when `CB_OPEN_COOLDOWN_MS` is too high plus persistent errors.

## Operational concerns

Pre-deploy checklist: secrets, `ALLOW_QUERY_USER_ID=false`, HA profile, OTEL endpoint. Post-deploy I run RB-7 from `runbooks.md`, health checks, and a sample admin audit query. I rotate keys quarterly; `INTERNAL_API_KEY` must update on sidecar and limiter simultaneously. I monitor Redis `used_memory` for audit, idempotency, and quota keys. I document 503 retry policy for API consumers, especially during Sentinel failover window. I keep `chaos/` and `benchmarks/` runnable in staging on every release candidate.

## Performance implications

Hardening overhead I measured:

- Idempotency claim: **9 to 15 ms** p95 under contention
- Audit append: ~**300µs** (miniredis); plus Redis RTT in prod
- Circuit allow: ~**120µs** miniredis
- OTEL tracing: ~1 to 2% at **1,000 RPS** (acceptable)

Sustainable throughput after hardening (all features on): **~1,000 actual RPS**, p99 **3.2 ms** on reference hardware.

Saturation unchanged: **5,000 target to 1,353 actual**, p99 **3.5 s**, **10% errors**. Capacity planning is required for higher load.

Idempotency replay **942 RPS**. Retry storm capacity is separate from the mutating path.

## Lessons learned

The name `dev-key-change-in-prod` saved a close call. An engineer paused before pasting into prod. Explicit scary defaults matter.

We ran HA without chaos first. Sentinel failover worked, but standalone `chaos_test.ps1` behavior was undocumented for mixed envs. Both gates are mandatory now.

`ALLOW_QUERY_USER_ID=true` failed a security audit; it is hard off in prod.

The benchmark gate caught a hierarchical quota change regression: p99 went **3.2 ms to 45 ms** from an audit verbosity bug.

Next up: a `production-hardening` script that validates env and auto-runs an RB-7 subset in CI.
