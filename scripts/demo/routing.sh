#!/bin/bash
echo "=========================================================="
echo "Scenario: Dynamic Gateway Routing & Failovers"
echo "=========================================================="
echo "Running routing test script..."
k6 run benchmarks/routing/routing-test.js
