# Re-open Grafana / Prometheus / Jaeger (stack must already be running)
$urls = @(
    "http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet?orgId=1&refresh=5s&from=now-15m&to=now",
    "http://localhost:9091/graph?g0.expr=sum(rate(rate_limiter_requests_total%5B1m%5D))%20by%20(allowed)&g0.tab=0&g0.range_input=15m",
    "http://localhost:16686/search?service=rate-sidecar&operation=sidecar.proxy&lookback=15m"
)

Write-Host "Opening observability UIs..." -ForegroundColor Cyan
foreach ($u in $urls) {
    Write-Host "  $u"
    try { Start-Process $u } catch { Write-Host "  (open manually)" -ForegroundColor Yellow }
}

Write-Host ""
Write-Host "Jaeger tip: click a trace, then Trace Timeline for the full span DAG." -ForegroundColor Green
Write-Host "Grafana tip: Last 15m + refresh 5s; folder 'Distributed Rate Limiter'." -ForegroundColor Green
