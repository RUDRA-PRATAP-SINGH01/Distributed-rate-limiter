# Chaos Engineering Tests

This folder contains scripts to verify the resilience of the rate limiter.

## Tests

### 1. Redis Failure (`chaos_test.ps1`)
- Kills the Redis container.
- Expects sidecar to return `503 Service Unavailable` (fail‑closed).
- Restarts Redis and verifies recovery.

Run:
```powershell
.\chaos\chaos_test.ps1