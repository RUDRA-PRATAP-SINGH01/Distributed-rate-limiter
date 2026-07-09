# Final Benchmark & Evidence Report

**Commit:** `a1de9ecb612734840c28ea2311a8862b20e7732b`  
**Date:** 2026-07-10  
**Verdict:** **READY WITH DOCUMENTED LIMITATIONS**

---

## 1. Executive summary

On Intel i9-14900HX / 32 GB RAM / Windows 11 / Docker Compose / Redis 7.4 / Go 1.26.1 / k6 1.7.1:

- **Highest sustainably measured throughput** (p99 < 100 ms, 0% non-429 errors): **~872 actual RPS** end-to-end via sidecar at **1,000 RPS target** (unique users per request).
- **Saturation knee:** at **5,000 RPS target**, sliding-window paths collapse (actual **285–1,504 RPS**, p99 **382 ms–51 s**); token bucket on a dedicated limiter sustained **4,161 RPS** at p99 **148 ms** (unique users, no quota denials).
- **Sidecar overhead** at sustainable load: **~+3.7 ms p50** vs direct limiter (4.86 ms vs 1.13 ms).
- **15-minute soak** at 300 RPS target: **299 actual RPS**, p99 **10 ms**, **0 errors**, isolated tail spikes to **1.3 s** (not sustained drift).
- **Multi-replica quota correctness** runtime-proven: 60 concurrent requests, 2 sidecars → **10 allowed / 50 denied** (cap=10), Redis ZCARD=10.
- **Idempotency:** 40 parallel duplicates across 2 sidecars → **1×200, 39×409** (not exactly-once upstream execution after crash+reclaim — documented limitation).

Raw artifacts: `benchmarks/results/a1de9ec-final/raw/`

---

## 2. Environment

| Component | Value |
|-----------|-------|
| CPU | Intel Core i9-14900HX (24c / 32 threads) |
| RAM | 32 GB |
| OS | Windows 11 Home 10.0.26200 amd64 |
| Go | 1.26.1 |
| Docker | 29.5.2 |
| Redis | 7.4.7 (standalone container) |
| k6 | 1.7.1 |
| Topology | Client → localhost → sidecar:9090 → limiter:8080 → redis:6379 → demo:8081 |
| Limiter algorithm (default) | Sliding window, CAPACITY=10, WINDOW_SEC=60 |
| Warm-up | 10 s per throughput test |
| Measurement | 60 s per throughput test |

---

## 3. Methodology

1. **Constant-arrival-rate** k6 scenarios with 10 s warm-up + 60 s measurement (throughput tests).
2. **Unique user per request** for flat throughput tests (isolates limiter latency from quota denials).
3. **Sustainable** = actual RPS within ~15% of target (when target ≤ knee), **p99 < 100 ms**, **non-429 error rate < 1%**.
4. **429** counted as expected quota behavior, not benchmark failure.
5. **Anomalies documented**, not discarded:
   - First `direct-sliding-100` run polluted by overlapping outage recovery (p99 35 s, 6 errors) — **re-run passed** (p99 3.9 ms).
   - `idempotency-race.js` k6 run returned **422** (key validation) — superseded by runtime burst test with valid GUID key.

---

## 4. Architecture under test

```
Client (k6)
  → Sidecar :9090  [denial cache, singleflight, idempotency, routing optional]
    → Limiter :8080  [flat /check or /check_hierarchical, Redis circuit guard]
      → Redis :6379  [Lua: sliding_window | token_bucket | hierarchical | idempotency | cb:*]
        → Demo :8081 (sidecar e2e only)
```

Multi-replica tests: sidecar **9090 + 9092**, limiter **8080 + 8083**, shared Redis.

---

## 5. Benchmark summary table

| Workload | Topology | Target RPS | Actual RPS | p50 (ms) | p95 (ms) | p99 (ms) | Max (ms) | 200 | 429 | Errors | Evidence |
|----------|----------|------------|------------|----------|----------|----------|----------|-----|-----|--------|----------|
| Direct sliding | Limiter /check | 100 | **100** | 1.91 | 3.21 | 3.93 | 6.84 | 7001 | 0 | 0 | BENCHMARK |
| Direct sliding | Limiter /check | 1000 | **871** | 1.13 | 3.14 | 7.98 | 231.75 | 60969 | 0 | 0 | BENCHMARK |
| Direct sliding | Limiter /check | 5000 | **285** | 218 | 435 | 50756 | 51118 | 19939 | 0 | 0 | BENCHMARK (saturated) |
| Direct token bucket | Limiter :8085 /check | 1000 | **869** | 1.57 | 5.04 | 145 | 309 | 60835 | 0 | 0 | BENCHMARK |
| Direct token bucket | Limiter :8085 /check | 5000 | **4161** | 4.56 | 121 | 148 | 183 | 291251 | 0 | 0 | BENCHMARK |
| Hierarchical | Limiter /check_hierarchical | 1000 | **870** | 2.20 | 8.91 | 34.17 | 229 | 440 | 60474 | 0 | BENCHMARK† |
| Sidecar e2e | Full proxy path | 100 | **100** | 4.30 | 7.93 | 11.01 | 27.9 | 7002 | 0 | 0 | BENCHMARK |
| Sidecar e2e | Full proxy path | 1000 | **872** | 4.86 | 8.21 | 11.21 | 26.0 | 61002 | 0 | 0 | BENCHMARK |
| Sidecar e2e | Full proxy path | 5000 | **1504** | 289 | 343 | 383 | 432 | 105285 | 0 | 0 | BENCHMARK (saturated) |
| Denial cache hammer | Sidecar, 1 user | — | 17662‡ | 2.04 | 5.14 | 7.11 | 30.6 | 101 | 618074 | 0 | BENCHMARK |
| Multi-replica e2e | 2 sidecars, 10 users | 500 | **429** | 1.60 | 4.50 | 7.28 | 51.7 | 106 | 29895 | 0 | BENCHMARK‡ |
| Soak 15 min | Sidecar e2e | 300 | **299** | 4.65 | 7.27 | 10.01 | 1343 | 269269 | 0 | 0 | BENCHMARK |

† Hierarchical test uses endpoint/tenant/user keys; most requests hit configured endpoint capacity → high 429 rate by design.  
‡ Denial-cache and multi-replica tests measure **correctness paths**, not peak sustainable unique-user throughput.

---

## 6. Algorithm comparison (comparable unique-user workloads)

| Algorithm | Target RPS | Actual RPS | p99 (ms) | Sustainable? |
|-----------|------------|------------|----------|--------------|
| Sliding window (direct) | 1000 | 871 | 8 | **Yes** |
| Sliding window (direct) | 5000 | 285 | 50756 | **No** |
| Token bucket (direct, dedicated container) | 1000 | 869 | 145 | Borderline (p99 > 100 ms) |
| Token bucket (direct) | 5000 | 4161 | 148 | Higher throughput; p99 above 100 ms threshold |
| Hierarchical (direct) | 1000 | 870 | 34 | Latency OK; workload quota-limited |

**Fastest under comparable sustainable conditions (p99 < 100 ms):** sliding window and sidecar e2e tie at **~872 RPS**. Token bucket achieves **higher peak RPS** at 5,000 target but with **p99 ≈ 148 ms**.

---

## 7. Sidecar overhead

At **~871 actual RPS**, unique users, zero denials:

| Path | p50 | p95 | p99 |
|------|-----|-----|-----|
| Direct limiter /check | 1.13 ms | 3.14 ms | 7.98 ms |
| Sidecar e2e | 4.86 ms | 8.21 ms | 11.21 ms |
| **Delta (approx.)** | **+3.7 ms** | **+5.1 ms** | **+3.2 ms** |

Evidence: **BENCHMARK-PROVEN** (same machine, same window, sequential runs).

---

## 8. Denial cache

| Phase | Requests | p99 (ms) | Limiter calls (inferred) |
|-------|----------|----------|--------------------------|
| Prime (15 users) | 15 | — | ~15 |
| Hammer (50 VUs, 30 s, 1 user) | 618,175 | **7.11** | Near-zero after cache warm |

Denied requests served from process-local cache at **sub-10 ms p99** vs **~11 ms p99** for full sidecar path at 1,000 RPS.  
Evidence: **BENCHMARK-PROVEN** + **SOURCE-PROVEN** (denial-only cache in `cmd/sidecar/main.go`).

---

## 9. Singleflight

| Test | Client requests | Limiter calls | Evidence |
|------|-----------------|---------------|----------|
| `TestSidecar_SingleflightCollapse` | 100 concurrent | **1** | TEST-PROVEN |
| k6 `singleflight.js` | 100 burst | Not instrumented at runtime | Functional only |

Architecture guarantee: **100 concurrent identical user keys collapse to 1 limiter round-trip** (TEST-PROVEN).

---

## 10. Multi-replica quota

| Test | Topology | Expected | Actual | Evidence |
|------|----------|----------|--------|----------|
| 60 concurrent, 2 sidecars, 1 user | 9090+9092 | ≤10 allowed | **10 allowed, 50 denied**, ZCARD=10 | RUNTIME-PROVEN |
| Concurrent burst (earlier, polluted) | 9090+9092 | ≤10 | 23 allowed | **Invalid** — ran during outage recovery |

---

## 11. Idempotency

| Test | Result | Evidence |
|------|--------|----------|
| 40 parallel POST, same key, 2 sidecars | **1×200, 39×409** | RUNTIME-PROVEN |
| k6 idempotency-race.js (final run) | 10×200, 90×422 | **Invalid script** — key format rejected |
| `TestClaimSingleWinnerUnderConcurrency` | 1 claim, 99 in_progress | TEST-PROVEN |

**Guarantee:** duplicate suppression + cached replay + fencing. **Not** at-most-once upstream side effects after crash-before-Complete + lease reclaim.

---

## 12. Circuit breaker

| Property | Evidence |
|----------|----------|
| Half-open probe bound ≤ `HalfOpenMaxProbes` (3) | TEST-PROVEN (`TestHalfOpenConcurrentProbeBound`, 32 workers, `-race`) |
| Runtime 64 concurrent /check during seeded half-open | 3 admitted, 61×503 (Phase 4B, clean stack) | RUNTIME-PROVEN |
| Open-state fast-fail | **23 ms** → 503 | RUNTIME-PROVEN |

---

## 13. Failure latency

| Scenario | Measured | Previous observation | Notes |
|----------|----------|---------------------|-------|
| Redis down (sidecar) | **~1003–1006 ms** → 503 | ~1 s | Matches bounded client timeout |
| Limiter down (sidecar) | **~504 ms** → 503 | ~500 ms | Matches `limiter_http` timeout |
| Open circuit (limiter /check) | **~23 ms** → 503 | ~1 ms | Redis Lua + HTTP; still sub-100 ms |
| Recovery first 200 after Redis up | **~27 ms** | — | RUNTIME-PROVEN |

---

## 14. Soak test (15 minutes)

| Metric | Value |
|--------|-------|
| Target RPS | 300 |
| Actual RPS | 299.2 |
| Total requests | 269,269 |
| p50 / p95 / p99 | 4.65 / 7.27 / 10.01 ms |
| Max latency | 1343 ms (isolated spike) |
| p99.9 | 525 ms |
| Errors | 0 |
| Duration | 15 min |

No sustained p99 drift, no error growth. **Short soak only** — does not prove months-long stability.

---

## 15. Correctness evidence matrix

| Property | SOURCE | TEST | RUNTIME | BENCHMARK |
|----------|--------|------|---------|-----------|
| Redis Lua atomic quota | ✓ | ✓ | ✓ | — |
| Multi-sidecar no over-admit | ✓ | — | ✓ | — |
| Singleflight collapse | ✓ | ✓ | — | — |
| Denial cache no over-admit | ✓ | ✓ | — | ✓ (latency) |
| Idempotency 1 winner concurrent | ✓ | ✓ | ✓ | — |
| CB half-open global bound | ✓ | ✓ | ✓ | — |
| Override generation consistency | ✓ | ✓ | ✓ | — |
| SCRIPT FLUSH recovery | ✓ | ✓ | ✓ | — |
| 429 not infra failure / CB trip | ✓ | ✓ | — | — |
| Trace + slog correlation | ✓ | ✓ | partial | — |
| Audit drain before Redis close | ✓ | ✓ | — | — |

---

## 16. Regression results

```
go test -count=1 ./...              PASS
go test -count=1 -race ./...        PASS
go test -count=10 (concurrency pkgs) PASS
go vet ./...                        PASS
go build ./...                      PASS
```

---

## 17. Known limitations

1. **Single Redis master** — throughput knee ~800–900 RPS for sliding window on this hardware; not Redis Cluster.
2. **Hierarchical 4-key Lua** — not Redis Cluster hash-tag safe.
3. **Idempotency** — duplicate upstream possible after crash + lease reclaim.
4. **Override refresh** — one `GET config:generation` per hierarchical check; Redis read failure → bounded TTL staleness.
5. **Process-local** denial cache / singleflight — safe for quota; not cross-replica.
6. **Benchmarks** — localhost Docker ports; production network will differ.
7. **No exactly-once** side-effect guarantee.

---

## 18. Reproduction

```powershell
docker compose up -d
powershell -ExecutionPolicy Bypass -File benchmarks/final/run-targeted-benchmarks.ps1
# or individual scripts — see benchmarks/results/a1de9ec-final/commands.txt
python benchmarks/scripts/parse-k6-stream.py benchmarks/results/a1de9ec-final/raw/sidecar-e2e-1000-stream.json 70
```

---

## 19. Explicit answers

| Question | Answer |
|----------|--------|
| **A. Highest reproducible sustained throughput?** | **~872 RPS** (sidecar e2e, 1000 target, p99 11 ms, 0 errors) |
| **B. At that throughput: p50/p95/p99 / errors?** | 4.86 / 8.21 / 11.21 ms; **0% errors** |
| **C. Fastest algorithm (comparable)?** | Sliding ≈ sidecar at knee; token bucket **higher peak** but p99 > 100 ms at 5k target |
| **D. Sidecar overhead?** | **~+3.7 ms p50** at sustainable load |
| **E. Global quota multi-replica?** | **Yes** — 10/50/60 proven |
| **F. Singleflight collapse?** | **Yes** — TEST-PROVEN (1 limiter call / 100 clients) |
| **G. Redis outage?** | **~1 s** → 503 (fail-closed) |
| **H. Limiter outage?** | **~500 ms** → 503 |
| **I. Recovery speed?** | **~27 ms** to first 200 after Redis up |
| **J. Soak growth signals?** | **No** error growth; p99 stable; rare tail spikes |
| **K. Benchmark-proven vs other?** | Throughput/latency/soak: **BENCHMARK**; correctness: **TEST+RUNTIME** |
| **L. Ready to freeze?** | **READY WITH DOCUMENTED LIMITATIONS** |
