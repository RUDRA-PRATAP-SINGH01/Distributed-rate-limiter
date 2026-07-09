# Sentinel Failover

## Problem Statement

Standalone Redis is a single point of failure. When the primary dies, every subsystem. rate limits, idempotency, circuits, audit, routing state. becomes unavailable. I needed **automatic Redis failover** without rewriting application logic, using Sentinel-managed promotion and a client that rediscovers the new master.

## Why the problem exists

Our architecture centralizes coordination in Redis (see `redis-lua-atomicity.md`). One dead primary means:

- Sidecars fail-closed on limit checks (503)
- Idempotency claims fail
- Circuit state frozen
- Audit writes drop or sync-fallback

Manual failover (promote replica, reconfigure apps) takes minutes and human error. Sentinel automates monitoring, quorum election, and replica promotion. go-redis `FailoverClient` subscribes to Sentinel topology updates.

## Design goals

1. Mode switch via env: `REDIS_MODE=sentinel` in `internal/redis/config.go`.
2. Universal client interface: `redis.UniversalClient` so limiter/idempotency packages unchanged.
3. Health visibility: `CheckHealth` reports role, replication, master addr.
4. Connection pool defaults: `PoolSize=100`, `MinIdleConns=10` for failover reconnect storm.
5. Documented drill: `benchmarks/sentinel/summary.md` and `docker-compose.ha.yml` profile.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Redis Cluster | Shard complexity; our keys don't need horizontal shard yet |
| Primary-replica with manual promotion | Too slow |
| Elasticache Multi-AZ | Cloud-specific; we support self-hosted Sentinel |
| Raft alternative (etcd) | Rewrite all Lua state machines |
| Read replicas for writes | Incorrect for atomic Lua. all writes must hit primary |

Sentinel + FailoverClient is the pragmatic HA layer.

## Final architecture

**Config** (`internal/redis/config.go`):

```go
ModeSentinel
MasterName       // REDIS_MASTER_NAME, default "mymaster"
SentinelAddrs    // REDIS_SENTINEL_ADDRS comma-separated
SentinelPassword // optional
```

**Client factory** (`internal/redis/client.go`):

```go
case ModeSentinel:
    opts := &redis.FailoverOptions{
        MasterName:    cfg.MasterName,
        SentinelAddrs:   cfg.SentinelAddrs,
        RouteByLatency: false,
        RouteRandomly:  false,
    }
    return redis.NewFailoverClient(opts)
```

Comments note Sentinel-driven topology updates. go-redis reconnects to promoted master.

**Health** (`internal/redis/health.go`):

- `Ping` → `Connected`
- `INFO replication` → `role`, `master_host`, `connected_slaves` summary
- JSON export for `/health` endpoint embedding

**Failover timeline** (`benchmarks/sentinel/summary.md`):

| Phase | Behavior |
|-------|----------|
| Master healthy | `role=master`, writes succeed |
| Master down | Sentinels detect after `down-after-milliseconds` (~5s) |
| Election | Quorum promotes replica |
| Client | FailoverClient discovers new master, reconnects |
| Old master returns | Rejoins as replica |

**Metrics**. `/health` `redis.role`, `circuit_breaker_transitions_total{target="redis"}`. `redis_failover_reconnects_total` is declared but unwired (audit §14).

## Tradeoffs

- Election window 5 to 30s; all sidecars fail-closed. user-visible 503 burst.
- Last seconds of writes may be lost if master dies before replicate; idempotency in-flight keys at risk (RPO > 0).
- Sentinel quorum: 3 sentinels recommended; split-brain if misconfigured.
- Single primary write scale: Sentinel solves HA not horizontal write scale.
- Replica promotion must complete replication backlog before serving writes.

## Failure modes

1. All sentinels down: Client holds last known master; may talk to stale master if partition.
2. Split brain (mis-quorum): Two masters. rare with proper sentinel count; data corruption risk.
3. Connection pool exhaustion during reconnect: Many sidecars reconnect simultaneously. tune pool.
4. SCRIPT FLUSH on new master: Go-redis resends scripts; transient latency spike.
5. Read your writes: App never reads quota from replica; good. replicas not used in our code paths.

## Operational concerns

- Start HA stack: `docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up`
- Drill: `docker stop redis-master` during `k6 run benchmarks/load-test.js`
- Verify: `curl http://localhost:8080/health | jq .redis`
- Monitor `redis.role` flips and reconnect metric.
- `go test -bench=. ./internal/redis/...` for client benchmarks.
- Chaos: `chaos/chaos_test.ps1` kills Redis container. related but tests total outage not graceful failover.

## Performance implications

FailoverClient maintains sentinel connections. small background overhead vs standalone `redis.NewClient`.

During steady state, performance matches standalone. same single-primary Lua execution ceiling (~1,000 RPS system knee per `benchmarks/summary.md`).

Reconnect storm after promotion may cause p99 spike across all sidecars for one pool refresh cycle.

`Describe(cfg)` logs `sentinel master=mymaster sentinels=[...]` at startup. verify correct topology in multi-env deploys.

## Lessons learned

HA Redis does not mean **zero downtime**; it means bounded recovery. Product and SLO docs must say so.

I test failover with load running, not idle. connection pool bugs only appear under concurrent `EVAL`.

Keeping `UniversalClient` interface meant limiter packages never learned about Sentinel. correct abstraction boundary.

Replication lag is the hidden SLA killer for idempotency. document RPO; fences don't help if claim metadata never replicated.

Run failover drill quarterly; Sentinel config drifts (wrong `down-after-milliseconds`) silently until real incident.

Instrument Redis with `telemetry.InstrumentRedis` (`internal/telemetry/redis.go`). traces show reconnect gaps in Jaeger.
