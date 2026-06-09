# Benchmark Environment

> Auto-detected on this machine. Re-run `collect-environment.ps1` before each benchmark session.

| Component | Version / Spec |
|-----------|----------------|
| **CPU** | Intel Core i9-14900HX (24 cores / 32 threads) |
| **RAM** | 32 GB |
| **OS** | Windows 11 Home |
| **Docker** | 29.5.2 |
| **Go** | 1.25.0 (module) / 1.26.1 (toolchain) |
| **Redis** | 7.4.9 (redis:7-alpine container) |
| **k6** | v1.7.1 |

## Stack Configuration

| Service | Port | Notes |
|---------|------|-------|
| Sidecar | 9090 | Benchmark entry point |
| Limiter | 8080 | Central rate limiter |
| Redis | 6379 | Quota state |
| Demo | 8081 | Upstream backend |

## Rate Limiter Settings (docker-compose)

- Algorithm: sliding window
- Per-user capacity: 10 req / 60s window
- Sidecar rate limit: 10 req/min per user

## Why Environment Matters

Benchmark numbers are only meaningful relative to hardware. A system sustaining 1,000 RPS on a laptop may sustain 10,000+ RPS on a 32-core server. Always report environment alongside results.
