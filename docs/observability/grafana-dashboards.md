# Grafana Dashboards

**Source:** `deploy/grafana/`

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

Panels include **System Overview**, request rates, Redis latency, circuit breaker state, routing scores, idempotency, and audit metrics — aligned with `internal/metrics/metrics.go` names.

---

## Duplicate JSON note

`distributed-rate-limiter.json` and `rate_limiter_dashboard.json` are **byte-identical** (same SHA-256 hash). Both exist in-repo; only one logical dashboard is needed at runtime.

Grafana imports by UID — duplicate files with the same UID do not create two dashboards when provisioned from the same directory, but maintaining two copies risks drift. Prefer editing one file and removing or symlinking the duplicate in a future cleanup.

---

## Datasource

`provisioning/datasources/prometheus.yml` points Grafana at the compose Prometheus service (scrape targets in `deploy/prometheus/prometheus.yml`):

- `limiter:8080`
- `sidecar:9090`
- `redis-exporter:9121`

---

## Access

Default compose: Grafana UI on port **3000**, Prometheus on **9091** (see root `docker-compose.yml`).

Import manually (non-compose): upload either JSON file; UID `dist-rate-limiter-dashboard` preserves deep links and provisioning references.
