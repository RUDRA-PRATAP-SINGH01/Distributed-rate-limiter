#!/usr/bin/env bash
# Distributed Rate Limiter — one-command stack + dashboards
# Usage (from repo root):
#   ./scripts/start.sh
#   ./scripts/start.sh --no-browser
#   ./scripts/start.sh --no-traffic
#   ./scripts/start.sh --build

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

NO_BROWSER=0
NO_TRAFFIC=0
BUILD=0
for arg in "$@"; do
  case "$arg" in
    --no-browser) NO_BROWSER=1 ;;
    --no-traffic) NO_TRAFFIC=1 ;;
    --build) BUILD=1 ;;
  esac
done

banner() {
  echo ""
  echo "=============================================================="
  echo "$1"
  echo "=============================================================="
}

wait_for() {
  local url="$1" name="$2" max="${3:-45}" attempt=1
  echo -n "Waiting for $name..."
  while [ "$attempt" -le "$max" ]; do
    if curl -s -o /dev/null -w "" --max-time 2 "$url" 2>/dev/null; then
      code="$(curl -s -o /dev/null -w "%{http_code}" --max-time 2 "$url" || true)"
      if [ "$code" -ge 200 ] && [ "$code" -lt 500 ]; then
        echo " OK"
        return 0
      fi
    fi
    echo -n "."
    sleep 2
    attempt=$((attempt + 1))
  done
  echo " FAILED"
  return 1
}

open_urls() {
  local urls=(
    "http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet?orgId=1&refresh=5s&from=now-15m&to=now"
    "http://localhost:9091/graph?g0.expr=sum(rate(rate_limiter_requests_total%5B1m%5D))%20by%20(allowed)&g0.tab=0&g0.range_input=15m"
    "http://localhost:16686/search?service=rate-sidecar&operation=sidecar.proxy&lookback=15m"
  )
  for u in "${urls[@]}"; do
    if command -v xdg-open >/dev/null 2>&1; then xdg-open "$u" >/dev/null 2>&1 || true
    elif command -v open >/dev/null 2>&1; then open "$u" >/dev/null 2>&1 || true
    else echo "Open manually: $u"
    fi
  done
}

fallback_traffic() {
  echo "k6 not found — starting bash fallback traffic (~10 RPS)..."
  (
    while true; do
      for i in 1 2 3 4 5 6 7 8 9 10; do
        uid="bg_user_$(( (RANDOM % 5) + 1 ))"
        curl -s -o /dev/null -H "X-User-ID: ${uid}" "http://localhost:9090/" || true
      done
      k="$(uuidgen 2>/dev/null || echo "demo-$RANDOM")"
      curl -s -o /dev/null -X POST "http://localhost:9090/api/orders" \
        -H "Content-Type: application/json" -H "X-User-ID: bg_idem" \
        -H "Idempotency-Key: $k" -d '{"amount":1}' || true
      sleep 1
    done
  ) >/dev/null 2>&1 &
  echo $! > /tmp/drl-bg-traffic.pid
  echo "Background traffic PID $(cat /tmp/drl-bg-traffic.pid) (kill \$(cat /tmp/drl-bg-traffic.pid) to stop)"
}

banner "Distributed Rate Limiter — Dashboard Quick Start"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required. Install Docker and retry."
  exit 1
fi

echo "Starting Docker Compose stack..."
if [ "$BUILD" -eq 1 ]; then
  docker compose up -d --build
else
  docker compose up -d
fi

ok=0
wait_for "http://localhost:8080/health" "Limiter" || ok=1
wait_for "http://localhost:9090/health" "Sidecar" || ok=1
wait_for "http://localhost:3000/api/health" "Grafana" || ok=1
wait_for "http://localhost:9091/-/healthy" "Prometheus" || ok=1
wait_for "http://localhost:16686/" "Jaeger" || ok=1

echo -n "Probe sidecar proxy..."
code="$(curl -s -o /dev/null -w "%{http_code}" -H "X-User-ID: startup_probe" "http://localhost:9090/" || true)"
if [ "$code" = "200" ] || [ "$code" = "429" ]; then echo " OK ($code)"; else echo " WARN ($code)"; fi

if [ "$NO_TRAFFIC" -eq 0 ]; then
  if command -v k6 >/dev/null 2>&1; then
    echo "Launching k6 background traffic (15 RPS)..."
    nohup k6 run benchmarks/demo/background-traffic.js >/tmp/drl-k6-bg.log 2>&1 &
    echo "k6 started (log: /tmp/drl-k6-bg.log)"
  else
    fallback_traffic
  fi
  for i in 1 2 3 4 5; do
    curl -s -o /dev/null -H "X-User-ID: demo_$i" "http://localhost:9090/" || true
  done
  k="$(uuidgen 2>/dev/null || echo "seed-$RANDOM")"
  curl -s -o /dev/null -X POST "http://localhost:9090/api/orders" \
    -H "Content-Type: application/json" -H "X-User-ID: demo" \
    -H "Idempotency-Key: $k" -d '{"amount":100}' || true
  curl -s -o /dev/null -X POST "http://localhost:9090/api/orders" \
    -H "Content-Type: application/json" -H "X-User-ID: demo" \
    -H "Idempotency-Key: $k" -d '{"amount":100}' || true
fi

if [ "$NO_BROWSER" -eq 0 ]; then
  echo "Opening Grafana, Prometheus, and Jaeger..."
  open_urls
fi

banner "Stack is ready — open these URLs"
cat <<'EOF'
  Grafana fleet dashboard
    http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet
    (anonymous access enabled — no login)

  Prometheus (try: sum(rate(rate_limiter_requests_total[1m])) by (allowed))
    http://localhost:9091/

  Jaeger traces (Service=rate-sidecar, Operation=sidecar.proxy)
    http://localhost:16686/

  Sidecar / Limiter
    http://localhost:9090/   |   http://localhost:8080/health

Tips:
  - Grafana: Last 15 minutes, refresh 5s
  - Jaeger: open a trace → Trace Timeline for the span DAG (not System Architecture)
  - Re-open UIs later:  ./scripts/open-observability.sh
  - Stop stack:         docker compose down
EOF

exit "$ok"
