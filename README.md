# Distributed Traffic Control Platform

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A cloud-native distributed traffic control platform built with Go, Redis, Lua, Docker, and Prometheus.

This project explores the architecture, consistency guarantees, performance characteristics, and operational challenges behind production-grade rate limiting systems used in API gateways, service meshes, multi-tenant SaaS platforms, and large-scale backend infrastructure.

The platform supports multiple rate limiting algorithms, hierarchical quota enforcement, runtime configuration management, sidecar-based deployment, observability, chaos engineering validation, and an automated benchmarking framework capable of identifying saturation points and measuring system behavior under load.

---

# Overview

Traditional in-memory rate limiters work only within a single application instance.

Once traffic is distributed across multiple servers, maintaining consistent quotas becomes significantly harder due to:

* Concurrent updates
* Race conditions
* Stale state
* Lost writes
* Network latency
* Distributed coordination overhead

This project addresses those challenges through:

* Redis-backed distributed state
* Atomic Lua script execution
* Hierarchical quota enforcement
* Sidecar-based request interception
* Runtime configuration updates
* Benchmark-driven validation
* Production-oriented observability
* Chaos engineering testing

---

# Key Features

## Rate Limiting Algorithms

Implemented multiple algorithms to compare deployment trade-offs and operational characteristics:

* In-Memory Token Bucket
* Redis Token Bucket
* Redis Atomic Token Bucket (Lua)
* Sliding Window Rate Limiter
* Hierarchical Multi-Level Rate Limiter

---

## Distributed Enforcement

All distributed quota updates execute through Redis Lua scripts.

Benefits:

* Atomic execution
* No race conditions
* No lost updates
* Consistent quota enforcement
* Single network round trip

---

## Hierarchical Quota Enforcement

Supports enforcement across multiple dimensions:

```text
Global
 └── Tenant
      └── User
           └── Endpoint
```

A request is accepted only when all quota levels have available capacity.

Example use cases:

* Platform-wide protection
* Multi-tenant SaaS isolation
* User abuse prevention
* Expensive endpoint protection

---

## Sidecar-Based Deployment

Traffic management is isolated from application business logic through a dedicated sidecar proxy.

Responsibilities:

* Request interception
* Denial-only caching
* Singleflight request deduplication
* Traffic forwarding
* Failure handling
* Path allowlisting
* TLS support

Backend services remain completely unaware of rate limiting implementation details.

---

## Dynamic Configuration

Runtime quota updates without service restarts.

Supported overrides:

* User-level limits
* Tenant-level limits
* Endpoint-level limits

Changes become effective immediately through the Admin API.

---

## Observability

Operational tooling includes:

* Prometheus metrics
* Health endpoints
* Docker health checks
* Request latency tracking
* Error monitoring
* Structured logging

---

## Reliability Engineering

Validated through fault injection and chaos testing.

Scenarios tested:

* Redis failures
* Network partitions
* High latency injection
* Concurrent traffic bursts
* Dependency degradation

---

# Architecture

```text
                           ┌─────────────┐
                           │   Client    │
                           └──────┬──────┘
                                  │
                                  ▼
                ┌─────────────────────────────────┐
                │          Sidecar Proxy          │
                │                                 │
                │ • Denial Cache                  │
                │ • Singleflight                  │
                │ • Path Allowlist                │
                │ • TLS Support                   │
                └───────────────┬─────────────────┘
                                │
                                ▼
                ┌─────────────────────────────────┐
                │     Central Limiter Service     │
                │                                 │
                │ • Token Bucket                  │
                │ • Sliding Window                │
                │ • Hierarchical Limits           │
                │ • Override Engine               │
                │ • Admin API                     │
                │ • Prometheus Metrics            │
                └───────────────┬─────────────────┘
                                │
                                ▼
                ┌─────────────────────────────────┐
                │              Redis              │
                │                                 │
                │ • Lua Scripts                   │
                │ • Bucket State                  │
                │ • Override Storage              │
                │ • Quota Tracking                │
                └─────────────────────────────────┘
```

---

# Request Lifecycle

## Allowed Request

```text
Client
   │
   ▼
Sidecar
   │
   ▼
Limiter
   │
   ▼
Redis Lua Script
   │
   ▼
Allowed
   │
   ▼
Backend
```

---

## Rejected Request

```text
Client
   │
   ▼
Sidecar
   │
   ▼
Limiter
   │
   ▼
Redis Lua Script
   │
   ▼
Quota Exceeded
   │
   ▼
HTTP 429
```

---

# Rate Limiting Algorithms

## In-Memory Token Bucket

Fastest implementation.

Characteristics:

* Process-local state
* No distributed coordination
* Suitable for single-node deployments

---

## Redis Token Bucket

Centralized state management.

Characteristics:

* Shared quota storage
* Distributed enforcement
* Network round-trip overhead

---

## Redis Atomic Token Bucket

Production implementation.

Characteristics:

* Redis-backed
* Lua-scripted
* Atomic updates
* No race conditions

---

## Sliding Window

Improves fairness compared to fixed windows.

Characteristics:

* Accurate request tracking
* Higher storage cost
* Better burst handling

---

## Hierarchical Limiter

Multi-level quota enforcement.

Supports:

* Global quotas
* Tenant quotas
* User quotas
* Endpoint quotas

---

# Performance Engineering

The platform includes a reproducible benchmarking framework built using:

* k6
* Docker metrics collection
* Automated analysis
* Graph generation
* Saturation detection

The benchmark suite evaluates the system across four dimensions:

| Test                   | Goal                                     |
| ---------------------- | ---------------------------------------- |
| Throughput             | Latency under increasing traffic         |
| Saturation Sweep       | Determine maximum sustainable throughput |
| Hot-Key Contention     | Stress Redis with concentrated traffic   |
| Enforcement Validation | Verify correctness                       |

---

# Benchmark Methodology

## Throughput Tests

Executed at:

* 100 RPS
* 1,000 RPS
* 5,000 RPS
* 10,000 RPS

Each request uses a unique user identifier.

Purpose:

* Measure raw system capacity
* Avoid triggering rate limits
* Isolate infrastructure performance

Metrics collected:

* p50 latency
* p95 latency
* p99 latency
* Actual throughput
* CPU utilization
* Memory utilization
* Error rate

---

## Saturation Sweep

Additional tests executed at:

* 1,500 RPS
* 2,000 RPS
* 2,500 RPS
* 3,000 RPS
* 3,500 RPS
* 4,000 RPS

Purpose:

* Identify collapse point
* Measure degradation behavior
* Determine sustainable throughput

---

## Sustainable Throughput Criteria

A workload is considered sustainable when:

* p99 latency < 100ms
* Non-rate-limit error rate < 1%

Maximum sustainable throughput is the highest actual throughput satisfying both conditions.

---

## Hot-Key Contention Test

Configuration:

* 5,000 RPS
* 10 active users

Purpose:

* Redis contention testing
* Sidecar cache validation
* Fast rejection verification
* Hot-key behavior analysis

429 responses are expected and considered successful enforcement.

---

## Enforcement Validation

Configuration:

* Single user
* 500 requests/minute

Purpose:

* Validate correctness
* Verify quota enforcement
* Measure rejection latency

Expected outcome:

* Initial requests allowed
* Remaining requests rejected

---

# Benchmark Toolchain

```text
docker compose up
        │
        ▼
k6 Load Generation
        │
        ▼
NDJSON Benchmark Results
        │
        ▼
Docker Resource Metrics
        │
        ▼
Automated Analysis
        │
        ▼
Graph Generation
        │
        ▼
Benchmark Reports
```

---

# Project Structure

```text
.
├── benchmarks/
│   ├── throughput/
│   ├── saturation/
│   ├── hot-key/
│   ├── enforcement/
│   ├── metrics/
│   ├── graphs/
│   ├── parse-results.py
│   ├── run-all.ps1
│   ├── run-saturation.ps1
│   ├── methodology.md
│   ├── environment.md
│   └── summary.md
│
├── chaos/
│   ├── chaos_test.ps1
│   ├── high_latency.py
│   └── network_partition.py
│
├── cmd/
│   ├── limiter/
│   ├── sidecar/
│   └── demo-backend/
│
├── deploy/
│   └── prometheus.yml
│
├── dockerfiles/
│
├── internal/
│   ├── auth/
│   ├── identity/
│   ├── limiter/
│   ├── metrics/
│   ├── override/
│   └── redis/
│
├── tests/
│
├── docker-compose.yml
└── README.md
```

---

# Getting Started

## Prerequisites

* Go 1.24+
* Docker
* Docker Compose
* Redis
* Python 3.10+
* k6

---

## Clone Repository

```bash
git clone https://github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter.git

cd Distributed-Rate-Limiter
```

---

## Start Platform

```bash
docker compose up -d --build
```

Verify service:

```bash
curl "http://localhost:9090/check?user_id=alice"
```

Health endpoint:

```bash
curl http://localhost:8080/health
```

Metrics endpoint:

```bash
curl http://localhost:8080/metrics
```

Shutdown:

```bash
docker compose down -v
```

---

# Running Tests

## Unit Tests

```bash
go test ./...
```

---

## Race Detection

```bash
go test -race ./...
```

---

## Coverage

```bash
go test -cover ./...
```

---

## Benchmark Tests

```bash
go test -bench=. ./...
```

---

# Running Benchmarks

## Full Benchmark Suite

Windows:

```powershell
.\benchmarks\run-all.ps1
```

---

## Saturation Sweep

Windows:

```powershell
.\benchmarks\run-saturation.ps1
```

---

## Throughput Test

```bash
k6 run `
-e TARGET_RPS=1000 `
benchmarks/throughput/throughput-test.js `
--out json=benchmarks/throughput/results/1000.json
```

---

## Hot-Key Test

```bash
k6 run benchmarks/hot-key/hot-key-test.js
```

---

## Enforcement Test

```bash
k6 run benchmarks/enforcement/enforcement-test.js
```

---

## Parse Results

```bash
python benchmarks/parse-results.py
```

---

## Generate Graphs

```bash
python benchmarks/graphs/generate-graphs.py
```

---

## Collect Environment Information

```powershell
.\benchmarks\collect-environment.ps1
```

---

# Chaos Engineering

## Redis Failure

```powershell
.\chaos\chaos_test.ps1
```

---

## Network Partition

```bash
python chaos/network_partition.py
```

---

## High Latency Injection

```bash
python chaos/high_latency.py
```

---

# Admin API

## Create User Override

```bash
curl -X POST \
http://localhost:8082/admin/limits/user/alice \
-H "Content-Type: application/json" \
-d '{
  "capacity": 20,
  "refill_rate": 2
}'
```

---

## Retrieve Override

```bash
curl http://localhost:8082/admin/limits/user/alice
```

---

## Delete Override

```bash
curl -X DELETE \
http://localhost:8082/admin/limits/user/alice
```

Equivalent APIs are available for tenants and endpoints.

---

# Technical Challenges

## Distributed Consistency

Concurrent requests must never corrupt quota state.

Solution:

* Redis Lua scripts
* Atomic execution
* Single update path

---

## Hierarchical Enforcement

Requests may satisfy one quota while violating another.

Solution:

* Multi-level validation
* Unified execution path

---

## Cache Correctness

Caching successful requests risks stale quota decisions.

Solution:

* Denial-only caching
* Correctness-first design

---

## Metrics Cardinality

Per-user metrics can overwhelm monitoring systems.

Solution:

* Bounded label design
* Controlled cardinality
  
---

# Trade-Offs

| Decision              | Benefit                 | Cost                   |
| --------------------- | ----------------------- | ---------------------- |
| Denial-only cache     | Strong correctness      | More limiter traffic   |
| Redis-backed state    | Distributed consistency | External dependency    |
| Single Redis instance | Simpler deployment      | No HA                  |
| Sidecar architecture  | Separation of concerns  | Additional network hop |
| Runtime overrides     | Operational flexibility | Added complexity       |

---

# Tech Stack

## Backend

* Go

## Distributed State

* Redis
* Lua

## Infrastructure

* Docker
* Docker Compose

## Observability

* Prometheus

## Performance Testing

* k6

## Testing

* Go Testing
* Chaos Engineering Scripts

## Analysis

* Python
* Matplotlib

---

# Future Improvements

* Redis Sentinel support
* Redis Cluster deployment
* OpenTelemetry tracing
* Grafana dashboards
* Helm charts
* Kubernetes deployment
* Horizontal autoscaling
* Multi-region quota synchronization

---

# License

Licensed under the MIT License.
