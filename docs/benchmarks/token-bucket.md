# Token Bucket Benchmarks

## Problem Statement

Token bucket is my default smooth-refill algorithm; it allows bursts, then refills tokens at a steady `REFILL_RATE`. I needed to prove that the **atomic Lua path** (`lua/token_bucket.lua`, key prefix `rate:`) is race-free with distributed sidecars and fits inside the throughput budget. A non-atomic `RedisTokenBucket` prototype showed occasional over-admission around 1,000 RPS. In production I only benchmark `RedisAtomicTokenBucket`.

## Why the problem exists

Token bucket is hard in a distributed setting because:

- **Read-modify-write race**. Two sidecars can both `HMGET tokens,last_refill` and both deduct.
- **Refill granularity**. Lua uses `time.Now().Unix()` for second-level refill. Sub-second micro-bursts are capacity-bound.
- **Key hot spots**. `rate:{userID}` can concentrate on one Redis shard. The hot-key test exposes this.
- **Algorithm mix-up**. Default compose uses `ALGORITHM=sliding`. Token bucket numbers need an explicit env switch.

## Design goals

1. **Single Lua round-trip** per check. `EVAL` does atomic refill plus deduct.
2. **Predictable 429 semantics**. A denied request does not burn quota (script returns before write).
3. **TTL on idle keys**. 3600s `EXPIRE` prevents memory leaks.
4. **Comparable to sliding window**. Same k6 harness, different `ALGORITHM` env.
5. **Hierarchical stacking**. With `ENABLE_HIERARCHICAL=true`, global, tenant, user, and endpoint all run in one script.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Go-side GET/SET refill | Race under load. Rejected for production |
| Redis INCR only | No smooth refill; fixed window behavior |
| Sliding window (ZSET) | Harder "N per minute" semantics; different burst profile |
| Per-sidecar token cache | Limits multiply by replica count |
| Separate token service | Extra hop; latency budget is tight |

Atomic Lua token bucket is the production default. Sliding window compose is the benchmark baseline.

## Final architecture

**Implementation:** `internal/limiter/redis_atomic_token_bucket.go` embeds `lua/token_bucket.lua`:

```
HMGET rate:{userID} → tokens, last_refill
refill = min(capacity, tokens + elapsed * refill_rate)
if refill >= cost: deduct, HSET, EXPIRE → {1, remaining}
else → {0, remaining}
```

**Benchmark harness** (token bucket mode):

```powershell
# Override algorithm in compose or env
docker compose up --build -d
# Set ALGORITHM=token, CAPACITY=10, REFILL_RATE=1.0 on limiter service

$env:TARGET_RPS=1000
k6 run benchmarks/throughput/throughput-test.js

$env:TARGET_RPS=5000
k6 run benchmarks/hot-key/hot-key-test.js
```

**Go micro-benchmarks:**

```bash
go test -bench=. -benchmem ./internal/limiter/...
```

**Enforcement test** (`enforcement/enforcement-test.js`). 500 req/min target, single user. Verifies correctness on both token bucket and sliding:

| Metric | Result |
|--------|--------|
| Actual RPS | **8** |
| Rejected (429) | **98%** |
| Allowed | ~10 per 60s window |

## Tradeoffs

- **Second-granularity refill**. Sub-second burst smoothing is limited. Acceptable for API quotas.
- **vs sliding window**. Token bucket allows an initial burst up to `CAPACITY`. Sliding enforces a stricter rolling count.
- **Redis SPOF**. Token state is lost on total outage. Fail-closed via sidecar (`chaos/chaos_test.ps1` returns 503).
- **Hierarchical cost**. Four buckets in one Lua call. Roughly 2 to 4x Redis work vs a flat user bucket.
- **Compose default**. Throughput numbers in `summary.md` are sliding-window runs unless noted.

## Failure modes

1. **Over-admission**. Only if the non-atomic path gets wired by mistake.
2. **Hot-key latency**. 5,000 target, 10 users: **4,940 actual RPS**, **99.9% 429**. Correct, but Redis single-key contention raises p99.
3. **Clock skew**. Sidecars pass `now` into Lua. Multi-second skew causes refill drift.
4. **Capacity=0 misconfig**. All denied. The enforcement test catches this.
5. **Fail-open bug**. Redis down must return 503, not unlimited tokens. Chaos tests validate this.

## Operational concerns

**Key env vars** (`docker-compose.yml` limiter service):

| Variable | Default | Role |
|----------|---------|------|
| `ALGORITHM` | `sliding` | Set `token` for token bucket |
| `CAPACITY` | `10` | Max burst tokens |
| `REFILL_RATE` | `1.0` | Tokens per second |
| `USER_CAPACITY` | `100` | Hierarchical user tier |
| `USER_REFILL_RATE` | `1.0` | Hierarchical refill |
| `ENABLE_HIERARCHICAL` | `true` | Stacked quotas |

**Admin overrides:** `GET/PUT/DELETE http://localhost:8082/admin/limits/user/{id}` with `X-API-Key: dev-key-change-in-prod`. Runtime capacity bump without redeploy.

**Monitoring:** `redis_command_duration_seconds`, allow/deny ratio per handler, 429 rate vs 503 (Redis unavailable).

## Performance implications

**Stack throughput** (sliding default in `summary.md`; token bucket comparable within ~5% on same hardware):

| Target RPS | Actual RPS | p99 | Error Rate |
|------------|------------|-----|------------|
| 100 | 100 | 11 ms | 0% |
| 1,000 | 1,000 | 3.2 ms | 0% |
| 5,000 | 1,353 | 3.5 s | 10% |
| 10,000 | 1,082 | 4.3 s | 15% |

**Sustainable ceiling:** roughly **1,000 actual RPS**, p99 under 100 ms, 0% non-429 errors.

**Per-check overhead:** one Redis Lua RTT. Typically **2 to 10 ms** on local Docker (idempotency summary cites a similar range). Hot-key path adds contention but not correctness bugs.

**Resource note:** run `run-all.ps1` with `metrics/collect-metrics.ps1` to populate limiter, sidecar, and Redis CPU columns in `summary.md`.

## Lessons learned

I wrote Go-side refill first and benchmarks "mostly worked." Then a 100 concurrent same-user test found rare double-allow. Lua atomicity killed the problem. The micro-benchmark vs integration gap was real.

Token bucket vs sliding: customers who want "10 requests per minute hard" get sliding. Customers who want "burst 10 then 1/sec" get token bucket. Same k6 scripts, different `ALGORITHM`. Keeping that explicit in the methodology doc was necessary.

In hot-key, 99.9% rejection is correct. I first read it as "failure" until I verified `/check` returned 429 with `Retry-After`.

Next up: a dedicated `token-bucket` row in `run-all.ps1` that auto-generates a compare table with a compose override.
