# Architecture Walkthrough: Hop-by-Hop Request Lifecycle

This document traces the path of an incoming HTTP request through each layer of the Distributed Rate Limiter platform, detailing the headers, network protocols, caching mechanisms, and atomic data mutations.

---

## High-Level Request Flow Map

```
[ Client ]
    │ (HTTP / JSON / Idempotency Key)
    ▼
[ Sidecar Proxy (Port 9090) ] ────► [ Denial Cache (In-Memory) ]
    │ (Fast Path: Allowed / Skip cache)
    ▼
[ Central Limiter (Port 8080) ] ◄──► [ Circuit Breaker / Local Memory ]
    │ (Atomic evaluation / EvalSHA)
    ▼
[ Redis Storage (Port 6379) ] ◄───► [ Lua Algorithms (Sliding/Token) ]
    │ (Boolean allow + token metadata)
    ▼
[ Sidecar Proxy ]
    │ (Upstream Forwarding)
    ▼
[ Upstream Backend (Port 8081) ]
```

---

## Detailed Lifecycle Breakdown

### 1. Hop 1: Client ──► Sidecar Proxy (Ingress)
- **Protocol**: HTTP/1.1 or HTTP/2 over TCP.
- **Entrypoint**: Sidecar listens on port `9090` and intercepts all inbound traffic meant for the backend service.
- **Header Parsing**:
  - The client provides standard headers, including `X-User-ID`, `X-Tenant-ID`, and optional transaction controllers such as `Idempotency-Key`.
- **Fast Denial Cache Validation**:
  - The sidecar checks its local, high-performance in-memory cache.
  - If the user/tenant identifier matches a known **denied (429) bucket** that is still within its TTL window, the sidecar immediately rejects the request locally, bypassing network propagation.
  - *Note: Only "denied" responses are cached to prevent attackers from freezing "allowed" states.*
- **Idempotency Check**:
  - If an `Idempotency-Key` header is present, the sidecar first queries the idempotency storage path to check if a identical request has been processed, is currently in progress, or failed.

---

### 2. Hop 2: Sidecar Proxy ──► Central Rate Limiter
- **Protocol**: HTTP/1.1 (optimized via Keep-Alive pool connections) targeting `/check` or `/check_hierarchical`.
- **Data Payload**:
  - The sidecar forwards metadata resolved about the request:
    - User ID (`user_id`)
    - Tenant ID (`tenant_id`)
    - API key / Client ID
    - Weight/cost of the target endpoint (e.g. `/export` requests might require 10 tokens instead of 1).
- **Concurrency Control (Singleflight)**:
  - If multiple concurrent requests arrive at the sidecar for the identical user key, the sidecar uses a `go-singleflight` group to collapse them into a single HTTP query to the central limiter, preventing central network saturation.

---

### 3. Hop 3: Central Rate Limiter ──► Redis Storage (Lua Execution)
- **Protocol**: Raw RESP (Redis Serialization Protocol) over connection pools.
- **Circuit Breaker Check**:
  - Before communicating with Redis, the limiter queries its local **Circuit Breaker store** (`redisCircuit`). If the breaker is in the `OPEN` state, it fails-fast, bypassing Redis to prevent database degradation.
- **Atomic Script Evaluation (`EVALSHA`)**:
  - The limiter invokes a pre-loaded Lua script on the Redis server:
    - **Token Bucket Script**: Evaluates token consumption and atomic token refill based on delta timestamps.
    - **Sliding Window Script**: Cleans old keys in a Sorted Set (ZSET) representing the window, inserts the current timestamp, and reads cardinality.
    - **Hierarchical Script**: Evaluates platform, tenant, user, and endpoint buckets atomically in a single pass.
  - By executing as a single Lua script, Redis guarantees that no other database operations can interleave, ensuring complete thread-safety and zero race conditions under concurrent horizontal load.

---

### 4. Hop 4: Central Rate Limiter ──► Sidecar Proxy (Response)
- **Payload**: Returns a JSON structure indicating:
  - `allowed`: Boolean status (`true`/`false`).
  - `remaining`: Number of tokens left in the window.
  - `reset`: Unix timestamp indicating when the window refills or resets.
  - `circuit_state`: Circuit status header (if degraded).
- **Metric Collection**:
  - The central limiter increments local Prometheus counters (e.g., `rate_limiter_requests_total`) to record system outcomes.

---

### 5. Hop 5: Sidecar Proxy ──► Upstream Backend Service (Forwarding)
- **If Allowed (`allowed=true`)**:
  - The sidecar forwards the raw request to the target backend (port `8081` or routing gateway).
  - Appends response headers informing the client:
    - `X-Ratelimit-Limit`
    - `X-Ratelimit-Remaining`
    - `X-Ratelimit-Reset`
- **If Denied (`allowed=false`)**:
  - The sidecar intercepts the request flow and terminates it immediately, returning `HTTP 429 Too Many Requests`.
  - Caches the denial state in its local memory store.
