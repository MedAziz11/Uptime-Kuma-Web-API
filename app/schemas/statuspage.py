from typing import List, Optional
from pydantic import BaseModel

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
    customCSS: Optional[str] = None  # Make customCSS field optional
    description: Optional[str]
    domainNameList: List
    footerText: Optional[str]
    googleAnalyticsId: Optional[str]
    icon: str
    id: int
    incident: Optional[Incident]
    maintenanceList: Optional[List]
    published: bool
    showPoweredBy: bool
    showTags: bool
    slug: str
    theme: str
    title: str
    publicGroupList: Optional[List[PublicGroup]]

class StatusPageList(BaseModel):
    statuspages: List[StatusPage]


class AddStatusPageResponse(BaseModel):
    msg: str

class SaveStatusPageRequest(BaseModel):
    id: int
    title: str
    description: Optional[str] = None
    theme: Optional[str] = "light"
    published: Optional[bool] = True
    showTags: Optional[bool] = False
    domainNameList: Optional[List[str]] = None
    googleAnalyticsId: Optional[str] = None
    customCSS: Optional[str] = ""
    footerText: Optional[str] = None
    showPoweredBy: Optional[bool] = True
    icon: Optional[str] = "/icon.svg"
    publicGroupList: Optional[List] = None
