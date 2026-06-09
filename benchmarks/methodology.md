# Benchmark Methodology

## Test Suite

| Test | Purpose | Key Metric |
|------|---------|------------|
| **Throughput** | Latency at fixed load levels (100–10,000 RPS) | p99 latency |
| **Saturation sweep** | Find max sustainable RPS (1,500–4,000 RPS) | Actual RPS at collapse |
| **Hot-key** | Contention on 10 shared users at 5,000 RPS | 429 rate + latency |
| **Enforcement** | Single user at 500 req/min | Allowed vs rejected |

## Saturation Criteria

A load level is **sustainable** when:

- p99 latency < **100 ms**
- Error rate (non-429 failures) < **1%**

**Max sustainable RPS** = highest *actual* throughput meeting both criteria.

**Collapse point** = first target RPS where p99 exceeds 100 ms or errors exceed 1%.

## Resource Metrics

During each test, `metrics/collect-metrics.ps1` samples `docker stats` every 5 seconds for:

- `rate-limiter` CPU / memory
- `rate-sidecar` CPU / memory
- `rate-redis` CPU / memory

Results saved to `metrics/results/{test-name}.json`.

## Important: Target vs Actual RPS

k6 sends at a *target* rate, but the system may not keep up. **Actual RPS** (requests completed / 60s) reveals true capacity:

```
Target: 10,000 RPS → Actual: 1,082 RPS  ← system saturated
```

Graphs use **actual RPS** on the x-axis where possible.

## Running Benchmarks

```powershell
# Full suite with metrics
.\benchmarks\run-all.ps1

# Saturation sweep only
.\benchmarks\run-saturation.ps1

# Parse + graph existing results
python benchmarks/parse-results.py
python benchmarks/graphs/generate-graphs.py
```

## Output

| File | Description |
|------|-------------|
| `summary.md` | Human-readable results table |
| `environment.md` | Machine specs |
| `graphs/latency-vs-rps.png` | Latency vs actual throughput |
| `graphs/saturation-curve.png` | Target vs actual RPS |
| `graphs/resource-utilization.png` | CPU vs actual RPS |
| `graphs/enforcement-allowed-vs-rejected.png` | Rate limit correctness |
