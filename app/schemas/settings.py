from enum import Enum
from pydantic import BaseModel
from typing import Any, Dict, List, Optional



class ImportHandleType(str, Enum):
    SKIP="skip"
    OVERWRITE="overwrite"
    KEEP="keep"

    
class NotificationListItem(BaseModel):
    id: int
    name: str
    config: str
    active: bool
    userId: int
    isDefault: bool


class MonitorListItem(BaseModel):
    id: Optional[int]
    name: Optional[str]
    url: Optional[str]
    method: Optional[str]
    hostname: Any
    port: Optional[int]
    maxretries: Optional[int]
    weight: Optional[int]
    active: Optional[int]
    type: Optional[str]
    interval: Optional[int]
    retryInterval: Optional[int]
    resendInterval: Optional[int]
    keyword: Any
    expiryNotification: bool
    ignoreTls: bool
    upsideDown: bool
    maxredirects: Optional[int]
    accepted_statuscodes: Optional[List[str]]
    dns_resolve_type: Optional[str]
    dns_resolve_server: Optional[Optional[str]]
    dns_last_result: Any
    pushToken: Any
    docker_container: Any
    docker_host: Any
    proxyId: Any
    notificationIDList: Optional[Dict[str, Any]]
    tags: Optional[List]
    mqttUsername: Any
    mqttPassword: Any
    mqttTopic: Any
    mqttSuccessMessage: Any
    databaseConnectionString: Any
    databaseQuery: Any
    authMethod: Optional[str]
    authWorkstation: Any
    authDomain: Any
    radiusUsername: Any
    radiusPassword: Any
    radiusCalledStationId: Any
    radiusCallingStationId: Any
    radiusSecret: Any
    headers: Any
    body: Any
    basic_auth_user: Any
    basic_auth_pass: Any

class Backup(BaseModel):
    version: Optional[str]
    notificationList: Optional[List[NotificationListItem]]
    monitorList: Optional[List[MonitorListItem]]
    proxyList: Optional[List]