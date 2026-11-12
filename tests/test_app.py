import re
import tomllib
from pathlib import Path

from fastapi.testclient import TestClient

from app import __version__
from app.main import create_app

ROOT = Path(__file__).resolve().parent.parent

# The only routes that may be called without a token.
PUBLIC_ROUTES = {("GET", "/"), ("GET", "/health"), ("POST", "/login/access-token")}


def test_every_route_but_the_public_ones_requires_a_token(client, kuma):
    """Guards against a new router being added without authentication."""
    paths = client.get("/openapi.json").json()["paths"]
    checked = 0
    for route_path, operations in paths.items():
        path = re.sub(r"{[^}]+}", "1", route_path)
        for method in operations:
            if (method.upper(), route_path) in PUBLIC_ROUTES:
                continue
            resp = client.request(method.upper(), path, json={})
            assert resp.status_code == 401, f"{method} {route_path} answered {resp.status_code}"
            checked += 1
    assert checked >= 60
    assert kuma.calls == []


def test_openapi_document(client):
    doc = client.get("/openapi.json").json()
    assert doc["info"]["version"] == __version__
    assert doc["info"]["license"]["name"] == "MIT"
    assert "OAuth2PasswordBearer" in doc["components"]["securitySchemes"]
    # Every operation is documented with a summary.
    for path, operations in doc["paths"].items():
        for method, operation in operations.items():
            assert operation.get("summary"), f"{method.upper()} {path} has no summary"


def test_cors_is_off_by_default(client):
    resp = client.get("/health", headers={"Origin": "https://dashboard.example.com"})
    assert "access-control-allow-origin" not in resp.headers


def test_cors_allows_configured_origins(settings):
    allowed = "https://dashboard.example.com"
    cors = settings.model_copy(update={"BACKEND_CORS_ORIGINS": [allowed]})
    with TestClient(create_app(cors)) as client:
        resp = client.get("/health", headers={"Origin": allowed})
        assert resp.headers["access-control-allow-origin"] == allowed

        resp = client.get("/health", headers={"Origin": "https://evil.example.com"})
        assert "access-control-allow-origin" not in resp.headers

        preflight = client.options(
            "/monitors",
            headers={
                "Origin": allowed,
                "Access-Control-Request-Method": "POST",
                "Access-Control-Request-Headers": "authorization",
            },
        )
        assert preflight.status_code == 200
        assert preflight.headers["access-control-allow-origin"] == allowed


def test_kuma_connection_is_closed_on_shutdown(settings):
    closed = []
    with TestClient(create_app(settings)) as client:
        client.app.state.kuma.close = lambda: closed.append(True)
    assert closed == [True]


def test_version_is_consistent_across_the_project():
    pyproject = tomllib.loads((ROOT / "pyproject.toml").read_text(encoding="utf-8"))
    assert pyproject["project"]["version"] == __version__

    changelog = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
    latest = re.search(r"^## \[?(\d+\.\d+\.\d+)", changelog, re.MULTILINE)
    assert latest and latest.group(1) == __version__

    assert (ROOT / "docs" / "releases" / f"v{__version__}.md").is_file()
