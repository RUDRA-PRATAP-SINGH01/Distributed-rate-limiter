# Architecture Evolution: Engineering Journal

This document traces the design history and critical engineering decisions behind the Distributed Rate Limiter platform, moving from simple local states to a highly resilient sidecar-limiter architecture.

---

## Iteration 1: In-Memory Limiter in the App
We started with in-memory token bucket and sliding window implementations written in pure Go.
* **Architecture**:
```
App Instance A          App Instance B
+--------------+        +--------------+
| map[user]int |        | map[user]int |   (independent state)
+--------------+        +--------------+
```
* **Decision**: Move state out of the application to prevent split-brain quota allocation and race conditions on horizontal scale.

---

## Iteration 2: Redis without Atomicity
Moved counters to a centralized Redis storage using basic read/write commands (`GET` and `SET`).
* **Problem**: Under heavy load, requests regularly overwrite each other's increments, resulting in lost updates and key capacity overrides.
* **Decision**: All quota operations must execute atomically on the storage server. We consolidated updates using atomic Redis Lua scripts.

---

## Iteration 3: Central Limiter Service
Instead of embedding Redis connection logic and raw Lua script evaluations directly into each application client, we abstracted the logic into two separate roles:
* **Central Limiter**: Manages Redis state and evaluates rate limit algorithms.
* **Sidecar Proxy**: Sits in front of the application server, intercepts incoming calls, queries the Central Limiter, and forwards traffic if allowed.
* **Decision**: Implement a sidecar pattern to isolate rate limit evaluation from the business logic layer.

---

## Iteration 4: Hierarchical Limits
Flat user limits do not protect complex platform architectures. We expanded the check logic to support four nested quota thresholds:
1. **Global platform limits** (abuse prevention)
2. **Tenant limits** (fair-share partitioning)
3. **User limits**
4. **Endpoint limits** (pricing or cost tier protection)
* **Design**: Consolidated all check thresholds into a single Lua script execution pass to ensure atomic evaluation across multiple levels.

---

## Iteration 5: Denial-Only Caching
To optimize performance, we added local caching at the sidecar level.
* **Problem**: Caching allowed states allows an attacker to freeze their status to "allowed" and bypass limits entirely.
* **Decision**: Only cache HTTP 429 denial states. Allowed calls must always verify their quota availability against the limiter.

---

## Iteration 6: Benchmarks and Chaos Validation
Once the core platform was stable, we implemented k6 performance tests to find the saturation point and chaos scripts to verify connection recovery when network boundaries partition or database hosts crash.
