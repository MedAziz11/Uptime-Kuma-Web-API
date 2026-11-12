"""Create the first user on a fresh Uptime Kuma instance (what its setup page does).

Usage: KUMA_USERNAME=... KUMA_PASSWORD=... python tests/e2e/setup_kuma.py [KUMA_URL]
"""

import os
import sys
import time

from uptime_kuma_api import UptimeKumaApi, UptimeKumaException


def main() -> int:
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:3001"
    username, password = os.environ["KUMA_USERNAME"], os.environ["KUMA_PASSWORD"]

    deadline = time.time() + 120
    while True:
        try:
            api = UptimeKumaApi(url)
            break
        except UptimeKumaException:
            if time.time() > deadline:
                print(f"Uptime Kuma is not reachable at {url}", file=sys.stderr)
                return 1
            time.sleep(2)

    try:
        if api.need_setup():
            api.setup(username, password)
            print(f"Created the Uptime Kuma user '{username}'")
        else:
            print("Uptime Kuma is already set up")
    finally:
        api.disconnect()
    return 0


if __name__ == "__main__":
    sys.exit(main())
