# Diagrams

Mermaid sources for this repository. Render in GitHub, VS Code (Mermaid extension), or [mermaid.live](https://mermaid.live).

| File | What it shows |
|------|----------------|
| [request-flow.mmd](request-flow.mmd) | End-to-end client → sidecar → limiter → upstream |
| [sidecar-flow.mmd](sidecar-flow.mmd) | Sidecar internal branches (idempotent vs normal) |
| [routing-flow.mmd](routing-flow.mmd) | Gateway scoring, circuit allow, failover loop |
| [idempotency-flow.mmd](idempotency-flow.mmd) | Idempotency state machine |
| [fencing-flow.mmd](fencing-flow.mmd) | Lease reclaim + stale worker rejection |
| [circuit-breaker.mmd](circuit-breaker.mmd) | Three-state breaker transitions |
| [sentinel-failover.mmd](sentinel-failover.mmd) | Sentinel quorum + client rediscovery |
| [audit-flow.mmd](audit-flow.mmd) | Async append + Redis indexes + trim |
| [tracing-flow.mmd](tracing-flow.mmd) | OTel spans across sidecar/limiter/Redis |
| [redis-layout.mmd](redis-layout.mmd) | Key namespace map |

Architecture docs embed or reference these files. When I change Lua key shapes, I update the matching diagram first. otherwise onboarding docs lie quietly.
