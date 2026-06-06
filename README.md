# Distributed Rate Limiter

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A cloud-native distributed rate limiting platform built in Go, Redis, and Lua.

The system provides distributed traffic control through multiple rate limiting algorithms, hierarchical quota enforcement, runtime configuration management, sidecar-based deployment, Prometheus observability, load testing, and chaos engineering validation.

Designed to explore architectural patterns commonly used in API gateways, service meshes, multi-tenant SaaS platforms, and large-scale backend systems.

---

## Overview

Traditional in-memory rate limiters work only within a single application instance. Once traffic is distributed across multiple servers, maintaining consistent quotas becomes significantly harder due to race conditions, stale state, and coordination overhead.

This project addresses those challenges through:

* Redis-backed distributed state
* Atomic Lua script execution
* Hierarchical quota enforcement
* Sidecar-based request interception
* Runtime configuration updates
* Production-oriented observability
* Failure testing through chaos engineering

---

## Features

### Rate Limiting Algorithms

Implemented multiple algorithms to compare trade-offs and deployment strategies:

* In-Memory Token Bucket
* Redis Token Bucket
* Redis Atomic Token Bucket (Lua)
* Sliding Window Rate Limiter
* Hierarchical Multi-Level Rate Limiter

---

### Distributed Enforcement

All distributed quota updates are executed through Redis Lua scripts to guarantee atomicity under concurrent traffic.

Benefits:

* No race conditions
* No lost updates
* Consistent quota enforcement
* Single round-trip execution

---

### Hierarchical Quota Management

Supports enforcement across four levels:

```text
Global
 └── Tenant
      └── User
           └── Endpoint
```

A request is permitted only if every level has available capacity.

Examples:

* Protect platform-wide traffic
* Limit individual tenants
* Control user abuse
* Restrict expensive endpoints

---

### Sidecar Proxy Architecture

A dedicated sidecar service sits in front of application services and handles rate limiting independently from business logic.

Responsibilities:

* Request interception
* Denial-only caching
* Singleflight request deduplication
* Traffic forwarding
* Failure handling
* Path filtering

This allows backend services to remain completely unaware of rate limiting implementation details.

---

### Dynamic Configuration

Runtime limits can be modified without restarting services.

Supported overrides:

* User-level limits
* Tenant-level limits
* Endpoint-level limits

Configuration changes become effective immediately through the Admin API.

---

### Observability

Integrated monitoring and operational tooling:

* Prometheus metrics
* Health endpoints
* Docker health checks
* Request latency tracking
* Error monitoring
* Structured logging

---

### Reliability Engineering

The system was tested under adverse conditions to validate operational behavior.

Scenarios tested:

* Redis failures
* Network partitions
* High latency injection
* Concurrent traffic bursts

---

## Architecture

```text
                        ┌─────────────┐
                        │   Client    │
                        └──────┬──────┘
                               │
                               ▼
                ┌─────────────────────────────┐
                │      Sidecar Proxy          │
                │                             │
                │ • Denial Cache              │
                │ • Singleflight              │
                │ • Path Allowlist            │
                │ • TLS Support               │
                └─────────────┬───────────────┘
                              │
                              ▼
                ┌─────────────────────────────┐
                │   Central Limiter Service   │
                │                             │
                │ • Token Bucket              │
                │ • Sliding Window            │
                │ • Hierarchical Limits       │
                │ • Admin API                 │
                │ • Prometheus Metrics        │
                └─────────────┬───────────────┘
                              │
                              ▼
                ┌─────────────────────────────┐
                │           Redis             │
                │                             │
                │ • Lua Scripts               │
                │ • Bucket State              │
                │ • Overrides                 │
                └─────────────────────────────┘
```

---

## Request Lifecycle

### Allowed Request

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

### Rejected Request

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

## Project Structure

```text
.
├── chaos/
│   ├── chaos_test.ps1
│   ├── high_latency.py
│   └── network_partition.py
│
├── cmd/
│   ├── demo-backend/
│   └── sidecar/
│
├── dockerfiles/
│   ├── Dockerfile.demo
│   ├── Dockerfile.limiter
│   └── Dockerfile.sidecar
│
├── internal/
│   ├── auth/
│   ├── identity/
│   ├── limiter/
│   │   ├── hierarchical.go
│   │   ├── redis_atomic_token_bucket.go
│   │   ├── redis_sliding_window.go
│   │   ├── redis_token_bucket.go
│   │   └── lua/
│   │       ├── token_bucket.lua
│   │       ├── sliding_window.lua
│   │       └── hierarchical.lua
│   │
│   ├── metrics/
│   ├── override/
│   └── redis/
│
├── tests/
│   └── legacy/
│
├── admin_api.go
├── config.go
├── limiter.go
├── ratelimit_http.go
├── main.go
├── load-test.js
├── prometheus.yml
├── docker-compose.yml
└── README.md
```

---

## Getting Started

### Prerequisites

* Go 1.24+
* Docker
* Docker Compose
* Redis

---

## Running with Docker

Clone the repository:

```bash
git clone https://github.com/RUDRA-PRATAP-SINGH01/Distributed-Rate-Limiter.git

cd Distributed-Rate-Limiter
```

Start the entire stack:

```bash
docker compose up -d --build
```

Verify the service:

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

## Running Tests

### Unit Tests

```bash
go test ./...
```

### Race Detection

```bash
go test -race ./...
```

### Load Testing

```bash
k6 run load-test.js
```

### Chaos Testing

Redis Failure:

```bash
./chaos/chaos_test.sh
```

Network Partition:

```bash
python chaos/network_partition.py
```

High Latency Injection:

```bash
python chaos/high_latency.py
```

---

## Admin API

### Create User Override

```bash
curl -X POST \
http://localhost:8082/admin/limits/user/alice \
-H "Content-Type: application/json" \
-d '{
      "capacity":20,
      "refill_rate":2
    }'
```

### Retrieve Override

```bash
curl http://localhost:8082/admin/limits/user/alice
```

### Delete Override

```bash
curl -X DELETE \
http://localhost:8082/admin/limits/user/alice
```

Equivalent APIs are available for tenants and endpoints.

---

## Technical Challenges

### Distributed Consistency

A distributed rate limiter must prevent concurrent requests from corrupting quota state.

Solved using Redis Lua scripts that execute atomically.

---

### Hierarchical Enforcement

A request may satisfy user-level limits while violating tenant-level limits.

Implemented multi-level validation through a single execution path.

---

### Cache Correctness

Caching successful requests can introduce stale decisions.

The sidecar uses denial-only caching to preserve correctness while reducing repeated rejection traffic.

---

### Metrics Cardinality

Per-user metrics can overwhelm monitoring systems.

Implemented bounded labels to maintain predictable Prometheus storage requirements.

---

## Trade-Offs

| Decision              | Benefit                 | Cost                             |
| --------------------- | ----------------------- | -------------------------------- |
| Denial-only cache     | Strong correctness      | More limiter traffic             |
| Redis-based state     | Distributed consistency | External dependency              |
| Single Redis instance | Simpler deployment      | No high availability             |
| Sidecar architecture  | Separation of concerns  | Additional network hop           |
| Runtime overrides     | Operational flexibility | Higher implementation complexity |

---

## Tech Stack

**Backend**

* Go

**Distributed State**

* Redis
* Lua

**Infrastructure**

* Docker
* Docker Compose

**Observability**

* Prometheus

**Testing**

* k6
* Go Testing
* Chaos Engineering Scripts

---


## License

This project is licensed under the MIT License.
