# Failure Mode: Routing Failures

**Status:** Documented  
**Severity:** High  
**Components:** `routing.Router`, `routing.RedisStore`, `Selector`, circuit breaker enrichment

---

## 1. Problem Statement

Intelligent routing can fail before any upstream gateway is contacted, during gateway execution, or after exhausting failover candidates. Each failure mode needs distinct HTTP semantics, metrics, and ops signals — a generic 502 hides whether Redis, selection, or all gateways failed.

## 2. Why the problem exists

Routing pulls live state from Redis (`route:index`, `route:gw:{id}`), enriches circuit snapshots per gateway, scores candidates, and sequentially attempts HTTP forwards. Any step errors. `StateUnknown` deliberately excludes gateways when `breaker.GetState` fails — routing prefers **no traffic** over **blind traffic**.

## 3. Design goals

- Fail with explicit errors logged: `probe list error`, `Routing forward error`, `all gateways unavailable`.
- Response headers on success: `X-Gateway-ID`, `X-Gateway-Score`, `X-Gateway-Failover`.
- Metrics: `routing_decisions_total`, `routing_failovers_total`, `routing_outcomes_total`, `routing_gateway_health_score`.
- Admin visibility: `/admin/routing/gateways` on limiter admin port `:8082`.
- Require `GATEWAYS` env at startup when `ENABLE_ROUTING=true` — fail fast if empty.

## 4. Alternative approaches considered

| Fallback | Why not default |
|----------|-----------------|
| Fall back to `UPSTREAM_URL` | Masks routing misconfiguration |
| Route to `StateUnknown` gateways | Could send traffic to open circuits |
| Infinite failover tries | Unbounded latency |

## 5. Final architecture

**Failure taxonomy:**

| Stage | Error | Client result |
|-------|-------|---------------|
| `ListGateways` Redis error | `Forward` returns err | 503 sidecar |
| No gateways in index | `no gateways configured` | 503 |
| All scores ≤ 0 / not selectable | `no healthy gateways available` | 503 |
| Circuit `Allow` denied | skip candidate, try next | failover or final 503 |
| Circuit `Allow` Redis error | `lastErr` set, continue | may exhaust candidates |
| Upstream 5xx or network error | `RecordOutcome` failure | failover |
| All candidates fail | `all gateways failed` | 503 `all gateways unavailable` |

**Selectable exclusion** (`internal/routing/types.go`):

```go
if s.CircuitState == circuitbreaker.StateOpen || s.CircuitState == circuitbreaker.StateUnknown {
    return false
}
```

`enrichCircuit` sets `StateUnknown` when `breaker.GetState` errors (`routing/store.go`).

**Health probes:** Background goroutine `StartHealthProbes` — probe failure calls `UpdateHealthProbe` → lowers `health_score`.

## 6. Tradeoffs

| Strict exclusion (unknown/open) | Loose routing |
|--------------------------------|---------------|
| Safer under Redis glitches | Might hit dying gateway |
| Lower available capacity | Higher success rate short-term |

## 7. Failure modes

- **Redis outage during route:** Same as redis-outage — cannot list gateways.
- **Single gateway enabled, circuit open:** Immediate `no healthy gateways`.
- **Weight 0 or missing:** Defaults to 100 on parse — misconfig can skew traffic.
- **Seed failure at startup:** `log.Fatalf("gateway seed failed")` — process exits.
- **Idempotent + routing failure:** `failIdempotent` stores 503 JSON for key retry.

## 8. Operational concerns

**Enable checklist:**

```
ENABLE_ROUTING=true
REDIS_ADDR=...
GATEWAYS=gateway-a|http://gateway-a:8081|100,...
ROUTING_PROBE_INTERVAL_SEC=15
```

**Debug:**

- Check `routing_gateway_health_score` per gateway.
- `routing_circuit_open` legacy gauge + `circuit_breaker_state{target="gateway-a"}`.
- OTEL span `sidecar.intelligent_route` attributes.
- Manual ops: `POST /admin/routing/gateways/{id}/enabled`, `DELETE /admin/circuit/gateway-a`.

**Benchmarks:** `benchmarks/routing/routing-test.js`.

## 9. Performance implications

`ListGateways` is O(n) Redis HGETALL per request — acceptable for n≤10. Failover multiplies upstream RTT. Probes add steady background traffic every `ROUTING_PROBE_INTERVAL_SEC` independent of user load — tune on large fleets.

## 10. Lessons learned

I once routed to a gateway with `StateUnknown` during a partial Redis flake and amplified errors to a payment partner. Excluding unknown state was the conservative fix — capacity drops but blast radius shrinks. Failover headers (`X-Gateway-Failover: true`) became my support team's fastest triage signal for "routing did its job but backends struggled."

**References:** `internal/routing/router.go`, `internal/routing/store.go`, `internal/routing/types.go`, `docs/decisions/why-weighted-routing.md`
