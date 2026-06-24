# Failover Benchmarks

## Problem Statement

In Redis standalone mode, master death means total quota outage. For production I added a Redis Sentinel HA overlay (`docker-compose.ha.yml`) and benchmarked the failover drill: master kill during load, sentinel promotion (5 to 30s), go-redis `FailoverClient` reconnect, and old master rejoin as replica. This document ties `benchmarks/sentinel/summary.md`, `benchmarks/circuitbreaker/summary.md`, and chaos tests into one failover narrative.

## Why the problem exists

HA failover testing covers different failure modes:

- **Sentinel election**. Quorum 2/3 promotes a replica, not the same as `docker stop rate-redis` total death.
- **Client reconnect storm**. All sidecars and limiters reconnect at once. Latency spike post-failover.
- **Split-brain window**. Writes to old master during partition. Fencing tokens and audit matter.
- **Circuit breaker interaction**. Redis errors trip `cb:redis`. Half-open recovery after reconnect.

Without a drill, the "HA ready" claim in the README stays theoretical.

## Design goals

1. **Automated drill**. `k6 run benchmarks/load-test.js` plus `docker stop redis-master`.
2. **Observable promotion**. `docker logs redis-sentinel-1`, `/health` shows `redis.role`.
3. **Metric tracking**. `redis_failover_reconnects_total`.
4. **Recovery validation**. `docker start redis-master` becomes replica role.
5. **Distinguish chaos vs HA**. Total Redis kill (`chaos_test.ps1`) vs graceful failover.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Redis Cluster | Shard complexity; Sentinel sufficient for v1 |
| Active-active multi-master | Conflict resolution hard for Lua scripts |
| External managed Redis (ElastiCache) | Prod target; local Sentinel for dev drill |
| Manual failover only | Not repeatable |
| No HA, accept outage | Violates production hardening goals |

Sentinel overlay with `REDIS_MODE=sentinel` env on limiter and sidecar.

## Final architecture

**Start HA stack:**

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build -d
```

**HA env overrides** (`docker-compose.ha.yml`):

```
REDIS_MODE=sentinel
REDIS_MASTER_NAME=mymaster
REDIS_SENTINEL_ADDRS=redis-sentinel-1:26379,redis-sentinel-2:26379,redis-sentinel-3:26379
REDIS_PASSWORD=${REDIS_PASSWORD:-dev-redis-password}
```

**Failover drill** (`benchmarks/sentinel/summary.md`):

```bash
# 1. Baseline throughput
k6 run benchmarks/load-test.js

# 2. Kill master during load
docker stop redis-master

# 3. Observe Sentinel promotion (5-30s) and client reconnect
docker logs redis-sentinel-1 --tail 20
curl http://localhost:8080/health | jq .redis

# 4. Restart old master (becomes replica)
docker start redis-master
```

**Expected phases:**

| Phase | Behavior |
|-------|----------|
| Master healthy | `role=master`, writes succeed |
| Master down | Sentinels detect after `down-after-milliseconds` (~5s) |
| Election | Quorum 2/3 promotes replica |
| Client | FailoverClient discovers new master, reconnects |
| Recovery | Old master rejoins as replica |

**Circuit breaker during Redis stress** (`circuitbreaker/summary.md`):

| Benchmark | ops/sec | ns/op |
|-----------|---------|-------|
| BenchmarkCircuitAllow | ~8k | ~120µs |
| BenchmarkCircuitRecord | ~4k | ~250µs |
| BenchmarkCircuitAllowRecordParallel | ~6k | ~180µs |

State machine: Closed to Open (50%+ failure over min samples) to Half-Open after `CB_OPEN_COOLDOWN_MS` (30000) to Closed after `CB_HALF_OPEN_SUCCESS_REQUIRED` (2) successes.

**Chaos contrast** (`chaos/chaos_test.ps1`):

- Kills standalone `rate-redis`. **503 fail-closed**, no election.
- `network_partition.py`. Sidecar disconnected from network. 503 or timeout.

## Tradeoffs

- **5 to 30s unavailability window**. During election, all Redis ops fail and return 503s, not zero-downtime.
- **Write-on-primary only**. Lua scripts always hit master. Replica lag is irrelevant for reads.
- **FailoverClient reconnect burst**. Post-promotion latency spike lasts seconds.
- **Profile complexity**. `--profile ha` vs `standalone`. Easy to run the wrong stack.
- **Local drill is not cloud SLA**. Laptop Sentinel timing differs from managed Redis.

## Failure modes

1. **Quorum loss**. 2/3 sentinels down. No promotion. Extended outage.
2. **Split brain**. Misconfigured `sentinel.conf`. Old master accepts writes.
3. **Stale master address**. Client cache. go-redis refreshes via sentinel pub/sub.
4. **Flapping**. Master bounce causes repeated failovers. Tune `down-after-milliseconds`.
5. **Circuit flapping post-failover**. Half-open probes during unstable network. Raise `CB_OPEN_COOLDOWN_MS`.

## Operational concerns

- Health check: `curl http://localhost:8080/health`. Check `redis.role`, `redis.replication`.
- Metrics: `redis_failover_reconnects_total`.
- Admin circuit reset after drill: `curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/gateway-c`.
- Run Sentinel drill **before** release. Run `chaos_test.ps1` on standalone profile separately.
- Go benchmarks: `go test -bench=. -benchmem ./internal/redis/...` and `./internal/audit/...`.
- Document client retry: exponential backoff on 503 during failover window.

## Performance implications

**Baseline before failover** (`benchmarks/summary.md`):

| Target RPS | Actual RPS | p99 | Errors |
|------------|------------|-----|--------|
| 1,000 | 1,000 | 3.2 ms | 0% |

**During master down:** effective RPS drops to **0** for mutating and quota paths. k6 load test shows an error spike until reconnect.

**Post-recovery:** expect **30s latency elevation** while pools warm and circuits half-open probe. Watch p99 on `load-test.js` continuation.

Circuit fast-fail while open (~120µs `Allow` on miniredis) prevents thread pile-up on dead Redis. Better than hanging until TCP timeout.

Saturation knee (**5,000 target, 1,353 actual**, p99 **3.5 s**) is unrelated to failover but sets the load test ceiling for the drill. Do not run the failover drill at collapse load initially.

## Lessons learned

First drill: I stopped standalone `rate-redis` and wondered why Sentinel logs were empty. Wrong compose profile. Keeping `--profile ha` explicit was necessary.

Reconnect took **18s** on my machine. The README range of "5 to 30s" is honest. Do not promise sub-second.

`chaos_test.ps1` and the Sentinel drill are different stories. Customers ask about both. Conflating them confused our on-call runbook.

Adding `redis.role` to `/health` JSON moved drill verification from eyeballing toward automation.

Next: a k6 scenario that auto-asserts error rate and recovery time during failover.
