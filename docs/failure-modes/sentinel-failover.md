# Failure Mode: Sentinel Failover

**Status:** Documented  
**Severity:** High (transient write unavailability)  
**Components:** `internal/redis`, Sentinel quorum, all Redis consumers

---

## Problem Statement

When the Redis master process crashes or loses network, clients must discover the promoted replica and resume writes. During the election window, Lua scripts fail and circuits open. I needed predictable behavior across limiter and sidecar `FailoverClient` instances without manual DNS updates.

## Why the problem exists

Sentinel failover is not instantaneous. Sentinels agree on subjective downtime (`down-after-milliseconds`), elect a leader, choose the best replica, and execute `SLAVEOF NO ONE`. go-redis clients poll Sentinels for the new master address. Any in-flight `EVAL` from the old master connection errors mid-request.

## Design goals

- Single config switch: `REDIS_MODE=sentinel` + `REDIS_SENTINEL_ADDRS`.
- Automatic client reconnect: Via `redis.NewFailoverClient` (`internal/redis/client.go`).
- Observable recovery: `redis_failover_reconnects_total` counter in `internal/metrics/metrics.go`.
- Health endpoint truth: Limiter `/health` reports `mode: sentinel`, `role`, `replication` string.
- No split code paths: In limiter vs sidecar. both call `redisclient.LoadConfigFromEnv()`.

## Alternative approaches considered

| Alternative | When to use instead |
|-------------|---------------------|
| **Managed Redis failover** | Production cloud. point `REDIS_ADDR` at provider endpoint |
| **Manual `SENTINEL failover`** | Emergency ops when auto-election stuck |
| **Keep serving during failover with stale data** | Rejected. violates enforcement correctness |

## Final architecture

Failover timeline (typical):

| Phase | Duration | System behavior |
|-------|----------|-----------------|
| Master down detection | ~5s | Existing connections error |
| Sentinel election | 1 to 10s | All writes fail |
| Replica promotion | seconds | New master accepts writes |
| Client reconnect | 1 to 2s | `Ping` succeeds; metric increment |
| Old master rejoin | variable | Becomes replica |

HA stack: `docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up`.

Sidecar startup (`connectSidecarRedis`): fatals if Sentinel addrs missing when mode is sentinel. misconfig caught early.

Limiter startup: `Ping` on boot; does not dynamically re-Ping on failover but FailoverClient handles topology.

## Tradeoffs

| Benefit | Cost |
|---------|------|
| Automatic promotion | Brief write blackout |
| No app-level failover logic | Sentinel ops burden (3 processes) |
| AOF everysec durability | Sub-second data loss possible |

## Failure modes

- Replication lag at promotion: Last second of idempotency claims may be lost. client retry may double-upstream if completion never persisted (mitigated by client idempotency keys).
- All Sentinels isolated: No quorum → no failover → extended outage identical to redis-outage.
- Split-brain with misconfig: Two masters if network partition. rare with proper quorum; use odd Sentinel count.
- Circuit storm: `cb:redis` opens cluster-wide; half-open probes compete across limiter pods.

## Operational concerns

**Before production:**

- Run `benchmarks/sentinel/` and chaos kill-master drills.
- Document `REDIS_MASTER_NAME` matching Sentinel monitor name.

**During incident:**

1. Check Sentinel logs: `+switch-master`, `+promoted-slave`.
2. Watch `redis_failover_reconnects_total`. should increment once per client recovery.
3. Verify `/health` shows new master role.
4. If stuck: manual `SENTINEL failover mymaster` (ops runbook).

**After recovery:**

- Force-close redis circuit if needed: `DELETE /admin/circuit/redis`.
- Review `circuit_breaker_transitions_total{target="redis"}`.

## Performance implications

Steady-state: no Sentinel overhead on each command. Failover window: 100% error rate on Redis ops → sidecar 503s, limiter 503s. Recovery: half-open probes add controlled load. tune `CB_OPEN_COOLDOWN_MS` if flapping during unstable networks.

## Lessons learned

During my first HA demo I killed only the master container and expected instant recovery. clients errored for ~15s and I had not wired `redis_failover_reconnects_total` yet, so I could not prove reconnection in Grafana. The lesson: **measure failover time in benchmarks**, not assumptions. Sentinel is correct for self-hosted; managed Redis is correct when you want someone else to run the election.

**References:** `docs/decisions/why-sentinel.md`, `docs/diagrams/sentinel-failover.mmd`, `deploy/redis/`, `benchmarks/sentinel/summary.md`
