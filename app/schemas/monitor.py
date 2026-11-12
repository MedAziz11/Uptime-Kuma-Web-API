from pydantic import BaseModel, ConfigDict
from uptime_kuma_api import AuthMethod, MonitorType


class MonitorUpdate(BaseModel):
    """Monitor settings.

    Only the fields sent in the request are forwarded to Uptime Kuma; the rest
    keep the Uptime Kuma defaults (on creation) or their current value (on
    update). Monitor options not listed here are accepted and passed through.
    """

    model_config = ConfigDict(extra="allow", use_enum_values=True)

    type: MonitorType | None = None
    name: str | None = None
    parent: int | None = None
    description: str | None = None
    interval: int | None = None
    retryInterval: int | None = None
    resendInterval: int | None = None
    maxretries: int | None = None
    upsideDown: bool | None = None
    notificationIDList: list[int] | None = None
    timeout: int | None = None

    # HTTP, KEYWORD, JSON_QUERY
    url: str | None = None
    expiryNotification: bool | None = None
    ignoreTls: bool | None = None
    maxredirects: int | None = None
    accepted_statuscodes: list[str] | None = None
    proxyId: int | None = None
    method: str | None = None
    httpBodyEncoding: str | None = None
    body: str | None = None
    headers: str | None = None
    authMethod: AuthMethod | None = None
    basic_auth_user: str | None = None
    basic_auth_pass: str | None = None
    authDomain: str | None = None
    authWorkstation: str | None = None

    # KEYWORD
    keyword: str | None = None
    invertKeyword: bool | None = None

    # JSON_QUERY
    jsonPath: str | None = None
    expectedValue: str | None = None

    # DNS, PING, STEAM, MQTT, PORT, RADIUS
    hostname: str | None = None
    port: int | None = None
    packetSize: int | None = None

    # DNS
    dns_resolve_server: str | None = None
    dns_resolve_type: str | None = None

    # MQTT
    mqttUsername: str | None = None
    mqttPassword: str | None = None
    mqttTopic: str | None = None
    mqttSuccessMessage: str | None = None

    # SQLSERVER, POSTGRES, MYSQL, MONGODB, REDIS
    databaseConnectionString: str | None = None
    databaseQuery: str | None = None

    # DOCKER
    docker_container: str | None = None
    docker_host: int | None = None

    # RADIUS
    radiusUsername: str | None = None
    radiusPassword: str | None = None
    radiusSecret: str | None = None
    radiusCalledStationId: str | None = None
    radiusCallingStationId: str | None = None

    # GAMEDIG
    game: str | None = None
    gamedigGivenPortOnly: bool | None = None


class Monitor(MonitorUpdate):
    type: MonitorType
    name: str


class MonitorTag(BaseModel):
    tag_id: int
    value: str = ""
