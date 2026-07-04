#!/bin/bash
set -e

echo "=========================================================="
echo "      Distributed Rate Limiter - One-Command Startup       "
echo "=========================================================="

# 1. Start containers
echo "Starting Docker Compose services..."
docker compose up -d

# 2. Helper function to check health
wait_for_health() {
    local url=$1
    local name=$2
    local max_attempts=30
    local attempt=1

    echo -n "Waiting for $name to be healthy..."
    while [ $attempt -le $max_attempts ]; do
        if curl -s -f -o /dev/null "$url"; then
            echo " [OK]"
            return 0
        fi
        echo -n "."
        sleep 2
        attempt=$((attempt + 1))
    done
    echo " [FAILED]"
    return 1
}

# 3. Wait for components
wait_for_health "http://localhost:8080/health" "Central Limiter"
wait_for_health "http://localhost:3000/api/health" "Grafana Server"

# Verify proxy behavior
echo -n "Verifying Sidecar Proxy routing..."
if curl -s -f -o /dev/null "http://localhost:9090/check?user_id=startup_probe"; then
    echo " [OK]"
else
    echo " [FAILED]"
fi

# 4. Start background traffic load
echo "Launching background metrics loader (15 RPS constant)..."
nohup k6 run benchmarks/demo/background-traffic.js > /dev/null 2>&1 &
echo "Background traffic loader launched successfully in background."

echo "=========================================================="
echo "🎉 System is UP and running!"
echo "=========================================================="
echo "Access endpoints:"
echo "  - Sidecar Proxy:       http://localhost:9090/"
echo "  - Central Limiter:     http://localhost:8080/health"
echo "  - Grafana Dashboard:   http://localhost:3000/d/dist-rate-limiter-dashboard/distributed-rate-limiter-fleet"
echo "  - Prometheus Console:  http://localhost:9091/"
echo "  - Jaeger UI:           http://localhost:16686/"
echo "=========================================================="
echo "To run interactive scenario scripts, explore 'scripts/demo/'."
