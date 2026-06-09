import importlib.util
import sys
from pathlib import Path

import matplotlib.pyplot as plt

BENCHMARKS_DIR = Path(__file__).resolve().parent.parent
OUTPUT_DIR = Path(__file__).resolve().parent


def load_parser():
    spec = importlib.util.spec_from_file_location(
        'parse_results', BENCHMARKS_DIR / 'parse-results.py'
    )
    module = importlib.util.module_from_spec(spec)
    sys.modules['parse_results'] = module
    spec.loader.exec_module(module)
    return module


def numeric_rows(rows):
    items = [(int(label), stats) for label, stats in rows if label.isdigit()]
    items.sort(key=lambda x: x[0])
    return items


def plot_latency_vs_actual_rps(numeric, out_path, parser, rows):
    if not numeric:
        return False

    actual = [s['actual_rps'] for _, s in numeric]
    p50 = [s['p50'] for _, s in numeric]
    p95 = [s['p95'] for _, s in numeric]
    p99 = [s['p99'] for _, s in numeric]

    plt.figure(figsize=(9, 5))
    plt.plot(actual, p50, marker='o', label='p50')
    plt.plot(actual, p95, marker='s', label='p95')
    plt.plot(actual, p99, marker='^', label='p99')

    max_sustainable = parser.find_max_sustainable(rows)
    if max_sustainable:
        plt.axvline(max_sustainable, color='green', linestyle='--', alpha=0.7,
                    label=f'Max sustainable ({max_sustainable:.0f} RPS)')

    plt.xlabel('Actual throughput (requests/sec achieved)')
    plt.ylabel('Latency (ms)')
    plt.title('Latency vs Actual Throughput')
    plt.legend()
    plt.grid(True, alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_saturation_curve(numeric, out_path, parser, rows):
    if not numeric:
        return False

    targets = [t for t, _ in numeric]
    actual = [s['actual_rps'] for _, s in numeric]

    plt.figure(figsize=(9, 5))
    plt.plot(targets, actual, marker='o', color='steelblue', linewidth=2, label='Actual RPS')
    plt.plot(targets, targets, linestyle='--', color='gray', alpha=0.6, label='Ideal (target = actual)')

    max_sustainable = parser.find_max_sustainable(rows)
    if max_sustainable:
        plt.axhline(max_sustainable, color='green', linestyle='--', alpha=0.7,
                    label=f'Max sustainable ({max_sustainable:.0f} RPS)')

    plt.xlabel('Target requests per second')
    plt.ylabel('Actual requests per second achieved')
    plt.title('Saturation Curve — Target vs Actual Throughput')
    plt.legend()
    plt.grid(True, alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_error_rates(numeric, out_path):
    if not numeric:
        return False

    actual = [s['actual_rps'] for _, s in numeric]
    failed_pct = [100 * s['failed'] / s['count'] for _, s in numeric]
    limited_pct = [100 * s['rate_limited'] / s['count'] for _, s in numeric]

    plt.figure(figsize=(9, 5))
    width = max(actual) * 0.04 if actual else 10
    plt.bar(actual, failed_pct, width=width, label='Failed (%)', alpha=0.85, color='crimson')
    plt.bar([a + width * 1.2 for a in actual], limited_pct, width=width,
            label='Rate limited 429 (%)', alpha=0.85, color='orange')
    plt.xlabel('Actual throughput (requests/sec achieved)')
    plt.ylabel('Percentage of requests')
    plt.title('Error and Rate-Limit Rate vs Actual Throughput')
    plt.legend()
    plt.grid(True, axis='y', alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_resource_utilization(rows, out_path):
    combined = [(label, stats) for label, stats in rows
                  if label.isdigit() and stats.get('metrics')]
    if not combined:
        return False

    combined.sort(key=lambda x: int(x[0]))
    actual = [s['actual_rps'] for _, s in combined]
    limiter_cpu = [s['metrics']['limiter_cpu_avg'] for _, s in combined]
    sidecar_cpu = [s['metrics']['sidecar_cpu_avg'] for _, s in combined]
    redis_cpu = [s['metrics']['redis_cpu_avg'] for _, s in combined]

    plt.figure(figsize=(9, 5))
    plt.plot(actual, limiter_cpu, marker='o', label='Limiter CPU %')
    plt.plot(actual, sidecar_cpu, marker='s', label='Sidecar CPU %')
    plt.plot(actual, redis_cpu, marker='^', label='Redis CPU %')
    plt.xlabel('Actual throughput (requests/sec achieved)')
    plt.ylabel('CPU utilization (%)')
    plt.title('Resource Utilization vs Actual Throughput')
    plt.legend()
    plt.grid(True, alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_enforcement_allowed_vs_rejected(rows, out_path):
    if not rows:
        return False

    _, stats = rows[0]
    allowed = stats['allowed']
    rejected = stats['rate_limited']
    failed = stats['failed']

    labels = ['Allowed (200)', 'Rejected (429)', 'Failed (5xx)']
    values = [allowed, rejected, failed]
    colors = ['#2ecc71', '#e74c3c', '#95a5a6']

    plt.figure(figsize=(7, 5))
    bars = plt.bar(labels, values, color=colors, alpha=0.9)
    plt.ylabel('Request count')
    plt.title(f'Enforcement Correctness — {allowed + rejected + failed} requests sent')
    plt.grid(True, axis='y', alpha=0.3)

    for bar, val in zip(bars, values):
        plt.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 5,
                 str(val), ha='center', va='bottom', fontweight='bold')

    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_single_test(title, rows, out_path, x_key='actual_rps'):
    if not rows:
        return False

    labels = []
    p99 = []
    for label, stats in rows:
        if x_key == 'actual_rps' and stats.get('target_rps'):
            labels.append(f"{stats['actual_rps']:.0f}")
        else:
            labels.append(label)
        p99.append(stats['p99'])

    plt.figure(figsize=(6, 4))
    plt.bar(labels, p99, color='steelblue', alpha=0.85)
    plt.xlabel('Actual RPS' if x_key == 'actual_rps' else 'Test')
    plt.ylabel('p99 latency (ms)')
    plt.title(title)
    plt.grid(True, axis='y', alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def main():
    parser = load_parser()
    saved = []

    throughput = parser.collect_results('throughput')
    saturation = parser.collect_results('saturation')
    combined_rows = throughput + saturation
    combined_numeric = numeric_rows(combined_rows)

    if plot_latency_vs_actual_rps(combined_numeric, OUTPUT_DIR / 'latency-vs-rps.png', parser, combined_rows):
        saved.append('latency-vs-rps.png')
    if plot_saturation_curve(combined_numeric, OUTPUT_DIR / 'saturation-curve.png', parser, combined_rows):
        saved.append('saturation-curve.png')
    if plot_error_rates(combined_numeric, OUTPUT_DIR / 'error-rate-vs-rps.png'):
        saved.append('error-rate-vs-rps.png')
    if plot_resource_utilization(combined_rows, OUTPUT_DIR / 'resource-utilization.png'):
        saved.append('resource-utilization.png')

    hotkey = parser.collect_results('hot-key')
    if plot_single_test('Hot-Key Test — p99 Latency', hotkey, OUTPUT_DIR / 'hot-key-latency.png'):
        saved.append('hot-key-latency.png')

    enforcement = parser.collect_results('enforcement')
    if plot_enforcement_allowed_vs_rejected(enforcement, OUTPUT_DIR / 'enforcement-allowed-vs-rejected.png'):
        saved.append('enforcement-allowed-vs-rejected.png')
    if plot_single_test('Enforcement Test — p99 Latency', enforcement,
                        OUTPUT_DIR / 'enforcement-latency.png', x_key='label'):
        saved.append('enforcement-latency.png')

    if saved:
        print('Saved graphs:')
        for name in saved:
            print(f'  {OUTPUT_DIR / name}')
    else:
        print('No benchmark results found. Run k6 tests first.')


if __name__ == '__main__':
    main()
