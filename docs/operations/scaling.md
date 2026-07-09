# Horizontal Scaling

**Source:** `docker-compose.scale.yml`

---

## Purpose

Multi-replica proof overlay — adds a second limiter and sidecar sharing the same Redis and network without changing default single-instance compose behavior.

---

## Usage

```bash
docker compose -f docker-compose.yml -f docker-compose.scale.yml --profile scale up --build
```

Profile name: **`scale`**.

---

## Services added

| Service | Host ports | Internal |
|---------|------------|----------|
| `limiter-b` | `8083→8080`, `8084→8082` (admin) | Shares `redis:6379` |
| `sidecar-b` | **`9092→9090`** | Points at `http://limiter:8080` |

Both replicas use the same env pattern as primary services (sliding algorithm, hierarchical limits, audit, internal API keys).

---

## Port note: 9092 not 9091

`sidecar-b` maps host port **9092** → container `9090`.

Comment in compose:

> `# 9091 is Prometheus in base compose`

Base `docker-compose.yml` already binds **9091** for Prometheus. Second sidecar must use **9092** on the host to avoid a port conflict.

Benchmark multi-replica scripts target both sidecars (e.g. `:9090` and `:9092`).

---

## Shared state

- **Redis** — single authoritative quota store; all limiter replicas execute the same Lua scripts against shared keys.
- **Limiter replicas** — horizontally scalable; no in-process quota state.
- **Sidecar replicas** — independent denial caches and singleflight groups; allowances always re-check central limiter.

Correctness under multi-replica load: `benchmarks/scripts/multi-replica-e2e.js`, `docs/correctness/multi-replica-correctness.md`.

---

## Health checks

Each replica has its own `/health`:

- Limiter-b: `http://localhost:8083/health`
- Sidecar-b: `http://localhost:9092/health`

Sidecar-b health still probes primary `limiter:8080` (compose `RATE_LIMITER_URL`), not `limiter-b` — adjust env if testing isolated limiter pairs.

---

## When to scale

| Component | Scale when | Caveat |
|-----------|------------|--------|
| Sidecar | Edge RPS / connection count | Cache is per-process |
| Limiter | Central check RPS exceeds single replica | Redis becomes bottleneck |
| Redis | Lua CPU or memory | Vertical scale or sharding (not built-in) |

See `docs/benchmarks/final-benchmark-report.md` for measured RPS ceilings on localhost Docker.
