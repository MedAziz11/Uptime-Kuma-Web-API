package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

// Request and response bodies. Their names are the component names of
// 2.0's OpenAPI document.

// Health is the body of GET /health.
type Health struct {
	Status  string `json:"status" example:"ok"`
	Version string `json:"version"`
}

// Token is the body of POST /login/access-token.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type" default:"bearer"`
}

// UserCreate is the body of POST /users (documentation only: the body is
// validated by parseUserCreate, with 2.0's messages).
type UserCreate struct {
	Username string   `json:"username" minLength:"3" maxLength:"20" pattern:"^[A-Za-z0-9_.-]+$"`
	Password string   `json:"password" minLength:"8" format:"password" doc:"At most 72 bytes (bcrypt's limit)."`
	_        struct{} `json:"-" additionalProperties:"true"`
}

// loginBody documents the login form (OAuth2 password grant).
type loginBody struct {
	GrantType    string   `json:"grant_type,omitempty" pattern:"^password$"`
	Username     string   `json:"username"`
	Password     string   `json:"password" format:"password"`
	Scope        string   `json:"scope,omitempty" default:""`
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty" format:"password"`
	_            struct{} `json:"-" additionalProperties:"true"`
}

// UserResponse is a user as the API shows it.
type UserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt Timestamp `json:"created_at"`
	LastVisit Timestamp `json:"last_visit"`
}

// Message is the body of DELETE /users/{username}.
type Message struct {
	Message string `json:"message"`
}

// HTTPError is the body of an error answer other than 422.
type HTTPError struct {
	Detail string `json:"detail"`
}

// HTTPValidationError is the body of a 422 answer.
type HTTPValidationError struct {
	Detail []ValidationError `json:"detail"`
}

// ValidationError is one problem in a 422 answer.
type ValidationError struct {
	Type  string         `json:"type"`
	Loc   []any          `json:"loc"`
	Msg   string         `json:"msg"`
	Input any            `json:"input,omitempty"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}

// Timestamp is a UTC time written as 2.0 writes it:
// "2026-11-13T15:43:41.808921Z", without the fraction when it is zero.
type Timestamp time.Time

// MarshalJSON implements json.Marshaler.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	u := time.Time(t).UTC()
	s := u.Format("2006-01-02T15:04:05")
	if us := u.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return []byte(`"` + s + `Z"`), nil
}

// Schema implements huma.SchemaProvider.
func (Timestamp) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Format: "date-time"}
}

func userResponse(u store.User) UserResponse {
	return UserResponse{ID: u.ID, Username: u.Username, CreatedAt: Timestamp(u.CreatedAt), LastVisit: Timestamp(u.LastVisit)}
}

type (
	healthOutput  struct{ Body Health }
	tokenOutput   struct{ Body Token }
	userOutput    struct{ Body UserResponse }
	usersOutput   struct{ Body []UserResponse }
	messageOutput struct{ Body Message }
	usernameParam struct {
		Username string `path:"username"`
	}
	rawBodyInput struct {
		ContentType string `header:"Content-Type" hidden:"true"`
		RawBody     []byte
	}
)

func (s *Server) routes(maxBody int64) {
	secured := []map[string][]string{{securityScheme: {}}}
	op := func(method, path, id, summary, tag string, auth bool) huma.Operation {
		o := huma.Operation{
			Method: method, Path: path, OperationID: id, Summary: summary, Tags: []string{tag},
			MaxBodyBytes: maxBody,
		}
		if auth {
			o.Security = secured
		}
		return o
	}

	// GET / redirects to the docs page (not documented, as in 2.0).
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/docs")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	s.mux.HandleFunc("GET /redoc", s.redoc)
	s.mux.HandleFunc("/", s.notFound)

	huma.Register(s.api, op(http.MethodGet, "/health", "health_health_get", "Liveness probe", "Health", false),
		func(context.Context, *struct{}) (*healthOutput, error) {
			return &healthOutput{Body: Health{Status: "ok", Version: s.version}}, nil
		})

	huma.Register(s.api, op(http.MethodPost, "/login/access-token", "login_access_token_login_access_token_post", "Get an access token", "Authentication", false),
		s.login)
	s.documentBody("/login/access-token", http.MethodPost, "application/x-www-form-urlencoded",
		"Body_login_access_token_login_access_token_post", reflect.TypeFor[loginBody]())

	create := op(http.MethodPost, "/users", "create_user_users_post", "Create a user (admin only)", "Users", true)
	create.DefaultStatus = http.StatusCreated
	huma.Register(s.api, create, s.createUser)
	s.documentBody("/users", http.MethodPost, "application/json", "UserCreate", reflect.TypeFor[UserCreate]())

	huma.Register(s.api, op(http.MethodGet, "/users", "get_users_users_get", "List users (admin only)", "Users", true), s.listUsers)
	huma.Register(s.api, op(http.MethodGet, "/users/me", "get_me_users_me_get", "Get the current user", "Users", true), s.me)
	huma.Register(s.api, op(http.MethodGet, "/users/{username}", "get_user_users__username__get", "Get a user (admin only)", "Users", true), s.getUser)
	huma.Register(s.api, op(http.MethodDelete, "/users/{username}", "delete_user_users__username__delete", "Delete a user (admin only)", "Users", true), s.deleteUser)

	s.kumaRoutes(op)
	s.documentValidationErrors()
}

// operationID is FastAPI's generated operation id: the handler's name, the
// path and the method, non-word characters replaced by "_".
func operationID(name, path, method string) string {
	id := []byte(name + path)
	for i, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			id[i] = '_'
		}
	}
	return string(id) + "_" + strings.ToLower(method)
}

// kumaRoutes registers the routes that talk to Uptime Kuma: 2.0's, with
// its paths, operation ids, summaries and tags.
func (s *Server) kumaRoutes(op func(method, path, id, summary, tag string, auth bool) huma.Operation) {
	reg := func(method, path, name, summary, tag string) huma.Operation {
		return op(method, path, operationID(name, path, method), summary, tag, true)
	}
	body := func(method, path, name string, t reflect.Type) {
		s.documentBody(path, method, "application/json", name, t)
	}
	const get, post, patch, del = http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete

	// Monitors.
	cachedRoute[struct{}, jsonOutput[Monitors]](s, reg(get, "/monitors", "get_monitors", "Get all monitors", "Monitor"), s.serveList(s.cache.monitors))
	huma.Register(s.api, reg(post, "/monitors", "create_monitor", "Create a monitor", "Monitor"), s.createMonitor)
	s.documentRequiredBody("/monitors", post, "Monitor", reflect.TypeFor[MonitorUpdate](), "type", "name")
	cachedRoute[monitorIDParam, jsonOutput[MonitorResponse]](s, reg(get, "/monitors/{monitor_id}", "get_monitor", "Get a monitor", "Monitor"), s.serveObject(s.cache.monitors, "monitor_id", "Not found"))
	huma.Register(s.api, reg(patch, "/monitors/{monitor_id}", "update_monitor", "Update a monitor", "Monitor"), s.updateMonitor)
	body(patch, "/monitors/{monitor_id}", "MonitorUpdate", reflect.TypeFor[MonitorUpdate]())
	huma.Register(s.api, reg(del, "/monitors/{monitor_id}", "delete_monitor", "Delete a monitor", "Monitor"), s.deleteMonitor)
	huma.Register(s.api, reg(post, "/monitors/{monitor_id}/pause", "pause_monitor", "Pause a monitor", "Monitor"), s.pauseMonitor)
	huma.Register(s.api, reg(post, "/monitors/{monitor_id}/resume", "resume_monitor", "Resume a monitor", "Monitor"), s.resumeMonitor)
	huma.Register(s.api, reg(get, "/monitors/{monitor_id}/beats", "monitor_beats", "Get the heartbeats of a monitor for the last N hours", "Monitor"), s.monitorBeats)
	huma.Register(s.api, reg(get, "/monitors/{monitor_id}/chart", "monitor_chart_data", "Get the chart data of a monitor for the last N hours", "Monitor"), s.monitorChartData)
	huma.Register(s.api, reg(get, "/monitors/{monitor_id}/important_beats", "monitor_important_beats", "Get the important heartbeats of a monitor, newest first", "Monitor"), s.importantBeats)
	huma.Register(s.api, reg(post, "/monitors/{monitor_id}/tag", "add_monitor_tag", "Add an existing tag to a monitor", "Monitor"), s.addMonitorTag)
	body(post, "/monitors/{monitor_id}/tag", "MonitorTag", reflect.TypeFor[MonitorTag]())
	huma.Register(s.api, reg(del, "/monitors/{monitor_id}/tag", "delete_monitor_tag", "Remove a tag from a monitor", "Monitor"), s.deleteMonitorTag)
	body(del, "/monitors/{monitor_id}/tag", "MonitorTag", reflect.TypeFor[MonitorTag]())

	// Status pages.
	cachedRoute[struct{}, jsonOutput[StatusPageList]](s, reg(get, "/statuspages", "get_all_status_pages", "Get all status pages", "Status Page"), s.serveList(s.cache.statusPages))
	huma.Register(s.api, reg(post, "/statuspages", "add_status_page", "Add a status page", "Status Page"), s.createStatusPage)
	body(post, "/statuspages", "AddStatusPageRequest", reflect.TypeFor[AddStatusPageRequest]())
	huma.Register(s.api, reg(get, "/statuspages/{slug}", "get_status_page", "Get a status page", "Status Page"), s.getStatusPage)
	huma.Register(s.api, reg(post, "/statuspages/{slug}", "save_status_page", "Save a status page", "Status Page"), s.saveStatusPage)
	body(post, "/statuspages/{slug}", "SaveStatusPageRequest", reflect.TypeFor[SaveStatusPageRequest]())
	huma.Register(s.api, reg(del, "/statuspages/{slug}", "delete_status_page", "Delete a status page", "Status Page"), s.deleteStatusPage)
	huma.Register(s.api, reg(post, "/statuspages/{slug}/incident", "post_incident", "Post an incident", "Status Page"), s.postIncident)
	body(post, "/statuspages/{slug}/incident", "PostIncidentRequest", reflect.TypeFor[PostIncidentRequest]())
	huma.Register(s.api, reg(del, "/statuspages/{slug}/incident/unpin", "unpin_incident", "Unpin the incident", "Status Page"), s.unpinIncident)
	huma.Register(s.api, reg(get, "/statuspages/{slug}/incidents", "get_incident_history", "Get the incidents of a status page, newest first", "Status Page"), s.incidentHistory)
	huma.Register(s.api, reg(patch, "/statuspages/{slug}/incidents/{incident_id}", "update_incident", "Update an incident", "Status Page"), s.updateIncident)
	body(patch, "/statuspages/{slug}/incidents/{incident_id}", "IncidentUpdate", reflect.TypeFor[IncidentUpdate]())
	huma.Register(s.api, reg(del, "/statuspages/{slug}/incidents/{incident_id}", "delete_incident", "Delete an incident", "Status Page"), s.deleteIncident)
	huma.Register(s.api, reg(post, "/statuspages/{slug}/incidents/{incident_id}/resolve", "resolve_incident", "Resolve an incident", "Status Page"), s.resolveIncident)

	// Maintenances.
	cachedRoute[struct{}, jsonOutput[Maintenances]](s, reg(get, "/maintenance", "get_maintenances", "Get all maintenances", "Maintenance"), s.serveList(s.cache.maintenances))
	huma.Register(s.api, reg(post, "/maintenance", "create_maintenance", "Create a maintenance", "Maintenance"), s.createMaintenance)
	s.documentRequiredBody("/maintenance", post, "Maintenance", reflect.TypeFor[MaintenanceUpdate](), "title", "strategy")
	huma.Register(s.api, reg(get, "/maintenance/{maintenance_id}", "get_maintenance", "Get a maintenance", "Maintenance"), s.getMaintenance)
	huma.Register(s.api, reg(patch, "/maintenance/{maintenance_id}", "update_maintenance", "Update a maintenance", "Maintenance"), s.updateMaintenance)
	body(patch, "/maintenance/{maintenance_id}", "MaintenanceUpdate", reflect.TypeFor[MaintenanceUpdate]())
	huma.Register(s.api, reg(del, "/maintenance/{maintenance_id}", "delete_maintenance", "Delete a maintenance", "Maintenance"), s.deleteMaintenance)
	huma.Register(s.api, reg(post, "/maintenance/{maintenance_id}/pause", "pause_maintenance", "Pause a maintenance", "Maintenance"), s.pauseMaintenance)
	huma.Register(s.api, reg(post, "/maintenance/{maintenance_id}/resume", "resume_maintenance", "Resume a maintenance", "Maintenance"), s.resumeMaintenance)
	huma.Register(s.api, reg(get, "/maintenance/{maintenance_id}/monitors", "get_monitor_maintenance", "Get the monitors of a maintenance", "Maintenance"), s.maintenanceMonitors)
	huma.Register(s.api, reg(post, "/maintenance/{maintenance_id}/monitors", "add_monitor_maintenance", "Set the monitors of a maintenance", "Maintenance"), s.setMaintenanceMonitors)
	body(post, "/maintenance/{maintenance_id}/monitors", "MonitorMaintenanceList", reflect.TypeFor[[]MonitorMaintenance]())

	// Tags.
	huma.Register(s.api, reg(get, "/tags", "get_tags", "Get all tags", "Tags"), s.listTags)
	huma.Register(s.api, reg(post, "/tags", "add_tag", "Add a tag", "Tags"), s.createTag)
	body(post, "/tags", "Tag", reflect.TypeFor[Tag]())
	huma.Register(s.api, reg(get, "/tags/{tag_id}", "get_tag", "Get a tag", "Tags"), s.getTag)
	huma.Register(s.api, reg(patch, "/tags/{tag_id}", "update_tag", "Update a tag", "Tags"), s.updateTag)
	body(patch, "/tags/{tag_id}", "TagUpdate", reflect.TypeFor[TagUpdate]())
	huma.Register(s.api, reg(del, "/tags/{tag_id}", "delete_tag", "Delete a tag", "Tags"), s.deleteTag)

	// Notifications.
	cachedRoute[struct{}, jsonOutput[Notifications]](s, reg(get, "/notifications", "get_notifications", "Get all notifications", "Notifications"), s.serveList(s.cache.notifications))
	huma.Register(s.api, reg(post, "/notifications", "add_notification", "Add a notification", "Notifications"), s.createNotification)
	body(post, "/notifications", "NotificationOptions", reflect.TypeFor[NotificationOptions]())
	huma.Register(s.api, reg(post, "/notifications/test", "test_notification", "Test a notification", "Notifications"), s.testNotification)
	body(post, "/notifications/test", "NotificationOptions", reflect.TypeFor[NotificationOptions]())
	cachedRoute[notificationIDParam, jsonOutput[NotificationResponse]](s, reg(get, "/notifications/{notification_id}", "get_notification", "Get a notification", "Notifications"), s.serveObject(s.cache.notifications, "notification_id", "notification does not exist"))
	huma.Register(s.api, reg(patch, "/notifications/{notification_id}", "update_notification", "Update a notification", "Notifications"), s.updateNotification)
	body(patch, "/notifications/{notification_id}", "NotificationOptions", reflect.TypeFor[NotificationOptions]())
	huma.Register(s.api, reg(del, "/notifications/{notification_id}", "delete_notification", "Delete a notification", "Notifications"), s.deleteNotification)

	// Proxies.
	cachedRoute[struct{}, jsonOutput[Proxies]](s, reg(get, "/proxies", "get_proxies", "Get all proxies", "Proxies"), s.serveList(s.cache.proxies))
	huma.Register(s.api, reg(post, "/proxies", "add_proxy", "Add a proxy", "Proxies"), s.createProxy)
	body(post, "/proxies", "ProxyOptions", reflect.TypeFor[ProxyOptions]())
	cachedRoute[proxyIDParam, jsonOutput[ProxyResponse]](s, reg(get, "/proxies/{proxy_id}", "get_proxy", "Get a proxy", "Proxies"), s.serveObject(s.cache.proxies, "proxy_id", "proxy does not exist"))
	huma.Register(s.api, reg(patch, "/proxies/{proxy_id}", "update_proxy", "Update a proxy", "Proxies"), s.updateProxy)
	body(patch, "/proxies/{proxy_id}", "ProxyOptions", reflect.TypeFor[ProxyOptions]())
	huma.Register(s.api, reg(del, "/proxies/{proxy_id}", "delete_proxy", "Delete a proxy", "Proxies"), s.deleteProxy)

	// Docker hosts.
	cachedRoute[struct{}, jsonOutput[DockerHosts]](s, reg(get, "/docker_hosts", "get_docker_hosts", "Get all docker hosts", "Docker Hosts"), s.serveList(s.cache.dockerHosts))
	huma.Register(s.api, reg(post, "/docker_hosts", "add_docker_host", "Add a docker host", "Docker Hosts"), s.createDockerHost)
	body(post, "/docker_hosts", "DockerHostOptions", reflect.TypeFor[DockerHostOptions]())
	huma.Register(s.api, reg(post, "/docker_hosts/test", "test_docker_host", "Test a docker host", "Docker Hosts"), s.testDockerHost)
	body(post, "/docker_hosts/test", "DockerHostOptions", reflect.TypeFor[DockerHostOptions]())
	cachedRoute[dockerHostIDParam, jsonOutput[DockerHostResponse]](s, reg(get, "/docker_hosts/{docker_host_id}", "get_docker_host", "Get a docker host", "Docker Hosts"), s.serveObject(s.cache.dockerHosts, "docker_host_id", "docker host does not exist"))
	huma.Register(s.api, reg(patch, "/docker_hosts/{docker_host_id}", "update_docker_host", "Update a docker host", "Docker Hosts"), s.updateDockerHost)
	body(patch, "/docker_hosts/{docker_host_id}", "DockerHostOptions", reflect.TypeFor[DockerHostOptions]())
	huma.Register(s.api, reg(del, "/docker_hosts/{docker_host_id}", "delete_docker_host", "Delete a docker host", "Docker Hosts"), s.deleteDockerHost)

	// API keys (Uptime Kuma 2.x).
	cachedRoute[struct{}, jsonOutput[APIKeys]](s, reg(get, "/api_keys", "get_api_keys", "Get all API keys", "API Keys"), s.serveList(s.cache.apiKeys))
	huma.Register(s.api, reg(post, "/api_keys", "add_api_key", "Add an API key", "API Keys"), s.createAPIKey)
	s.documentRequiredBody("/api_keys", post, "APIKeyOptions", reflect.TypeFor[APIKeyOptions](), "name")
	cachedRoute[apiKeyIDParam, jsonOutput[APIKeyResponse]](s, reg(get, "/api_keys/{api_key_id}", "get_api_key", "Get an API key", "API Keys"), s.serveObject(s.cache.apiKeys, "api_key_id", apiKeyNotFound))
	huma.Register(s.api, reg(post, "/api_keys/{api_key_id}/enable", "enable_api_key", "Enable an API key", "API Keys"), s.enableAPIKey)
	huma.Register(s.api, reg(post, "/api_keys/{api_key_id}/disable", "disable_api_key", "Disable an API key", "API Keys"), s.disableAPIKey)
	huma.Register(s.api, reg(del, "/api_keys/{api_key_id}", "delete_api_key", "Delete an API key", "API Keys"), s.deleteAPIKey)

	// Remote browsers (Uptime Kuma 2.x).
	cachedRoute[struct{}, jsonOutput[RemoteBrowsers]](s, reg(get, "/remote_browsers", "get_remote_browsers", "Get all remote browsers", "Remote Browsers"), s.serveList(s.cache.remoteBrowsers))
	huma.Register(s.api, reg(post, "/remote_browsers", "add_remote_browser", "Add a remote browser", "Remote Browsers"), s.createRemoteBrowser)
	body(post, "/remote_browsers", "RemoteBrowserOptions", reflect.TypeFor[RemoteBrowserOptions]())
	huma.Register(s.api, reg(post, "/remote_browsers/test", "test_remote_browser", "Test a remote browser", "Remote Browsers"), s.testRemoteBrowser)
	body(post, "/remote_browsers/test", "RemoteBrowserOptions", reflect.TypeFor[RemoteBrowserOptions]())
	cachedRoute[remoteBrowserIDParam, jsonOutput[RemoteBrowserResponse]](s, reg(get, "/remote_browsers/{remote_browser_id}", "get_remote_browser", "Get a remote browser", "Remote Browsers"), s.serveObject(s.cache.remoteBrowsers, "remote_browser_id", remoteBrowserNotFound))
	huma.Register(s.api, reg(patch, "/remote_browsers/{remote_browser_id}", "update_remote_browser", "Update a remote browser", "Remote Browsers"), s.updateRemoteBrowser)
	body(patch, "/remote_browsers/{remote_browser_id}", "RemoteBrowserOptions", reflect.TypeFor[RemoteBrowserOptions]())
	huma.Register(s.api, reg(del, "/remote_browsers/{remote_browser_id}", "delete_remote_browser", "Delete a remote browser", "Remote Browsers"), s.deleteRemoteBrowser)

	huma.Register(s.api, reg(get, "/push_examples/{language}", "get_push_example", "Get example code for a push monitor", "Monitor"), s.pushExample)

	// Settings, database, server and statistics.
	backup := reg(post, "/settings/upload_backup", "upload_backup", "Import a backup", "Settings")
	backup.Deprecated = true
	backup.Description = "Removed: Uptime Kuma 2.x cannot import backups. Always answers 410 Gone."
	huma.Register(s.api, backup, s.uploadBackup)
	o := operations(s.api.OpenAPI().Paths["/settings/upload_backup"])[post]
	clear(o.Responses)
	o.Responses["410"] = &huma.Response{Description: "Gone", Content: map[string]*huma.MediaType{
		"application/json": {Schema: s.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[HTTPError](), true, "HTTPError")},
	}}
	huma.Register(s.api, reg(get, "/database/size", "get_db_size", "Get the Uptime Kuma database size", "DataBase"), s.databaseSize)
	huma.Register(s.api, reg(post, "/database/shrink", "shrink_db", "Shrink the Uptime Kuma database", "DataBase"), s.shrinkDatabase)
	cachedRoute[struct{}, jsonOutput[ByMonitor]](s, reg(get, "/cert_info", "get_cert_info", "Get certificate info", "Certification Info"), s.serveList(s.cache.certInfo))
	cachedRoute[struct{}, jsonOutput[ServerInfo]](s, reg(get, "/info", "get_info", "Get server info", "Informations"), s.serveList(s.cache.info))
	cachedRoute[struct{}, jsonOutput[ByMonitor]](s, reg(get, "/ping", "get_avg_ping", "Get the average ping of each monitor", "Ping Average"), s.serveList(s.cache.avgPing))
	cachedRoute[struct{}, jsonOutput[ByMonitor]](s, reg(get, "/uptime", "get_uptime", "Get the uptime of each monitor", "Uptime"), s.serveList(s.cache.uptime))
}

// cachedRoute registers a read served from the response cache by a plain
// handler h, and documents it as huma would with input I and output O.
// huma's per-request work (an input and an output struct, its context,
// content negotiation, re-validating the encoded body) costs about 0.8 µs
// and 6 allocations per request (benchmarks in docs/performance.md), where
// a cached read needs neither.
func cachedRoute[I, O any](s *Server, op huma.Operation, h http.HandlerFunc) {
	huma.Register(s.docs, op, func(context.Context, *I) (*O, error) {
		return nil, errors.New("documentation only")
	})
	s.mux.Handle(op.Method+" "+op.Path, h)
}

// docsOnly is the huma adapter of Server.docs: it serves nothing.
type docsOnly struct{}

func (docsOnly) Handle(*huma.Operation, func(huma.Context)) {}

func (docsOnly) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

// redocPage is FastAPI's ReDoc page (get_redoc_html), which 2.0 serves at
// /redoc: it loads ReDoc from the same CDN and reads /openapi.json.
const redocPage = `<!DOCTYPE html>
<html>
<head>
<title>Uptime-Kuma-API - ReDoc</title>
<!-- needed for adaptive design -->
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">
<link rel="shortcut icon" href="https://fastapi.tiangolo.com/img/favicon.png">
<!--
ReDoc doesn't change outer page styles
-->
<style>
  body {
    margin: 0;
    padding: 0;
  }
</style>
</head>
<body>
<noscript>
    ReDoc requires Javascript to function. Please enable it to browse the documentation.
</noscript>
<redoc spec-url="/openapi.json"></redoc>
<script src="https://cdn.jsdelivr.net/npm/redoc@2/bundles/redoc.standalone.js"> </script>
</body>
</html>
`

func (s *Server) redoc(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, redocPage)
}

// documentBody replaces the "binary" request body huma documents for a raw
// body with the schema of t, registered under name.
func (s *Server) documentBody(path, method, contentType, name string, t reflect.Type) {
	registry := s.api.OpenAPI().Components.Schemas
	schema := registry.Schema(t, false, name)
	registry.Map()[name] = schema
	o := operations(s.api.OpenAPI().Paths[path])[method]
	o.RequestBody = &huma.RequestBody{Content: map[string]*huma.MediaType{
		contentType: {Schema: &huma.Schema{Ref: "#/components/schemas/" + name}},
	}}
}

// documentRequiredBody documents a body with the schema of t in which the
// named fields are required, registered under name.
func (s *Server) documentRequiredBody(path, method, name string, t reflect.Type, required ...string) {
	registry := s.api.OpenAPI().Components.Schemas
	base := registry.Schema(t, false, name)
	schema := *base
	schema.Required = append(slices.Clone(base.Required), required...)
	registry.Map()[name] = &schema
	o := operations(s.api.OpenAPI().Paths[path])[method]
	o.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		"application/json": {Schema: &huma.Schema{Ref: "#/components/schemas/" + name}},
	}}
}

// documentValidationErrors documents the 422 answer of every operation
// that reads a body or a path parameter, as 2.0's document does, and the
// {"detail": "..."} body of the other errors in place of huma's default
// error model, which this API never sends.
func (s *Server) documentValidationErrors() {
	registry := s.api.OpenAPI().Components.Schemas
	validation := registry.Schema(reflect.TypeFor[HTTPValidationError](), true, "HTTPValidationError")
	httpError := registry.Schema(reflect.TypeFor[HTTPError](), true, "HTTPError")
	for _, item := range s.api.OpenAPI().Paths {
		for _, o := range operations(item) {
			o.Responses["default"] = &huma.Response{
				Description: "Error",
				Content:     map[string]*huma.MediaType{"application/json": {Schema: httpError}},
			}
			if o.RequestBody == nil && len(o.Parameters) == 0 {
				continue
			}
			o.Responses["422"] = &huma.Response{
				Description: "Validation Error",
				Content:     map[string]*huma.MediaType{"application/json": {Schema: validation}},
			}
		}
	}
	delete(registry.Map(), "ErrorModel")
	delete(registry.Map(), "ErrorDetail")
}

// operations returns the operations of a path by method.
func operations(item *huma.PathItem) map[string]*huma.Operation {
	ops := map[string]*huma.Operation{}
	for m, o := range map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
	} {
		if o != nil {
			ops[m] = o
		}
	}
	return ops
}

func currentUser(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

func requireAdmin(ctx context.Context) error {
	u, ok := currentUser(ctx)
	if !ok {
		return errNotAuthenticated() // unreachable behind withAuthentication
	}
	if u.Username != AdminUsername {
		return errAdminRequired()
	}
	return nil
}

func (s *Server) internal(ctx context.Context, what string, err error) error {
	s.log.ErrorContext(ctx, what, "request_id", infoFrom(ctx).id, "error", err)
	return errInternal()
}

func (s *Server) login(ctx context.Context, in *rawBodyInput) (*tokenOutput, error) {
	if err := s.waitAdmin(ctx); err != nil {
		return nil, err
	}
	form, aerr := parseLoginForm(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	var hash string
	user, err := s.users.User(ctx, form.Username)
	switch {
	case err == nil:
		hash = user.PasswordHash
	case !errors.Is(err, store.ErrNotFound):
		return nil, s.internal(ctx, "look up user", err)
	}
	// An unknown user is checked against a dummy hash, so that the answer
	// takes as long as for a wrong password.
	ok, err := s.verifyPassword(ctx, form.Password, hash)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.log.InfoContext(ctx, "failed login", "request_id", infoFrom(ctx).id, "username", form.Username)
		return nil, errBadCredentials()
	}
	if err := s.users.TouchLastVisit(ctx, user.Username); err != nil {
		if errors.Is(err, store.ErrNotFound) { // deleted meanwhile
			return nil, errBadCredentials()
		}
		return nil, s.internal(ctx, "update last visit", err)
	}
	return &tokenOutput{Body: Token{AccessToken: s.tokens.Issue(user.Username), TokenType: "bearer"}}, nil
}

// waitAdmin waits until the admin user is set up (Options.AdminReady),
// at most passwordWait.
func (s *Server) waitAdmin(ctx context.Context) *apiError {
	select {
	case <-s.adminReady:
		return nil
	default:
	}
	if s.adminReady == nil {
		return nil
	}
	t := time.NewTimer(s.passwordWait)
	defer t.Stop()
	select {
	case <-s.adminReady:
		return nil
	case <-t.C:
	case <-ctx.Done():
	}
	return errStarting()
}

// verifyPassword and hashPassword run bcrypt, waiting at most passwordWait
// for one of the bounded bcrypt slots (auth.Passwords); a request that gets
// none in time is answered 503 with Retry-After instead of waiting longer.
func (s *Server) verifyPassword(ctx context.Context, password, hash string) (bool, error) {
	wctx, cancel := context.WithTimeout(ctx, s.passwordWait)
	defer cancel()
	ok, err := s.passwords.VerifyContext(wctx, password, hash)
	if err != nil {
		return false, s.passwordError(ctx, err)
	}
	return ok, nil
}

func (s *Server) hashPassword(ctx context.Context, password string) (string, error) {
	wctx, cancel := context.WithTimeout(ctx, s.passwordWait)
	defer cancel()
	hash, err := s.passwords.HashContext(wctx, password)
	if err != nil {
		return "", s.passwordError(ctx, err)
	}
	return hash, nil
}

func (s *Server) passwordError(ctx context.Context, err error) error {
	if errors.Is(err, auth.ErrBusy) {
		s.log.WarnContext(ctx, "no bcrypt slot in time", "request_id", infoFrom(ctx).id)
		return errPasswordsBusy()
	}
	return s.internal(ctx, "hash password", err)
}

func (s *Server) createUser(ctx context.Context, in *rawBodyInput) (*userOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	username, password, aerr := parseUserCreate(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	// Checked before hashing, so that a duplicate costs no bcrypt; the
	// insert below still catches a race.
	if _, err := s.users.User(ctx, username); err == nil {
		return nil, newError(http.StatusConflict, "Username already exists")
	}
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return nil, err
	}
	u, err := s.users.CreateUser(ctx, username, hash)
	if errors.Is(err, store.ErrExists) {
		return nil, newError(http.StatusConflict, "Username already exists")
	}
	if err != nil {
		return nil, s.internal(ctx, "create user", err)
	}
	s.log.InfoContext(ctx, "created user", "request_id", infoFrom(ctx).id, "username", username)
	return &userOutput{Body: userResponse(u)}, nil
}

func (s *Server) listUsers(ctx context.Context, _ *struct{}) (*usersOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	users, err := s.users.Users(ctx)
	if err != nil {
		return nil, s.internal(ctx, "list users", err)
	}
	out := make([]UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, userResponse(u))
	}
	return &usersOutput{Body: out}, nil
}

func (s *Server) me(ctx context.Context, _ *struct{}) (*userOutput, error) {
	u, ok := currentUser(ctx)
	if !ok {
		return nil, errNotAuthenticated()
	}
	return &userOutput{Body: userResponse(u)}, nil
}

// errPathNotFound is 2.0's answer for a username containing "/" (sent as
// %2F): its router matches no route.
func errPathNotFound() *apiError { return newError(http.StatusNotFound, "Not Found") }

func (s *Server) getUser(ctx context.Context, in *usernameParam) (*userOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if strings.Contains(in.Username, "/") {
		return nil, errPathNotFound()
	}
	u, err := s.users.User(ctx, in.Username)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newError(http.StatusNotFound, fmt.Sprintf("User %s not found", in.Username))
	}
	if err != nil {
		return nil, s.internal(ctx, "get user", err)
	}
	return &userOutput{Body: userResponse(u)}, nil
}

func (s *Server) deleteUser(ctx context.Context, in *usernameParam) (*messageOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if strings.Contains(in.Username, "/") {
		return nil, errPathNotFound()
	}
	if in.Username == AdminUsername {
		return nil, newError(http.StatusBadRequest, "The admin user cannot be deleted")
	}
	err := s.users.DeleteUser(ctx, in.Username)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newError(http.StatusNotFound, fmt.Sprintf("User %s not found", in.Username))
	}
	if err != nil {
		return nil, s.internal(ctx, "delete user", err)
	}
	s.log.InfoContext(ctx, "deleted user", "request_id", infoFrom(ctx).id, "username", in.Username)
	return &messageOutput{Body: Message{Message: "Deleted user " + in.Username}}, nil
}
