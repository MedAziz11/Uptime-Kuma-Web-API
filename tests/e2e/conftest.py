"""End-to-end tests: the API in front of a real Uptime Kuma. See README.md in this folder."""

import os
from collections.abc import Iterator

import pytest
import requests

API_URL = os.environ.get("E2E_API_URL", "").rstrip("/")
ADMIN_PASSWORD = os.environ.get("E2E_ADMIN_PASSWORD", "")


def pytest_collection_modifyitems(items: list[pytest.Item]) -> None:
    skip = pytest.mark.skip(reason="set E2E_API_URL and E2E_ADMIN_PASSWORD to run")
    for item in items:
        if "e2e" in item.path.parts:
            item.add_marker(pytest.mark.e2e)
            if not (API_URL and ADMIN_PASSWORD):
                item.add_marker(skip)


class Api:
    """Thin wrapper that checks the status code of every call."""

    def __init__(self, url: str) -> None:
        self.url = url
        self.session = requests.Session()

    def login(self, username: str, password: str) -> None:
        resp = requests.post(
            f"{self.url}/login/access-token",
            data={"username": username, "password": password},
            timeout=30,
        )
        assert resp.status_code == 200, resp.text
        self.session.headers["Authorization"] = f"Bearer {resp.json()['access_token']}"

    def call(self, method: str, path: str, expect: int = 200, **kwargs):
        resp = self.session.request(method, self.url + path, timeout=60, **kwargs)
        assert resp.status_code == expect, f"{method} {path} -> {resp.status_code}: {resp.text}"
        return resp.json()

    def get(self, path: str, **kwargs):
        return self.call("GET", path, **kwargs)

    def post(self, path: str, **kwargs):
        return self.call("POST", path, **kwargs)

    def patch(self, path: str, **kwargs):
        return self.call("PATCH", path, **kwargs)

    def delete(self, path: str, **kwargs):
        return self.call("DELETE", path, **kwargs)


@pytest.fixture(scope="session")
def api() -> Api:
    client = Api(API_URL)
    client.login("admin", ADMIN_PASSWORD)
    return client


@pytest.fixture
def monitor(api: Api) -> Iterator[int]:
    """A monitor that checks the API's own health endpoint."""
    created = api.post(
        "/monitors",
        json={
            "type": "http",
            "name": "e2e monitor",
            "url": os.environ.get("E2E_MONITOR_URL", "http://api:8000/health"),
            "interval": 20,
        },
    )
    yield created["monitorID"]
    api.delete(f"/monitors/{created['monitorID']}")
