#!/bin/bash
echo "=========================================================="
echo "Scenario: Redis Sentinel HA Failover Demo"
echo "=========================================================="
echo "In Sentinel mode, client automatically redirects writes to promoted replica."
echo "If running the Sentinel HA stack, kill the master container:"
echo "  docker stop redis-master"
echo "Observe the reconnection log events and circuit status on Grafana."
echo ""
echo "For single-node setup, restarting Redis performs a quick recovery cycle:"
docker restart rate-redis
