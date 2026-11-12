from collections.abc import Iterator
from typing import Any

import pytest
from fastapi.testclient import TestClient

from app.config import Settings
from app.main import create_app

ADMIN_PASSWORD = "admin-password"


class FakeKuma:
    """Stands in for `KumaClient`: records calls and returns canned results."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, tuple, dict]] = []
        self.results: dict[str, Any] = {}
        self.errors: dict[str, Exception] = {}

    def __getattr__(self, name: str) -> Any:
        if name.startswith("_"):
            raise AttributeError(name)

        def method(*args: Any, **kwargs: Any) -> Any:
            self.calls.append((name, args, kwargs))
            if name in self.errors:
                raise self.errors[name]
            return self.results.get(name, {"msg": "ok"})

        return method

    def close(self) -> None:
        pass

    @property
    def last_call(self) -> tuple[str, tuple, dict]:
        return self.calls[-1]


@pytest.fixture
def settings(tmp_path) -> Settings:
    return Settings(
        _env_file=None,
        KUMA_SERVER="http://kuma.invalid:3001/",
        KUMA_USERNAME="kuma-user",
        KUMA_PASSWORD="kuma-password",
        ADMIN_PASSWORD=ADMIN_PASSWORD,
        DB_PATH=tmp_path / "db" / "db.sqlite3",
    )


@pytest.fixture
def client(settings: Settings) -> Iterator[TestClient]:
    with TestClient(create_app(settings)) as test_client:
        yield test_client


@pytest.fixture
def kuma(client: TestClient) -> FakeKuma:
    fake = FakeKuma()
    client.app.state.kuma = fake
    return fake


def login(client: TestClient, username: str = "admin", password: str = ADMIN_PASSWORD) -> dict:
    resp = client.post("/login/access-token", data={"username": username, "password": password})
    assert resp.status_code == 200, resp.text
    return {"Authorization": f"Bearer {resp.json()['access_token']}"}


@pytest.fixture
def auth(client: TestClient) -> dict[str, str]:
    return login(client)
