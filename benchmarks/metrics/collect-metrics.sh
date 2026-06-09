#!/usr/bin/env bash
# Collect docker stats during a k6 benchmark run.
# Usage: ./collect-metrics.sh throughput-1000 65

set -euo pipefail

TEST_NAME="${1:?Usage: collect-metrics.sh <test-name> [duration-sec]}"
DURATION="${2:-65}"
INTERVAL=5
CONTAINERS="rate-limiter rate-sidecar rate-redis demo-backend"
OUT_DIR="$(dirname "$0")/results"
mkdir -p "$OUT_DIR"

RAW_FILE="$OUT_DIR/${TEST_NAME}-raw.jsonl"
SUMMARY_FILE="$OUT_DIR/${TEST_NAME}.json"

echo "Collecting docker stats for ${DURATION}s..."
END=$((SECONDS + DURATION))

> "$RAW_FILE"
while [ "$SECONDS" -lt "$END" ]; do
    TS="$(date -Iseconds)"
    docker stats --no-stream --format '{{json .}}' $CONTAINERS 2>/dev/null | while read -r line; do
        echo "{\"timestamp\":\"$TS\",\"data\":$line}" >> "$RAW_FILE"
    done
    sleep "$INTERVAL"
done

python3 - "$RAW_FILE" "$SUMMARY_FILE" "$TEST_NAME" <<'PY'
import json, re, sys
from collections import defaultdict

raw_file, summary_file, test_name = sys.argv[1:4]
samples = []
with open(raw_file) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        data = row.get('data', row)
        samples.append({
            'container': data['Name'],
            'cpu': float(data['CPUPerc'].replace('%', '')),
            'mem_mb': _parse_mem(data['MemUsage']),
        })

def _parse_mem(s):
    m = re.search(r'([\d.]+)\s*MiB', s)
    if m: return float(m.group(1))
    m = re.search(r'([\d.]+)\s*GiB', s)
    if m: return float(m.group(1)) * 1024
    return 0.0

by_container = defaultdict(list)
for s in samples:
    by_container[s['container']].append(s)

summary = {'test': test_name, 'samples': len(samples)}
key_map = {'rate-limiter': 'limiter', 'rate-sidecar': 'sidecar', 'rate-redis': 'redis'}
for container, rows in by_container.items():
    key = key_map.get(container, container)
    summary[f'{key}_cpu_avg'] = round(sum(r['cpu'] for r in rows) / len(rows), 1)
    summary[f'{key}_mem_avg_mb'] = round(sum(r['mem_mb'] for r in rows) / len(rows), 1)

summary['total_mem_avg_mb'] = round(sum(r['mem_mb'] for r in samples), 1)
with open(summary_file, 'w') as f:
    json.dump(summary, f, indent=2)
print(f'Saved metrics to {summary_file}')
PY
