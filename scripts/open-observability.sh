#!/usr/bin/env bash
# Re-open Grafana / Prometheus / Jaeger (stack must already be running)
set -euo pipefail

urls=(
  "http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet?orgId=1&refresh=5s&from=now-15m&to=now"
  "http://localhost:9091/graph?g0.expr=sum(rate(rate_limiter_requests_total%5B1m%5D))%20by%20(allowed)&g0.tab=0&g0.range_input=15m"
  "http://localhost:16686/search?service=rate-sidecar&operation=sidecar.proxy&lookback=15m"
)

echo "Opening observability UIs..."
for u in "${urls[@]}"; do
  echo "  $u"
  if command -v xdg-open >/dev/null 2>&1; then xdg-open "$u" >/dev/null 2>&1 || true
  elif command -v open >/dev/null 2>&1; then open "$u" >/dev/null 2>&1 || true
  fi
done

echo ""
echo "Jaeger tip: click a trace → Trace Timeline for the full span DAG."
echo "Grafana tip: Last 15m + refresh 5s; folder 'Distributed Rate Limiter'."
