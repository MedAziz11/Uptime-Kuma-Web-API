from typing import List, Optional
from pydantic import BaseModel


from uptime_kuma_api import MonitorType, AuthMethod


class Monitor(BaseModel):
    type: MonitorType
    name: str
    interval: int = 60
    retryInterval: int = 60
    resendInterval: int = 0
    maxretries: int = 0
    upsideDown: bool = False
    notificationIDList: Optional[List] = None

    # HTTP KEYWORD
    url: Optional[str] = None
    expiryNotification: bool = False
    ignoreTls: bool = False
    maxredirects: int = 10
    accepted_statuscodes: Optional[List] = None
    proxyId: Optional[int] = None
    method: str = "GET"
    body: Optional[str] = None
    headers: Optional[str] = None
    authMethod: AuthMethod = AuthMethod.NONE
    basic_auth_user: Optional[str] = None
    basic_auth_pass: Optional[str] = None
    authDomain: Optional[str] = None
    authWorkstation: Optional[str] = None

    # KEYWORD
    keyword: Optional[str] = None

    # DNS PING STEAM MQTT
    hostname: Optional[str] = None

    # DNS STEAM MQTT
    port: int = 53

    # DNS
    dns_resolve_server: str = "1.1.1.1"
    dns_resolve_type: str = "A"

    # MQTT
    mqttUsername: Optional[str] = None
    mqttPassword: Optional[str] = None
    mqttTopic: Optional[str] = None
    mqttSuccessMessage: Optional[str] = None

    # SQLSERVER POSTGRES
    databaseConnectionString: Optional[str] = None
    databaseQuery: Optional[str] = None

    # DOCKER
    docker_container: str = ""
    docker_host: Optional[int] = None

    # RADIUS
    radiusUsername: Optional[str] = None
    radiusPassword: Optional[str] = None
    radiusSecret: Optional[str] = None
    radiusCalledStationId: Optional[str] = None
    radiusCallingStationId: Optional[str] = None

    class Config:  
        use_enum_values = True

class MonitorUpdate(Monitor):
    type: Optional[MonitorType]= None
    name: Optional[str] = None