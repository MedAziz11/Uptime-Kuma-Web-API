from typing import List, Optional
from pydantic import BaseModel
import datetime


from uptime_kuma_api import MaintenanceStrategy


class Maintenance(BaseModel):

    """ title (str) – Title

        strategy (MaintenanceStrategy) – Strategy

        active (bool, optional) – True if maintenance is active, defaults to True

        description (str, optional) – Description, defaults to ""

        dateRange (list, optional) – DateTime Range, defaults to ["<current date>"]

        intervalDay (int, optional) – Interval (Run once every day), defaults to 1

        weekdays (list, optional) – List that contains the days of the week on which the maintenance is enabled (Sun = 0, Mon = 1, …, Sat = 6). Required for strategy RECURRING_WEEKDAY., defaults to [].

        daysOfMonth (list, optional) – List that contains the days of the month on which the maintenance is enabled (Day 1 = 1, Day 2 = 2, …, Day 31 = 31) and the last day of the month (Last Day of Month = "lastDay1", 2nd Last Day of Month = "lastDay2", 3rd Last Day of Month = "lastDay3", 4th Last Day of Month = "lastDay4"). Required for strategy RECURRING_DAY_OF_MONTH., defaults to [].

        timeRange (list, optional) – Maintenance Time Window of a Day, defaults to [{"hours": 2, "minutes": 0}, {"hours": 3, "minutes": 0}]."""
    title: str
    strategy: MaintenanceStrategy
    active: Optional[bool] = True
    description: Optional[str] = ""
    dateRange: Optional[list] = [datetime.date.today().strftime("%Y-%m-%d 00:00:00")]
    intervalDay: Optional[int] = 1
    weekdays: Optional[list] = []
    daysOfMonth: Optional[list] = []
    timeRange: Optional[list] = [{"hours": 2, "minutes": 0}, {"hours": 3, "minutes": 0}]

    class Config:  
        use_enum_values = True


class MaintenanceUpdate(Maintenance):
    title: Optional[str]=None
    strategy: Optional[MaintenanceStrategy]=None


class MonitorMaintenance(BaseModel):
    id: int
    name: str
