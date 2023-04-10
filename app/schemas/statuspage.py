from typing import List, Optional
from pydantic import BaseModel, Field, HttpUrl, constr


class Incident(BaseModel):
    content: str
    createdDate: str
    id: int
    lastUpdatedDate: Optional[str]
    pin: int
    style: str
    title: str


class Monitor(BaseModel):
    id: int
    maintenance: Optional[bool]
    name: str
    sendUrl: int


class PublicGroup(BaseModel):
    id: int
    monitorList: List[Monitor]
    name: str
    weight: int


class StatusPage(BaseModel):
    customCSS: Optional[str] = None
    description: Optional[str]
    domainNameList: List[HttpUrl]
    footerText: Optional[str]
    googleAnalyticsId: Optional[str]
    icon: str
    id: int
    incident: Optional[Incident]
    maintenanceList: Optional[List]
    published: bool
    showPoweredBy: bool
    showTags: bool
    slug: constr(min_length=1)
    theme: str
    title: str
    publicGroupList: Optional[List[PublicGroup]]


class StatusPageList(BaseModel):
    statuspages: List[StatusPage]


class AddStatusPageRequest(BaseModel):
    slug: Optional[str] = None
    title: Optional[str] = None
    msg: Optional[str] = None


class AddStatusPageResponse(BaseModel):
    msg: Optional[str] = None


class SaveStatusPageRequest(BaseModel):
    id: int
    title: str
    slug: constr(min_length=1)
    description: Optional[str] = None
    theme: Optional[str] = "light"
    published: Optional[bool] = True
    showTags: Optional[bool] = False
    domainNameList: Optional[List[HttpUrl]] = None
    googleAnalyticsId: Optional[str] = None
    customCSS: Optional[str] = ""
    footerText: Optional[str] = None
    showPoweredBy: Optional[bool] = True
    icon: Optional[str] = "/icon.svg"
    publicGroupList: Optional[List] = None


class SaveStatusPageResponse(BaseModel):
    detail: str


class DeleteStatusPageResponse(BaseModel):
    detail: Optional[str] = Field(None, description="Error detail, if any")


## Error
# uptime-kuma-web-api-api-1  | pydantic.error_wrappers.ValidationError: 1 validation error for DeleteStatusPageResponse
# uptime-kuma-web-api-api-1  | response
# uptime-kuma-web-api-api-1  |   none is not an allowed value (type=type_error.none.not_allowed)
