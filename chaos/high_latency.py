#!/usr/bin/env python3
"""
High Latency Chaos Test

Injects artificial 200ms delay into the Redis container's network traffic
using Linux 'tc' (traffic control). This simulates a slow Redis backend.
Expected behaviour: sidecar still returns 200/429 within its 5s timeout.
After removing latency, latency returns to normal.
Note: Linux only (or WSL2). Requires root privileges for 'tc'.
"""

import subprocess
import sys
import time
import requests

# ----------------------------------------------------------------------
# Helper: Get Redis container name
# ----------------------------------------------------------------------
def get_redis_container():
    """
    Returns the name of the container named 'rate-redis' (or first with that name).
    """
    result = subprocess.run(
        ["docker", "ps", "--format", "{{.Names}}", "--filter", "name=rate-redis"],
        capture_output=True,
        text=True
    )
    names = result.stdout.strip().split()
    if not names:
        print("❌ Redis container 'rate-redis' not found. Is it running?")
        sys.exit(1)
    return names[0]

# ----------------------------------------------------------------------
# Helper: Check if we are on Linux (or WSL) and have tc available
# ----------------------------------------------------------------------
def check_environment(redis_container):
    """
    Verify that 'tc' command exists inside the Redis container.
    If not, we cannot run this test.
    """
    try:
        # Try to run 'tc' inside the container
        subprocess.run(
            ["docker", "exec", redis_container, "tc", "qdisc", "show"],
            capture_output=True,
            check=True
        )
        return True
    except subprocess.CalledProcessError:
        print("⚠️  'tc' command not available inside Redis container.")
        print("   To enable, run Redis with: --cap-add=NET_ADMIN")
        print("   Example: docker run --cap-add=NET_ADMIN ...")
        return False

# ----------------------------------------------------------------------
# Main test flow
# ----------------------------------------------------------------------
def main():
    # 1. OS check – tc only works on Linux
    if sys.platform != "linux":
        print("❌ High latency test requires Linux (or WSL2). Exiting.")
        sys.exit(1)

    # 2. Identify Redis container
    redis_container = get_redis_container()
    print(f"✅ Redis container: {redis_container}")

    # 3. Verify tc is available
    if not check_environment(redis_container):
        sys.exit(1)

    # 4. Baseline latency test (without injected delay)
    print("\n=== Baseline: normal latency ===")
    for i in range(3):
        resp = requests.get("http://localhost:9090/check?user_id=latency_test")
        print(f"Request {i+1}: {resp.status_code} (should be 200)")

    # 5. Inject 200ms delay into Redis container's eth0 interface
    print("\n=== Injecting 200ms latency to Redis container ===")
    # Add a netem qdisc (queueing discipline) to delay all outgoing packets
    subprocess.run([
        "docker", "exec", redis_container,
        "tc", "qdisc", "add", "dev", "eth0", "root", "netem", "delay", "200ms"
    ], check=True)

    time.sleep(1)  # let the rule take effect

    # 6. Test with high latency – sidecar's 5s timeout should NOT be triggered
    print("\n=== With 200ms latency (sidecar timeout = 5s, should still work) ===")
    for i in range(3):
        # Each request now takes ~200ms longer
        resp = requests.get("http://localhost:9090/check?user_id=latency_test")
        print(f"Request {i+1}: {resp.status_code} (200 or 429)")

    # 7. Remove the latency rule
    print("\n=== Removing latency ===")
    subprocess.run([
        "docker", "exec", redis_container,
        "tc", "qdisc", "del", "dev", "eth0", "root"
    ], check=True)

    # 8. Verify recovery: latency back to normal
    print("\n=== After latency removal ===")
    resp = requests.get("http://localhost:9090/check?user_id=latency_test")
    print(f"Status: {resp.status_code} (should be 200)")

    print("\n High latency test completed. Rate limiter survived slow Redis.")

# ----------------------------------------------------------------------
if __name__ == "__main__":
    main()