from typing import Any

from pydantic import BaseModel, ConfigDict
from uptime_kuma_api import MaintenanceStrategy


class MaintenanceUpdate(BaseModel):
    """Maintenance settings. Fields left out keep the Uptime Kuma defaults.

    - **dateRange**: start and end, e.g. `["2024-01-01 00:00:00", "2024-01-02 00:00:00"]`
    - **weekdays**: days of the week (Sun = 0 ... Sat = 6), for `recurring-weekday`
    - **daysOfMonth**: days `1`-`31` or `"lastDay1"`-`"lastDay4"`, for `recurring-day-of-month`
    - **timeRange**: daily window, e.g. `[{"hours": 2, "minutes": 0}, {"hours": 3, "minutes": 0}]`
    - **cron** / **durationMinutes**: for the `cron` strategy
    """

    model_config = ConfigDict(use_enum_values=True)

    title: str | None = None
    strategy: MaintenanceStrategy | None = None
    active: bool | None = None
    description: str | None = None
    dateRange: list[str] | None = None
    intervalDay: int | None = None
    weekdays: list[int] | None = None
    daysOfMonth: list[int | str] | None = None
    timeRange: list[dict[str, Any]] | None = None
    cron: str | None = None
    durationMinutes: int | None = None
    timezoneOption: str | None = None


class Maintenance(MaintenanceUpdate):
    title: str
    strategy: MaintenanceStrategy


class MonitorMaintenance(BaseModel):
    id: int
    name: str | None = None
