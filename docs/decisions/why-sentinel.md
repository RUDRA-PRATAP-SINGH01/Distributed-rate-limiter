# Why Redis Sentinel for HA

**Status:** Accepted  
**Date:** 2025-06  
**Scope:** `internal/redis`, `REDIS_MODE=sentinel`, FailoverClient, HA compose profile

---

## Problem Statement

A single Redis instance is a single point of failure. When the master dies, every limiter `/check`, idempotency claim, and circuit breaker read fails until manual intervention. I needed automatic master promotion without requiring Kubernetes operators or managed Redis on day one.

## Why the problem exists

This platform stores **live enforcement state** in Redis. Minutes of write unavailability mean either total 503 traffic (`FAIL_OPEN=false`) or unbounded traffic (`FAIL_OPEN=true`). both unacceptable in production. Replicas alone do not failover; something must detect master death and promote a replica.

## Design goals

- Config-driven mode switch: `REDIS_MODE=standalone|sentinel` in `internal/redis/config.go`.
- No code forks: Same `redis.UniversalClient` interface; Sentinel uses `redis.NewFailoverClient`.
- Quorum-based failover: Three Sentinel processes in `docker-compose.ha.yml` profile.
- Transparent reconnect: Go-redis queries Sentinels for new master address after promotion.
- Health visibility: Limiter `/health` reports `redis.mode`, `role`, `replication`.
- Metric: `redis_failover_reconnects_total` on ping recovery.

## Alternative approaches considered

| Alternative | Why I rejected it |
|-------------|-------------------|
| **Manual failover runbook** | Too slow; violates payment-grade uptime expectations. |
| **Redis Cluster (sharding)** | Needed HA before horizontal shard complexity. |
| **Managed Redis (ElastiCache/Memorystore)** | Valid prod path; Sentinel keeps self-hosted story testable. |
| **Primary-replica with VIP + keepalived** | Extra moving parts outside Redis ecosystem. |
| **Multi-master CRDT counters** | Overkill for rate limits; conflict resolution is hard. |

## Final architecture

`internal/redis/client.go`:

```go
case ModeSentinel:
    opts := &redis.FailoverOptions{
        MasterName:       cfg.MasterName,       // REDIS_MASTER_NAME, default mymaster
        SentinelAddrs:    cfg.SentinelAddrs,  // REDIS_SENTINEL_ADDRS comma-separated
        Password:         cfg.Password,
        SentinelPassword: cfg.SentinelPassword,
    }
    return redis.NewFailoverClient(opts)
```

Startup validation:

- Limiter: `redisclient.Ping` after `New`. fatal if unreachable.
- Sidecar: `connectSidecarRedis` fatals if `REDIS_SENTINEL_ADDRS` empty when `REDIS_MODE=sentinel`.

Failover sequence (documented in README):

1. Sentinels mark master down (`down-after-milliseconds` ~5s).
2. Quorum elects leader sentinel.
3. Best replica promoted.
4. Clients reconnect via Sentinel topology.
5. Old master rejoins as replica when restored.

Deploy: `docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build`.

## Tradeoffs

| Choice | Benefit | Cost |
|--------|---------|------|
| Sentinel (3 nodes) | Automatic failover without K8s | 3 extra processes to monitor |
| AOF everysec (deploy config) | Durability with low overhead | Sub-second loss window on crash |
| FailoverClient | App code unchanged | Brief write unavailability during election |
| 2 replicas | Failover candidates + read scaling potential | 3× memory |

## Failure modes

- Split-brain (misconfigured quorum): Two masters. rare with proper `sentinel monitor` and majority; ops must enforce odd Sentinel count.
- All Sentinels down: Clients cannot discover master; same as Redis total outage.
- Promotion during write: In-flight Lua scripts fail; circuit breaker opens on `TargetRedis`.
- Async replication lag: Promoted replica may miss last writes. idempotency/limits may allow brief inconsistency.

## Operational concerns

- Env: `REDIS_MASTER_NAME`, `REDIS_SENTINEL_ADDRS`, `REDIS_SENTINEL_PASSWORD` (defaults to `REDIS_PASSWORD`).
- Run chaos drills: `chaos/` scripts, `benchmarks/sentinel/`.
- Watch `redis_failover_reconnects_total` spike during drills. confirms client recovery.
- After failover, verify `/health` shows `role: master` on new primary.

## Performance implications

Sentinel adds discovery overhead only on connect/reconnect, not steady-state EVAL latency. Failover window (typically 5 to 30s) dominates user-visible impact. circuit breakers trip, sidecars return 503. Read scaling from replicas is **not** used for limiter writes (`RouteByLatency: false`). all enforcement hits master.

## Lessons learned

The first version ran single Redis in Docker and I killed the container during a demo. entire stack froze. Sentinel taught me to separate **"Redis slow"** from **"Redis gone"** in metrics. I would still choose Sentinel for self-hosted deployments; for cloud I would likely use managed failover and keep `REDIS_MODE=standalone` pointed at the provider endpoint. The code path stays the same either way.

**References:** `internal/redis/`, `deploy/redis/`, `docs/diagrams/sentinel-failover.mmd`, `docs/failure-modes/sentinel-failover.md`
