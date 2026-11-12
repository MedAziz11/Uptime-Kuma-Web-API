"""`KumaClient` with the real `uptime-kuma-api` client against a stand-in Socket.IO server.

The route tests replace `KumaClient` and the client tests replace `UptimeKumaApi`;
this module exercises the real Socket.IO path in between (connect, login, events,
session loss, dropped socket) without needing an Uptime Kuma instance.
"""

import asyncio
import socket
import threading
import time
from collections.abc import Iterator

import pytest
import socketio
import uvicorn
from uptime_kuma_api import UptimeKumaException

from app.config import Settings
from app.kuma import KumaClient, KumaUnavailableError

MONITOR = {"id": 1, "name": "site", "type": "http", "notificationIDList": {}, "authMethod": None}


class StandInKuma:
    """Implements just enough of Uptime Kuma's Socket.IO protocol."""

    def __init__(self) -> None:
        self.sio = socketio.AsyncServer(async_mode="asgi")
        self.logins: list[str] = []
        self.sessions: set[str] = set()
        self.sio.on("login", self._login)
        self.sio.on("getMonitorBeats", self._beats)
        self.sio.on("addTag", self._add_tag)
        self.sio.on("disconnect", self._disconnect)

        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            self.port = sock.getsockname()[1]
        config = uvicorn.Config(
            socketio.ASGIApp(self.sio), host="127.0.0.1", port=self.port, log_level="error"
        )
        self.server = uvicorn.Server(config)
        self.thread = threading.Thread(target=self.server.run, daemon=True)

    async def _login(self, sid, data):
        self.logins.append(data["username"])
        if data["password"] != "right-password":
            return {"ok": False, "msg": "Incorrect username or password."}
        self.sessions.add(sid)
        await self.sio.emit("monitorList", {"1": MONITOR}, to=sid)
        return {"ok": True, "token": "session-token"}

    async def _beats(self, sid, monitor_id, hours):
        if sid not in self.sessions:
            return {"ok": False, "msg": "You are not logged in."}
        if monitor_id != 1:
            return {"ok": False, "msg": "You do not own this monitor."}
        return {"ok": True, "data": [{"status": 1, "important": 0}]}

    async def _add_tag(self, sid, tag):
        return {"ok": True, "tag": {"id": 7, **tag}}

    async def _disconnect(self, sid, *args):
        self.sessions.discard(sid)

    def start(self) -> None:
        self.thread.start()
        deadline = time.time() + 10
        while not self.server.started:
            assert time.time() < deadline, "stand-in server did not start"
            time.sleep(0.05)

    def stop(self) -> None:
        self.server.should_exit = True
        self.thread.join(timeout=10)

    def drop_sockets(self) -> None:
        loop = self.server.servers[0].get_loop()
        for sid in list(self.sessions):
            asyncio.run_coroutine_threadsafe(self.sio.disconnect(sid), loop).result(timeout=5)


@pytest.fixture(scope="module")
def server() -> Iterator[StandInKuma]:
    stand_in = StandInKuma()
    stand_in.start()
    yield stand_in
    stand_in.stop()


def make_client(
    server: StandInKuma,
    tmp_path,
    password: str = "right-password",  # noqa: S107
) -> KumaClient:
    return KumaClient(
        Settings(
            _env_file=None,
            KUMA_SERVER=f"http://127.0.0.1:{server.port}",
            KUMA_USERNAME="kuma",
            KUMA_PASSWORD=password,
            ADMIN_PASSWORD="unused",
            KUMA_TIMEOUT=5,
            DB_PATH=tmp_path / "db.sqlite3",
        )
    )


@pytest.fixture
def kuma(server: StandInKuma, tmp_path) -> Iterator[KumaClient]:
    server.logins.clear()
    client = make_client(server, tmp_path)
    yield client
    client.close()


def test_logs_in_once_and_reads_pushed_and_requested_data(server, kuma):
    monitors = kuma.get_monitors()
    assert [m["name"] for m in monitors] == ["site"]
    assert kuma.get_monitor_beats(1, 1) == [{"status": 1, "important": False}]
    assert server.logins == ["kuma"]


def test_options_called_name_reach_the_server(kuma):
    assert kuma.add_tag(name="prod", color="#fff")["name"] == "prod"


def test_logs_in_again_when_the_server_forgot_the_session(server, kuma):
    kuma.get_monitor_beats(1, 1)
    server.sessions.clear()  # what a restarted Uptime Kuma looks like after auto-reconnect
    assert kuma.get_monitor_beats(1, 1)
    assert len(server.logins) == 2


def test_reconnects_after_the_socket_was_dropped(server, kuma):
    kuma.get_monitor_beats(1, 1)
    server.drop_sockets()
    time.sleep(0.5)
    assert kuma.get_monitor_beats(1, 1)
    assert len(server.logins) == 2


def test_server_errors_are_passed_through(kuma):
    with pytest.raises(UptimeKumaException, match="do not own"):
        kuma.get_monitor_beats(2, 1)


def test_statistics_never_pushed_are_empty(kuma):
    started = time.time()
    assert kuma.monitor_stats("cert_info") == {}
    assert time.time() - started < 4  # did not wait for the whole KUMA_TIMEOUT


def test_wrong_credentials(server, tmp_path):
    client = make_client(server, tmp_path, password="wrong-password")
    with pytest.raises(KumaUnavailableError, match="rejected the login"):
        client.get_monitors()


def test_unreachable_server(tmp_path):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        free_port = sock.getsockname()[1]
    settings = Settings(
        _env_file=None,
        KUMA_SERVER=f"http://127.0.0.1:{free_port}",
        KUMA_USERNAME="kuma",
        KUMA_PASSWORD="right-password",
        ADMIN_PASSWORD="unused",
        KUMA_TIMEOUT=2,
        DB_PATH=tmp_path / "db.sqlite3",
    )
    with pytest.raises(KumaUnavailableError, match="unable to connect"):
        KumaClient(settings).get_monitors()
