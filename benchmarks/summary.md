# Benchmark Summary (run on 8‑core, 16GB RAM)

| Test | Target RPS | p99 Latency | Error Rate | Notes |
|------|------------|-------------|------------|-------|
| Throughput | 100 | 11ms | 0% | Pass |
| Throughput | 1000 | 3.2ms | 0% | Pass |
| Throughput | 5000 | 3.5s | 10% | Hardware limit |
| Throughput | 10000 | 4.3s | 15% | Hardware limit |
| Hot‑key | 5000 | – | 99.9% (429) | Correct |
| Enforcement | 500/min | – | 98% (429) | Correct |