#!/bin/bash
echo "=========================================================="
echo "Scenario: Infrastructure Chaos Engineering Simulation"
echo "=========================================================="
echo "1. Spawning payments routing traffic in background..."
nohup k6 run benchmarks/routing/routing-test.js > /dev/null 2>&1 &
K6_PID=$!
sleep 3

echo "2. Injecting fault: stopping gateway-b..."
docker stop gateway-b
sleep 10

echo "3. Injecting fault: stopping Redis database..."
docker stop rate-redis
sleep 10

echo "4. Recovering system: starting gateway-b and Redis..."
docker start gateway-b
docker start rate-redis

# Wait for k6 to finish
wait $K6_PID
echo "=========================================================="
echo "🎉 Chaos test completed successfully. Check Grafana for the error peaks!"
echo "=========================================================="
