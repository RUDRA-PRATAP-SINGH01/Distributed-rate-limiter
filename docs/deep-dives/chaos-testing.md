# Chaos Testing

## Problem Statement

Unit tests prove Lua scripts and Go parsers work in isolation. They do not prove the **assembled sidecar** fails safely when Redis dies, when the network partitions, or when latency spikes 10×. I needed repeatable chaos experiments that validate our **fail-closed** contract and recovery behavior under real Docker Compose topology.

## Why the problem exists

Distributed systems fail in production ways tests mock poorly:

- TCP half-open connections when Redis container stops.
- Sidecar still running but isolated from Redis network namespace.
- Recovery race when Redis restarts before connection pools refresh.
- Operators assume HA fixes total outage. chaos proves what 503 storm looks like.

Without chaos, we ship confidence based on happy-path benchmarks (`benchmarks/summary.md` at 1,000 RPS) while resilience claims stay theoretical.

## Design goals

1. Automated scripts: `chaos/` runnable in CI nightly or pre-release.
2. Clear pass/fail: expect 503 fail-closed, then recovery.
3. Compose integration: Partition test requires full stack, not `go run` locally.
4. Document prerequisites: Port conflicts, profile flags.
5. Complement benchmarks: Throughput proves speed; chaos proves survival.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Only unit tests | Miss network/process failures |
| Gremlin SaaS | Cost; Docker chaos sufficient for our scale |
| Manual "stop Redis and click" | Not repeatable |
| Jepsen | Overkill for v1 |
| Game days only | Too infrequent |

Scripted Docker chaos in-repo won.

## Final architecture

**Test inventory** (`chaos/`):

| Asset | What it does |
|-------|----------------|
| `chaos_test.ps1` | Kill Redis container → expect 503 → restart → verify recovery |
| `network_partition.py` | Disconnect `rate-sidecar` from Compose network → 503/timeout → reconnect |
| `high_latency.py` | Inject latency (supporting experiment) |
| `README.md` | Run instructions and expectations |

**Redis failure test** (`chaos_test.ps1`):

1. Assert sidecar healthy
2. Stop/kill Redis container
3. Hit sidecar endpoints. **expect `503 Service Unavailable`** (fail-closed limiter/idempotency)
4. Restart Redis
5. Verify requests succeed again. pool reconnect via go-redis

**Network partition** (`network_partition.py`):

- Requires `docker compose up -d --build` full stack
- Disconnects sidecar from network (cannot reach Redis even if Redis alive)
- Expect 503 or timeout. fail-closed, not fail-open unlimited traffic
- Reconnect and verify recovery

**Related HA testing**. `benchmarks/sentinel/summary.md` covers graceful master failover (different failure mode than total Redis death). Chaos folder focuses on **liveness** failures; Sentinel covers **leader election**.

**High latency** (`high_latency.py`). stress degraded Redis RTT; pair with `benchmarks/saturation/` to see latency knee interaction.

## Tradeoffs

- Split scripts for OS convenience; CI must run on compatible runner.
- Compose-specific names: `rate-sidecar`, network labels. break if compose file renames services.
- No random production injection; staging only.
- 503 vs 429 on Redis down: fail-closed may surface as 503 (dependency unavailable) not 429 (quota). document client retry semantics.
- No automated assertions in all scripts: Some rely on human observation; improve with pytest exit codes over time.

## Failure modes

1. False pass if fail-open bug: Test must assert 503, not merely "no crash."
2. Local `go run` on 8080 blocks compose. README warns to stop first.
3. Recovery flake: Pool reconnect slower than script sleep. add retry backoff in script.
4. Partition test on wrong network: Python docker SDK must target compose project network name.
5. Sentinel vs standalone: killing `redis-master` in HA profile should failover, not total outage. different test matrix; don't conflate with `chaos_test.ps1` killing only Redis container in simple compose.

## Operational concerns

- Run `chaos_test.ps1` before releases alongside `benchmarks/run-all.ps1`.
- Full partition: `python chaos/network_partition.py` after `docker compose up -d --build`.
- Log scrape during chaos. verify `metrics` show Redis errors, audit drops, idempotency unavailable.
- Document expected client behavior: retry with backoff on 503, do not assume idempotency during outage window.
- Integrate into CI with Docker-in-Docker if runner supports it.

## Performance implications

Chaos is not load test, but run light k6 during Redis failure to see connection pool behavior under concurrent errors.

Expect error latency lower than success path (fast fail) unless timeouts are misconfigured long.

After recovery, **latency spike** possible while scripts reload and pools warm. watch p99 for 30s post-recovery.

Saturation benchmark (`benchmarks/run-saturation.ps1`) + `high_latency.py` explores performance under degradation, not just correctness.

## Lessons learned

Fail-closed is easy to say in README, hard to trust without `chaos_test.ps1`. The first time I ran it, we returned 500 with empty body. fixed error mapping to 503.

Network partition found bugs **container kill did not**. sidecar process healthy but network blackholed mimics AZ routing failures.

Chaos README stating "not local go run" saved hours. partition test meaningless without compose DNS names.

Pair chaos with **Sentinel drill**. total kill vs failover are different stories; customers ask both questions.

I want next step: assert JSON health body fields (`redis.connected: false`) automatically, not curl by eye.

Chaos tests document **operational truth** for `internal/redis/client.go`, `internal/limiter/*`, and `internal/idempotency/*`. when Redis unavailable, every Lua path errors and sidecar must not passthrough unlimited traffic.

Benchmarks prove how fast we go; chaos proves we stop safely. both required for production credibility.
