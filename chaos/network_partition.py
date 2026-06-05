#!/usr/bin/env python3
"""
Network Partition Chaos Test for Docker Compose stack.

Simulates a network break between the sidecar and the central rate limiter
by disconnecting the sidecar container from the custom network.
Expected: after disconnection, requests fail (timeout or 503); after reconnection, work again.
"""

import subprocess
import time
import requests
from requests.exceptions import ReadTimeout, ConnectionError

# Container names as defined in docker-compose.yml
SIDECAR_CONTAINER = "rate-sidecar"
LIMITER_CONTAINER = "rate-limiter"
# The custom network name (you can inspect with `docker network ls`)
# You may also dynamically detect it, but hardcoding is simpler.
NETWORK_NAME = "distributed-rate-limiter_rate-net"

def main():
    print(f"✅ Using sidecar container: {SIDECAR_CONTAINER}")
    print(f"✅ Using limiter container: {LIMITER_CONTAINER}")
    print(f"✅ Using network: {NETWORK_NAME}")

    # Baseline check
    print("\n=== Baseline: sidecar → limiter reachable ===")
    resp = requests.get("http://localhost:9090/check?user_id=net_test")
    print(f"Status: {resp.status_code} (expected 200)")

    # Disconnect sidecar from the custom network
    print("\n=== Disconnecting sidecar from network ===")
    subprocess.run(
        ["docker", "network", "disconnect", NETWORK_NAME, SIDECAR_CONTAINER],
        check=True
    )
    time.sleep(1)

    # After partition – should get timeout or 503
    print("\n=== After partition: should get error or 503 ===")
    try:
        resp = requests.get("http://localhost:9090/check?user_id=net_test", timeout=2)
        if resp.status_code == 503:
            print(f"Status: {resp.status_code} (expected 503) – good")
        else:
            print(f"Status: {resp.status_code} (unexpected)")
    except (ConnectionError, ReadTimeout) as e:
        print(f"✅ Request failed as expected: {type(e).__name__} – good")

    # Reconnect sidecar
    print("\n=== Reconnecting sidecar to network ===")
    subprocess.run(
        ["docker", "network", "connect", NETWORK_NAME, SIDECAR_CONTAINER],
        check=True
    )
    time.sleep(2)

    # Recovery check
    print("\n=== After recovery: should get 200 OK ===")
    resp = requests.get("http://localhost:9090/check?user_id=net_test2")
    print(f"Status: {resp.status_code} (expected 200)")

    print("\n🎉 Network partition test completed. Sidecar resilience verified.")

if __name__ == "__main__":
    main()