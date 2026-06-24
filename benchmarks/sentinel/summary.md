# Redis Sentinel HA Benchmarks

## Start HA stack

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build -d
```

## Failover drill

```bash
# 1. Baseline throughput
k6 run benchmarks/load-test.js

# 2. Kill master during load
docker stop redis-master

# 3. Observe Sentinel promotion (5–30s) and client reconnect
docker logs redis-sentinel-1 --tail 20
curl http://localhost:8080/health | jq .redis

# 4. Restart old master (becomes replica)
docker start redis-master
```

## Expected behavior

| Phase | Behavior |
|-------|----------|
| Master healthy | `role=master`, writes succeed |
| Master down | Sentinels detect after `down-after-milliseconds` (5s) |
| Election | Quorum 2/3 promotes a replica |
| Client | go-redis FailoverClient discovers new master, reconnects |
| Recovery | Old master rejoins as replica |

## Metrics

- `redis_failover_reconnects_total` — reconnect after failover
- `/health` → `redis.role`, `redis.replication`

## Go benchmarks

```bash
go test -bench=. -benchmem ./internal/redis/...
go test -bench=. -benchmem ./internal/audit/...
```
