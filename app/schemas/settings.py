from enum import StrEnum
from typing import Any

from pydantic import BaseModel, ConfigDict


class ImportHandleType(StrEnum):
    SKIP = "skip"
    OVERWRITE = "overwrite"
    KEEP = "keep"


class Backup(BaseModel):
    """An Uptime Kuma backup file (the JSON produced by Settings > Backup > Export)."""

    model_config = ConfigDict(extra="allow")

    version: str | None = None
    notificationList: list[dict[str, Any]] | None = None
    monitorList: list[dict[str, Any]] | None = None
    proxyList: list[dict[str, Any]] | None = None
