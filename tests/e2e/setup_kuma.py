"""Create the first user on a fresh Uptime Kuma 2 instance (what its setup page does).

Usage: KUMA_USERNAME=... KUMA_PASSWORD=... python tests/e2e/setup_kuma.py [KUMA_URL]
"""

import os
import sys
import threading
import time

import socketio


def main() -> int:
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:3001"
    username, password = os.environ["KUMA_USERNAME"], os.environ["KUMA_PASSWORD"]

    # Uptime Kuma registers its event handlers only after it has sent these events:
    # a command sent earlier is never answered.
    ready = threading.Event()
    sio = socketio.Client(reconnection=False)
    for event in ("loginRequired", "autoLogin", "setup"):
        sio.on(event, lambda *_: ready.set())

    deadline = time.time() + 120
    while True:
        try:
            sio.connect(url, transports=["websocket"], wait_timeout=10)
            break
        except socketio.exceptions.ConnectionError:
            if time.time() > deadline:
                print(f"Uptime Kuma is not reachable at {url}", file=sys.stderr)
                return 1
            time.sleep(2)

    try:
        if not ready.wait(30):
            print("Uptime Kuma did not finish the connection", file=sys.stderr)
            return 1
        if sio.call("needSetup", timeout=30) is not True:
            print("Uptime Kuma is already set up")
            return 0
        res = sio.call("setup", (username, password), timeout=30)
        if not res.get("ok"):
            print(f"Setup failed: {res.get('msg')}", file=sys.stderr)
            return 1
        print(f"Created the Uptime Kuma user '{username}'")
        return 0
    finally:
        sio.disconnect()


if __name__ == "__main__":
    sys.exit(main())
