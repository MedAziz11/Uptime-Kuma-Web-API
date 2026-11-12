import logging
import os
import secrets
from pathlib import Path

from pydantic import Field, SecretStr, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

logger = logging.getLogger(__name__)

ADMIN_USERNAME = "admin"


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore", case_sensitive=True)

    PROJECT_NAME: str = "Uptime-Kuma-API"

    # Uptime Kuma connection
    KUMA_SERVER: str
    KUMA_USERNAME: str
    KUMA_PASSWORD: SecretStr
    KUMA_TIMEOUT: float = Field(default=10, gt=0)
    KUMA_SSL_VERIFY: bool = True

    # API authentication
    ADMIN_PASSWORD: SecretStr
    SECRET_KEY: SecretStr | None = None
    ACCESS_TOKEN_EXPIRATION: int = Field(default=60 * 24 * 8, gt=0)  # minutes (8 days)

    DB_PATH: Path = Path("db/db.sqlite3")
    BACKEND_CORS_ORIGINS: list[str] = []
    LOG_LEVEL: str = "INFO"

    @field_validator("SECRET_KEY", "KUMA_PASSWORD", "ADMIN_PASSWORD", mode="before")
    @classmethod
    def _strip_blank(cls, value: object) -> object:
        """Treat blank values (e.g. `SECRET_KEY=` in a compose file) as unset."""
        if isinstance(value, str) and not value.strip():
            return None
        return value

    @field_validator("KUMA_SERVER")
    @classmethod
    def _normalize_server(cls, value: str) -> str:
        return value.rstrip("/")

    @field_validator("LOG_LEVEL")
    @classmethod
    def _check_log_level(cls, value: str) -> str:
        value = value.upper()
        if value not in logging.getLevelNamesMapping():
            raise ValueError(f"unknown log level '{value}'")
        return value

    def resolve_secret_key(self) -> str:
        """Return the JWT signing key.

        When SECRET_KEY is not set, a key is generated once and stored next to
        the database so that tokens stay valid across restarts.
        """
        if self.SECRET_KEY is not None:
            return self.SECRET_KEY.get_secret_value()

        key_file = self.DB_PATH.parent / "secret_key"
        if key_file.exists():
            key = key_file.read_text(encoding="utf-8").strip()
            if key:
                return key

        key = secrets.token_urlsafe(48)
        key_file.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(key_file, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as fh:
            fh.write(key)
        logger.warning("SECRET_KEY is not set: generated one and stored it in %s", key_file)
        return key
