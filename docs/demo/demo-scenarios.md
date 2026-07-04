# Telemetry Verification & Demo Scenarios

Use these interactive scripts to trigger distinct behaviors and observe how the Grafana dashboard captures each state in real-time.

---

## 🟢 Scenario 1: Normal Traffic (Test 1)
* **How to run**:
  - POSIX: `./scripts/demo/normal.sh`
  - Windows: `.\scripts\demo\normal.ps1`
* **What it does**: Sends a steady 50 RPS flow of requests distributed across unique users.
* **What to observe**:
  - **Allowed Requests** rises immediately to 50 RPS.
  - **Rejected Requests (429)** remains at `0`.
  - **P95 Latency** is `< 10 ms` (typically 1–3 ms locally).
  - **P99 Latency** is `< 20 ms`.
* **Success Criteria**: All requests succeed (HTTP 200) with sub-millisecond latencies.

---

## 🔴 Scenario 2: Individual Rate Limiting (Test 2)
* **How to run**: Send a loop of requests targeting a single user `rudra`.
  - Bash: `for i in {1..150}; do curl -s -o /dev/null -w "%{http_code}\n" http://localhost:9090/check -H "X-User-ID: rudra"; done`
  - PowerShell: `1..150 | ForEach-Object { Invoke-WebRequest -Uri "http://localhost:9090/check?user_id=rudra" -UseBasicParsing -ErrorAction SilentlyContinue }`
* **What it does**: Floods the sidecar proxy with requests under a single user context.
* **What to observe**:
  - The first 10 requests return `200 OK`.
  - The remaining 140 requests return `429 Too Many Requests`.
  - **Quota Block Rate (%)** gauge spikes to `> 90%`.
* **Success Criteria**: The Allowed vs. Rejected stacked chart clearly splits, showing Allowed traffic flatlining at 10 and Rejected traffic spiking.

---

## 👥 Scenario 3: Multi-User Quota Isolation (Test 3)
* **How to run**: Run the normal script or manual parallel loops targeting multiple user keys.
  - PowerShell: `@("alice", "bob", "charlie", "rudra") | ForEach-Object { $u=$_; 1..30 | ForEach-Object { Invoke-WebRequest -Uri "http://localhost:9090/check?user_id=$u" -UseBasicParsing -ErrorAction SilentlyContinue } }`
* **What to observe**:
  - Different users are rate-limited independently.
  - The metrics show user traffic distribution shifting across active identities.

---

## 💥 Scenario 4: Redis Database Crash (Test 4)
* **How to run**:
  - POSIX: `./scripts/demo/redis-down.sh`
  - Windows: `.\scripts\demo\redis-down.ps1`
* **What it does**: Shuts down the backend Redis database container.
* **What to observe**:
  - **Redis Storage Status** indicator turns **CRIMSON RED (DOWN)**.
  - **System Error Rate** spikes immediately to **100%** (as request checking fails).
  - **P99 Server Latency** shows a brief upward spike representing connection timeouts.
  - **Limiter & Sidecar Status** remain healthy (UP) because the container processes are active.

---

## ♻️ Scenario 5: Database Recovery & Circuit Breaker (Test 5)
* **How to run**:
  - POSIX: `./scripts/demo/redis-up.sh`
  - Windows: `.\scripts\demo\redis-up.ps1`
* **What it does**: Restarts the Redis container.
* **What to observe**:
  - Redis health card returns to **green (UP)**.
  - Sidecar requests immediately recover to **HTTP 200**.
  - **Circuit Breaker state** transitions on the dashboard (if metrics logged): `OPEN -> HALF-OPEN -> CLOSED`.

---

## ⚡ Scenario 6: System Saturation Sweep (Test 6)
* **How to run**:
  - POSIX: `./scripts/demo/saturation.sh`
  - Windows: `.\scripts\demo\saturation.ps1`
* **What it does**: Generates a flat arrival rate of 500 RPS.
* **What to observe**:
  - Overall system **CPU utilization** increases.
  - The request processing rates flatten, showing the limits of local I/O thread processing.

---

## 🔑 Scenario 7: Hot Key Abuse (Test 7)
* **How to run**:
  - POSIX: `./scripts/demo/hotkey.sh`
  - Windows: `.\scripts\demo\hotkey.ps1`
* **What it does**: Concentrates 5000 RPS on a narrow list of 10 users.
* **What to observe**:
  - Massive rate limiting triggers.
  - **Redis Lua executions/sec** spikes to capacity.
  - Command durations for evaluated scripts increase.

---

## 🔀 Scenario 8: Stripe Idempotency Concurrency (Test 8)
* **How to run**:
  - POSIX: `./scripts/demo/idempotency.sh`
  - Windows: `.\scripts\demo\idempotency.ps1`
* **What it does**: Shoots 100 simultaneous requests sharing the identical `Idempotency-Key` to the orders endpoint.
* **What to observe**:
  - **Claims**: Exactly 1 claim is marked as `claimed`.
  - **In Progress (409)**: Around 40+ concurrent requests get blocked with lock conflicts.
  - **Replay Hits**: Around 50+ requests get served directly from replay cache once completed.

---

## 🌪️ Scenario 9: Full Chaos Simulation (Test 9)
* **How to run**:
  - POSIX: `./scripts/demo/chaos.sh`
  - Windows: `.\scripts\demo\chaos.ps1`
* **What it does**: Floods the routing path while stopping gateways and database containers sequentially.
* **What to observe**:
  - Allows tracing failures precisely to the stopped component (Gateway vs. Redis) directly from the Grafana dashboard without viewing terminal logs.
