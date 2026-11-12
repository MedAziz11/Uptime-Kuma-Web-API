import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import Depends, FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import RedirectResponse

from app import __version__
from app.config import Settings
from app.database import ensure_admin, register_db
from app.deps import get_current_user
from app.errors import register_exception_handlers
from app.kuma import KumaClient
from app.routers import (
    auth,
    cert,
    database,
    docker_hosts,
    info,
    maintenance,
    monitors,
    notifications,
    ping,
    proxies,
    settings,
    statuspages,
    tags,
    uptime,
    users,
)

logger = logging.getLogger(__name__)


def create_app(app_settings: Settings | None = None) -> FastAPI:
    app_settings = app_settings or Settings()
    logging.basicConfig(
        level=app_settings.LOG_LEVEL,
        format="%(asctime)s %(levelname)s [%(name)s] %(message)s",
    )

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        app.state.settings = app_settings
        app.state.secret_key = app_settings.resolve_secret_key()
        # Connects lazily on first use, so the API starts even when Kuma is down.
        app.state.kuma = KumaClient(app_settings)
        try:
            async with register_db(app, app_settings):
                await ensure_admin(app_settings)
                logger.info("%s %s started", app_settings.PROJECT_NAME, __version__)
                yield
        finally:
            app.state.kuma.close()

    app = FastAPI(
        title=app_settings.PROJECT_NAME,
        version=__version__,
        description="A REST API wrapper for Uptime Kuma.",
        license_info={"name": "MIT", "identifier": "MIT"},
        lifespan=lifespan,
    )

    if app_settings.BACKEND_CORS_ORIGINS:
        app.add_middleware(
            CORSMiddleware,
            allow_origins=app_settings.BACKEND_CORS_ORIGINS,
            allow_credentials=True,
            allow_methods=["*"],
            allow_headers=["*"],
        )

    register_exception_handlers(app)

    app.include_router(auth.router, prefix="/login", tags=["Authentication"])
    app.include_router(users.router, prefix="/users", tags=["Users"])

    # Everything that talks to Uptime Kuma requires a valid token.
    authenticated = [Depends(get_current_user)]
    for router, prefix, tag in (
        (monitors.router, "/monitors", "Monitor"),
        (statuspages.router, "/statuspages", "Status Page"),
        (maintenance.router, "/maintenance", "Maintenance"),
        (tags.router, "/tags", "Tags"),
        (notifications.router, "/notifications", "Notifications"),
        (proxies.router, "/proxies", "Proxies"),
        (docker_hosts.router, "/docker_hosts", "Docker Hosts"),
        (settings.router, "/settings", "Settings"),
        (database.router, "/database", "DataBase"),
        (cert.router, "/cert_info", "Certification Info"),
        (info.router, "/info", "Informations"),
        (ping.router, "/ping", "Ping Average"),
        (uptime.router, "/uptime", "Uptime"),
    ):
        app.include_router(router, prefix=prefix, tags=[tag], dependencies=authenticated)

    @app.get("/", include_in_schema=False)
    async def root() -> RedirectResponse:
        return RedirectResponse(url="/docs")

    @app.get("/health", tags=["Health"], summary="Liveness probe")
    async def health() -> dict[str, str]:
        return {"status": "ok", "version": __version__}

    return app
