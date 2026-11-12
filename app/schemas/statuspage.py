from typing import Any

from pydantic import BaseModel, ConfigDict, Field
from uptime_kuma_api import IncidentStyle


class _Loose(BaseModel):
    """Response model that keeps fields added by newer Uptime Kuma versions."""

    model_config = ConfigDict(extra="allow")


class Incident(_Loose):
    id: int
    title: str
    content: str
    style: str
    pin: bool | None = None
    createdDate: str | None = None
    lastUpdatedDate: str | None = None


class StatusPageSummary(_Loose):
    id: int
    slug: str
    title: str
    description: str | None = None
    icon: str | None = None
    theme: str | None = None
    published: bool | None = None
    showTags: bool | None = None
    domainNameList: list[str] = []
    customCSS: str | None = None
    footerText: str | None = None
    showPoweredBy: bool | None = None
    googleAnalyticsId: str | None = None


class StatusPage(StatusPageSummary):
    incident: Incident | None = None
    publicGroupList: list[dict[str, Any]] = []
    maintenanceList: list[dict[str, Any]] = []


class StatusPageList(BaseModel):
    statuspages: list[StatusPageSummary] = []


class AddStatusPageRequest(BaseModel):
    slug: str = Field(min_length=1)
    title: str = Field(min_length=1)


class AddStatusPageResponse(BaseModel):
    msg: str | None = None


class SaveStatusPageRequest(BaseModel):
    """Fields left out keep their current value."""

    title: str | None = None
    description: str | None = None
    theme: str | None = None
    published: bool | None = None
    showTags: bool | None = None
    domainNameList: list[str] | None = None
    googleAnalyticsId: str | None = None
    customCSS: str | None = None
    footerText: str | None = None
    showPoweredBy: bool | None = None
    showCertificateExpiry: bool | None = None
    icon: str | None = None
    publicGroupList: list[dict[str, Any]] | None = None


class Detail(BaseModel):
    detail: Any = None


class PostIncidentRequest(BaseModel):
    title: str
    content: str
    style: IncidentStyle = IncidentStyle.PRIMARY
