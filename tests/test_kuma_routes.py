import pytest
from uptime_kuma_api import MonitorType, UptimeKumaException

from app.kuma import KumaInvalidRequestError, KumaTimeoutError, KumaUnavailableError


def test_monitor_crud(client, kuma, auth):
    kuma.results["get_monitors"] = [{"id": 1, "type": MonitorType.HTTP, "name": "site"}]
    assert client.get("/monitors", headers=auth).json() == {
        "monitors": [{"id": 1, "type": "http", "name": "site"}]
    }

    kuma.results["add_monitor"] = {"msg": "Added Successfully.", "monitorID": 2}
    body = {"type": "http", "name": "new", "url": "https://example.com", "jsonPath": "$.ok"}
    resp = client.post("/monitors", json=body, headers=auth)
    assert resp.json()["monitorID"] == 2
    # Only what the client sent is forwarded, including options the schema does not list.
    assert kuma.last_call == ("add_monitor", (), body)

    kuma.results["get_monitor"] = {"id": 2}
    assert client.get("/monitors/2", headers=auth).json() == {"monitor": {"id": 2}}
    assert kuma.last_call == ("get_monitor", (2,), {})

    resp = client.patch("/monitors/2", json={"interval": 30}, headers=auth)
    assert resp.json() == {"msg": "ok", "monitor_data": {"interval": 30}}
    assert kuma.last_call == ("edit_monitor", (2,), {"interval": 30})

    for action in ("pause", "resume"):
        assert client.post(f"/monitors/2/{action}", headers=auth).status_code == 200
        assert kuma.last_call == (f"{action}_monitor", (2,), {})

    assert client.delete("/monitors/2", headers=auth).status_code == 200
    assert kuma.last_call == ("delete_monitor", (2,), {})


def test_monitor_validation(client, kuma, auth):
    assert client.post("/monitors", json={"name": "no type"}, headers=auth).status_code == 422
    assert (
        client.post("/monitors", json={"type": "nope", "name": "x"}, headers=auth).status_code
        == 422
    )
    assert client.get("/monitors/abc", headers=auth).status_code == 422
    assert client.get("/monitors/1/beats?hours=0", headers=auth).status_code == 422
    assert kuma.calls == []


def test_monitor_beats_and_tags(client, kuma, auth):
    kuma.results["get_monitor_beats"] = [{"status": 1}]
    resp = client.get("/monitors/1/beats?hours=6", headers=auth)
    assert resp.json() == {"monitor_beats": [{"status": 1}]}
    assert kuma.last_call == ("get_monitor_beats", (1, 6.0), {})

    client.post("/monitors/1/tag", json={"tag_id": 3}, headers=auth)
    assert kuma.last_call == ("add_monitor_tag", (), {"monitor_id": 1, "tag_id": 3, "value": ""})
    client.request("DELETE", "/monitors/1/tag", json={"tag_id": 3, "value": "v"}, headers=auth)
    assert kuma.last_call == (
        "delete_monitor_tag",
        (),
        {"monitor_id": 1, "tag_id": 3, "value": "v"},
    )


def test_tags(client, kuma, auth):
    kuma.results["get_tags"] = [{"id": 1, "name": "prod", "color": "#fff"}]
    assert client.get("/tags", headers=auth).json()["tags"][0]["name"] == "prod"
    client.post("/tags", json={"name": "prod", "color": "#fff"}, headers=auth)
    assert kuma.last_call == ("add_tag", (), {"name": "prod", "color": "#fff"})
    client.patch("/tags/1", json={"color": "#000"}, headers=auth)
    assert kuma.last_call == ("edit_tag", (1,), {"color": "#000"})
    client.get("/tags/1", headers=auth)
    assert kuma.last_call == ("get_tag", (1,), {})
    client.delete("/tags/1", headers=auth)
    assert kuma.last_call == ("delete_tag", (1,), {})


def test_maintenance(client, kuma, auth):
    body = {"title": "upgrade", "strategy": "manual"}
    client.post("/maintenance", json=body, headers=auth)
    assert kuma.last_call == ("add_maintenance", (), body)
    assert client.post("/maintenance", json={"title": "x"}, headers=auth).status_code == 422

    kuma.results["get_maintenances"] = [{"id": 1}]
    assert client.get("/maintenance", headers=auth).json() == {"maintenances": [{"id": 1}]}

    kuma.results["get_maintenance"] = {"id": 1, "title": "upgrade"}
    assert client.get("/maintenance/1", headers=auth).json() == {
        "maintenance": {"id": 1, "title": "upgrade"}
    }
    assert kuma.last_call == ("get_maintenance", (1,), {})

    resp = client.patch("/maintenance/1", json={"active": False}, headers=auth)
    assert resp.json()["maintenance_data"] == {"active": False}
    assert kuma.last_call == ("edit_maintenance", (1,), {"active": False})

    for action in ("pause", "resume"):
        client.post(f"/maintenance/1/{action}", headers=auth)
        assert kuma.last_call == (f"{action}_maintenance", (1,), {})

    client.post(
        "/maintenance/1/monitors", json=[{"id": 4, "name": "site"}, {"id": 5}], headers=auth
    )
    assert kuma.last_call == ("add_monitor_maintenance", (1, [{"id": 4}, {"id": 5}]), {})

    kuma.results["get_monitor_maintenance"] = [{"id": 4}]
    assert client.get("/maintenance/1/monitors", headers=auth).json() == [{"id": 4}]

    client.delete("/maintenance/1", headers=auth)
    assert kuma.last_call == ("delete_maintenance", (1,), {})


def test_status_pages(client, kuma, auth):
    page = {
        "id": 1,
        "slug": "main",
        "title": "Main",
        # Uptime Kuma stores bare host names here; v1 validated them as URLs and failed.
        "domainNameList": ["status.example.com"],
        "published": True,
        "futureField": "kept",
    }
    kuma.results["get_status_pages"] = [page]
    assert client.get("/statuspages", headers=auth).json() == {"statuspages": [page]}

    kuma.results["get_status_page"] = {**page, "incident": None, "publicGroupList": []}
    resp = client.get("/statuspages/main", headers=auth)
    assert resp.status_code == 200
    assert resp.json()["domainNameList"] == ["status.example.com"]

    client.post("/statuspages", json={"slug": "new", "title": "New"}, headers=auth)
    assert kuma.last_call == ("add_status_page", ("new", "New"), {})
    assert (
        client.post("/statuspages", json={"slug": "", "title": "New"}, headers=auth).status_code
        == 422
    )

    client.post("/statuspages/new", json={"title": "Renamed", "theme": "dark"}, headers=auth)
    assert kuma.last_call == ("save_status_page", ("new",), {"title": "Renamed", "theme": "dark"})

    kuma.results["post_incident"] = {"id": 7, "title": "Down", "content": "...", "style": "danger"}
    resp = client.post(
        "/statuspages/new/incident",
        json={"title": "Down", "content": "...", "style": "danger"},
        headers=auth,
    )
    assert resp.json()["id"] == 7
    assert kuma.last_call[1][:3] == ("new", "Down", "...")

    assert client.delete("/statuspages/new/incident/unpin", headers=auth).json() == {
        "detail": "success"
    }
    assert client.delete("/statuspages/new", headers=auth).json() == {"detail": "success"}
    assert kuma.last_call == ("delete_status_page", ("new",), {})


@pytest.mark.parametrize(
    ("prefix", "one", "body"),
    [
        ("notifications", "notification", {"name": "hook", "type": "webhook"}),
        ("proxies", "proxy", {"protocol": "http", "host": "127.0.0.1", "port": 8080}),
        ("docker_hosts", "docker_host", {"name": "local", "dockerType": "socket"}),
    ],
)
def test_generic_resources(client, kuma, auth, prefix, one, body):
    kuma.results[f"get_{prefix}"] = [{"id": 1}]
    assert client.get(f"/{prefix}", headers=auth).json() == {prefix: [{"id": 1}]}
    client.post(f"/{prefix}", json=body, headers=auth)
    assert kuma.last_call == (f"add_{one}", (), body)
    client.get(f"/{prefix}/1", headers=auth)
    assert kuma.last_call == (f"get_{one}", (1,), {})
    client.patch(f"/{prefix}/1", json={"name": "renamed"}, headers=auth)
    assert kuma.last_call == (f"edit_{one}", (1,), {"name": "renamed"})
    client.delete(f"/{prefix}/1", headers=auth)
    assert kuma.last_call == (f"delete_{one}", (1,), {})


def test_test_endpoints(client, kuma, auth):
    client.post("/notifications/test", json={"type": "webhook"}, headers=auth)
    assert kuma.last_call == ("test_notification", (), {"type": "webhook"})
    client.post("/docker_hosts/test", json={"dockerType": "socket"}, headers=auth)
    assert kuma.last_call == ("test_docker_host", (), {"dockerType": "socket"})


def test_info_endpoints(client, kuma, auth):
    kuma.results.update(
        info={"version": "1.23.16"},
        uptime={1: {24: 1, 720: 0.99}},
        avg_ping={1: 42},
        cert_info={1: {"valid": True}},
        get_database_size={"size": 1024},
        shrink_database={},
    )
    assert client.get("/info", headers=auth).json() == {"version": "1.23.16"}
    assert client.get("/uptime", headers=auth).json() == {"1": {"24": 1, "720": 0.99}}
    assert client.get("/ping", headers=auth).json() == {"1": 42}
    assert client.get("/cert_info", headers=auth).json() == {"1": {"valid": True}}
    assert client.get("/database/size", headers=auth).json() == {"size": 1024, "unit": "octet"}
    assert client.post("/database/shrink", headers=auth).json()["message"] == "DB Shrinked"


def test_upload_backup(client, kuma, auth):
    backup = {"version": "1.23.16", "monitorList": [{"name": "site"}], "extra": 1}
    resp = client.post("/settings/upload_backup?import_handle=overwrite", json=backup, headers=auth)
    assert resp.status_code == 200
    name, args, _ = kuma.last_call
    assert name == "upload_backup"
    assert args[1] == "overwrite"
    assert '"monitorList":[{"name":"site"}]' in args[0]
    assert "notificationList" not in args[0]

    resp = client.post("/settings/upload_backup?import_handle=bad", json=backup, headers=auth)
    assert resp.status_code == 422


@pytest.mark.parametrize(
    ("error", "status", "detail"),
    [
        (UptimeKumaException("monitor does not exist"), 404, "monitor does not exist"),
        (UptimeKumaException("Maintenance not found"), 404, "Maintenance not found"),
        # Raw errors Uptime Kuma 1.23 returns for unknown ids.
        (
            UptimeKumaException("Cannot read properties of null (reading 'toJSON')"),
            404,
            "Not found",
        ),
        (UptimeKumaException("No slug?"), 404, "Not found"),
        (UptimeKumaException("You do not own this monitor."), 404, "Not found"),
        (
            UptimeKumaException("INSERT INTO monitor_tag ... FOREIGN KEY constraint failed"),
            404,
            "Not found",
        ),
        (UptimeKumaException("Incorrect something"), 400, "Incorrect something"),
        (KumaInvalidRequestError("unknown argument 'foo'"), 422, "unknown argument 'foo'"),
        (KumaTimeoutError("slow"), 504, "Uptime Kuma did not answer in time"),
        (KumaUnavailableError("unable to connect"), 503, "unable to connect"),
    ],
)
def test_kuma_errors_map_to_http_statuses(client, kuma, auth, error, status, detail):
    kuma.errors["get_monitor"] = error
    resp = client.get("/monitors/1", headers=auth)
    assert resp.status_code == status
    assert resp.json() == {"detail": detail}
