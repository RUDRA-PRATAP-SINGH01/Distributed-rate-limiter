# Sentinel HA Architecture

> Engineering journal. How I addressed Redis as a single point of failure with Sentinel failover.

## Problem Statement

The rate limiter, idempotency, circuit breaker, audit trail, and routing state **all depend on Redis**. A standalone `redis:6379` container is fine for dev, but in production a master crash blinds the whole fleet (fail-closed circuits, no quota, no idempotency). I needed **automatic master promotion** and **application-side transparent reconnect** without rewriting limiter or sidecar code.

## Why the problem exists

Redis primary-replica replication is async. Manual failover is slow and error-prone; it is unrealistic for every app instance to track topology changes (new master IP). go-redis `FailoverClient` handles Sentinel queries, but configuration (master name, sentinel addrs, passwords) and health reporting must be wired consistently. In Docker Compose, standalone and HA profiles should coexist so local dev stays simple.

## Design goals

1. **Redis Sentinel**. Quorum-based failover monitoring.
2. **1 master plus 2 replicas**. Read scaling is secondary. Replicas are failover candidates.
3. **`FailoverClient`** in `internal/redis`. Sentinel-driven master discovery.
4. **Compose overlay**. `docker-compose.ha.yml` switches the fleet without forking the base stack.
5. **Health checks**. Redis ping plus replication INFO in `/health`.
6. **Password auth**. Master, replica, and sentinel aligned.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Standalone Redis only | Unacceptable SPOF for HA demo and prod path |
| Redis Cluster (sharding) | Overkill. Our keys fit single master memory. |
| Managed ElastiCache or MemoryDB | Valid prod path. I wanted self-hosted compose. |
| Manual DNS failover | Slow. No automatic promotion. |
| Sentinel in app (custom) | Reinventing go-redis FailoverClient |
| Active-active multi-master | Conflict resolution complexity for Lua scripts |

## Final architecture

### Topology (HA profile)

```text
                    ┌─────────────────┐
                    │  redis-sentinel │ ×3 (26379)
                    │  quorum monitor │
                    └────────┬────────┘
                             │ monitor + failover
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
       redis-master    redis-replica-1  redis-replica-2
         :6379              :6379            :6379
       (read/write)      (read-only)       (read-only)
```

### Docker Compose overlay (`docker-compose.ha.yml`)

**Invocation**:

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build
```

**Profile behavior**:

| Service | Profile | Role |
|---------|---------|------|
| `redis` (standalone) | `standalone` | Disabled in HA mode |
| `redis-master` | `ha` | Primary with AOF |
| `redis-replica-1/2` | `ha` | `replicaof redis-master 6379` |
| `redis-sentinel-1/2/3` | `ha` | Monitor and failover |
| `limiter`, `sidecar` | (base) | Env overridden to Sentinel mode |

**Limiter and sidecar Sentinel env** (in overlay):

```yaml
REDIS_MODE=sentinel
REDIS_MASTER_NAME=mymaster
REDIS_SENTINEL_ADDRS=redis-sentinel-1:26379,redis-sentinel-2:26379,redis-sentinel-3:26379
REDIS_PASSWORD=dev-redis-password
```

`depends_on: redis-sentinel-1: condition: service_healthy`

### Deploy Redis configs (`deploy/redis/`)

**`master.conf`**:

- `bind 0.0.0.0`, port 6379
- `appendonly yes`, `appendfsync everysec`
- `requirepass dev-redis-password`

**`replica.conf`**:

- `replica-read-only yes`
- `requirepass` and `masterauth dev-redis-password`
- `replicaof` via docker command line: `--replicaof redis-master 6379`

**`sentinel.conf`**:

```
port 26379
sentinel monitor mymaster redis-master 6379 2
sentinel auth-pass mymaster dev-redis-password
sentinel down-after-milliseconds mymaster 5000
sentinel failover-timeout mymaster 60000
sentinel parallel-syncs mymaster 1
sentinel resolve-hostnames yes
sentinel announce-hostnames yes
```

Quorum is 2 of 3 sentinels. Master is deemed down after 5s. Failover timeout is 60s.

### FailoverClient (`internal/redis/client.go`)

```go
func New(cfg Config) redis.UniversalClient {
  switch cfg.Mode {
  case ModeSentinel:
    return redis.NewFailoverClient(&redis.FailoverOptions{
      MasterName:       cfg.MasterName,      // default "mymaster"
      SentinelAddrs:    cfg.SentinelAddrs,
      Password:         cfg.Password,
      SentinelPassword: cfg.SentinelPassword,
      DB:               cfg.DB,
      PoolSize:         100,
      MinIdleConns:     10,
      RouteByLatency:   false,
      RouteRandomly:    false,
    })
  default:
    return redis.NewClient(&redis.Options{Addr: cfg.Addr, ...})
  }
}
```

`RouteByLatency` and `RouteRandomly` are false. **All writes and Lua scripts go to the current master** (required for atomicity).

### Config from env (`internal/redis/config.go`)

| Env | Purpose |
|-----|---------|
| `REDIS_MODE` | `standalone` (default) or `sentinel` |
| `REDIS_ADDR` | Standalone address |
| `REDIS_SENTINEL_ADDRS` | Comma-separated sentinel hosts |
| `REDIS_MASTER_NAME` | Sentinel logical name (`mymaster`) |
| `REDIS_PASSWORD` | Master and replica auth |
| `REDIS_SENTINEL_PASSWORD` | Defaults to `REDIS_PASSWORD` |
| `REDIS_DB` | DB index |

`Describe(cfg)` logs a human-readable mode for startup messages.

### Health checks

**Container healthchecks** (compose):

- Master: `redis-cli -a dev-redis-password ping` every 5s
- Sentinel-1: `redis-cli -p 26379 ping`

**Application `/health`** (`internal/redis/health.go`):

```go
CheckHealth(ctx, client, cfg):
  Ping → Connected
  INFO replication → Role (master/slave), master_host, connected_slaves summary
```

Limiter health JSON:

```json
{
  "status": "healthy",
  "redis": {
    "mode": "sentinel",
    "connected": true,
    "role": "master",
    "replication": "role=master slaves=2"
  }
}
```

Sidecar health proxies limiter `/health`. Redis visibility is indirect.

### Failover behavior (runtime)

1. Master stops. Sentinels detect (5s `down-after-milliseconds`).
2. Quorum promotes the best replica to new master.
3. `FailoverClient` receives topology update via Sentinel.
4. Connection pool reconnects to the new master.
5. Brief window: `Ping` failures. Circuit breaker may open (`redis` target).
6. Failover visibility: `/health` `redis.role`, `circuit_breaker_transitions_total{target="redis"}`.

### Startup validation

Both `cmd/limiter/main.go` and `cmd/sidecar/connectSidecarRedis`:

```go
rdb := redisclient.New(redisCfg)
if err := redisclient.Ping(rdb); err != nil {
  log.Fatalf("Redis unreachable (%s): %v", redisclient.Describe(redisCfg), err)
}
```

Fail fast. An unhealthy fleet does not serve partial state.

## Tradeoffs

- **Async replication**. A promoted replica may miss the last writes (RPO greater than 0). Idempotency and audit may lose seconds of data on crash.
- **Sentinel compose complexity**. Six extra containers vs one Redis.
- **All writes to master**. Replicas are unused for reads in current code. Simpler correctness.
- **dev-redis-password in repo**. Demo only. Prod needs secrets management.
- **Single shard**. Vertical scale limit. Cluster is a future path.
- **Sidecar depends on sentinel-1 health only**. Other sentinels may lag on startup.

## Failure modes

| Scenario | Effect |
|----------|--------|
| Master crash | Roughly 5 to 60s failover. Requests fail until reconnect. |
| Split-brain (network partition) | Sentinel quorum prevents dual master with proper deploy. |
| All sentinels down | No failover. Stale master assumption. |
| Wrong `REDIS_MASTER_NAME` | Client connects to wrong or empty topology. |
| Password mismatch | Auth errors across the replication chain. |
| Replica lag at promotion | Short inconsistency window. |
| Lua scripts during failover | `EXEC` or `EVAL` errors. Circuit breaker trips. |

## Operational concerns

- Runbook: `docker compose ... --profile ha` for HA demo. Default compose uses `standalone` profile.
- Monitor `/health` `redis.role`. Should be `master` on the connected node after failover.
- `circuit_breaker_state{target="redis"}` spikes during failover. Expected.
- AOF `appendfsync everysec`. Up to 1s write loss on catastrophic failure.
- Chaos testing: see `chaos/README.md` for failure injection patterns.
- Diagram: `docs/diagrams/sentinel-failover.md`.
- Production: odd number of sentinels (3+), spread AZs, separate `requirepass` rotation procedure.

## Performance implications

- **Sentinel queries**. Cached by go-redis. Negligible steady-state overhead.
- **Replication lag**. Does not affect read path (we do not read replicas).
- **Failover window**. All Redis ops fail. Sidecar cache may serve **denials only** (not allowances) briefly.
- **Connection pool**. `PoolSize=100`, `MinIdleConns=10` per process. Reconnect storm after failover across many pods. Watch `/health`, `circuit_breaker_state{target="redis"}`, and connection limits.
- **AOF everysec**. Small write amplification vs RDB only. Durability trade accepted.

## Lessons learned

I kept **compose profiles** for standalone and HA dual mode. Developers do not need to run Sentinel for a local quick start. `FailoverClient` with `RouteByLatency=false` was a conscious choice: Lua atomicity beats read scaling from replicas. Sentinel `resolve-hostnames` and `announce-hostnames` avoid IP drift in Docker networking. Adding replication INFO to the health endpoint shows instantly who is master during failover drills. Password on sentinel, master, and replica must stay in sync. `masterauth` in replica conf is explicit.
