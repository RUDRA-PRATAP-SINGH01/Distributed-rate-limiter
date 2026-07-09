#!/usr/bin/env python3
"""Parse k6 JSON stream export for latency percentiles and HTTP status counts."""

import json
import statistics
import sys
from pathlib import Path


def percentile(sorted_vals, p):
    if not sorted_vals:
        return 0.0
    n = len(sorted_vals)
    if n == 1:
        return sorted_vals[0]
    idx = (p / 100) * (n - 1)
    lo, hi = int(idx), min(int(idx) + 1, n - 1)
    frac = idx - lo
    return sorted_vals[lo] * (1 - frac) + sorted_vals[hi] * frac


def parse_stream(path: Path, duration_sec: float = 60.0):
    durations = []
    status = {'200': 0, '429': 0, 'errors': 0, 'other': 0}

    with path.open(encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                point = json.loads(line)
            except json.JSONDecodeError:
                continue
            if point.get('type') != 'Point':
                continue
            metric = point.get('metric')
            data = point.get('data', {})
            if metric == 'http_req_duration':
                durations.append(data['value'])
            elif metric == 'http_reqs':
                code = data.get('tags', {}).get('status', 'other')
                if code == '200':
                    status['200'] += 1
                elif code == '429':
                    status['429'] += 1
                elif code == '409':
                    status['other'] += 1  # idempotency in-progress, not infra error
                elif code.startswith('5') or (code.startswith('4') and code not in ('429', '409')):
                    status['errors'] += 1
                else:
                    status['other'] += 1

    if not durations:
        return None

    sd = sorted(durations)
    total = len(sd)
    return {
        'total': total,
        'rps': round(total / duration_sec, 1),
        'p50': round(percentile(sd, 50), 2),
        'p95': round(percentile(sd, 95), 2),
        'p99': round(percentile(sd, 99), 2),
        'p999': round(percentile(sd, 99.9), 2) if total >= 1000 else None,
        'max': round(max(sd), 2),
        'avg': round(statistics.mean(sd), 2),
        **status,
    }


def main():
    if len(sys.argv) < 2:
        print('usage: parse-k6-stream.py <stream.json> [duration_sec]')
        sys.exit(1)
    path = Path(sys.argv[1])
    dur = float(sys.argv[2]) if len(sys.argv) > 2 else 60.0
    r = parse_stream(path, dur)
    if not r:
        print('no data')
        sys.exit(1)
    p999 = r['p999'] if r['p999'] is not None else '-'
    print(
        f"total={r['total']} rps={r['rps']} p50={r['p50']} p95={r['p95']} "
        f"p99={r['p99']} p999={p999} max={r['max']} "
        f"200={r['200']} 429={r['429']} errors={r['errors']} other={r['other']}"
    )


if __name__ == '__main__':
    main()
