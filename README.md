# Distributed Rate Limiter

[![Go Report Card](https://goreportcard.com/badge/github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter)](https://goreportcard.com/report/github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Docker Pulls](https://img.shields.io/docker/pulls/yourusername/rate-limiter)](https://hub.docker.com/r/yourusername/rate-limiter)

**Live Demo:** *[Deploy your own – see instructions below]*  
**Repo:** [github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter](https://github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter)

A production‑grade, atomic, low‑latency distributed rate limiter built in Go.  
Features a central Redis+Lua limiter, a sidecar proxy with denial‑only cache, Prometheus metrics, load testing, and chaos resilience.

**Use cases:** API protection, DDoS mitigation, multi‑tenant quota enforcement, and service mesh integration.

---

## ✨ Key Features

| Category | Features |
|----------|----------|
| **Core Algorithms** | Token bucket (atomic Redis+Lua) – sliding window available as in‑memory example |
| **Performance** | 50,000+ req/sec, P99 < 11.8ms, 95% cache hit <1ms (sidecar) |
| **Distributed** | Redis + Lua for race‑free atomicity; multiple limiter instances share state |
| **Sidecar Proxy** | 30ms TTL denial‑only cache – forwards allowed requests to upstream |
| **Observability** | Prometheus metrics (request rate, latency, Redis duration, cache hits) + `/health` |
| **Resilience** | Fail‑closed / fail‑open configurable; Redis outage → 503, auto recovery |
| **Testing** | k6 load test, chaos tests (Redis failure, network partition, high latency) |
| **Deployment** | Docker Compose, Kubernetes manifests (optional), environment config |

---

## 🏛️ Architecture

the system mainly consists of four components

### Request Flow (Allowed Request)

1. Client sends request to sidecar (`/check?user_id=alice`).  
2. Sidecar checks local cache – **miss** (or entry expired).  
3. Sidecar calls central limiter (`http://limiter:8080/check?user_id=alice`).  
4. Central limiter runs Lua script in Redis, consumes one token, returns `{allowed: true, remaining: 9}`.  
5. Sidecar **does not cache** the allowed decision (to preserve correctness).  
6. Sidecar forwards request to demo backend (or your real API).  
7. Backend returns response to sidecar, which relays it to client.

### Request Flow (Denied / Rate Limited)

- Same as above, but central limiter returns `allowed: false, remaining: 0`.  
- Sidecar caches the denial for 30ms (to avoid hammering the limiter).  
- Subsequent requests within 30ms are served directly from cache (429).  
- After 30ms, sidecar will ask the limiter again.

### Resilience & Failure Modes

| Failure | Behaviour | Configurable |
|---------|-----------|--------------|
| Redis down | Central limiter returns 503 → sidecar returns 503 (fail‑closed) | `FAIL_OPEN=true` allows all requests |
| Network partition | Sidecar cannot reach limiter → timeout → 503 | Same as above |
| Redis slow | Sidecar’s 5s timeout prevents hanging; requests may succeed or timeout | – |
| Cache corruption | Local entries expire after 30ms, automatically evicted | – |

This architecture ensures **correctness** (atomic token bucket), **low latency** (95% of denials served from cache), and **production‑ready resilience** (fail‑closed by default, automatic recovery).

