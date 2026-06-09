import json
import statistics
from pathlib import Path

BENCHMARKS_DIR = Path(__file__).resolve().parent
TEST_DURATION_SEC = 60


def parse_k6_json(filepath):
    durations = []
    failed = 0
    rate_limited = 0

    with open(filepath, encoding='utf-8') as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            point = json.loads(line)
            if point['type'] != 'Point':
                continue
            metric = point['metric']
            tags = point['data'].get('tags', {})

            if metric == 'http_req_duration':
                durations.append(point['data']['value'])
            elif metric == 'http_req_failed' and point['data']['value'] == 1:
                failed += 1
            elif metric == 'http_reqs' and tags.get('status') == '429':
                rate_limited += 1

    if not durations:
        return None

    sorted_d = sorted(durations)
    n = len(sorted_d)

    def percentile(p):
        if n == 1:
            return sorted_d[0]
        idx = (p / 100) * (n - 1)
        lo, hi = int(idx), min(int(idx) + 1, n - 1)
        frac = idx - lo
        return sorted_d[lo] * (1 - frac) + sorted_d[hi] * frac

    return {
        'p50': percentile(50),
        'p95': percentile(95),
        'p99': percentile(99),
        'avg': statistics.mean(durations),
        'count': n,
        'actual_rps': n / TEST_DURATION_SEC,
        'failed': failed,
        'rate_limited': rate_limited,
    }


def collect_results(test_type):
    results_dir = BENCHMARKS_DIR / test_type / 'results'
    if not results_dir.is_dir():
        return []

    rows = []
    for json_file in sorted(results_dir.glob('*.json')):
        label = json_file.stem
        stats = parse_k6_json(json_file)
        if stats:
            rows.append((label, stats))
    return rows


def print_table(title, rows, rps_label='RPS'):
    print(f'\n## {title}\n')
    print(f'| {rps_label} | Actual RPS | p50 (ms) | p95 (ms) | p99 (ms) | Failed | 429s |')
    print(f'|{"-" * len(rps_label)}|------------|----------|----------|----------|--------|------|')
    for label, stats in rows:
        print(
            f'| {label} '
            f'| {stats["actual_rps"]:.0f} '
            f'| {stats["p50"]:.2f} '
            f'| {stats["p95"]:.2f} '
            f'| {stats["p99"]:.2f} '
            f'| {stats["failed"]} '
            f'| {stats["rate_limited"]} |'
        )


def main():
    print_table('Throughput Results', collect_results('throughput'))
    print_table('Hot-Key Results', collect_results('hot-key'), rps_label='Target RPS')
    print_table('Enforcement Results', collect_results('enforcement'), rps_label='Test')


if __name__ == '__main__':
    main()
