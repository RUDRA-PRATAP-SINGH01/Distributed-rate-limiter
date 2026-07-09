# Deployment

## Problem Statement

I needed a repeatable way to deploy the distributed rate limiter stack from local dev through to production. Redis, the limiter, the sidecar, the demo upstream, optional HA Sentinel, and observability (Jaeger) should all come up from one `docker-compose.yml`. This deployment doc is my journal: which profile to use when, which env vars are secrets, and what benchmark or chaos prerequisites I expect before I trust a deploy.

## Why the problem exists

Without documented deployment, things fall apart fast:

- Engineers mix `go run` and compose, which leads to port conflicts (the chaos README warns about 8080, 8081, and 9090).
- `ADMIN_API_KEY=dev-key-change-in-prod` leaks into production.
- The HA stack starts under the wrong profile, and Sentinel drills fail silently.
- Sidecar behavior diverges across environments because `FAIL_OPEN=false` vs `true` is easy to get wrong.

## Design goals

1. Single compose entry: `docker compose up --build -d` for standalone.
2. HA overlay: `docker-compose.ha.yml --profile ha` for Sentinel.
3. Explicit secrets: `REDIS_PASSWORD`, `ADMIN_API_KEY`, `INTERNAL_API_KEY`.
4. Health-gated startup via `depends_on: condition: service_healthy`.
5. Benchmark-ready: sidecar `:9090` exposed for k6.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Kubernetes Helm only | Overkill for local; compose first |
| Manual binary deploy | No Redis topology reproducibility |
| Sidecar as library only | Loses ops visibility |
| Managed Redis only | Prod target; compose for dev/bench |
| Single container monolith | Violates sidecar architecture |

I landed on Docker Compose multi-service with an optional HA profile.

## Final architecture

**Standalone deploy:**

```bash
docker compose up --build -d
```

**HA deploy:**

```bash
docker compose -f docker-compose.yml -f docker-compose.ha.yml --profile ha up --build -d
```

**Service map:**

| Service | Container | Ports | Role |
|---------|-----------|-------|------|
| redis | rate-redis | 6379 | Quota + idempotency state |
| limiter | rate-limiter | 8080, 8082 | Central limiter + admin API |
| sidecar | rate-sidecar | 9090 | Client entry, routing, idempotency |
| demo | demo-backend | 8081 | Upstream for benchmarks |
| gateway-a/b/c | gateway-* | internal | Routing simulation |
| jaeger | jaeger | 16686, 4318 | OTLP traces (export when `OTEL_ENABLED=true`) |
| prometheus | prometheus | 9091 | Metrics scrape console |
| grafana | grafana | 3000 | Provisioned fleet dashboard |
| redis-exporter | redis-exporter | 9121 | Redis server metrics for Prometheus |

**Critical limiter env:**

```
PORT=8080
REDIS_ADDR=redis:6379
REDIS_PASSWORD=${REDIS_PASSWORD:-dev-redis-password}
ALGORITHM=sliding
ENABLE_HIERARCHICAL=true
ENABLE_ADMIN_API=true
ADMIN_PORT=8082
ADMIN_API_KEY=dev-key-change-in-prod
INTERNAL_API_KEY=dev-internal-key-change-in-prod
OTEL_ENABLED=true
OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318
```

**Critical sidecar env:**

```
PORT=9090
UPSTREAM_URL=http://demo:8081
RATE_LIMITER_URL=http://limiter:8080
ENABLE_ROUTING=true
ENABLE_IDEMPOTENCY=true
FAIL_OPEN=false
REDIS_ADDR=redis:6379
INTERNAL_API_KEY=dev-internal-key-change-in-prod
```

**Verify:**

```bash
curl http://localhost:8080/health
curl http://localhost:9090/health
curl -H "X-API-Key: dev-key-change-in-prod" http://localhost:8082/admin/limits/user/default
```

## Tradeoffs

Dev keys in compose are convenient, but I must override them in prod. Jaeger, Prometheus, and Grafana all start in default compose; set `OTEL_ENABLED=true` on limiter and sidecar to export traces. On Windows Docker Desktop, networking adds latency compared to a Linux host. The `standalone` vs `ha` profile split confuses people sometimes. Using `--build` on every `up` is slow; I only use it when code changes.

## Failure modes

Port conflict is the classic one: local processes on 8080 or 9090 cause compose healthchecks to fail. Redis auth mismatch happens when `REDIS_PASSWORD` does not match across limiter, sidecar, and redis. Starting Sentinel without the HA profile leaves the limiter pointing at the standalone redis hostname. The admin API on `:8082` binds to `0.0.0.0` in dev; I firewall it in prod. Stale images bite me when I forget `--build` after a Lua script change.

## Operational concerns

Before deploy I rotate `ADMIN_API_KEY`, `INTERNAL_API_KEY`, and `REDIS_PASSWORD`. After deploy I run `benchmarks/collect-environment.ps1` for benchmark sessions. I stop local `go run` before compose (per the chaos README). For HA I verify `REDIS_MODE=sentinel` on limiter and sidecar after the overlay. Post-deploy smoke is a short `k6 run benchmarks/load-test.js`. For logs I use `docker logs rate-sidecar --tail 50` and `docker logs rate-limiter --tail 50`.

## Performance implications

On my laptop (`environment.md`), the deployed stack sustains **~872 actual RPS** end-to-end with p99 < 100 ms on the final benchmark run (`docs/benchmarks/final-benchmark-report.md`).

Resource planning: limiter, sidecar, and Redis each need headroom; `metrics/collect-metrics.ps1` during `run-all.ps1` samples CPU and memory.

`OVERRIDE_CACHE_TTL_MS=5000` bounds per-key Redis reads when generation is unchanged. Admin limit changes are visible on the **next** `/check_hierarchical` on each replica after `config:generation` increments (not after a fixed TTL delay).

`ENABLE_AUDIT_TRAIL=true` adds a Redis append per decision. `BenchmarkAuditAppend` is ~300µs on miniredis; production adds RTT.

## Lessons learned

The most common deploy failure I hit is port conflict. I bolded it in the README: stop local processes first.

I named `dev-key-change-in-prod` on purpose to look scary. One engineer still copy-pasted it into prod. Secret rotation is now item #1 on my deployment checklist.

Keeping the HA overlay in a separate file from main compose was the right call. Standalone dev stays fast, and HA drills stay explicit.

Next up: a `deploy/` folder with a production compose override template and no default secrets.
