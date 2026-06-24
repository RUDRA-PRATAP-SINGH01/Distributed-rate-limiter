# Debugging

## Problem Statement

When the sidecar returns 503, or a client complains about rate limiting, or idempotency reports a duplicate charge, I need a systematic debug path. This doc is my troubleshooting journal: which endpoints, admin APIs, k6 reproductions, and chaos scripts I run, and in what order. Benchmark numbers give context (for example, 429 at 99.9% hot-key is correct behavior, not a bug).

## Why the problem exists

Distributed rate limiter bugs hide across layers:

- Sidecar vs limiter vs Redis vs upstream
- 429 (quota) vs 503 (dependency) vs 409 (idempotency in_progress)
- Routing failover vs circuit open vs gateway real 5xx
- Sentinel reconnect vs total Redis death
- k6 "failures" that are expected 409s

Without a playbook, every incident starts in Redis CLI.

## Design goals

1. Layer isolation via health checks per component.
2. Minimal reproduction with a single k6 script per scenario.
3. Admin API first for runtime state without redeploy.
4. Trace correlation using Jaeger trace ID from response headers or logs.
5. Chaos validation to confirm fail-closed behavior vs a config bug.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| tcpdump first | Too low-level for app bugs |
| Only unit tests | Miss compose networking |
| Production replay | Risky; local compose reproduce |
| Random config changes | Makes incidents worse |
| Skip benchmarks | Lose baseline numbers |

I debug in layers: health, then admin, then traces, then targeted k6, then chaos.

## Final architecture

**Step 0. Symptom map:**

| Symptom | Likely layer | First check |
|---------|--------------|-------------|
| 429 + Retry-After | Quota OK | `admin/limits`, user_id |
| 503 | Redis/limiter down | `/health`, `chaos_test.ps1` history |
| 409 + X-Idempotency-Status | Idempotency | `admin/idempotency/{scope}/{key}` |
| 502/504 | Upstream/gateway | routing admin, circuit state |
| High latency, low errors | Saturation | compare to **1,353 actual RPS** knee |

**Step 1. Health:**

```bash
curl -s http://localhost:9090/health | jq .
curl -s http://localhost:8080/health | jq .
docker ps --filter name=rate-
```

**Step 2. Admin state** (`X-API-Key: dev-key-change-in-prod`):

```bash
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/limits/user/USER_ID
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/routing/gateways
curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/idempotency/user/KEY
curl -H "X-API-Key: $ADMIN_API_KEY" "http://localhost:8082/admin/audit?decision=denied&limit=10"
```

**Step 3. Reproduce with k6:**

```bash
# Throughput baseline
k6 run -e TARGET_RPS=1000 benchmarks/throughput/throughput-test.js

# Hot-key correctness (expect 99.9% 429)
k6 run benchmarks/hot-key/hot-key-test.js

# Idempotency duplicate check
k6 run benchmarks/idempotency/idempotency-race.js
curl http://localhost:8081/api/orders/count  # expect 1

# Routing
k6 run benchmarks/routing/routing-test.js

# Circuit
k6 run benchmarks/circuitbreaker/circuit-test.js
```

**Step 4. Traces:** Jaeger at `http://localhost:16686`, service `rate-sidecar`, search 503 spans.

**Step 5. Chaos confirm:**

```powershell
.\chaos\chaos_test.ps1
python chaos/network_partition.py
```

**Step 6. HA drill** (if Sentinel):

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up -d
k6 run benchmarks/load-test.js &
docker stop redis-master
curl http://localhost:8080/health | jq .redis
```

## Tradeoffs

Local reproduce is not prod, but the code paths are directionally the same. I do not log prod admin keys when debugging with dev keys. k6 counts 409 as failure during idempotency race; I check `checks` pass rate instead of the failure column. `DEBUG=false` is the default; I enable `DEBUG=true` on the sidecar only briefly for verbose logs. I prefer admin APIs over Redis CLI; CLI is for script debugging only.

## Failure modes

Misdiagnosed saturation is common: latency **3.5 s** at **1,353 actual RPS** is capacity, not a Redis bug. Wrong `user_id` happens when `ALLOW_QUERY_USER_ID=true` in dev but prod should trust headers only. Stale override cache: `OVERRIDE_CACHE_TTL_MS=5000`, so I wait 5s after admin PUT. Circuit may need manual reset via `DELETE /admin/circuit/gateway-c` after a drill. Port conflict causes false 503 when compose is not running and a local go process holds the port.

## Operational concerns

I capture health JSON, admin circuit and routing snapshot, last 100 sidecar logs, and trace ID. For idempotency incidents I never delete a key without checking upstream count. I document `INTERNAL_API_KEY` mismatch: sidecar and limiter auth failures show as 401/403. For isolated bugs I run `go test ./internal/idempotency/... -v -run TestName`. After a fix I run a `benchmarks/run-all.ps1` subset to confirm no regression from the **1,000 RPS** baseline.

## Performance implications

Debug traffic should stay below **1,000 RPS** sustained unless I am intentionally stress testing.

Single-request debug: limiter check is ~**3.2 ms** p99 at sustainable load; above the knee latency explodes. I do not profile bugs at **5,000 target RPS** initially.

Idempotency claim debug: expect **9 to 15 ms** claim latency under contention; that is normal.

Circuit open fast-fail is **~120µs** `Allow`. Errors should be low-latency 503/502, not 30s hangs; if I see hangs I check timeout config.

## Lessons learned

My most expensive bug was `FAIL_OPEN` accidentally true on one sidecar replica. About 25% of traffic went unlimited. A drop in admin audit `decision=denied` rate gave me the hint.

An idempotency race showed upstream count = 2. The client was retrying with different keys. `idempotency-race.js` separated that from a real Lua bug.

`network_partition.py` showed a bug that `chaos_test.ps1` did not: process healthy, network blackholed.

I now cite a benchmark baseline row in every postmortem. "Was p99 3ms or 3.5s?" is an objective question.

Next up: a debug CLI at `cmd/debug` that dumps health plus admin snapshot as one JSON file.
