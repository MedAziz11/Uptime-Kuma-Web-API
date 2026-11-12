from datetime import timedelta

import pytest
from fastapi.testclient import TestClient
from pydantic import SecretStr

from app.main import create_app
from app.security import create_access_token
from tests.conftest import ADMIN_PASSWORD, login

NEW_USER = {"username": "alice", "password": "alice-password"}


def test_health_and_docs_are_public(client):
    assert client.get("/health").json()["status"] == "ok"
    assert client.get("/", follow_redirects=False).headers["location"] == "/docs"
    assert client.get("/openapi.json").status_code == 200


def test_login_rejects_bad_credentials(client):
    for username, password in [
        ("admin", "wrong"),
        ("nobody", ADMIN_PASSWORD),
        ("admin", "x" * 100),
    ]:
        resp = client.post("/login/access-token", data={"username": username, "password": password})
        assert resp.status_code == 401
        assert resp.headers["www-authenticate"] == "Bearer"


def test_token_does_not_leak_kuma_session(client, auth):
    import jwt

    token = auth["Authorization"].split()[1]
    payload = jwt.decode(token, options={"verify_signature": False})
    assert payload["sub"] == "admin"
    assert set(payload) == {"sub", "iat", "exp"}


@pytest.mark.parametrize(
    "path",
    ["/monitors", "/tags", "/statuspages", "/maintenance", "/notifications", "/proxies",
     "/docker_hosts", "/info", "/uptime", "/ping", "/cert_info", "/database/size", "/users",
     "/users/me"],
)  # fmt: skip
def test_routes_require_a_token(client, kuma, path):
    assert client.get(path).status_code == 401
    assert client.get(path, headers={"Authorization": "Bearer garbage"}).status_code == 401
    assert kuma.calls == []


def test_expired_and_foreign_tokens_are_rejected(client):
    key = client.app.state.secret_key
    expired = create_access_token("admin", key, timedelta(seconds=-1))
    forged = create_access_token("admin", "another-key-another-key-another-key", timedelta(hours=1))
    ghost = create_access_token("ghost", key, timedelta(hours=1))
    for token in (expired, forged, ghost):
        resp = client.get("/users/me", headers={"Authorization": f"Bearer {token}"})
        assert resp.status_code == 401


def test_sign_up_is_admin_only(client, auth):
    assert client.post("/users", json=NEW_USER).status_code == 401

    resp = client.post("/users", json=NEW_USER, headers=auth)
    assert resp.status_code == 201
    assert resp.json()["username"] == "alice"
    assert "password" not in resp.text

    assert client.post("/users", json=NEW_USER, headers=auth).status_code == 409

    alice = login(client, **NEW_USER)
    assert client.get("/users/me", headers=alice).json()["username"] == "alice"
    assert (
        client.post("/users", json={**NEW_USER, "username": "bob"}, headers=alice).status_code
        == 403
    )
    assert client.get("/users", headers=alice).status_code == 403
    assert client.delete("/users/admin", headers=alice).status_code == 403


@pytest.mark.parametrize(
    "body",
    [
        {"username": "al", "password": "long-enough"},
        {"username": "has space", "password": "long-enough"},
        {"username": "alice", "password": "short"},
        {"username": "alice", "password": "x" * 73},
    ],
)
def test_user_validation(client, auth, body):
    assert client.post("/users", json=body, headers=auth).status_code == 422


def test_user_management(client, auth):
    client.post("/users", json=NEW_USER, headers=auth)
    alice = login(client, **NEW_USER)

    assert [u["username"] for u in client.get("/users", headers=auth).json()] == ["admin", "alice"]
    assert client.get("/users/alice", headers=auth).status_code == 200
    assert client.get("/users/nobody", headers=auth).status_code == 404

    assert client.delete("/users/admin", headers=auth).status_code == 400
    assert client.delete("/users/nobody", headers=auth).status_code == 404
    assert client.delete("/users/alice", headers=auth).status_code == 200
    # A deleted user's token stops working immediately.
    assert client.get("/users/me", headers=alice).status_code == 401


def test_state_survives_restart_and_admin_password_follows_env(settings):
    with TestClient(create_app(settings)) as first:
        auth = login(first)
        first.post("/users", json=NEW_USER, headers=auth)

    with TestClient(create_app(settings)) as second:
        # Same generated secret key and same database after a restart.
        assert second.get("/users/alice", headers=auth).status_code == 200

    changed = settings.model_copy(update={"ADMIN_PASSWORD": SecretStr("a-new-password")})
    with TestClient(create_app(changed)) as third:
        login(third, password="a-new-password")
        resp = third.post(
            "/login/access-token", data={"username": "admin", "password": ADMIN_PASSWORD}
        )
        assert resp.status_code == 401
