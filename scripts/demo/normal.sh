#!/bin/bash
echo "=========================================================="
echo "Scenario: Normal Traffic Flow (50 RPS)"
echo "=========================================================="
echo "Running throughput script..."
k6 run -e TARGET_RPS=50 benchmarks/throughput/throughput-test.js
