# Benchmark Methodology

## Problem Statement

I needed a repeatable benchmark harness that does not just say "fast feels fast," but measures **actual throughput**, **p99 latency**, and **error budget** in one framework. For a distributed rate limiter that matters even more. 429 rejections are correct behavior, but 5xx responses and timeout collapse tell a different story. I built `benchmarks/` for exactly that: k6 scripts, a Docker Compose stack, metrics collection, and a pipeline all the way to `summary.md`.

## Why the problem exists

Load testing distributed systems has three common traps:

1. **Target RPS is not actual RPS**. k6 sends at a constant arrival rate, but a saturated system queues requests. In my runs at a 10,000 RPS target, actual throughput was only **1,082 RPS**.
2. **Algorithm confusion**. Sliding window and token bucket give different burst semantics. Comparing numbers without an explicit `ALGORITHM` env is misleading.
3. **Environment variance**. A 2x gap between a laptop and a CI runner is normal. Without `benchmarks/environment.md`, the numbers are meaningless.

Without a methodology doc, every engineer runs their own ad-hoc `curl` loop and we end up writing contradictory claims in release notes.

## Design goals

1. **Sustainable load definition**. p99 under 100 ms and non-429 error rate under 1%.
2. **Full suite automation**. `benchmarks/run-all.ps1` runs throughput, saturation, hot-key, and enforcement in one command.
3. **Resource correlation**. `metrics/collect-metrics.ps1` samples `docker stats` during every test.
4. **Parseable output**. k6 JSON goes through `parse-results.py` into `summary.md` plus graphs.
5. **Compose fidelity**. Production-like topology: sidecar on `:9090`, limiter on `:8080`, Redis on `:6379`, demo on `:8081`.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| `ab` / `wrk` | No scenario scripting; hard to model idempotency races |
| Locust | Python overhead; team already standardized on k6 |
| In-process Go benchmarks only | Misses sidecar + Redis RTT stack |
| Cloud load generator (k6 cloud) | Cost; local Docker sufficient for regression |
| Manual JMeter | GUI-heavy; poor CI integration |

k6 + Docker Compose + PowerShell orchestration won. Reproducible on my Windows dev machine (`environment.md`: i9-14900HX, 32 GB RAM).

## Final architecture

**Test inventory** (`benchmarks/`):

| Test | Script | Purpose | Key metric |
|------|--------|---------|------------|
| Throughput | `throughput/throughput-test.js` | 100 to 10,000 target RPS | p99 latency |
| Saturation | `saturation/saturation-test.js` via `run-saturation.ps1` | 1,500 to 4,000 RPS knee | Max sustainable actual RPS |
| Hot-key | `hot-key/hot-key-test.js` | 10 shared users at 5,000 target | 429 rate correctness |
| Enforcement | `enforcement/enforcement-test.js` | 500 req/min single user | Allowed vs rejected |
| Baseline load | `load-test.js` | General smoke / Sentinel drill | Health under load |
| Idempotency | `idempotency/idempotency-race.js`, `idempotency-replay.js` | Race + replay | Upstream execution count |
| Routing | `routing/routing-test.js` | Weighted gateway selection | Failover headers |
| Circuit breaker | `circuitbreaker/circuit-test.js` | State machine under failure | Fast-fail while open |

**Run sequence:**

```powershell
docker compose up --build -d
.\benchmarks\collect-environment.ps1
.\benchmarks\run-all.ps1
python benchmarks/parse-results.py
python benchmarks/graphs/generate-graphs.py
```

**Sustainability criteria** (from `benchmarks/methodology.md`):

- p99 under **100 ms**
- Error rate (non-429) under **1%**
- Max sustainable RPS = highest *actual* throughput meeting both

**Stack entry point:** sidecar `http://localhost:9090/check?user_id=...`. That mimics the production path.

## Tradeoffs

- **Sliding window default**. `docker-compose.yml` sets `ALGORITHM=sliding`; token bucket comparison needs a separate compose override.
- **60s test duration**. Throughput tests run for 60 seconds with warmup included; shorter runs are noisy.
- **Local Docker overhead**. Windows plus Docker Desktop networking adds roughly 1 to 3 ms vs bare metal Linux.
- **Thresholds in k6**. `throughput-test.js` has `p(99)<50`, but methodology uses a 100 ms sustainable threshold. The two serve different purposes (regression vs capacity).
- **Metrics gaps**. CPU columns in the resource table stay blank until `run-all.ps1` collects fresh metrics.

## Failure modes

1. **False pass at 5,000 target**. Actual 1,353 RPS, p99 **3.5 s**, **10% errors**. Saturated, not sustainable.
2. **429 counted as k6 failure**. Idempotency race shows 14% 409 as "failed" but checks pass at 100%. The parse script must distinguish them.
3. **Port conflict**. Local `go run` on 8080/9090 blocks compose; the chaos README warns about the same thing.
4. **Stale results**. If you do not regenerate `summary.md`, old numbers get cited.
5. **Hot-key misread**. 99.9% 429 is *correct* enforcement, not system failure.

## Operational concerns

- Before every release: `collect-environment.ps1`, then `run-all.ps1`, then diff `summary.md`.
- CI needs k6 installed (`v1.7.1` per environment.md) and `docker compose up --build -d` as a prerequisite.
- Graphs: `graphs/latency-vs-rps.png`, `saturation-curve.png`, `error-rate-vs-rps.png`, `resource-utilization.png`, `enforcement-allowed-vs-rejected.png`.
- Admin API (`:8082`, `X-API-Key: $ADMIN_API_KEY`) for runtime overrides. Document any intentional quota change during a benchmark run.
- Chaos tests (`chaos/chaos_test.ps1`) run after benchmarks. Survival and speed are different axes.

## Performance implications

**Measured throughput** (`benchmarks/summary.md`):

| Target RPS | Actual RPS | p99 | Error Rate | Verdict |
|------------|------------|-----|------------|---------|
| 100 | 100 | 11 ms | 0% | Pass |
| 1,000 | 1,000 | 3.2 ms | 0% | **Max sustainable** |
| 5,000 | 1,353 | 3.5 s | 10% | Saturated |
| 10,000 | 1,082 | 4.3 s | 15% | Collapsed |

**Other tests:**

| Test | Actual RPS | Result |
|------|------------|--------|
| Hot-key (5,000 target) | 4,940 | 99.9% rejected (429). Correct |
| Enforcement (500/min) | 8 | 98% rejected. ~10 allowed per window |

Key insight: latency grows **exponentially** past roughly 1,000 actual RPS on my laptop stack. The saturation sweep (`run-saturation.ps1`, 1,500 to 4,000 RPS) locates the knee more precisely.

## Lessons learned

The biggest lesson: **graph actual RPS, not target**. I once saw a 10,000 RPS target and wrote "we handle 10k." Embarrassing when actual came out at 1,082.

Second: 429-heavy tests (hot-key, enforcement) need different success criteria. They are correctness proofs, not throughput proofs.

Third: running `metrics/collect-metrics.ps1` as a job alongside every test gives CPU correlation. Without it, saturation root cause stays guesswork.

Fourth: do not merge benchmarks and chaos/Sentinel drills into one narrative. `load-test.js` is the baseline; `docker stop redis-master` is a separate HA story.

Next step I want: auto-attach `environment.md` and `summary.md` as CI artifacts so PR reviewers get hardware context alongside the numbers.
