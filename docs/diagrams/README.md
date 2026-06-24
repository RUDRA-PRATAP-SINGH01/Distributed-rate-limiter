# Diagrams

GitHub renders Mermaid only inside markdown code fences, not standalone `.md` files. Each diagram below is a markdown page with an embedded `mermaid` block so it renders when you browse the repo on GitHub.

| Diagram | Description |
|---------|-------------|
| [request-flow.md](request-flow.md) | Client through sidecar, limiter, Redis, and upstream |
| [sidecar-flow.md](sidecar-flow.md) | Sidecar internal branches (idempotent vs normal) |
| [routing-flow.md](routing-flow.md) | Gateway scoring, circuit allow, failover loop |
| [idempotency-flow.md](idempotency-flow.md) | Idempotency state machine |
| [fencing-flow.md](fencing-flow.md) | Lease reclaim and stale worker rejection |
| [circuit-breaker.md](circuit-breaker.md) | Three state breaker transitions |
| [sentinel-failover.md](sentinel-failover.md) | Sentinel quorum and client rediscovery |
| [audit-flow.md](audit-flow.md) | Async append, Redis indexes, retention trim |
| [tracing-flow.md](tracing-flow.md) | OpenTelemetry spans across services |
| [redis-layout.md](redis-layout.md) | Redis key namespace map |

When I change Lua key shapes or request flow, I update the matching diagram page first so the docs stay honest.

Local preview: VS Code with a Mermaid extension, or paste a diagram block into [mermaid.live](https://mermaid.live).
