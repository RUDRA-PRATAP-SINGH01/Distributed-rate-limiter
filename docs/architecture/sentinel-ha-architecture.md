# Sentinel HA Architecture

> इंजीनियरिंग जर्नल — मैंने Redis single point of failure को Sentinel failover से कैसे address किया।

## Problem Statement

Rate limiter, idempotency, circuit breaker, audit trail, और routing state **सब Redis पर depend** करते हैं। Standalone `redis:6379` container dev के लिए ठीक है, पर production में master crash = पूरी fleet blind (fail-closed circuits, no quota, no idempotency)। मुझे **automatic master promotion** और application-side transparent reconnect** चाहिए था — बिना limiter/sidecar code rewrite के।

## Why the problem exists

Redis primary-replica replication async है — manual failover slow और error-prone। हर app instance को topology changes (new master IP) track करना unrealistic है। go-redis `FailoverClient` Sentinel queries handle करता है, पर configuration (master name, sentinel addrs, passwords) और health reporting consistently wire करनी पड़ती है। Docker Compose में standalone और HA profiles coexist करने चाहिए थे ताकि local dev simple रहे।

## Design goals

1. **Redis Sentinel** — quorum-based failover monitoring।
2. **1 master + 2 replicas** — read scaling secondary; failover candidates।
3. **`FailoverClient`** in `internal/redis` — Sentinel-driven master discovery।
4. **Compose overlay** — `docker-compose.ha.yml` switches fleet without forking base stack।
5. **Health checks** — Redis ping + replication INFO in `/health`।
6. **Password auth** — master, replica, sentinel aligned।

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Standalone Redis only | Unacceptable SPOF for HA demo/prod path |
| Redis Cluster (sharding) | Overkill; our keys fit single master memory |
| Managed ElastiCache/MemoryDB | Valid prod path; wanted self-hosted compose |
| Manual DNS failover | Slow; no automatic promotion |
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
| `redis-sentinel-1/2/3` | `ha` | Monitor + failover |
| `limiter`, `sidecar` | (base) | Env overridden to Sentinel mode |

**Limiter/sidecar Sentinel env** (in overlay):

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
- `requirepass` + `masterauth dev-redis-password`
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

Quorum `2` of 3 sentinels — master deemed down after 5s, failover timeout 60s。

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

`RouteByLatency` / `RouteRandomly` false — **all writes and Lua scripts go to current master** (required for atomicity)।

### Config from env (`internal/redis/config.go`)

| Env | Purpose |
|-----|---------|
| `REDIS_MODE` | `standalone` (default) or `sentinel` |
| `REDIS_ADDR` | Standalone address |
| `REDIS_SENTINEL_ADDRS` | Comma-separated sentinel hosts |
| `REDIS_MASTER_NAME` | Sentinel logical name (`mymaster`) |
| `REDIS_PASSWORD` | Master/replica auth |
| `REDIS_SENTINEL_PASSWORD` | Defaults to `REDIS_PASSWORD` |
| `REDIS_DB` | DB index |

`Describe(cfg)` logs human-readable mode for startup messages。

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

Sidecar health proxies limiter `/health` — indirect Redis visibility。

### Failover behavior (runtime)

1. Master stops → sentinels detect (5s `down-after-milliseconds`)
2. Quorum promotes best replica → new master
3. `FailoverClient` receives topology update via Sentinel
4. Connection pool reconnects to new master
5. Brief window: `Ping` failures, circuit breaker may open (`redis` target)
6. Metric `redis_failover_reconnects_total` reserved for reconnect counting

### Startup validation

Both `cmd/limiter/main.go` and `cmd/sidecar/connectSidecarRedis`:

```go
rdb := redisclient.New(redisCfg)
if err := redisclient.Ping(rdb); err != nil {
  log.Fatalf("Redis unreachable (%s): %v", redisclient.Describe(redisCfg), err)
}
```

Fail fast — unhealthy fleet doesn't serve partial state。

## Tradeoffs

- **Async replication** — promoted replica may miss last writes (RPO > 0); idempotency/audit may lose seconds of data on crash。
- **Sentinel compose complexity** — 6 extra containers vs 1 Redis。
- **All writes to master** — replicas unused for reads in current code; simpler correctness。
- **dev-redis-password in repo** — demo only; prod needs secrets management。
- **Single shard** — vertical scale limit; Cluster future path。
- **Sidecar depends on sentinel-1 health only** — other sentinels may lag starting。

## Failure modes

| Scenario | Effect |
|----------|--------|
| Master crash | ~5-60s failover; requests fail until reconnect |
| Split-brain (network partition) | Sentinel quorum prevents dual master (with proper deploy) |
| All sentinels down | No failover; stale master assumption |
| Wrong `REDIS_MASTER_NAME` | Client connects to wrong or empty topology |
| Password mismatch | Auth errors across replication chain |
| Replica lag at promotion | Short inconsistency window |
| Lua scripts during failover | `EXEC`/`EVAL` errors; circuit breaker trips |

## Operational concerns

- Runbook: `docker compose ... --profile ha` for HA demo; default compose uses `standalone` profile。
- Monitor `/health` redis.role — should be `master` on connected node post-failover。
- `circuit_breaker_state{target="redis"}` during failover spikes — expected。
- AOF `appendfsync everysec` — up to 1s write loss on catastrophic failure。
- Chaos testing: see `chaos/README.md` for failure injection patterns。
- Diagram: `docs/diagrams/sentinel-failover.mmd`。
- Production: odd number of sentinels (3+), spread AZs, separate `requirepass` rotation procedure。

## Performance implications

- **Sentinel queries** — cached by go-redis; negligible steady-state overhead。
- **Replication lag** — doesn't affect read path (we don't read replicas)।
- **Failover window** — all Redis ops fail; sidecar cache may serve **denials only** (not allowances) briefly。
- **Connection pool** — `PoolSize=100`, `MinIdleConns=10` per process; reconnect storm after failover across many pods — watch `redis_failover_reconnects_total` and connection limits。
- **AOF everysec** — small write amplification vs RDB only; durability trade accepted。

## Lessons learned

मैंने **compose profiles** से standalone/HA dual-mode रखा — developers को Sentinel नहीं चलाना पड़ता local quick start के लिए। `FailoverClient` with `RouteByLatency=false` conscious choice: Lua atomicity > read scaling from replicas। Sentinel `resolve-hostnames` + `announce-hostnames` Docker networking में IP drift से बचाते हैं। Health endpoint में replication INFO add करना failover drills में "कौन master है" instantly दिखाता है। Password on sentinel + master + replica तीनों जगह sync रखना deploy footgun है — `masterauth` replica conf में explicit। अगला step होगा `RecordRedisFailoverReconnect` को go-redis hook पर wire करना ताकि failover SLO measure हो सके।
