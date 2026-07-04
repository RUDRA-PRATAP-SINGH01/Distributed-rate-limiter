#!/bin/bash
echo "=========================================================="
echo "Scenario: Progressive System Saturation (500 RPS)"
echo "=========================================================="
echo "Running saturation script..."
k6 run -e TARGET_RPS=500 benchmarks/saturation/saturation-test.js
