#!/usr/bin/env python3
"""
Network Partition Chaos Test for Docker Compose stack.

Simulates a network break between the sidecar and the central rate limiter
by disconnecting the sidecar container from the compose network.
Expected: after disconnection, requests fail (timeout or 503); after reconnection, work again.

Requires: docker compose stack running (rate-sidecar, rate-limiter, redis, demo).
Run from project root: python chaos/network_partition.py
"""

from __future__ import annotations

import subprocess
import sys
import time

import requests
from requests.exceptions import ConnectionError, ReadTimeout

SIDECAR_CONTAINER = "rate-sidecar"
LIMITER_CONTAINER = "rate-limiter"
SIDECAR_URL = "http://localhost:9090/check"


def log(msg: str) -> None:
    # ASCII-only output for Windows consoles (cp1252).
    print(msg, flush=True)


def run_docker(args: list[str], check: bool = True) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            ["docker", *args],
            capture_output=True,
            text=True,
            check=check,
        )
    except subprocess.CalledProcessError as exc:
        stderr = (exc.stderr or exc.stdout or "").strip()
        log(f"FAIL: docker {' '.join(args)}")
        if stderr:
            log(stderr)
        raise


def container_running(name: str) -> bool:
    result = run_docker(
        ["ps", "--filter", f"name=^{name}$", "--format", "{{.Names}}"],
        check=False,
    )
    return result.stdout.strip() == name


def detect_compose_network(container_name: str) -> str:
    """Find the compose network attached to the sidecar (ends with _rate-net or is rate-net)."""
    result = run_docker(["inspect", "-f", "{{json .NetworkSettings.Networks}}", container_name])
    import json

    networks = json.loads(result.stdout.strip() or "{}")
    if not networks:
        raise RuntimeError(f"container {container_name} has no networks")

    for name in networks:
        if name == "rate-net" or name.endswith("_rate-net"):
            return name

    # Fallback: first custom network that is not the default bridge.
    for name in networks:
        if name != "bridge":
            return name

    raise RuntimeError(f"could not detect compose network for {container_name}")


def preflight() -> str:
    log("Pre-flight checks...")
    run_docker(["info"], check=True)

    if not container_running(SIDECAR_CONTAINER):
        log(f"FAIL: container '{SIDECAR_CONTAINER}' is not running.")
        log("Start the stack: docker compose up -d --build")
        sys.exit(1)

    if not container_running(LIMITER_CONTAINER):
        log(f"FAIL: container '{LIMITER_CONTAINER}' is not running.")
        log("Start the stack: docker compose up -d --build")
        sys.exit(1)

    network_name = detect_compose_network(SIDECAR_CONTAINER)
    log(f"OK: sidecar={SIDECAR_CONTAINER}, limiter={LIMITER_CONTAINER}, network={network_name}")

    try:
        resp = requests.get(f"{SIDECAR_URL}?user_id=net_preflight", timeout=5)
    except requests.RequestException as exc:
        log(f"FAIL: sidecar not reachable on localhost:9090 ({exc})")
        sys.exit(1)

    if resp.status_code != 200:
        log(f"FAIL: preflight request returned {resp.status_code}, expected 200")
        sys.exit(1)

    log("OK: sidecar reachable and limiter path healthy.")
    return network_name


def check_sidecar(user_suffix: str, timeout: float = 5) -> requests.Response:
    return requests.get(f"{SIDECAR_URL}?user_id=net_{user_suffix}", timeout=timeout)


def main() -> None:
    log("")
    log("Chaos Test: Network Partition (sidecar <-> limiter)")
    log("")

    network_name = preflight()
    partitioned = False

    try:
        log("")
        log("=== Baseline: sidecar -> limiter reachable ===")
        resp = check_sidecar("baseline")
        log(f"Status: {resp.status_code} (expected 200)")
        if resp.status_code != 200:
            log("FAIL: baseline check failed")
            sys.exit(1)

        log("")
        log("=== Disconnecting sidecar from compose network ===")
        run_docker(["network", "disconnect", network_name, SIDECAR_CONTAINER])
        partitioned = True
        time.sleep(1)

        log("")
        log("=== After partition: expect 503 or connection error ===")
        try:
            resp = check_sidecar("partition", timeout=2)
            if resp.status_code == 503:
                log(f"Status: {resp.status_code} (expected 503) - good")
            else:
                log(f"FAIL: unexpected status {resp.status_code}, expected 503")
                sys.exit(1)
        except (ConnectionError, ReadTimeout) as exc:
            log(f"OK: request failed as expected: {type(exc).__name__}")

        log("")
        log("=== Reconnecting sidecar to compose network ===")
        run_docker(["network", "connect", network_name, SIDECAR_CONTAINER])
        partitioned = False
        time.sleep(2)

        log("")
        log("=== After recovery: expect 200 OK ===")
        resp = check_sidecar("recovery")
        log(f"Status: {resp.status_code} (expected 200)")
        if resp.status_code != 200:
            log("FAIL: recovery check failed")
            sys.exit(1)

        log("")
        log("CHAOS TEST PASSED")
        log("Sidecar survived a network partition from the central limiter.")

    finally:
        if partitioned:
            log("")
            log("Cleaning up: reconnecting sidecar to network...")
            run_docker(["network", "connect", network_name, SIDECAR_CONTAINER], check=False)


if __name__ == "__main__":
    main()
