# Recovery Behavior

**Sources:** `chaos/chaos_test.ps1`, `docker-compose.ha.yml`, `internal/circuitbreaker/store.go`, go-redis script caching, `docs/benchmarks/final-benchmark-report.md`

---

## Redis restart (standalone)

**Chaos script flow** (`chaos/chaos_test.ps1`):

1. Verify limiter + sidecar healthy.
2. `docker stop rate-redis` → sidecar returns **503** (fail-closed).
3. `docker start rate-redis` → traffic recovers.

**Expected:** 503 → 200 within ~**30s** of Redis healthy (runbook RB-1).

Limiter startup always pings Redis; a restarted limiter process will fatal if Redis is still down.

---

## SCRIPT FLUSH / new Redis master

After Sentinel promotion or manual `SCRIPT FLUSH` on a new master:

- **go-redis** caches Lua script SHA1s in-process; on `NOSCRIPT`, client **resends** script bodies automatically.
- **Transient latency spike** on first commands after SCRIPT FLUSH or cold script cache.
- No manual redeploy required for script definitions embedded via `//go:embed` in Go packages.

Benchmark verification: **SCRIPT FLUSH recovery ✓** (`docs/benchmarks/final-benchmark-report.md`).

Operational note: expect elevated p99 for one pool refresh cycle after failover — not instant zero-downtime.

---

## Circuit breaker recovery

Automatic path:

1. **Open** — fast-fail without hitting dependency.
2. **Cooldown** — `CB_OPEN_COOLDOWN_MS` (default 30s).
3. **Half-open** — up to `CB_HALF_OPEN_MAX_PROBES` probe requests; need `CB_HALF_OPEN_SUCCESS_REQUIRED` successes to close.

Manual path (ops, after dependency confirmed healthy):

```bash
curl -X DELETE -H "X-API-Key: $ADMIN_API_KEY" \
  http://localhost:8082/admin/circuit/redis
```

Same pattern for `central-limiter` and `gateway-{id}`.

Watch `circuit_breaker_transitions_total` during recovery.

---

## Sentinel failover recovery

**Trigger:** Master loss; Sentinel promotes replica.

**Timeline:** ~5–30s write unavailability typical (`docs/operations/runbooks.md` RB-2).

**Verify:**

```bash
curl http://localhost:8080/health | jq .redis
# role may flip master → replica on old node after rejoin
```

Clients reconnect via Sentinel-aware client (`REDIS_MODE=sentinel`). Connection pool refresh may cause brief p99 spike across all pods.

---

## Process restart

| Process | State impact | Recovery |
|---------|--------------|----------|
| Limiter | Quota in Redis — preserved | Sidecar `/health` follows limiter probe |
| Sidecar | Local denial cache **lost** | Safe — allowances re-check limiter |
| Sidecar | singleflight groups **lost** | Safe — may duplicate limiter calls briefly |
| Redis | All shared state | Restore from persistence / replica |

Override cache (`OVERRIDE_CACHE_TTL_MS`) repopulates from Redis on first check after limiter restart.

---

## Fail-open env (dev only)

If recovery appears "instant" with 200 during Redis outage, check:

- `FAIL_OPEN=true` (sidecar)
- `IDEMPOTENCY_FAIL_OPEN=true`
- `CIRCUIT_FAIL_OPEN=true`

Chaos script explicitly fails on 200 during Redis stop when fail-closed is expected.

---

## Post-recovery validation

```powershell
.\chaos\chaos_test.ps1
curl http://localhost:8080/health
curl http://localhost:9090/health
```

Optional load: `k6 run benchmarks/load-test.js` — expect ~30s latency elevation while circuits half-open probe (`docs/benchmarks/failover.md`).
