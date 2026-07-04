#!/bin/bash
echo "=========================================================="
echo "Scenario: Stripe-Style Idempotency Race (100 concurrent VUs)"
echo "=========================================================="
echo "Running idempotency script..."
k6 run benchmarks/idempotency/idempotency-race.js
