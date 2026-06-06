# Chaos Engineering Tests

This folder contains scripts to verify the resilience of the rate limiter.

## Tests

### 1. Redis Failure (`chaos_test.ps1`)
- Kills the Redis container.
- Expects sidecar to return `503 Service Unavailable` (fail-closed).
- Restarts Redis and verifies recovery.

Run:
```powershell
.\chaos\chaos_test.ps1
```

### 2. Network Partition (`network_partition.py`)
- Disconnects `rate-sidecar` from the Docker Compose network.
- Expects sidecar to return `503` or time out (fail-closed).
- Reconnects the network and verifies recovery.

**Requires the full Docker Compose stack** (not local `go run`):
```powershell
docker compose up -d --build
python chaos/network_partition.py
```

Stop local processes on ports 8080/8081/9090 before starting Compose to avoid port conflicts.
