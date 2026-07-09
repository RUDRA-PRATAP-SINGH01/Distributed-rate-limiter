# Benchmark Methodology

यह document बताता है कि throughput numbers कैसे measure होते हैं, **sustainable** क्या माना जाता है, और warmup क्यों mandatory है। Primary artifact: `benchmarks/results/bench-progress.log` (commit `a1de9ec`, 2026-07-10)।

---

## Goals

1. **Actual RPS** measure करना — target RPS नहीं (saturated systems queue करते हैं)
2. Algorithm compare करना — explicit topology (`direct` vs `sidecar-e2e`, `sliding` vs `token`)
3. 429 को quota behavior मानना — infrastructure failure नहीं
4. Anomalies document करना — discard नहीं

---

## Test harness

| Piece | Path |
|-------|------|
| Orchestration | `benchmarks/final/run-targeted-benchmarks.ps1` |
| k6 scripts | `benchmarks/scripts/*.js` |
| Stream parser | `benchmarks/scripts/parse-k6-stream.py` |
| Progress log | `benchmarks/results/bench-progress.log` |
| Raw JSON | `benchmarks/results/a1de9ec-final/raw/` |

Topology (default):

```
k6 → localhost:9090 (sidecar) → limiter:8080 → redis:6379 → demo:8081
Direct tests: k6 → limiter:8080 (or :8085 token bucket container)
```

---

## Scenario structure

**Constant-arrival-rate** k6 executor:

| Phase | Duration | Purpose |
|-------|----------|---------|
| **Warmup** | **10 s** | JVM/Go GC, Redis connection pool, Docker DNS settle |
| **Measurement** | **60 s** | Parsed window for RPS/latency |

Parser typically skips first **70 s** of stream (warmup + ramp) when extracting steady-state — `parse-k6-stream.py <file> 70`。

---

## Sustainable definition

एक run **sustainable** तभी जब:

| Criterion | Threshold |
|-----------|-----------|
| p99 latency | **< 100 ms** |
| Non-429 error rate | **< 1%** |
| Throughput honesty | Actual RPS reported (not target alone) |

**Max sustainable RPS** = ऊपर criteria pass करने वाला highest **actual** RPS।

### What is NOT a failure

- **429** — expected quota enforcement
- **409** — idempotency in-progress (correctness tests)
- High 429 in hierarchical benchmark — endpoint capacity by design

### What IS a failure

- **5xx / timeouts** — saturation or outage
- **6 errors** in polluted run — document + rerun

---

## Workload classes

| Class | Example | Success metric |
|-------|---------|----------------|
| Throughput (unique user) | `sidecar-e2e-1000` | p99 + actual RPS |
| Saturation | `direct-sliding-5000` | knee documentation |
| Correctness | `multi-replica-500`, `denial-cache` | allow/deny counts |
| Soak | `soak-15m` | zero error drift |
| Idempotency | runtime 40-parallel | 1×200, 39×409 |

---

## Environment pinning

Record before every suite:

```powershell
docker compose up -d
# capture to benchmarks/results/a1de9ec-final/environment.txt
```

Reference hardware: Intel i9-14900HX, 32 GB RAM, Windows 11, Docker 29.x, Redis 7.4, Go 1.26.1, k6 1.7.1。

Windows + Docker Desktop networking adds ~1–3 ms vs bare-metal Linux — compare runs only on same environment file。

---

## Anomaly policy

1. Log raw result in `bench-progress.log`
2. Note root cause (outage overlap, invalid script, port conflict)
3. **Rerun** when infrastructure invalidates test
4. Report **both** invalid and valid runs in `performance-analysis.md`

Example: `direct-sliding-100` first run — 6 errors, p99 35 s → rerun p99 3.93 ms。

---

## Regression gates

```powershell
go test -count=1 ./...
go test -count=1 -race ./...
go vet ./...
go build ./...
```

Benchmarks complement — do not replace — unit/integration tests。

---

## Related docs

- [performance-analysis.md](performance-analysis.md) — number interpretation
- [reproducibility.md](reproducibility.md) — exact commands
- [final-benchmark-report.md](final-benchmark-report.md) — executive summary
