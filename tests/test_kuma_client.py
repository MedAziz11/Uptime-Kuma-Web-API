from types import SimpleNamespace
from typing import ClassVar

import pytest
import socketio.exceptions
from uptime_kuma_api import Timeout, UptimeKumaException

import app.kuma as kuma_module
from app.kuma import KumaClient, KumaInvalidRequestError, KumaTimeoutError, KumaUnavailableError


class FakeApi:
    """Replaces `UptimeKumaApi` so no network connection is made."""

    instances: ClassVar[list["FakeApi"]] = []
    connect_error: Exception | None = None
    login_error: Exception | None = None

    def __init__(self, url, timeout, ssl_verify):
        if FakeApi.connect_error:
            raise FakeApi.connect_error
        self.url = url
        self.timeout = timeout
        self.stats = {1: {"valid": True}}
        self.sio = SimpleNamespace(connected=True)
        self.logged_in = False
        self.side_effects: list[Exception] = []
        FakeApi.instances.append(self)

    def login(self, username, password):
        if FakeApi.login_error:
            raise FakeApi.login_error
        self.credentials = (username, password)
        self.logged_in = True

    def disconnect(self):
        self.sio.connected = False

    def get_monitors(self):
        if self.side_effects:
            raise self.side_effects.pop(0)
        return [{"id": 1, "api": len(FakeApi.instances)}]

    def add_monitor(self, **kwargs):
        raise TypeError("unknown argument 'foo'")

    def add_tag(self, **kwargs):
        return kwargs

    def cert_info(self):
        self.timeout_during_call = self.timeout
        if self.side_effects:
            raise self.side_effects.pop(0)
        return self.stats

    avg_ping = uptime = cert_info


@pytest.fixture
def client(settings, monkeypatch) -> KumaClient:
    FakeApi.instances = []
    FakeApi.connect_error = FakeApi.login_error = None
    monkeypatch.setattr(kuma_module, "UptimeKumaApi", FakeApi)
    return KumaClient(settings)


def test_connects_lazily_logs_in_and_reuses_the_connection(client):
    assert FakeApi.instances == []
    assert client.get_monitors() == [{"id": 1, "api": 1}]
    client.get_monitors()
    assert len(FakeApi.instances) == 1
    api = FakeApi.instances[0]
    assert api.url == "http://kuma.invalid:3001"  # trailing slash stripped
    assert api.credentials == ("kuma-user", "kuma-password")


def test_options_named_like_internal_parameters_are_forwarded(client):
    options = {"name": "prod", "args": 2, "kwargs": 3}
    assert client.add_tag(**options) == options


def test_only_public_api_methods_are_exposed(client):
    with pytest.raises(AttributeError):
        client.not_a_kuma_method  # noqa: B018
    with pytest.raises(AttributeError):
        client._call  # noqa: B018


def test_reconnects_when_the_socket_dropped(client):
    client.get_monitors()
    FakeApi.instances[0].sio.connected = False
    assert client.get_monitors() == [{"id": 1, "api": 2}]


@pytest.mark.parametrize(
    "error",
    [
        socketio.exceptions.BadNamespaceError("/ is not a connected namespace."),
        UptimeKumaException("You are not logged in."),
    ],
)
def test_retries_once_after_losing_the_session(client, error):
    client.get_monitors()
    FakeApi.instances[0].side_effects = [error]
    assert client.get_monitors() == [{"id": 1, "api": 2}]
    assert FakeApi.instances[0].sio.connected is False


def test_timeouts_are_not_retried(client):
    client.get_monitors()
    FakeApi.instances[0].side_effects = [Timeout("no answer")]
    with pytest.raises(KumaTimeoutError):
        client.get_monitors()
    FakeApi.instances[0].side_effects = [socketio.exceptions.TimeoutError()]
    with pytest.raises(KumaTimeoutError):
        client.get_monitors()
    assert len(FakeApi.instances) == 1


def test_kuma_errors_and_invalid_arguments(client):
    client.get_monitors()
    FakeApi.instances[0].side_effects = [UptimeKumaException("monitor does not exist")]
    with pytest.raises(UptimeKumaException):
        client.get_monitors()
    with pytest.raises(KumaInvalidRequestError, match="unknown argument"):
        client.add_monitor(foo=1)


def test_unreachable_server_and_bad_credentials(client):
    FakeApi.connect_error = UptimeKumaException("unable to connect")
    with pytest.raises(KumaUnavailableError, match="unable to connect"):
        client.get_monitors()

    FakeApi.connect_error = None
    FakeApi.login_error = UptimeKumaException("Incorrect username or password.")
    with pytest.raises(KumaUnavailableError, match="rejected the login"):
        client.get_monitors()
    assert FakeApi.instances[-1].sio.connected is False

    # Recovers as soon as Kuma is reachable again.
    FakeApi.login_error = None
    assert client.get_monitors()


def test_close_disconnects(client):
    client.get_monitors()
    client.close()
    assert FakeApi.instances[0].sio.connected is False
    client.close()  # idempotent


def test_monitor_stats(client):
    assert client.monitor_stats("cert_info") == {1: {"valid": True}}
    api = FakeApi.instances[0]
    # Waits briefly for pushed data, then restores the configured timeout.
    assert api.timeout_during_call == 2
    assert api.timeout == 10

    # No monitor at all: the library returns a list.
    api.stats = []
    assert client.monitor_stats("uptime") == {}

    # Nothing was ever pushed (e.g. no HTTPS monitor): "no data", not an error.
    api.side_effects = [Timeout("Timed out while waiting for event Event.CERT_INFO")]
    assert client.monitor_stats("cert_info") == {}
    assert api.timeout == 10

    api.side_effects = [socketio.exceptions.BadNamespaceError("/")]
    with pytest.raises(KumaUnavailableError):
        client.monitor_stats("avg_ping")

    with pytest.raises(AttributeError):
        client.monitor_stats("delete_monitor")
