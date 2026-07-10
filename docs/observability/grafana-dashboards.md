# Grafana Dashboards

**Source:** `deploy/grafana/`

---

## Quick start (see live panels)

From the repository root:

```powershell
# Windows
.\scripts\start.ps1
```

```bash
# Linux / macOS
./scripts/start.sh
```

That command starts Docker Compose, waits for health, generates traffic, and opens:

| UI | Deep link |
|----|-----------|
| Grafana fleet dashboard | http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet |
| Prometheus | http://localhost:9091 |
| Jaeger | http://localhost:16686 |

Already running? Only re-open browsers:

```powershell
.\scripts\open-observability.ps1
```

```bash
./scripts/open-observability.sh
```

**Grafana tips**

- Anonymous access is enabled in compose (`GF_AUTH_ANONYMOUS_ENABLED=true`) — no login.
- Time range: **Last 15 minutes**, refresh: **5s**.
- Folder in UI: **Distributed Rate Limiter**.
- Dashboard UID: `dist-rate-limiter-dashboard`.

**Jaeger tips**

- Service: `rate-sidecar`, Operation: `sidecar.proxy` (or `POST /api/orders` for idempotency).
- Open a trace → **Trace Timeline** for the span DAG.
- System Architecture DAG only shows service edges (`rate-sidecar` → `rate-limiter`), not Redis/upstream nodes.

**Prometheus starter query**

```promql
sum(rate(rate_limiter_requests_total[1m])) by (allowed)
```

---

## Layout

```
deploy/grafana/
├── dashboards/
│   ├── distributed-rate-limiter.json
│   └── rate_limiter_dashboard.json
└── provisioning/
    ├── dashboards/dashboard.yml
    └── datasources/prometheus.yml
```

Provisioning mounts dashboards from `/var/lib/grafana/dashboards` into folder **Distributed Rate Limiter** (`dashboard.yml`).

---

## Dashboard UID

Both JSON files declare the same UID:

```json
"uid": "dist-rate-limiter-dashboard"
```

Panels include **System Overview**, request rates, Redis latency, circuit breaker state, routing scores, idempotency, audit metrics, dependency-failure latency, and override generation — aligned with `internal/metrics/metrics.go`.

---

## Duplicate JSON note

`distributed-rate-limiter.json` and `rate_limiter_dashboard.json` are kept in sync. Grafana imports by UID — one logical dashboard at runtime. Prefer editing both (or treating `rate_limiter_dashboard.json` as canonical) to avoid drift.

---

## Datasource

`provisioning/datasources/prometheus.yml` points Grafana at the compose Prometheus service (scrape targets in `deploy/prometheus/prometheus.yml`):

- `limiter:8080`
- `sidecar:9090`
- `redis-exporter:9121`

---

## Access

Default compose: Grafana UI on port **3000**, Prometheus on **9091**, Jaeger on **16686** (see root `docker-compose.yml`).

Import manually (non-compose): upload either JSON file; UID `dist-rate-limiter-dashboard` preserves deep links and provisioning references.
