"""Second capture: resource lifecycle commands against a Uptime Kuma that is already set up.

Usage: python capture_kuma2.py KUMA_URL OUT_DIR USERNAME PASSWORD
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

MONITOR_DEFAULTS = {
    "type": "http", "name": "", "parent": None, "url": "https://", "wsSubprotocol": "", "method": "GET",
    "protocol": None, "location": "world", "ipFamily": None, "interval": 60, "retryInterval": 60,
    "resendInterval": 0, "maxretries": 0, "retryOnlyOnStatusCodeFailure": False, "notificationIDList": {},
    "ignoreTls": False, "upsideDown": False, "expiryNotification": False, "domainExpiryNotification": True,
    "maxredirects": 10, "accepted_statuscodes": ["200-299"], "saveResponse": False, "saveErrorResponse": True,
    "responseMaxLength": 1024, "dns_resolve_type": "A", "dns_resolve_server": "1.1.1.1", "docker_container": "",
    "docker_host": None, "proxyId": None, "basic_auth_user": "", "basic_auth_pass": "", "bearer_token": "",
    "mqttUsername": "", "mqttPassword": "", "mqttTopic": "", "mqttWebsocketPath": "", "mqttSuccessMessage": "",
    "mqttCheckType": "keyword", "authMethod": None, "oauth_auth_method": "client_secret_basic",
    "httpBodyEncoding": "json", "kafkaProducerBrokers": [], "kafkaProducerSaslOptions": {"mechanism": "None"},
    "cacheBust": False, "kafkaProducerSsl": False, "kafkaProducerAllowAutoTopicCreation": False,
    "gamedigGivenPortOnly": True, "gamedigToken": "", "remote_browser": None, "screenshot_delay": 0,
    "rabbitmqNodes": [], "rabbitmqUsername": "", "rabbitmqPassword": "", "conditions": [],
    "system_service_name": "", "sshAuthMethod": "password", "ntpStratumThreshold": 5,
    "ntpTimeOffsetThreshold": 1000, "ntpRootDispersionThreshold": 500,
}


@sio.on("*")
def on_any(event, *args):
    pushed.append({"after": last[0], "event": event, "args": list(args)})


last = ["connect"]


def call(name, *args, key=None):
    last[0] = key or name
    try:
        res = sio.call(name, args if len(args) != 1 else args[0], timeout=20) if args else sio.call(name, timeout=20)
    except Exception as exc:  # noqa: BLE001 - recording tool
        res = {"__error__": f"{type(exc).__name__}: {exc}"}
    acks[key or name] = {"args": list(args), "ack": res}
    time.sleep(0.4)
    return res or {}


sio.connect(url, transports=["websocket"], wait_timeout=20)
login = call("login", {"username": username, "password": password, "token": ""})
token = login.pop("token", None)
login["token"] = "<redacted>"
time.sleep(2)

mon = call("add", {**MONITOR_DEFAULTS, "name": "capture http", "url": "http://127.0.0.1:3001"}, key="add.http")
mid = mon.get("monitorID")
call("add", {**MONITOR_DEFAULTS, "type": "group", "name": "capture group"}, key="add.group")
call("add", {**MONITOR_DEFAULTS, "type": "push", "name": "capture push"}, key="add.push")
time.sleep(6)
call("getMonitor", mid)
call("getMonitorBeats", mid, 1)
call("getMonitorChartData", mid, 1)
call("monitorImportantHeartbeatListCount", mid)
call("monitorImportantHeartbeatListPaged", mid, 0, 25)
call("pauseMonitor", mid)
call("resumeMonitor", mid)
full = (acks["getMonitor"]["ack"] or {}).get("monitor") or {}
call("editMonitor", {**full, "name": "capture http renamed"})
tag = call("addTag", {"name": "capture2", "color": "#00ff41"}).get("tag", {})
call("addMonitorTag", tag.get("id"), mid, "v1")
call("getTags", key="getTags.after")
call("editTag", {**tag, "name": "capture2b"})
call("deleteMonitorTag", tag.get("id"), mid, "v1")
notif = call("addNotification", {"name": "capture hook", "type": "webhook", "isDefault": False, "applyExisting": False,
                                 "webhookURL": "http://127.0.0.1:9/hook", "webhookContentType": "json"}, None)
call("addProxy", {"protocol": "http", "host": "127.0.0.1", "port": 3128, "auth": False, "active": True,
                  "default": False, "applyExisting": False}, None)
call("addDockerHost", {"name": "capture docker", "dockerType": "socket", "dockerDaemon": "/var/run/docker.sock"}, None)
maint = call("addMaintenance", {"title": "capture maint", "description": "", "strategy": "manual", "active": True,
                                "intervalDay": 1, "dateRange": [None], "timeRange": [{"hours": 2, "minutes": 0},
                                {"hours": 3, "minutes": 0}], "weekdays": [], "daysOfMonth": [], "cron": "30 3 * * *",
                                "durationMinutes": 60, "timezoneOption": "UTC"})
mtid = maint.get("maintenanceID")
call("addMonitorMaintenance", mtid, [{"id": mid}])
call("getMonitorMaintenance", mtid)
call("getMaintenance", mtid)
call("pauseMaintenance", mtid)
call("addStatusPage", "Capture", "capture")
call("getStatusPage", "capture")
call("postIncident", "capture", {"title": "inc", "content": "c", "style": "info"})
call("getIncidentHistory", "capture", None)
call("unpinIncident", "capture")
call("addAPIKey", {"name": "capture key", "expires": None, "active": 1})
call("getAPIKeyList", key="getAPIKeyList.after")
call("setSettings", {"serverTimezone": "UTC"}, password, key="setSettings")
call("loginByToken", "not-a-token", key="loginByToken.invalid")
call("deleteStatusPage", "capture")
call("deleteMaintenance", mtid)
call("deleteMonitor", mid)
sio.disconnect()

# Reconnect with the session token, as the API will after a dropped connection.
sio2 = socketio.Client(reconnection=False)
sio2.connect(url, transports=["websocket"], wait_timeout=20)
try:
    acks["loginByToken.valid"] = {"args": ["<redacted>"], "ack": sio2.call("loginByToken", token, timeout=20)}
finally:
    sio2.disconnect()

text = json.dumps({"acks": acks, "pushed": pushed}, default=str)
for secret in filter(None, [token, password]):
    text = text.replace(secret, "<redacted>")
data = json.loads(text)
(out / "acks.json").write_text(json.dumps(data["acks"], indent=2), encoding="utf-8")
(out / "pushed.json").write_text(json.dumps(data["pushed"], indent=2), encoding="utf-8")
print("pushed:", sorted({(p["event"]) for p in pushed}))
for k, v in data["acks"].items():
    print(f"{k:36} -> {json.dumps(v['ack'])[:130]}")
