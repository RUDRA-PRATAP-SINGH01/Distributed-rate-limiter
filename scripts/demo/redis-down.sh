#!/bin/bash
echo "=========================================================="
echo "Scenario: Redis Crash Simulation"
echo "=========================================================="
echo "Stopping Redis container..."
docker stop rate-redis
echo "Redis is now OFFLINE. Send traffic now to see 100% Error Rate and health indicators drop."
