#!/usr/bin/env bash
# Distributed Rate Limiter — test automation runner
# Usage (from repo root):
#   ./scripts/qa.sh unit
#   ./scripts/qa.sh process-smoke
#   ./scripts/qa.sh smoke
#   ./scripts/qa.sh sanity
#   ./scripts/qa.sh sanity --changed
#   ./scripts/qa.sh race
#   ./scripts/qa.sh integration
#   ./scripts/qa.sh coverage
#   ./scripts/qa.sh quality-gate
#   ./scripts/qa.sh exploratory

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MODE="${1:-help}"
CHANGED=0
shift || true
for arg in "$@"; do
  case "$arg" in
    --changed|-Changed) CHANGED=1 ;;
  esac
done

banner() {
  echo ""
  echo "=============================================================="
  echo "$1"
  echo "=============================================================="
}

run_go() {
  echo "go $*"
  go "$@"
}

changed_packages() {
  {
    git diff --name-only
    git diff --name-only --cached
    git diff --name-only origin/main...HEAD 2>/dev/null || true
  } | awk '
    /\.go$/ {
      n = split($0, a, "/")
      if (n < 2) next
      dir = a[1]
      for (i = 2; i < n; i++) dir = dir "/" a[i]
      if (dir !~ /^(vendor|testdata)/) print "./" dir
    }
  ' | sort -u
}

help() {
  cat <<'EOF'
Test automation framework — Distributed Rate Limiter

  unit            Full unit + handler suite (go test ./...)
  process-smoke   Fast critical-path tests (no Docker)
  smoke           Live stack is-up checks (needs compose)
  sanity          Live happy-path checks after a change
  sanity --changed Also run go test on packages touched in git
  race            Race detector
  integration     Lua packages against REDIS_TEST_ADDR
  coverage        coverage.out + func summary
  quality-gate    vet + process-smoke + unit (local merge bar)
  exploratory     Print session charters (manual)

Docs: docs/testing/quality-management.md
EOF
}

process_smoke() {
  banner "Process smoke (white-box critical path, no Docker)"
  run_go test -count=1 -timeout 60s ./cmd/limiter/ \
    -run 'TestHealthEndpoint_Healthy$|TestCheckHandler_TokenBucket$'
  run_go test -count=1 -timeout 60s ./cmd/sidecar/ \
    -run 'TestSidecarHealth_LimiterAndRedisHealthy$'
}

live_smoke() {
  banner "Deploy smoke (black-box, live stack)"
  QA_REQUIRE_STACK=1 run_go test -count=1 -tags=smoke -timeout 2m -v ./tests/smoke/...
}

live_sanity() {
  banner "Sanity (black-box happy path, live stack)"
  QA_REQUIRE_STACK=1 run_go test -count=1 -tags=sanity -timeout 2m -v ./tests/sanity/...
  if [ "$CHANGED" -eq 1 ]; then
    mapfile -t pkgs < <(changed_packages)
    if [ "${#pkgs[@]}" -eq 0 ]; then
      echo "No changed Go packages — live sanity only."
      return
    fi
    banner "Sanity on changed packages: ${pkgs[*]}"
    run_go test -count=1 -timeout 5m "${pkgs[@]}"
  fi
}

case "$MODE" in
  help|-h|--help) help ;;
  unit)
    banner "Unit + handler tests"
    run_go test -count=1 ./...
    ;;
  process-smoke) process_smoke ;;
  smoke) live_smoke ;;
  sanity) live_sanity ;;
  race)
    banner "Race detector"
    run_go test -count=1 -race ./...
    ;;
  integration)
    banner "Redis integration (set REDIS_TEST_ADDR)"
    if [ -z "${REDIS_TEST_ADDR:-}" ]; then
      echo "REDIS_TEST_ADDR is required (example: 127.0.0.1:6379)" >&2
      exit 1
    fi
    run_go test -count=1 -p 1 -v \
      ./internal/limiter/... \
      ./internal/circuitbreaker/... \
      ./internal/idempotency/... \
      ./internal/audit/... \
      ./internal/routing/...
    ;;
  coverage)
    banner "Coverage"
    run_go test -count=1 -coverprofile=coverage.out ./...
    run_go tool cover -func=coverage.out
    ;;
  quality-gate)
    banner "Quality gate (local merge bar)"
    run_go vet ./...
    process_smoke
    run_go test -count=1 ./...
    echo ""
    echo "Quality gate passed: vet + process-smoke + unit."
    echo "CI also runs lint, vuln, race, redis-integration, chaos."
    ;;
  exploratory)
    banner "Exploratory testing — session charters"
    echo "Open and timebox one charter from:"
    echo "  docs/testing/exploratory-charters.md"
    echo ""
    echo "Suggested next sessions:"
    echo "  ET-1  Quota allow / deny / headers"
    echo "  ET-2  Auth: no ?user_id=, missing key = 401"
    echo "  ET-3  Redis down -> 503 fail-closed"
    echo "  ET-4  Hierarchical tenant / user / endpoint"
    echo "  ET-5  Idempotency replay vs first write"
    echo "  ET-6  Routing failover + circuit"
    echo "  ET-7  Admin override visible on next /check"
    echo "  ET-8  Grafana / Jaeger /health after start.ps1"
    ;;
  *)
    echo "unknown mode: $MODE" >&2
    help
    exit 1
    ;;
esac
