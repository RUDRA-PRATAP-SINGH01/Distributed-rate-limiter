#!/usr/bin/env python3
"""Parse k6 --summary-export JSON files into a markdown table row."""

import json
import sys
from pathlib import Path


def status_counts(metrics):
    counts = {'200': 0, '429': 0, 'errors': 0, 'other': 0}
    by_status = metrics.get('http_req_duration', {}).get('values', {})
    # k6 summary export stores status in sub-metrics via root.http_reqs.values.count
    # Use http_req_failed + checks for errors; parse tags from custom if present.
    reqs = metrics.get('http_reqs', {}).get('values', {})
    failed = metrics.get('http_req_failed', {}).get('values', {}).get('rate', 0)
    total = reqs.get('count', 0)
    rate = reqs.get('rate', 0)
    return total, rate, failed


def percentile(metrics, p):
    key = f'p({p})'
    return metrics.get('http_req_duration', {}).get('values', {}).get(key, 0)


def parse_summary(path: Path):
    data = json.loads(path.read_text(encoding='utf-8'))
    m = data.get('metrics', {})
    total, rps, fail_rate = status_counts(m)
    return {
        'file': path.name,
        'total': int(total),
        'rps': round(rps, 1),
        'p50': round(percentile(m, 50), 2),
        'p95': round(percentile(m, 95), 2),
        'p99': round(percentile(m, 99), 2),
        'max': round(m.get('http_req_duration', {}).get('values', {}).get('max', 0), 2),
        'fail_rate_pct': round(fail_rate * 100, 2),
        'raw': data,
    }


def main():
    if len(sys.argv) < 2:
        print('usage: parse-k6-summary.py <summary.json> [...]')
        sys.exit(1)
    print('| File | Total | RPS | p50 | p95 | p99 | max | fail% |')
    print('|------|-------|-----|-----|-----|-----|-----|-------|')
    for arg in sys.argv[1:]:
        r = parse_summary(Path(arg))
        print(
            f"| {r['file']} | {r['total']} | {r['rps']} | {r['p50']} | "
            f"{r['p95']} | {r['p99']} | {r['max']} | {r['fail_rate_pct']} |"
        )


if __name__ == '__main__':
    main()
