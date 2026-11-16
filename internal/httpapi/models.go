package httpapi

import "reflect"

// Request bodies. They document the API (OpenAPI) and are the models the
// bodies are checked against (modelOf): the fields 2.0 documented, with
// their types. Fields that are not documented pass through to Uptime Kuma,
// which is the authority on what a value means.

// MonitorUpdate is the body of PATCH /monitors/{monitor_id}. Only the fields
// sent are changed; monitor options not listed here are accepted and passed
// through. POST /monitors takes the same body with type and name required.
type MonitorUpdate struct {
	Type               *string  `json:"type,omitempty" enum:"group,http,port,ping,keyword,json-query,grpc-keyword,dns,docker,real-browser,push,steam,gamedig,mqtt,kafka-producer,sqlserver,postgres,mysql,mongodb,radius,redis,tailscale-ping,websocket-upgrade,snmp,ntp,smtp,sftp,rabbitmq,system-service,pm2,globalping,oracledb,manual,sip-options"`
	Name               *string  `json:"name,omitempty"`
	Parent             *int64   `json:"parent,omitempty"`
	Description        *string  `json:"description,omitempty"`
	Interval           *int64   `json:"interval,omitempty" doc:"Seconds, at least 20."`
	RetryInterval      *int64   `json:"retryInterval,omitempty" doc:"Seconds, at least 20."`
	ResendInterval     *int64   `json:"resendInterval,omitempty"`
	MaxRetries         *int64   `json:"maxretries,omitempty"`
	UpsideDown         *bool    `json:"upsideDown,omitempty"`
	NotificationIDList []int64  `json:"notificationIDList,omitempty"`
	Timeout            *float64 `json:"timeout,omitempty" doc:"Seconds."`

	URL                *string  `json:"url,omitempty" doc:"HTTP, keyword, JSON query."`
	ExpiryNotification *bool    `json:"expiryNotification,omitempty"`
	IgnoreTLS          *bool    `json:"ignoreTls,omitempty"`
	MaxRedirects       *int64   `json:"maxredirects,omitempty"`
	AcceptedStatuscode []string `json:"accepted_statuscodes,omitempty" doc:"For example [\"200-299\"]."`
	ProxyID            *int64   `json:"proxyId,omitempty"`
	Method             *string  `json:"method,omitempty"`
	HTTPBodyEncoding   *string  `json:"httpBodyEncoding,omitempty"`
	Body               *string  `json:"body,omitempty"`
	Headers            *string  `json:"headers,omitempty"`
	AuthMethod         *string  `json:"authMethod,omitempty" enum:",basic,ntlm,mtls,oauth2-cc,bearer"`
	BasicAuthUser      *string  `json:"basic_auth_user,omitempty"`
	BasicAuthPass      *string  `json:"basic_auth_pass,omitempty"`
	AuthDomain         *string  `json:"authDomain,omitempty"`
	AuthWorkstation    *string  `json:"authWorkstation,omitempty"`

	Keyword       *string `json:"keyword,omitempty"`
	InvertKeyword *bool   `json:"invertKeyword,omitempty"`
	JSONPath      *string `json:"jsonPath,omitempty"`
	ExpectedValue *string `json:"expectedValue,omitempty"`

	Hostname   *string `json:"hostname,omitempty" doc:"DNS, ping, port, steam, MQTT, RADIUS."`
	Port       *int64  `json:"port,omitempty"`
	PacketSize *int64  `json:"packetSize,omitempty"`

	DNSResolveServer *string `json:"dns_resolve_server,omitempty"`
	DNSResolveType   *string `json:"dns_resolve_type,omitempty"`

	MQTTUsername       *string `json:"mqttUsername,omitempty"`
	MQTTPassword       *string `json:"mqttPassword,omitempty"`
	MQTTTopic          *string `json:"mqttTopic,omitempty"`
	MQTTSuccessMessage *string `json:"mqttSuccessMessage,omitempty"`

	DatabaseConnectionString *string `json:"databaseConnectionString,omitempty"`
	DatabaseQuery            *string `json:"databaseQuery,omitempty"`

	DockerContainer *string `json:"docker_container,omitempty"`
	DockerHost      *int64  `json:"docker_host,omitempty"`

	RadiusUsername         *string `json:"radiusUsername,omitempty"`
	RadiusPassword         *string `json:"radiusPassword,omitempty"`
	RadiusSecret           *string `json:"radiusSecret,omitempty"`
	RadiusCalledStationID  *string `json:"radiusCalledStationId,omitempty"`
	RadiusCallingStationID *string `json:"radiusCallingStationId,omitempty"`

	Game                 *string `json:"game,omitempty"`
	GamedigGivenPortOnly *bool   `json:"gamedigGivenPortOnly,omitempty"`

	_ struct{} `json:"-" additionalProperties:"true"`
}

// MonitorTag is the body of POST and DELETE /monitors/{monitor_id}/tag.
type MonitorTag struct {
	TagID int64  `json:"tag_id" required:"true"`
	Value string `json:"value,omitempty" default:""`
}

// Tag is the body of POST /tags.
type Tag struct {
	Name  string `json:"name" required:"true"`
	Color string `json:"color" required:"true"`
}

// TagUpdate is the body of PATCH /tags/{tag_id}.
type TagUpdate struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}

// NotificationOptions is the body of the notification routes: the options
// of the notification type (Uptime Kuma's own names, e.g. webhookURL for
// "webhook") pass through to Uptime Kuma.
type NotificationOptions struct {
	Name          *string  `json:"name,omitempty" doc:"Required to add."`
	Type          *string  `json:"type,omitempty" doc:"Required to add, e.g. webhook, slack, telegram, smtp."`
	IsDefault     *bool    `json:"isDefault,omitempty"`
	ApplyExisting *bool    `json:"applyExisting,omitempty"`
	_             struct{} `json:"-" additionalProperties:"true"`
}

// ProxyOptions is the body of the proxy routes.
type ProxyOptions struct {
	Protocol      *string  `json:"protocol,omitempty" enum:"http,https,socks,socks5,socks5h,socks4" doc:"Required to add."`
	Host          *string  `json:"host,omitempty" doc:"Required to add."`
	Port          *int64   `json:"port,omitempty" doc:"Required to add."`
	Auth          *bool    `json:"auth,omitempty"`
	Username      *string  `json:"username,omitempty"`
	Password      *string  `json:"password,omitempty"`
	Active        *bool    `json:"active,omitempty"`
	Default       *bool    `json:"default,omitempty"`
	ApplyExisting *bool    `json:"applyExisting,omitempty"`
	_             struct{} `json:"-" additionalProperties:"true"`
}

// DockerHostOptions is the body of the docker host routes.
type DockerHostOptions struct {
	Name         *string  `json:"name,omitempty" doc:"Required to add."`
	DockerType   *string  `json:"dockerType,omitempty" enum:"socket,tcp" doc:"Required to add."`
	DockerDaemon *string  `json:"dockerDaemon,omitempty" doc:"Default: /var/run/docker.sock (socket), tcp://localhost:2375 (tcp)."`
	_            struct{} `json:"-" additionalProperties:"true"`
}

// MaintenanceUpdate is the body of PATCH /maintenance/{maintenance_id}.
// Fields left out keep their value (on creation: Uptime Kuma's defaults).
//
//   - dateRange: start and end, e.g. ["2024-01-01 00:00:00", "2024-01-02 00:00:00"]
//   - weekdays: days of the week (Sun = 0 ... Sat = 6), for recurring-weekday
//   - daysOfMonth: days 1-31 or "lastDay1"-"lastDay4", for recurring-day-of-month
//   - timeRange: daily window, e.g. [{"hours": 2, "minutes": 0}, {"hours": 3, "minutes": 0}]
//   - cron / durationMinutes: for the cron strategy
type MaintenanceUpdate struct {
	Title           *string          `json:"title,omitempty"`
	Strategy        *string          `json:"strategy,omitempty" enum:"manual,single,recurring-interval,recurring-weekday,recurring-day-of-month,cron"`
	Active          *bool            `json:"active,omitempty"`
	Description     *string          `json:"description,omitempty"`
	DateRange       []string         `json:"dateRange,omitempty"`
	IntervalDay     *int64           `json:"intervalDay,omitempty"`
	Weekdays        []int64          `json:"weekdays,omitempty"`
	DaysOfMonth     []any            `json:"daysOfMonth,omitempty"`
	TimeRange       []map[string]any `json:"timeRange,omitempty"`
	Cron            *string          `json:"cron,omitempty"`
	DurationMinutes *int64           `json:"durationMinutes,omitempty"`
	TimezoneOption  *string          `json:"timezoneOption,omitempty"`
	_               struct{}         `json:"-" additionalProperties:"true"`
}

// MonitorMaintenance is an item of the body of POST
// /maintenance/{maintenance_id}/monitors.
type MonitorMaintenance struct {
	ID   int64   `json:"id" required:"true"`
	Name *string `json:"name,omitempty"`
}

// AddStatusPageRequest is the body of POST /statuspages.
type AddStatusPageRequest struct {
	Slug  string `json:"slug" required:"true" minLength:"1"`
	Title string `json:"title" required:"true" minLength:"1"`
}

// SaveStatusPageRequest is the body of POST /statuspages/{slug}. Fields
// left out keep their current value.
type SaveStatusPageRequest struct {
	Title                 *string          `json:"title,omitempty"`
	Description           *string          `json:"description,omitempty"`
	Theme                 *string          `json:"theme,omitempty" doc:"auto (default), light or dark."`
	Published             *bool            `json:"published,omitempty"`
	ShowTags              *bool            `json:"showTags,omitempty"`
	DomainNameList        []string         `json:"domainNameList,omitempty"`
	GoogleAnalyticsID     *string          `json:"googleAnalyticsId,omitempty" doc:"Uptime Kuma 2.x: analyticsId with analyticsType google."`
	CustomCSS             *string          `json:"customCSS,omitempty"`
	FooterText            *string          `json:"footerText,omitempty"`
	ShowPoweredBy         *bool            `json:"showPoweredBy,omitempty"`
	ShowCertificateExpiry *bool            `json:"showCertificateExpiry,omitempty"`
	Icon                  *string          `json:"icon,omitempty"`
	PublicGroupList       []map[string]any `json:"publicGroupList,omitempty"`
	_                     struct{}         `json:"-" additionalProperties:"true"`
}

// PostIncidentRequest is the body of POST /statuspages/{slug}/incident.
type PostIncidentRequest struct {
	Title   string `json:"title" required:"true"`
	Content string `json:"content" required:"true"`
	Style   string `json:"style,omitempty" enum:"info,warning,danger,primary,light,dark" default:"primary"`
}

// APIKeyOptions is the body of POST /api_keys.
type APIKeyOptions struct {
	Name    *string  `json:"name,omitempty" doc:"Required."`
	Expires *string  `json:"expires,omitempty" doc:"Expiry date, e.g. \"2027-01-01 00:00:00\"; null (default) never expires."`
	Active  *bool    `json:"active,omitempty" doc:"Default true."`
	_       struct{} `json:"-" additionalProperties:"true"`
}

// RemoteBrowserOptions is the body of the remote browser routes.
type RemoteBrowserOptions struct {
	Name *string  `json:"name,omitempty" doc:"Required to add."`
	URL  *string  `json:"url,omitempty" doc:"Required to add and test: the Playwright server, e.g. ws://chrome:3000/playwright."`
	_    struct{} `json:"-" additionalProperties:"true"`
}

// IncidentUpdate is the body of PATCH /statuspages/{slug}/incidents/{incident_id}.
// Fields left out keep their value.
type IncidentUpdate struct {
	Title   *string  `json:"title,omitempty"`
	Content *string  `json:"content,omitempty"`
	Style   *string  `json:"style,omitempty" enum:"info,warning,danger,primary,light,dark"`
	Pin     *bool    `json:"pin,omitempty"`
	_       struct{} `json:"-" additionalProperties:"true"`
}

// The models of the bodies, built once from the types above.
var (
	monitorUpdateModel      = modelOf(reflect.TypeFor[MonitorUpdate]())
	monitorCreateModel      = monitorUpdateModel.required("type", "name")
	monitorTagModel         = modelOf(reflect.TypeFor[MonitorTag]())
	tagModel                = modelOf(reflect.TypeFor[Tag]())
	tagUpdateModel          = modelOf(reflect.TypeFor[TagUpdate]())
	notificationModel       = modelOf(reflect.TypeFor[NotificationOptions]())
	proxyModel              = modelOf(reflect.TypeFor[ProxyOptions]())
	dockerHostModel         = modelOf(reflect.TypeFor[DockerHostOptions]())
	maintenanceUpdateModel  = modelOf(reflect.TypeFor[MaintenanceUpdate]())
	maintenanceCreateModel  = maintenanceUpdateModel.required("title", "strategy")
	monitorMaintenanceModel = modelOf(reflect.TypeFor[MonitorMaintenance]())
	addStatusPageModel      = modelOf(reflect.TypeFor[AddStatusPageRequest]())
	saveStatusPageModel     = modelOf(reflect.TypeFor[SaveStatusPageRequest]())
	postIncidentModel       = modelOf(reflect.TypeFor[PostIncidentRequest]())
	apiKeyModel             = modelOf(reflect.TypeFor[APIKeyOptions]())
	remoteBrowserModel      = modelOf(reflect.TypeFor[RemoteBrowserOptions]())
	incidentUpdateModel     = modelOf(reflect.TypeFor[IncidentUpdate]())
)

// Response bodies, for the OpenAPI document. The objects are Uptime Kuma's,
// as 2.0 returned them: the main fields are documented, others are present.

// MonitorObject is a monitor.
type MonitorObject struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Type               string   `json:"type"`
	Active             bool     `json:"active"`
	URL                *string  `json:"url"`
	Hostname           *string  `json:"hostname"`
	Interval           int64    `json:"interval"`
	NotificationIDList []int64  `json:"notificationIDList"`
	Tags               []any    `json:"tags"`
	_                  struct{} `json:"-" additionalProperties:"true"`
}

// KumaObject is an object of Uptime Kuma (notification, proxy, docker host,
// maintenance, status page, incident, heartbeat).
type KumaObject struct {
	ID int64    `json:"id"`
	_  struct{} `json:"-" additionalProperties:"true"`
}

// KumaMessage is Uptime Kuma's answer to a command: a message and, for
// commands that create something, its id (monitorID, maintenanceID, id).
type KumaMessage struct {
	Msg string   `json:"msg,omitempty"`
	_   struct{} `json:"-" additionalProperties:"true"`
}

// Monitors is the body of GET /monitors.
type Monitors struct {
	Monitors []MonitorObject `json:"monitors"`
}

// MonitorResponse is the body of GET /monitors/{monitor_id}.
type MonitorResponse struct {
	Monitor MonitorObject `json:"monitor"`
}

// MonitorUpdated is the body of PATCH /monitors/{monitor_id}.
type MonitorUpdated struct {
	Msg         string         `json:"msg"`
	MonitorID   int64          `json:"monitorID"`
	MonitorData map[string]any `json:"monitor_data" doc:"The fields that were sent."`
}

// MonitorBeats is the body of GET /monitors/{monitor_id}/beats.
type MonitorBeats struct {
	MonitorBeats []KumaObject `json:"monitor_beats"`
}

// TagObject is a tag.
type TagObject struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Tags is the body of GET /tags.
type Tags struct {
	Tags []TagObject `json:"tags"`
}

// TagResponse is the body of GET /tags/{tag_id}.
type TagResponse struct {
	Tag TagObject `json:"tag"`
}

// Notifications is the body of GET /notifications.
type Notifications struct {
	Notifications []KumaObject `json:"notifications"`
}

// NotificationResponse is the body of GET /notifications/{notification_id}.
type NotificationResponse struct {
	Notification KumaObject `json:"notification"`
}

// Proxies is the body of GET /proxies.
type Proxies struct {
	Proxies []KumaObject `json:"proxies"`
}

// ProxyResponse is the body of GET /proxies/{proxy_id}.
type ProxyResponse struct {
	Proxy KumaObject `json:"proxy"`
}

// DockerHosts is the body of GET /docker_hosts.
type DockerHosts struct {
	DockerHosts []KumaObject `json:"docker_hosts"`
}

// DockerHostResponse is the body of GET /docker_hosts/{docker_host_id}.
type DockerHostResponse struct {
	DockerHost KumaObject `json:"docker_host"`
}

// Maintenances is the body of GET /maintenance.
type Maintenances struct {
	Maintenances []KumaObject `json:"maintenances"`
}

// MaintenanceResponse is the body of GET /maintenance/{maintenance_id}.
type MaintenanceResponse struct {
	Maintenance KumaObject `json:"maintenance"`
}

// MaintenanceUpdated is the body of PATCH /maintenance/{maintenance_id}.
type MaintenanceUpdated struct {
	Msg             string         `json:"msg"`
	MaintenanceID   int64          `json:"maintenanceID"`
	MaintenanceData map[string]any `json:"maintenance_data" doc:"The fields that were sent."`
}

// StatusPageList is the body of GET /statuspages.
type StatusPageList struct {
	StatusPages []KumaObject `json:"statuspages"`
}

// StatusPage is the body of GET /statuspages/{slug}: the configuration with
// the pinned incident, the groups of monitors and the maintenances.
type StatusPage struct {
	ID              int64        `json:"id"`
	Slug            string       `json:"slug"`
	Title           string       `json:"title"`
	Incident        *KumaObject  `json:"incident"`
	PublicGroupList []KumaObject `json:"publicGroupList"`
	MaintenanceList []KumaObject `json:"maintenanceList"`
	_               struct{}     `json:"-" additionalProperties:"true"`
}

// AddStatusPageResponse is the body of POST /statuspages.
type AddStatusPageResponse struct {
	Msg *string `json:"msg"`
}

// Detail is the body of the status page writes.
type Detail struct {
	Detail any `json:"detail"`
}

// DatabaseSize is the body of GET /database/size.
type DatabaseSize struct {
	Size int64  `json:"size"`
	Unit string `json:"unit" example:"octet"`
}

// DatabaseShrink is the body of POST /database/shrink.
type DatabaseShrink struct {
	Message string         `json:"message" example:"DB Shrinked"`
	Details map[string]any `json:"details"`
}

// ByMonitor maps monitor ids to a value (GET /ping, /uptime, /cert_info).
type ByMonitor map[string]any

// ServerInfo is the body of GET /info.
type ServerInfo struct {
	Version string   `json:"version"`
	_       struct{} `json:"-" additionalProperties:"true"`
}

// MonitorMaintenanceList is the body of GET
// /maintenance/{maintenance_id}/monitors.
type MonitorMaintenanceList []KumaObject

// Gone is the body of POST /settings/upload_backup.
type Gone = HTTPError

// APIKeyObject is an API key, without the key itself.
type APIKeyObject struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Active      bool     `json:"active"`
	Expires     *string  `json:"expires"`
	Status      string   `json:"status" enum:"active,inactive,expired"`
	CreatedDate string   `json:"createdDate"`
	_           struct{} `json:"-" additionalProperties:"true"`
}

// APIKeys is the body of GET /api_keys.
type APIKeys struct {
	APIKeys []APIKeyObject `json:"api_keys"`
}

// APIKeyResponse is the body of GET /api_keys/{api_key_id}.
type APIKeyResponse struct {
	APIKey APIKeyObject `json:"api_key"`
}

// APIKeyCreated is the body of POST /api_keys.
type APIKeyCreated struct {
	Msg   string `json:"msg"`
	Key   string `json:"key" doc:"The clear key, to send as the password of HTTP basic authentication to Uptime Kuma. It is shown only here."`
	KeyID int64  `json:"keyID"`
}

// RemoteBrowsers is the body of GET /remote_browsers.
type RemoteBrowsers struct {
	RemoteBrowsers []KumaObject `json:"remote_browsers"`
}

// RemoteBrowserResponse is the body of GET /remote_browsers/{remote_browser_id}.
type RemoteBrowserResponse struct {
	RemoteBrowser KumaObject `json:"remote_browser"`
}

// IncidentHistory is the body of GET /statuspages/{slug}/incidents: one page
// of incidents, newest first.
type IncidentHistory struct {
	Incidents  []KumaObject `json:"incidents"`
	Total      int64        `json:"total" doc:"Incidents of the page in all."`
	NextCursor *string      `json:"nextCursor" doc:"The cursor of the next page, or null."`
	HasMore    bool         `json:"hasMore"`
}

// IncidentSaved is the body of an incident edit or resolution.
type IncidentSaved struct {
	Msg      string     `json:"msg"`
	Incident KumaObject `json:"incident"`
}

// ChartData is the body of GET /monitors/{monitor_id}/chart.
type ChartData struct {
	ChartData []ChartPoint `json:"chart_data"`
}

// ChartPoint is the statistics of one minute, hour or day.
type ChartPoint struct {
	Up        int64    `json:"up"`
	Down      int64    `json:"down"`
	AvgPing   *float64 `json:"avgPing"`
	MinPing   *float64 `json:"minPing"`
	MaxPing   *float64 `json:"maxPing"`
	Timestamp int64    `json:"timestamp" doc:"Start of the period, Unix seconds."`
	_         struct{} `json:"-" additionalProperties:"true"`
}

// ImportantBeats is the body of GET /monitors/{monitor_id}/important_beats:
// the heartbeats that changed the monitor's status, newest first.
type ImportantBeats struct {
	ImportantBeats []map[string]any `json:"important_beats"`
	Total          int64            `json:"total" doc:"Important heartbeats of the monitor in all."`
}
