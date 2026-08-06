# Quality Management

**Sources:** `scripts/qa.ps1`, `scripts/qa.sh`, `.github/workflows/ci.yml`, `docs/operations/runbooks.md` (RB-7)

Quality management here is not a separate tool. It is the set of **gates, owners, and evidence** that decide whether a change may merge or ship.

---

## Quality policy

1. Every mergeable change has automated evidence in CI.
2. A green unit suite is not enough to call the system healthy — live smoke must pass after deploy.
3. A green smoke is not enough to call a change correct — sanity covers the touched happy path.
4. Exploratory sessions find what scripts cannot encode. Findings become tests or runbook steps.
5. Fail-closed stays non-negotiable: Redis/limiter outage → **503**, never a silent allow (`FAIL_OPEN=false`).

---

## Roles

| Role | Owns |
|------|------|
| Author | Local `quality-gate`, process-smoke, targeted sanity |
| Reviewer | Test plan on the PR (what is white-box vs black-box) |
| CI | Lint, vuln, unit, race, Redis integration, process-smoke, chaos |
| Operator | Deploy smoke + RB-7 before calling a release done |

---

## Quality gates

```
local laptop                CI (PR / main)              post-deploy
──────────────              ──────────────              ──────────
vet                         static-build                live smoke
process-smoke               lint + vuln                 live sanity
unit                        process-smoke               exploratory (release)
                            unit + race                 RB-7 benchmarks
                            redis-integration           (optional k6)
                            chaos
```

Local merge bar:

```powershell
.\scripts\qa.ps1 quality-gate
```

```bash
./scripts/qa.sh quality-gate
```

Runs `go vet`, the process-smoke subset, then `go test ./...`. CI still owns lint, govulncheck, race, real Redis, and chaos — do not treat a local gate as a substitute.

---

## When to run which suite

| Event | Suite | Command |
|-------|-------|---------|
| Saved a file, want a 30s signal | Process smoke | `.\scripts\qa.ps1 process-smoke` |
| Stack just came up / after deploy | Smoke | `.\scripts\qa.ps1 smoke` |
| Finished a feature or bugfix | Sanity | `.\scripts\qa.ps1 sanity -Changed` |
| Before opening a PR | Quality gate | `.\scripts\qa.ps1 quality-gate` |
| Lua / Redis script change | Integration | `REDIS_TEST_ADDR=127.0.0.1:6379 .\scripts\qa.ps1 integration` |
| Concurrency change | Race | `.\scripts\qa.ps1 race` |
| Pre-release | RB-7 + exploratory | see [runbooks.md](../operations/runbooks.md) |

---

## Defect severity (how we triage test failures)

| Severity | Example | Gate |
|----------|---------|------|
| Blocker | Allow when Redis is down; quota exceeded still 200 | Must fail CI / smoke |
| High | Missing `Retry-After` on 429; admin reachable without key | Must fail unit or sanity |
| Medium | Metric cardinality leak; docs drift | Lint / review |
| Low | Log wording; Grafana panel polish | Backlog |

Never reclassify a 503 as a 429 in a test to "make it green." See [failure-testing.md](failure-testing.md).

---

## Evidence we keep

| Artifact | Produced by | Used for |
|----------|-------------|----------|
| CI job logs | `.github/workflows/ci.yml` | Merge decision |
| `coverage.out` | `coverage` job / `qa coverage` | Review, not a hard threshold |
| Chaos R1 log | `go test -tags=chaos ./chaos/...` | Fail-closed proof |
| Benchmark summary | `benchmarks/` | Capacity claims |
| Exploratory notes | session template in [exploratory-charters.md](exploratory-charters.md) | New tests |

Coverage is **informational**. There is no percentage gate — a 100% covered wrong invariant is still wrong.

---

## Related

- [test-strategy.md](test-strategy.md) — pyramid and package map
- [blackbox-whitebox.md](blackbox-whitebox.md) — how existing tests split
- [exploratory-charters.md](exploratory-charters.md) — session-based testing
- [../ci/continuous-integration.md](../ci/continuous-integration.md) — job list
