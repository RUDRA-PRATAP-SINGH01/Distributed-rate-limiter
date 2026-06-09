import json
import statistics
from pathlib import Path

BENCHMARKS_DIR = Path(__file__).resolve().parent
METRICS_DIR = BENCHMARKS_DIR / 'metrics' / 'results'
TEST_DURATION_SEC = 60
P99_SUSTAINABLE_MS = 100
ERROR_RATE_SUSTAINABLE = 0.01


def parse_k6_json(filepath):
    durations = []
    failed = 0
    rate_limited = 0
    allowed = 0

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
                if tags.get('status') != '429':
                    failed += 1
            elif metric == 'http_reqs':
                status = tags.get('status')
                if status == '429':
                    rate_limited += 1
                elif status == '200':
                    allowed += 1

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
        'allowed': allowed,
        'error_rate': failed / n if n else 0,
    }


def load_metrics(label):
    """Load docker stats averages saved by collect-metrics script."""
    metrics_file = METRICS_DIR / f'{label}.json'
    if not metrics_file.is_file():
        return None
    with open(metrics_file, encoding='utf-8') as f:
        return json.load(f)


def collect_results(test_type):
    results_dir = BENCHMARKS_DIR / test_type / 'results'
    if not results_dir.is_dir():
        return []

    rows = []
    for json_file in sorted(results_dir.glob('*.json'), key=lambda p: _sort_key(p.stem)):
        label = json_file.stem
        stats = parse_k6_json(json_file)
        if not stats:
            continue
        target_rps = int(label) if label.isdigit() else None
        stats['target_rps'] = target_rps
        stats['metrics'] = load_metrics(f'{test_type}-{label}')
        rows.append((label, stats))
    return rows


def _sort_key(label):
    return (0, int(label)) if label.isdigit() else (1, label)


def find_max_sustainable(rows):
    """Max actual RPS where p99 < threshold and error rate < 1%."""
    candidates = []
    for label, stats in rows:
        label_str = str(label)
        if not label_str.isdigit():
            continue
        if stats['p99'] < P99_SUSTAINABLE_MS and stats['error_rate'] < ERROR_RATE_SUSTAINABLE:
            candidates.append(stats['actual_rps'])

    if not candidates:
        return None
    return max(candidates)


def find_collapse_point(rows):
    """First target RPS where p99 exceeds threshold or error rate spikes."""
    numeric = sorted(
        [(int(label), stats) for label, stats in rows if label.isdigit()],
        key=lambda x: x[0],
    )
    for target, stats in numeric:
        if stats['p99'] >= P99_SUSTAINABLE_MS or stats['error_rate'] >= ERROR_RATE_SUSTAINABLE:
            return target, stats
    return None


def format_metrics(stats):
    m = stats.get('metrics')
    if not m:
        return '-', '-', '-', '-'
    return (
        f"{m.get('limiter_cpu_avg', '-'):.0f}%" if isinstance(m.get('limiter_cpu_avg'), (int, float)) else '-',
        f"{m.get('sidecar_cpu_avg', '-'):.0f}%" if isinstance(m.get('sidecar_cpu_avg'), (int, float)) else '-',
        f"{m.get('redis_cpu_avg', '-'):.0f}%" if isinstance(m.get('redis_cpu_avg'), (int, float)) else '-',
        f"{m.get('total_mem_avg_mb', '-'):.0f}MB" if isinstance(m.get('total_mem_avg_mb'), (int, float)) else '-',
    )


def print_table(title, rows, rps_label='Target RPS'):
    print(f'\n## {title}\n')
    print(
        f'| {rps_label} | Actual RPS | p50 (ms) | p95 (ms) | p99 (ms) | '
        f'Limiter CPU | Sidecar CPU | Redis CPU | Memory | Failed | 429s |'
    )
    print(
        f'|{"-" * len(rps_label)}|------------|----------|----------|----------|'
        f'-------------|-------------|-----------|--------|--------|------|'
    )
    for label, stats in rows:
        lim_cpu, side_cpu, redis_cpu, mem = format_metrics(stats)
        target = stats.get('target_rps')
        target_str = str(target) if target is not None else label
        print(
            f'| {target_str} '
            f'| {stats["actual_rps"]:.0f} '
            f'| {stats["p50"]:.2f} '
            f'| {stats["p95"]:.2f} '
            f'| {stats["p99"]:.2f} '
            f'| {lim_cpu} '
            f'| {side_cpu} '
            f'| {redis_cpu} '
            f'| {mem} '
            f'| {stats["failed"]} '
            f'| {stats["rate_limited"]} |'
        )


def print_saturation_analysis(throughput_rows, saturation_rows):
    combined = throughput_rows + saturation_rows
    max_rps = find_max_sustainable(combined)
    collapse = find_collapse_point(combined)

    print('\n## Saturation Analysis\n')
    if max_rps:
        print(
            f'System sustains **{max_rps:.0f} actual RPS** with '
            f'p99 < {P99_SUSTAINABLE_MS}ms and error rate < {ERROR_RATE_SUSTAINABLE * 100:.0f}%.'
        )
    else:
        print('No sustainable throughput point found within tested range.')

    if collapse:
        target, stats = collapse
        print(
            f'Beyond **{target} target RPS** (actual {stats["actual_rps"]:.0f}), '
            f'p99 rises to {stats["p99"]:.0f}ms with {stats["error_rate"] * 100:.1f}% errors - '
            f'latency grows exponentially.'
        )


def print_enforcement_summary(rows):
    if not rows:
        return
    _, stats = rows[0]
    total = stats['allowed'] + stats['rate_limited'] + stats['failed']
    print('\n## Enforcement Summary\n')
    print(f'| Sent | Allowed (200) | Rejected (429) | Failed |')
    print(f'|------|---------------|----------------|--------|')
    print(
        f'| {total} '
        f'| {stats["allowed"]} '
        f'| {stats["rate_limited"]} '
        f'| {stats["failed"]} |'
    )


def main():
    throughput = collect_results('throughput')
    saturation = collect_results('saturation')
    hotkey = collect_results('hot-key')
    enforcement = collect_results('enforcement')

    print_table('Throughput Results', throughput)
    if saturation:
        print_table('Saturation Sweep Results', saturation)
    print_saturation_analysis(throughput, saturation)
    print_table('Hot-Key Results', hotkey, rps_label='Target RPS')
    print_table('Enforcement Results', enforcement, rps_label='Test')
    print_enforcement_summary(enforcement)


if __name__ == '__main__':
    main()
