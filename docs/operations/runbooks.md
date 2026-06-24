# Runbooks

## Problem Statement

3 AM page: "API returning errors." On-call needs step-by-step runbooks. Is Redis down? Quota exhausted? Circuit open? Sentinel failover? I wrote my incident responses into runbook format, with exact commands (`docker`, `curl`, `k6`, chaos scripts) and expected outcomes for each scenario. Benchmark anchors (**~1,000 RPS** healthy, **99.9% 429** hot-key correct) stop wrong escalations.

## Why the problem exists

Rate limiter incidents confuse triage:

- Product sees 429 and calls it a bug.
- SRE sees 503 and asks if Redis is down.
- Payments sees duplicate charge and points at idempotency.
- Gateway team sees 502 and blames routing.

Without a runbook, every on-call takes a different path. Mean time to misdiagnose stays high.

## Design goals

1. Symptom-driven index: start from the client-visible error.
2. Copy-paste commands in PowerShell and bash where needed.
3. Expected outputs with explicit pass/fail criteria.
4. Escalation boundaries: when to page Redis vs the app team.
5. Post-incident benchmarks with optional `run-all.ps1` verification.

## Alternative approaches considered

| Approach | Verdict |
|----------|---------|
| Wiki outside repo | Drifts from code |
| PagerDuty only | No technical steps |
| Single mega-runbook | Hard to scan; split by scenario |
| No expected outputs | False fixes |
| Automated remediation only | v2; manual runbooks v1 |

I keep scenario runbooks in-repo, linked to chaos and benchmark scripts.

## Final architecture

### RB-1: Elevated 503 rate

**Trigger:** `503` > 1% for 5 min.

**Steps:**

1. `curl -s http://localhost:8080/health | jq .redis`
2. `docker ps --filter name=redis`
3. If standalone: `docker logs rate-redis --tail 30`
4. If HA: `docker logs redis-sentinel-1 --tail 20`
5. Run `.\chaos\chaos_test.ps1` pattern manually: restart Redis if down
6. Check `circuit_breaker_state` for `cb:redis` via admin
7. **Expected recovery:** 503 to 200 within 30s of Redis healthy

**Env check:** `FAIL_OPEN=false`, `IDEMPOTENCY_FAIL_OPEN=false`

---

### RB-2: Redis Sentinel failover

**Trigger:** `/health` shows `redis.role` change or `redis_failover_reconnects_total` spike.

**Steps:**

1. Confirm HA profile: `docker ps --filter name=redis-sentinel`
2. `docker stop redis-master` (drill) or identify failed node (incident)
3. Watch: `docker logs redis-sentinel-1 --tail 20`
4. `curl http://localhost:8080/health | jq .redis`
5. `docker start redis-master`. verify replica role
6. Optional load: `k6 run benchmarks/load-test.js`

**Expected:** 5 to 30s write unavailability, then recovery (`sentinel/summary.md`)

---

### RB-3: 429 storm (quota)

**Trigger:** Customer reports throttling.

**Steps:**

1. `curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/limits/user/USER_ID`
2. Check hierarchical tenant and global limits
3. Reproduce: `k6 run -e TARGET_RPS=100 benchmarks/throughput/throughput-test.js` with same user_id
4. If hot-key scenario: expect **99.9% 429** at 5,000 target. That is **correct**

**Mitigation:** `PUT /admin/limits/user/USER_ID` bump capacity; wait `OVERRIDE_CACHE_TTL_MS` (5000ms)

---

### RB-4: Duplicate payment / idempotency

**Trigger:** Duplicate upstream execution report.

**Steps:**

1. `curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/idempotency/user/KEY`
2. `curl http://localhost:8081/api/orders/count`
3. Reproduce: `k6 run benchmarks/idempotency/idempotency-race.js`. expect **1** upstream
4. If count > 1: check `ENABLE_IDEMPOTENCY=true`, Redis health
5. Stuck key: `DELETE /admin/idempotency/user/KEY` after upstream verify

**Benchmark reference:** 100 VUs to **1** execution, p95 **14.9 ms**

---

### RB-5: Gateway / routing degradation

**Trigger:** High 502/504 or failover headers.

**Steps:**

1. `curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/routing/gateways`
2. `curl -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit`
3. `k6 run benchmarks/routing/routing-test.js`
4. Reset stuck circuit: `curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" http://localhost:8082/admin/circuit/gateway-c`
5. Tune env: `ROUTING_CIRCUIT_ERROR_RATE`, `CB_OPEN_COOLDOWN_MS`

**Expected:** gateway-a gets majority traffic; gateway-c drops under errors

---

### RB-6: Latency SLO breach (no error spike)

**Trigger:** p99 > 100 ms, errors < 1%.

**Steps:**

1. Check actual RPS vs **~1,000** sustainable ceiling
2. `k6 run -e TARGET_RPS=5000 benchmarks/throughput/throughput-test.js`. if actual ~**1,353**, p99 **3.5s**, that is saturation
3. `docker stats rate-limiter rate-sidecar rate-redis`
4. Run `.\benchmarks\run-saturation.ps1` for knee documentation
5. Scale horizontally or reduce per-user `CAPACITY`

---

### RB-7: Pre-release validation

```powershell
docker compose up --build -d
.\benchmarks\run-all.ps1
.\chaos\chaos_test.ps1
k6 run benchmarks/idempotency/idempotency-race.js
```

**Pass criteria:** summary.md sustainable row, chaos 503 to recovery, idempotency count=1

## Tradeoffs

Scripts are Windows-centric (`chaos_test.ps1`, `run-all.ps1`); Linux needs adaptation. Runbooks use localhost URLs; prod runbooks need host substitution. Steps are manual, not all automated assertions. 429 is often not an incident; the runbook educates stakeholders. Admin key rotation breaks runbook commands if the key changed without a doc update.

## Failure modes

Wrong runbook hurts: bumping quota on a 503 incident (RB-3) worsens overload. Premature idempotency delete risks duplicate charge during stuck in_progress. Failover drill on standalone has no Sentinel and gives a false negative. Circuit delete in prod floods a bad gateway; I use it during known recovery only. Running benchmarks during an incident adds load; I use low TARGET_RPS only.

## Operational concerns

I keep `ADMIN_API_KEY` in a secrets manager; runbooks reference `$ADMIN_API_KEY` env. Post-incident I use `admin/audit/replay?id=<event-id>` for denied decisions timeline. I link runbooks to PagerDuty services: Redis, Sidecar, Upstream. Quarterly game day combines RB-2 and RB-7. I document tenant override approval; audit trail captures changes.

## Performance implications

RB-6 saturation: system knee at **~1,000 actual RPS** healthy; **5,000 target to 1,353 actual** is unhealthy. Scale before tuning Lua.

RB-3 hot-key: **4,940 actual RPS** with **99.9% 429**. Redis is hot but correct; fix via key sharding or higher capacity, not "disable limiter."

RB-4 replay capacity: **~942 RPS** idempotency replay. Retry storms are safe post-recovery.

Circuit open reduces load on a dead gateway. Latency improves for the fast-fail path (~**120µs** allow check).

## Lessons learned

Keeping RB-1 and RB-2 separate was essential. On-call ran `docker restart rate-redis` during failover when Sentinel election was what we needed.

RB-7 as a pre-release gate caught a `FAIL_OPEN` regression; it is mandatory now.

For customer 429 tickets, an RB-3 admin limits screenshot is my standard response. Escalations dropped.

Next up: runbook JSON in a machine-readable format for CI smoke mapping to RB-7 steps.
