# Benchmark Reproducibility

This document enables any engineer to repeat benchmarks on the same commit and regenerate artifacts like `bench-progress.log`.

---

## Pin commit & environment

```powershell
git checkout a1de9ecb612734840c28ea2311a8862b20e7732b
docker compose up --build -d
```

Capture environment:

```powershell
# Manual or via collect script
go version > benchmarks/results/a1de9ec-final/environment.txt
docker version >> benchmarks/results/a1de9ec-final/environment.txt
k6 version >> benchmarks/results/a1de9ec-final/environment.txt
```

Reference `benchmarks/results/a1de9ec-final/environment.txt` for full hardware snapshot.

---

## Full targeted suite (recommended)

```powershell
powershell -ExecutionPolicy Bypass -File benchmarks/final/run-targeted-benchmarks.ps1
```

Writes progress lines to:

```
benchmarks/results/bench-progress.log
```

Raw k6 JSON streams + summaries:

```
benchmarks/results/a1de9ec-final/raw/
  direct-sliding-100-stream.json
  direct-sliding-100-summary.json
  direct-sliding-100-rerun-stream.json    # after anomaly rerun
  direct-sliding-5000-stream.json
  direct-token-1000-stream.json
  direct-token-5000-stream.json
  hierarchical-1000-stream.json
  sidecar-e2e-100-stream.json
  sidecar-e2e-1000-stream.json
  sidecar-e2e-5000-stream.json
  denial-cache-stream.json
  singleflight-stream.json
  multi-replica-500-stream.json
  idempotency-race-stream.json
  soak-15m-stream.json
  ...
```

Command transcript:

```
benchmarks/results/a1de9ec-final/commands.txt
```

---

## Individual workloads

```powershell
# Direct limiter — sliding (default compose :8080)
k6 run -e TARGET_RPS=100 benchmarks/scripts/direct-limiter.js
k6 run -e TARGET_RPS=1000 benchmarks/scripts/direct-limiter.js
k6 run -e TARGET_RPS=5000 benchmarks/scripts/direct-limiter.js

# Direct limiter — token bucket (:8085 container)
k6 run -e TARGET_RPS=1000 -e LIMITER_URL=http://localhost:8085 benchmarks/scripts/direct-limiter.js
k6 run -e TARGET_RPS=5000 -e LIMITER_URL=http://localhost:8085 benchmarks/scripts/direct-limiter.js

# Hierarchical
k6 run -e TARGET_RPS=1000 benchmarks/scripts/hierarchical-limiter.js

# Sidecar end-to-end (production path)
k6 run -e TARGET_RPS=100 benchmarks/scripts/sidecar-e2e.js
k6 run -e TARGET_RPS=1000 benchmarks/scripts/sidecar-e2e.js
k6 run -e TARGET_RPS=5000 benchmarks/scripts/sidecar-e2e.js

# Correctness / specialty
k6 run benchmarks/scripts/denial-cache.js
k6 run benchmarks/scripts/singleflight.js
k6 run -e TARGET_RPS=500 benchmarks/scripts/multi-replica-e2e.js
k6 run benchmarks/scripts/idempotency-race.js   # expect 422 — use runtime test instead
k6 run -e TARGET_RPS=300 -e DURATION=15m benchmarks/scripts/soak.js
```

Multi-replica requires second sidecar on **:9092** (see `commands.txt`).

---

## Parse stream → bench-progress line

```powershell
python benchmarks/scripts/parse-k6-stream.py benchmarks/results/a1de9ec-final/raw/sidecar-e2e-1000-stream.json 70
```

Output format:

```
sidecar-e2e-1000|total=61002 rps=871.5 p50=4.86 p95=8.21 p99=11.21 ...
```

Append to `benchmarks/results/bench-progress.log` manually or via suite script.

---

## Idempotency runtime proof (valid test)

k6 `idempotency-race.js` uses invalid keys → 422. Use documented runtime burst:

```powershell
# 2 sidecars (9090 + 9092), 40 parallel POST, same GUID Idempotency-Key
# Expected: 1×200, 39×409
```

See `docs/testing/concurrency-and-race-testing.md` and `benchmarks/scripts/idempotency-race.js` (fix key format before trusting k6 row).

---

## direct-sliding-100 anomaly rerun

If first run shows errors + 35 s p99:

1. Ensure clean stack: `docker compose restart limiter redis`
2. Wait for `/health` 200
3. Rerun:

```powershell
k6 run -e TARGET_RPS=100 benchmarks/scripts/direct-limiter.js `
  --out json=benchmarks/results/a1de9ec-final/raw/direct-sliding-100-rerun-stream.json
python benchmarks/scripts/parse-k6-stream.py benchmarks/results/a1de9ec-final/raw/direct-sliding-100-rerun-stream.json 70
```

Expected rerun: **rps≈100, p99<10 ms, errors=0**.

---

## Regression tests (non-k6)

```powershell
go test -count=1 ./...
go test -count=1 -race ./...
go vet ./...
go build ./...
```

---

## Artifact checklist

| Artifact | Path |
|----------|------|
| Progress log | `benchmarks/results/bench-progress.log` |
| Raw k6 streams | `benchmarks/results/a1de9ec-final/raw/*-stream.json` |
| k6 summaries | `benchmarks/results/a1de9ec-final/raw/*-summary.json` |
| Environment | `benchmarks/results/a1de9ec-final/environment.txt` |
| Commands | `benchmarks/results/a1de9ec-final/commands.txt` |
| Analysis docs | `docs/benchmarks/performance-analysis.md` |
| Methodology | `docs/benchmarks/methodology.md` |
| Executive report | `docs/benchmarks/final-benchmark-report.md` |

Legacy duplicate run folder (optional):

```
benchmarks/final/results/a1de9ec-2026-07-09-2351/
```

---

## Prerequisites

| Tool | Version (reference run) |
|------|-------------------------|
| Go | 1.26.1 |
| Docker | 29.5.2 |
| k6 | 1.7.1 |
| Python | 3.x (parser scripts) |
| Redis | 7.4.7 (compose image) |

Ports free: **8080** limiter, **9090** sidecar, **6379** redis, **8081** demo, **8085** token limiter, **9092** second sidecar (multi-replica).

---

## Related

- [methodology.md](methodology.md) — sustainable definition + warmup
- [performance-analysis.md](performance-analysis.md) — number interpretation
