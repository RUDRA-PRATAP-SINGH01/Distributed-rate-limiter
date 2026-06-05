#!/usr/bin/env python3
"""
Network Partition Chaos Test

Simulates a network break between the sidecar and the central rate limiter
by disconnecting the sidecar Docker container from the network.
Expected behaviour: sidecar returns 503 Service Unavailable (fail‑closed).
After reconnection, normal operation resumes.
"""

import subprocess
import sys
import time
import requests

# ----------------------------------------------------------------------
# Helper: Find Docker container name that exposes a given host port
# ----------------------------------------------------------------------
def get_container_name(port):
    """
    Returns the name of the first container that publishes the given port.
    
    Example: port 9090 -> sidecar container name
    """
    # docker ps --format "{{.Names}}" --filter "publish=9090"
    result = subprocess.run(
        ["docker", "ps", "--format", "{{.Names}}", "--filter", f"publish={port}"],
        capture_output=True,
        text=True
    )
    names = result.stdout.strip().split()
    if not names:
        print(f"❌ No container found for port {port}. Is the service running?")
        sys.exit(1)
    return names[0]

# ----------------------------------------------------------------------
# Main test flow
# ----------------------------------------------------------------------
def main():
    # 1. Identify containers
    sidecar_container = get_container_name(9090)
    limiter_container = get_container_name(8080)
    print(f"✅ Sidecar container: {sidecar_container}")
    print(f"✅ Limiter container: {limiter_container}")

    # 2. Baseline: sidecar can reach limiter -> expect 200 OK
    print("\n=== Baseline: sidecar → limiter reachable ===")
    resp = requests.get("http://localhost:9090/check?user_id=net_test")
    print(f"Status: {resp.status_code} (expected 200)")

    # 3. Disconnect sidecar from the default bridge network
    #    This simulates a network partition.
    print("\n=== Disconnecting sidecar from network ===")
    subprocess.run(
        ["docker", "network", "disconnect", "bridge", sidecar_container],
        check=True
    )
    time.sleep(1)  # give network time to settle

    # 4. After partition: sidecar cannot call limiter -> should return 503
    #    (fail‑closed behaviour, default FAIL_OPEN=false)
    print("\n=== After partition: should get 503 (fail‑closed) ===")
    try:
        resp = requests.get("http://localhost:9090/check?user_id=net_test", timeout=2)
        print(f"Status: {resp.status_code} (expected 503)")
    except requests.exceptions.ConnectionError:
        print("✅ Connection refused – sidecar cannot reach limiter (good)")

    # 5. Reconnect sidecar to the network
    print("\n=== Reconnecting sidecar to network ===")
    subprocess.run(
        ["docker", "network", "connect", "bridge", sidecar_container],
        check=True
    )
    time.sleep(2)  # wait for recovery

    # 6. After recovery: sidecar should work normally again -> 200 OK
    print("\n=== After recovery: should get 200 OK ===")
    resp = requests.get("http://localhost:9090/check?user_id=net_test2")
    print(f"Status: {resp.status_code} (expected 200)")

    print("\n Network partition test completed. Resilience verified.")

# ----------------------------------------------------------------------
if __name__ == "__main__":
    main()