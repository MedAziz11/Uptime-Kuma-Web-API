"""Record what a real Uptime Kuma sends: pushed events and command acks, as JSON fixtures.

Usage: python capture_kuma.py KUMA_URL OUT_DIR USERNAME PASSWORD
"""

import json
import sys
import time
from pathlib import Path

import socketio

url, out, username, password = sys.argv[1], Path(sys.argv[2]), sys.argv[3], sys.argv[4]
out.mkdir(parents=True, exist_ok=True)
pushed: list[dict] = []
acks: dict[str, object] = {}

sio = socketio.Client(reconnection=False)


@sio.on("*")
def on_any(event, *args):
    pushed.append({"event": event, "args": list(args)})


def call(name, *args, key=None):
    try:
        res = sio.call(name, args if len(args) != 1 else args[0], timeout=15) if args else sio.call(name, timeout=15)
    except Exception as exc:  # noqa: BLE001 - recording tool
        res = {"__error__": f"{type(exc).__name__}: {exc}"}
    acks[key or name] = {"args": list(args), "ack": res}
    return res


sio.connect(url, transports=["websocket"], wait_timeout=20)
time.sleep(1)
if call("needSetup") is True:
    call("setup", username, password)
login = call("login", {"username": username, "password": password, "token": ""})
if isinstance(login, dict) and "token" in login:
    login["token"] = "<redacted>"
time.sleep(3)

call("getSettings")
call("getTags")
call("getMonitorList")
call("getGameList")
call("getDatabaseSize")
call("getMaintenanceList")
call("getAPIKeyList")
call("twoFAStatus")
call("getMonitor", 999999, key="getMonitor.missing")
call("deleteMonitor", 999999, key="deleteMonitor.missing")
call("getStatusPage", "nope", key="getStatusPage.missing")
call("deleteTag", 999999, key="deleteTag.missing")
tag = call("addTag", {"name": "capture", "color": "#00ff41"})
call("add", {"type": "http", "name": "minimal", "url": "http://127.0.0.1:1"}, key="add.minimal")

sio.disconnect()

# One token per login: keep the capture free of secrets.
for p in pushed:
    if p["event"] == "info" and p["args"] and isinstance(p["args"][0], dict):
        p["args"][0].pop("primaryBaseURL", None)
(out / "acks.json").write_text(json.dumps(acks, indent=2, default=str), encoding="utf-8")
(out / "pushed.json").write_text(json.dumps(pushed, indent=2, default=str), encoding="utf-8")
print("pushed events:", sorted({p["event"] for p in pushed}))
for k, v in acks.items():
    print(f"{k:28} -> {json.dumps(v['ack'], default=str)[:150]}")
