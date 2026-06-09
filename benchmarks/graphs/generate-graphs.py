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


def plot_throughput_latency(rows, out_path):
    numeric = [(int(label), stats) for label, stats in rows if label.isdigit()]
    if not numeric:
        return False

    numeric.sort()
    rps = [r for r, _ in numeric]
    p50 = [s['p50'] for _, s in numeric]
    p95 = [s['p95'] for _, s in numeric]
    p99 = [s['p99'] for _, s in numeric]

    plt.figure(figsize=(8, 5))
    plt.plot(rps, p50, marker='o', label='p50')
    plt.plot(rps, p95, marker='s', label='p95')
    plt.plot(rps, p99, marker='^', label='p99')
    plt.xlabel('Target requests per second')
    plt.ylabel('Latency (ms)')
    plt.title('Latency vs Throughput')
    plt.legend()
    plt.grid(True, alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_error_rates(rows, out_path):
    numeric = [(int(label), stats) for label, stats in rows if label.isdigit()]
    if not numeric:
        return False

    numeric.sort()
    rps = [r for r, _ in numeric]
    failed_pct = [100 * s['failed'] / s['count'] for _, s in numeric]
    limited_pct = [100 * s['rate_limited'] / s['count'] for _, s in numeric]

    plt.figure(figsize=(8, 5))
    plt.bar(rps, failed_pct, width=max(rps) * 0.08, label='Failed (%)', alpha=0.8)
    plt.bar(
        [r + max(rps) * 0.1 for r in rps],
        limited_pct,
        width=max(rps) * 0.08,
        label='Rate limited 429 (%)',
        alpha=0.8,
    )
    plt.xlabel('Target requests per second')
    plt.ylabel('Percentage of requests')
    plt.title('Error and Rate-Limit Rate vs Throughput')
    plt.legend()
    plt.grid(True, axis='y', alpha=0.3)
    plt.savefig(out_path, dpi=150, bbox_inches='tight')
    plt.close()
    return True


def plot_single_test(title, rows, out_path):
    if not rows:
        return False

    labels = [label for label, _ in rows]
    p99 = [stats['p99'] for _, stats in rows]

    plt.figure(figsize=(6, 4))
    plt.bar(labels, p99, color='steelblue', alpha=0.85)
    plt.xlabel('Test')
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
    if plot_throughput_latency(throughput, OUTPUT_DIR / 'latency-vs-rps.png'):
        saved.append('latency-vs-rps.png')
    if plot_error_rates(throughput, OUTPUT_DIR / 'error-rate-vs-rps.png'):
        saved.append('error-rate-vs-rps.png')

    hotkey = parser.collect_results('hot-key')
    if plot_single_test('Hot-Key Test — p99 Latency', hotkey, OUTPUT_DIR / 'hot-key-latency.png'):
        saved.append('hot-key-latency.png')

    enforcement = parser.collect_results('enforcement')
    if plot_single_test('Enforcement Test — p99 Latency', enforcement, OUTPUT_DIR / 'enforcement-latency.png'):
        saved.append('enforcement-latency.png')

    if saved:
        print('Saved graphs:')
        for name in saved:
            print(f'  {OUTPUT_DIR / name}')
    else:
        print('No benchmark results found. Run k6 tests first.')


if __name__ == '__main__':
    main()
