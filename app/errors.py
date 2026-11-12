import logging
import re

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from uptime_kuma_api import UptimeKumaException

from app.kuma import KumaInvalidRequestError, KumaTimeoutError, KumaUnavailableError

logger = logging.getLogger(__name__)

_NOT_FOUND = re.compile(r"does not exist|not found|no .*found", re.IGNORECASE)


def _error(status_code: int, detail: str) -> JSONResponse:
    return JSONResponse(status_code=status_code, content={"detail": detail})


def register_exception_handlers(app: FastAPI) -> None:
    @app.exception_handler(UptimeKumaException)
    async def kuma_error(request: Request, exc: UptimeKumaException) -> JSONResponse:
        detail = str(exc) or "Uptime Kuma returned an error"
        logger.info("%s %s: %s", request.method, request.url.path, detail)
        return _error(404 if _NOT_FOUND.search(detail) else 400, detail)

    @app.exception_handler(KumaInvalidRequestError)
    async def kuma_invalid(request: Request, exc: KumaInvalidRequestError) -> JSONResponse:
        return _error(422, str(exc))

    @app.exception_handler(KumaTimeoutError)
    async def kuma_timeout(request: Request, exc: KumaTimeoutError) -> JSONResponse:
        logger.warning("%s %s: %s", request.method, request.url.path, exc)
        return _error(504, "Uptime Kuma did not answer in time")

    @app.exception_handler(KumaUnavailableError)
    async def kuma_unavailable(request: Request, exc: KumaUnavailableError) -> JSONResponse:
        logger.error("%s %s: %s", request.method, request.url.path, exc)
        return _error(503, str(exc))
