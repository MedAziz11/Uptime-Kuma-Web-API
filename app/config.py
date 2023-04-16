from pydantic import AnyHttpUrl, BaseSettings
from fastapi.logger import logger as fast_api_logger
from typing import List
import secrets
import logging
import os

logger = logging.getLogger("gunicorn.error")
fast_api_logger.handlers = logger.handlers


class Settings(BaseSettings):
    PROJECT_NAME: str = "Uptime-Kuma-API"
    BACKEND_CORS_ORIGINS: List[AnyHttpUrl] = []

    ACCESS_TOKEN_EXPIRE: int = os.environ.get(
        "ACCESS_TOKEN_EXPIRATION", 60 * 24 * 8
    )  # 8 days
    SECRET_KEY: str = os.environ.get("SECRET_KEY", secrets.token_urlsafe(32))

    KUMA_SERVER: str = os.environ.get("KUMA_SERVER")
    KUMA_USERNAME: str = os.environ.get("KUMA_USERNAME")
    KUMA_PASSWORD: str = os.environ.get("KUMA_PASSWORD")

    ADMIN_PASSWORD: str = os.environ.get("ADMIN_PASSWORD")

    class Config:
        case_sensitive = True
        env_file = ".env"


settings = Settings()
