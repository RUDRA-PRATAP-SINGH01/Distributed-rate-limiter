# Getting Started with the Demo Stack

Welcome to the Distributed Rate Limiter developer playground! This repository has been structured to run a production-ready, zero-configuration local sandbox with complete telemetry.

---

## Prerequisites

Before starting, ensure you have the following installed on your host system:
1. **Docker Desktop** (with Compose v2 support)
2. **k6** (load generation binary, see [k6 installation guides](https://grafana.com/docs/k6/latest/get-started/installation/))
3. **cURL** (command-line web request utility)

---

## Quick Start (One-Command Boot)

Run the bootstrapper script matching your operating system from the repository root:

### Linux / macOS
```bash
chmod +x scripts/start.sh scripts/demo/*.sh
./scripts/start.sh
```

### Windows (PowerShell Core / Desktop)
```powershell
.\scripts\start.ps1
```

The script will automatically:
1. Orchestrate the complete container fleet in detached mode.
2. Poll service endpoints until the entire network is healthy.
3. Launch a lightweight **15 RPS background traffic simulator** so that your dashboards are active immediately.

---

## Port and Endpoint Mappings

Once running, the stack exposes the following web interfaces and service portals:

| Service / Interface | Port | Local Endpoint URL | Description |
| :--- | :--- | :--- | :--- |
| **Grafana Dashboard** | `3000` | [http://localhost:3000/...](http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet) | Complete systems dashboard (preloaded) |
| **Jaeger Tracing UI** | `16686` | [http://localhost:16686/](http://localhost:16686/) | Distributed trace visualizations |
| **Prometheus Console** | `9091` | [http://localhost:9091/](http://localhost:9091/) | Direct query console for system metrics |
| **Sidecar Proxy Entry** | `9090` | [http://localhost:9090/check](http://localhost:9090/check) | Main sidecar rate limiter API endpoint |
| **Limiter Health Endpoint**| `8080` | [http://localhost:8080/health](http://localhost:8080/health) | Central Rate Limiter status JSON |
| **Limiter Admin API** | `8082` | [http://localhost:8082/](http://localhost:8082/) | Admin configurations and override router |

---

## Run Demo Scenarios

Interactive load scenarios are located in `scripts/demo/`. Refer to [demo-scenarios.md](./demo-scenarios.md) to start validating system resilience.
