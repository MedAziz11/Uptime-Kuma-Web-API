import time

import pytest
import requests

from tests.e2e.conftest import Api

UNKNOWN = 99999


def test_public_endpoints(api: Api):
    assert requests.get(f"{api.url}/health", timeout=30).json()["status"] == "ok"
    assert requests.get(f"{api.url}/monitors", timeout=30).status_code == 401
    assert requests.get(f"{api.url}/docs", timeout=30).status_code == 200


def test_server_info(api: Api):
    info = api.get("/info")
    assert info["version"].startswith("1.")
    assert "size" in api.get("/database/size")
    assert api.post("/database/shrink")["message"] == "DB Shrinked"


def test_tags(api: Api):
    tag_id = api.post("/tags", json={"name": "e2e", "color": "#ff0000"})["id"]
    try:
        assert api.get(f"/tags/{tag_id}")["tag"]["name"] == "e2e"
        api.patch(f"/tags/{tag_id}", json={"color": "#00ff00"})
        assert api.get(f"/tags/{tag_id}")["tag"] == {
            "id": tag_id,
            "name": "e2e",
            "color": "#00ff00",
        }
        assert tag_id in [tag["id"] for tag in api.get("/tags")["tags"]]
    finally:
        api.delete(f"/tags/{tag_id}")
    api.get(f"/tags/{tag_id}", expect=404)
    api.patch(f"/tags/{UNKNOWN}", json={"name": "x"}, expect=404)
    api.delete(f"/tags/{UNKNOWN}", expect=404)


def test_notifications(api: Api):
    options = {
        "name": "e2e hook",
        "type": "webhook",
        "webhookURL": "http://127.0.0.1:9/hook",
        "webhookContentType": "json",
    }
    notification_id = api.post("/notifications", json=options)["id"]
    try:
        assert api.get(f"/notifications/{notification_id}")["notification"]["name"] == "e2e hook"
        api.patch(f"/notifications/{notification_id}", json={"name": "renamed hook"})
        names = [n["name"] for n in api.get("/notifications")["notifications"]]
        assert "renamed hook" in names
    finally:
        api.delete(f"/notifications/{notification_id}")
    api.post("/notifications", json={**options, "notAnOption": 1}, expect=422)
    api.get(f"/notifications/{UNKNOWN}", expect=404)
    api.delete(f"/notifications/{UNKNOWN}", expect=404)


def test_proxies(api: Api):
    proxy_id = api.post("/proxies", json={"protocol": "http", "host": "127.0.0.1", "port": 8080})[
        "id"
    ]
    try:
        api.patch(f"/proxies/{proxy_id}", json={"port": 8081})
        assert api.get(f"/proxies/{proxy_id}")["proxy"]["port"] == 8081
        assert proxy_id in [proxy["id"] for proxy in api.get("/proxies")["proxies"]]
    finally:
        api.delete(f"/proxies/{proxy_id}")
    api.delete(f"/proxies/{UNKNOWN}", expect=404)


def test_docker_hosts(api: Api):
    options = {"name": "e2e", "dockerType": "socket", "dockerDaemon": "/var/run/docker.sock"}
    host_id = api.post("/docker_hosts", json=options)["id"]
    try:
        api.patch(f"/docker_hosts/{host_id}", json={"name": "renamed"})
        assert api.get(f"/docker_hosts/{host_id}")["docker_host"]["name"] == "renamed"
        assert host_id in [host["id"] for host in api.get("/docker_hosts")["docker_hosts"]]
    finally:
        api.delete(f"/docker_hosts/{host_id}")
    api.delete(f"/docker_hosts/{UNKNOWN}", expect=404)


def test_monitor_lifecycle(api: Api, monitor: int):
    fetched = api.get(f"/monitors/{monitor}")["monitor"]
    assert fetched["name"] == "e2e monitor"
    assert fetched["interval"] == 20
    assert monitor in [m["id"] for m in api.get("/monitors")["monitors"]]

    patched = api.patch(f"/monitors/{monitor}", json={"name": "renamed", "maxretries": 2})
    assert patched["monitor_data"] == {"name": "renamed", "maxretries": 2}
    fetched = api.get(f"/monitors/{monitor}")["monitor"]
    assert (fetched["name"], fetched["maxretries"], fetched["interval"]) == ("renamed", 2, 20)

    api.post(f"/monitors/{monitor}/pause")
    assert api.get(f"/monitors/{monitor}")["monitor"]["active"] is False
    api.post(f"/monitors/{monitor}/resume")
    assert api.get(f"/monitors/{monitor}")["monitor"]["active"] is True


def test_monitor_validation_and_unknown_ids(api: Api):
    api.post("/monitors", json={"type": "http", "name": "bad", "interval": 1}, expect=422)
    api.post("/monitors", json={"name": "no type"}, expect=422)
    api.get(f"/monitors/{UNKNOWN}", expect=404)
    api.patch(f"/monitors/{UNKNOWN}", json={"name": "x"}, expect=404)
    api.delete(f"/monitors/{UNKNOWN}", expect=404)
    api.post(f"/monitors/{UNKNOWN}/pause", expect=404)
    api.post(f"/monitors/{UNKNOWN}/tag", json={"tag_id": UNKNOWN}, expect=404)


def test_monitor_tags(api: Api, monitor: int):
    tag_id = api.post("/tags", json={"name": "e2e-monitor-tag", "color": "#123456"})["id"]
    try:
        api.post(f"/monitors/{monitor}/tag", json={"tag_id": tag_id, "value": "v"})
        tags = api.get(f"/monitors/{monitor}")["monitor"]["tags"]
        assert [(tag["tag_id"], tag["value"]) for tag in tags] == [(tag_id, "v")]
        api.delete(f"/monitors/{monitor}/tag", json={"tag_id": tag_id, "value": "v"})
        assert api.get(f"/monitors/{monitor}")["monitor"]["tags"] == []
    finally:
        # Uptime Kuma 1.21 drops a tag together with its last monitor link.
        if tag_id in [tag["id"] for tag in api.get("/tags")["tags"]]:
            api.delete(f"/tags/{tag_id}")


def test_heartbeats_and_statistics(api: Api, monitor: int):
    beats = []
    deadline = time.time() + 60
    while not beats and time.time() < deadline:
        time.sleep(2)
        beats = api.get(f"/monitors/{monitor}/beats?hours=1")["monitor_beats"]
    assert beats, "the monitor produced no heartbeat within 60 s"
    assert beats[0]["monitor_id"] == monitor

    assert str(monitor) in api.get("/uptime")
    assert isinstance(api.get("/ping"), dict)
    # No HTTPS monitor exists: an empty answer, not a timeout.
    started = time.time()
    assert isinstance(api.get("/cert_info"), dict)
    assert time.time() - started < 8


def test_maintenance(api: Api, monitor: int):
    maintenance_id = api.post("/maintenance", json={"title": "e2e window", "strategy": "manual"})[
        "maintenanceID"
    ]
    try:
        assert api.get(f"/maintenance/{maintenance_id}")["maintenance"]["title"] == "e2e window"
        api.patch(f"/maintenance/{maintenance_id}", json={"title": "renamed window"})
        titles = [m["title"] for m in api.get("/maintenance")["maintenances"]]
        assert "renamed window" in titles

        api.post(f"/maintenance/{maintenance_id}/monitors", json=[{"id": monitor}])
        linked = api.get(f"/maintenance/{maintenance_id}/monitors")
        assert [m["id"] for m in linked] == [monitor]  # Kuma < 1.23 also returns the name

        api.post(f"/maintenance/{maintenance_id}/pause")
        assert api.get(f"/maintenance/{maintenance_id}")["maintenance"]["active"] is False
        api.post(f"/maintenance/{maintenance_id}/resume")
        assert api.get(f"/maintenance/{maintenance_id}")["maintenance"]["active"] is True
    finally:
        api.delete(f"/maintenance/{maintenance_id}")

    cron_id = api.post(
        "/maintenance",
        json={"title": "e2e cron", "strategy": "cron", "cron": "0 3 * * *", "durationMinutes": 30},
    )["maintenanceID"]
    api.delete(f"/maintenance/{cron_id}")

    api.get(f"/maintenance/{UNKNOWN}", expect=404)
    api.patch(f"/maintenance/{UNKNOWN}", json={"title": "x"}, expect=404)
    api.delete(f"/maintenance/{UNKNOWN}", expect=404)
    api.post(f"/maintenance/{UNKNOWN}/pause", expect=404)


def test_status_pages(api: Api, monitor: int):
    slug = "e2e-page"
    api.post("/statuspages", json={"slug": slug, "title": "E2E"})
    try:
        api.post(
            f"/statuspages/{slug}",
            json={
                "title": "E2E renamed",
                "theme": "dark",
                "domainNameList": ["status.example.com"],
                "publicGroupList": [
                    {"name": "Group", "weight": 1, "monitorList": [{"id": monitor}]}
                ],
            },
        )
        page = api.get(f"/statuspages/{slug}")
        assert page["title"] == "E2E renamed"
        assert page["theme"] == "dark"
        assert page["domainNameList"] == ["status.example.com"]
        assert page["publicGroupList"][0]["monitorList"][0]["id"] == monitor
        assert slug in [p["slug"] for p in api.get("/statuspages")["statuspages"]]

        incident = api.post(
            f"/statuspages/{slug}/incident",
            json={"title": "Down", "content": "Investigating", "style": "danger"},
        )
        assert (incident["title"], incident["style"]) == ("Down", "danger")
        assert api.get(f"/statuspages/{slug}")["incident"]["title"] == "Down"
        api.delete(f"/statuspages/{slug}/incident/unpin")
        assert api.get(f"/statuspages/{slug}").get("incident") is None
    finally:
        api.delete(f"/statuspages/{slug}")

    api.get(f"/statuspages/{slug}", expect=404)
    api.post("/statuspages/unknown-page", json={"title": "x"}, expect=404)
    api.delete("/statuspages/unknown-page", expect=404)
    api.post("/statuspages/unknown-page/incident", json={"title": "a", "content": "b"}, expect=404)
    api.post("/statuspages", json={"slug": "Bad Slug!", "title": "x"}, expect=400)


def test_backup_import(api: Api):
    backup = {"version": "1.23.0", "notificationList": [], "monitorList": [], "proxyList": []}
    api.post("/settings/upload_backup?import_handle=skip", json=backup)
    api.post("/settings/upload_backup?import_handle=nope", json=backup, expect=422)


def test_users(api: Api):
    credentials = {"username": "e2e-user", "password": "e2e-user-password"}
    api.call("POST", "/users", expect=201, json=credentials)
    try:
        api.call("POST", "/users", expect=409, json=credentials)
        user = Api(api.url)
        user.login(**credentials)
        assert user.get("/users/me")["username"] == "e2e-user"
        user.get("/monitors")  # full access to Uptime Kuma
        user.get("/users", expect=403)  # but not to user management
        user.call("POST", "/users", expect=403, json={**credentials, "username": "other"})
    finally:
        api.delete("/users/e2e-user")
    user.get("/users/me", expect=401)
    api.delete("/users/admin", expect=400)


@pytest.mark.parametrize("password", ["wrong-password", ""])
def test_login_failures(api: Api, password: str):
    resp = requests.post(
        f"{api.url}/login/access-token",
        data={"username": "admin", "password": password},
        timeout=30,
    )
    assert resp.status_code in (401, 422)
