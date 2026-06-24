# Audit Trail Benchmarks

```bash
go test -bench=BenchmarkAudit -benchmem ./internal/audit/...
```

## Admin search

```bash
curl -H "X-API-Key: dev-key-change-in-prod" \
  "http://localhost:8082/admin/audit?tenant_id=default&decision=denied&limit=20"

curl -H "X-API-Key: dev-key-change-in-prod" \
  "http://localhost:8082/admin/audit/replay?id=<event-id>"
```

## Sample append latency (miniredis, local)

| Benchmark | ~ns/op |
|-----------|--------|
| BenchmarkAuditAppend | ~300µs |

Production latency depends on Redis RTT and Lua script execution.
