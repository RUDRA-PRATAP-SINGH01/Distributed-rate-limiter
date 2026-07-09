# Telemetry Verification and Demo Scenarios

Use these interactive scripts to trigger distinct behaviors and observe how the Grafana dashboard captures each state in real-time.

---

## Mission 1: Normal Baseline Traffic
* **Run Command**:
  - POSIX: `./scripts/demo/normal.sh`
  - Windows: `.\scripts\demo\normal.ps1`
* **What it does**: Sends a steady 50 RPS flow of requests distributed across unique users.
* **Observe**:
  - Allowed Requests line series rising to 50 RPS.
  - Rejected Requests (429) staying at exactly 0.
  - P95 Latency remaining under 10 ms (typically 1-3 ms).
* **Expected Output**:
  - Allowed increases (Spikes to 50)
  - Rejected (429) stays at 0 (Flatline)
  - Latency P95 < 10ms, P99 < 20ms

---

## Mission 2: Key Exhaustion and Abuse (Hot Key)
* **Run Command**:
  - POSIX: `./scripts/demo/hotkey.sh`
  - Windows: `.\scripts\demo\hotkey.ps1`
* **What it does**: Concentrates 5000 RPS on a narrow list of 10 users.
* **Observe**:
  - Rejected Requests (429) climbing rapidly.
  - Quota Block Rate (%) gauge spiking to > 90%.
  - Lua Executions/sec and CPU metrics rising under massive concurrent evaluation.
* **Expected Output**:
  - Allowed decreases (Allowed 200 flatlines at quota capacity)
  - Rejected increases (Rejected 429 area spikes to 4900+ RPS)
  - Lua increases (EvalSHA executions spike to 5000/s)
  - Circuit CLOSED (Breaker remains closed for allowed users)

---

## Mission 3: Database Outage and Resiliency
* **Run Command (Crash)**:
  - POSIX: `./scripts/demo/redis-down.sh`
  - Windows: `.\scripts\demo\redis-down.ps1`
* **What it does**: Shuts down the backend Redis database container.
* **Observe**:
  - Redis Storage Health indicator turning DOWN.
  - Limiter Decision Error Rate (%) spiking (requires `ENABLE_AUDIT_TRAIL=true`; measures `audit_events_total{decision="error"}` — not raw HTTP 5xx).
  - Circuit Breaker State panel showing the transition to OPEN.
* **Expected Outage Output**:
  - Redis DOWN
  - Errors increases (Spikes to 100%, limiter returns 503 Service Unavailable)
  - Circuit OPEN (State code 1)
* **Run Command (Recovery)**:
  - POSIX: `./scripts/demo/redis-up.sh`
  - Windows: `.\scripts\demo\redis-up.ps1`
* **Expected Recovery Output**:
  - Redis UP
  - Errors decreases (Drops back to 0%)
  - Circuit Transitions: OPEN (1) --> HALF-OPEN (2) --> CLOSED (0)

---

## Mission 4: Stripe Concurrency and Idempotency Races
* **Run Command**:
  - POSIX: `./scripts/demo/idempotency.sh`
  - Windows: `.\scripts\demo\idempotency.ps1`
* **What it does**: Shoots 100 simultaneous requests sharing the identical Idempotency-Key to the orders endpoint.
* **Observe**:
  - Idempotency Claims showing exactly 1 successful claim.
  - Replay Cache Hits showing successful repeats.
  - In-Progress Conflicts (HTTP 409) indicating concurrent lock blocks.
* **Expected Output**:
  - Claims Exactly 1 Claimed (result="claimed")
  - Replays 50+ served from cache (result="replay")
  - In Progress 40+ concurrent lock waits blocked (result="in_progress")

---

## Mission 5: Multi-User Quota Isolation
* **Run Command**:
  - Run the normal script or manual loops targeting multiple user keys.
  - PowerShell example: `@("alice", "bob", "charlie", "rudra") | ForEach-Object { $u=$_; 1..30 | ForEach-Object { Invoke-WebRequest -Uri "http://localhost:9090/check?user_id=$u" -UseBasicParsing -ErrorAction SilentlyContinue } }`
* **Observe**:
  - Different users are rate-limited independently.
  - Traffic distribution shifts across active identities.

---

## Mission 6: System Saturation Sweep
* **Run Command**:
  - POSIX: `./scripts/demo/saturation.sh`
  - Windows: `.\scripts\demo\saturation.ps1`
* **What it does**: Generates a flat arrival rate of 500 RPS.
* **Observe**:
  - Overall system CPU utilization increases.
  - Request processing rates flatten, showing limits of local I/O thread processing.

---

## Mission 7: Infrastructure Chaos Simulation
* **Run Command**:
  - POSIX: `./scripts/demo/chaos.sh`
  - Windows: `.\scripts\demo\chaos.ps1`
* **What it does**: Floods routing paths while stopping gateways and database containers sequentially.
* **Observe**:
  - Allows tracing failures precisely to the stopped component (Gateway vs. Redis) directly from the Grafana dashboard without viewing terminal logs.
