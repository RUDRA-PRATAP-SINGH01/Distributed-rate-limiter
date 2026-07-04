#!/bin/bash
echo "=========================================================="
echo "Scenario: Hot Key / Single User Spam (5000 RPS)"
echo "=========================================================="
echo "Running hot-key script..."
k6 run benchmarks/hot-key/hot-key-test.js
