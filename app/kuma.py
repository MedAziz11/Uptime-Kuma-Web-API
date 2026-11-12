"""A single, shared, self-healing connection to Uptime Kuma.

`uptime-kuma-api` is a blocking Socket.IO client. v1 of this project opened a
new connection per request and never closed it. `KumaClient` keeps one
authenticated connection, serializes access to it and reconnects when needed.
Its methods block, so route handlers using it are plain `def` functions that
FastAPI runs in its thread pool.
"""

import logging
import threading
from collections.abc import Callable
from typing import Any

import requests
import socketio.exceptions
from uptime_kuma_api import Timeout, UptimeKumaApi, UptimeKumaException

from app.config import Settings

logger = logging.getLogger(__name__)

_NOT_LOGGED_IN = "not logged in"


class KumaUnavailableError(Exception):
    """Uptime Kuma cannot be reached or refuses the configured credentials."""


class KumaTimeoutError(Exception):
    """Uptime Kuma did not answer in time."""


class KumaInvalidRequestError(Exception):
    """The library rejected the arguments before sending them to Uptime Kuma."""


class KumaClient:
    def __init__(self, settings: Settings) -> None:
        self._settings = settings
        self._lock = threading.Lock()
        self._api: UptimeKumaApi | None = None

    def __getattr__(self, name: str) -> Callable[..., Any]:
        """Expose the public methods of `UptimeKumaApi`, e.g. `kuma.get_monitors()`."""
        if name.startswith("_") or not callable(getattr(UptimeKumaApi, name, None)):
            raise AttributeError(name)

        def method(*args: Any, **kwargs: Any) -> Any:
            return self.call(name, *args, **kwargs)

        method.__name__ = name
        return method

    def call(self, name: str, *args: Any, **kwargs: Any) -> Any:
        with self._lock:
            try:
                return self._invoke(name, args, kwargs)
            except _ConnectionLostError:
                # The connection dropped (or was silently re-established without a
                # session) before the request was sent: retrying once is safe.
                self._disconnect()
                try:
                    return self._invoke(name, args, kwargs)
                except _ConnectionLostError as exc:
                    self._disconnect()
                    raise KumaUnavailableError("lost the connection to Uptime Kuma") from exc

    def close(self) -> None:
        with self._lock:
            self._disconnect()

    def _invoke(self, name: str, args: tuple, kwargs: dict) -> Any:
        api = self._connect()
        try:
            return getattr(api, name)(*args, **kwargs)
        except Timeout as exc:
            raise KumaTimeoutError(str(exc)) from exc
        except socketio.exceptions.TimeoutError as exc:
            raise KumaTimeoutError(f"timed out while calling '{name}'") from exc
        except (socketio.exceptions.SocketIOError, requests.ConnectionError) as exc:
            raise _ConnectionLostError(str(exc)) from exc
        except UptimeKumaException as exc:
            if _NOT_LOGGED_IN in str(exc).lower():
                raise _ConnectionLostError(str(exc)) from exc
            raise
        except (TypeError, ValueError) as exc:
            raise KumaInvalidRequestError(str(exc) or "invalid arguments") from exc

    def _connect(self) -> UptimeKumaApi:
        if self._api is not None:
            if self._api.sio.connected:
                return self._api
            self._disconnect()

        settings = self._settings
        try:
            api = UptimeKumaApi(
                settings.KUMA_SERVER,
                timeout=settings.KUMA_TIMEOUT,
                ssl_verify=settings.KUMA_SSL_VERIFY,
            )
        except Exception as exc:
            raise KumaUnavailableError(
                f"unable to connect to Uptime Kuma at {settings.KUMA_SERVER}"
            ) from exc

        try:
            api.login(settings.KUMA_USERNAME, settings.KUMA_PASSWORD.get_secret_value())
        except Exception as exc:
            _safe_disconnect(api)
            if isinstance(exc, UptimeKumaException) and not isinstance(exc, Timeout):
                raise KumaUnavailableError(f"Uptime Kuma rejected the login: {exc}") from exc
            raise KumaUnavailableError("unable to log in to Uptime Kuma") from exc

        logger.info("Connected to Uptime Kuma at %s", settings.KUMA_SERVER)
        self._api = api
        return api

    def _disconnect(self) -> None:
        if self._api is not None:
            _safe_disconnect(self._api)
            self._api = None


class _ConnectionLostError(Exception):
    pass


def _safe_disconnect(api: UptimeKumaApi) -> None:
    try:
        api.disconnect()
    except Exception:
        logger.debug("Error while disconnecting from Uptime Kuma", exc_info=True)
