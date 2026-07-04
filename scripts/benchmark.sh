#!/bin/bash
set -e

echo "=========================================================="
echo "      Distributed Rate Limiter - Benchmark Runner        "
echo "=========================================================="

FULL_RUN=false
for arg in "$@"; do
  if [ "$arg" == "--full" ]; then
    FULL_RUN=true
  fi
done

# Ensure results directory exists
mkdir -p benchmarks/throughput/results

if [ "$FULL_RUN" = true ]; then
  echo "Running FULL production benchmark suite..."
  if command -v pwsh &> /dev/null; then
    pwsh ./benchmarks/run-all.ps1
  else
    echo "Error: PowerShell Core (pwsh) is required to run the full parallel metrics collection."
    exit 1
  fi
else
  echo "Running QUICK benchmark validation (10 seconds, 100 RPS)..."
  k6 run -e TARGET_RPS=100 --duration 10s benchmarks/throughput/throughput-test.js --out json=benchmarks/throughput/results/100.json
  
  echo "Compiling parser records..."
  python benchmarks/parse-results.py
  
  echo "Regenerating Matplotlib performance graphs..."
  python benchmarks/graphs/generate-graphs.py
  echo "=========================================================="
  echo "🎉 Benchmark validation completed successfully!"
  echo "Graphs generated in: benchmarks/graphs/"
  echo "=========================================================="
fi
