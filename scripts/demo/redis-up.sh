#!/bin/bash
echo "=========================================================="
echo "Scenario: Redis Recovery Simulation"
echo "=========================================================="
echo "Starting Redis container..."
docker start rate-redis
echo "Redis is now ONLINE. Circuit states should transition from OPEN -> HALF-OPEN -> CLOSED."
