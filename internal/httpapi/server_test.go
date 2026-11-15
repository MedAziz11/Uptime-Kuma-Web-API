package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/kumatest"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

const testKey = "server-test-key-server-test-key"

type testEnv struct {
	srv    *Server
	store  *store.Store
	kuma   *kumatest.Server
	client *kuma.Client
	logs   *bytes.Buffer
	pw     *auth.Passwords
	tk     *auth.Tokens

	adminToken string // set by call
}

func newEnv(t testing.TB, opts Options) *testEnv {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	pw, _ := auth.NewPasswords(bcrypt.MinCost)
	if _, err := EnsureAdmin(context.Background(), st, pw, "admin-password"); err != nil {
		t.Fatal(err)
	}
	tk, _ := auth.NewTokens(testKey, time.Hour)
	k := kumatest.New()
	t.Cleanup(k.Close)
	session, err := kuma.New(kuma.Config{URL: k.URL, Username: kumatest.Username, Password: kumatest.Password, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	env := &testEnv{store: st, kuma: k, client: kuma.NewClient(session), logs: &bytes.Buffer{}, pw: pw, tk: tk}
	if opts.Users == nil {
		opts.Users = st
	}
	opts.Passwords, opts.Tokens, opts.Kuma = pw, tk, env.client
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewJSONHandler(syncWriter{mu: &sync.Mutex{}, w: env.logs}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	opts.Version = "3.0.0-test"
	env.srv, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (e *testEnv) do(t testing.TB, method, path, token, contentType, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec.Result()
}

func (e *testEnv) login(t testing.TB, user, password string) string {
	t.Helper()
	res := e.do(t, "POST", "/login/access-token", "", "application/x-www-form-urlencoded",
		url.Values{"username": {user}, "password": {password}}.Encode())
	if res.StatusCode != 200 {
		t.Fatalf("login %s: %d", user, res.StatusCode)
	}
	var tok Token
	json.NewDecoder(res.Body).Decode(&tok)
	return tok.AccessToken
}

func detail(t *testing.T, res *http.Response) string {
	t.Helper()
	var body struct{ Detail string }
	b, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("body %q: %v", b, err)
	}
	return body.Detail
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("no error")
	}
}

func TestOpenAPIDocument(t *testing.T) {
	env := newEnv(t, Options{})
	res := env.do(t, "GET", "/openapi.json", "", "", "")
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	var doc struct {
		Info struct {
			Title, Version string
			License        struct{ Name, Identifier string }
		}
		Paths      map[string]map[string]map[string]any
		Components struct {
			Schemas         map[string]any
			SecuritySchemes map[string]map[string]any
		}
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Info.Title != "Uptime-Kuma-API" || doc.Info.Version != "3.0.0-test" || doc.Info.License.Name != "MIT" {
		t.Fatalf("info %+v", doc.Info)
	}
	flows, _ := json.Marshal(doc.Components.SecuritySchemes["OAuth2PasswordBearer"])
	if string(flows) != `{"flows":{"password":{"scopes":{},"tokenUrl":"/login/access-token"}},"type":"oauth2"}` {
		t.Fatalf("security scheme %s", flows)
	}
	// 2.0's operation ids, so that generated clients keep their names.
	wantIDs := map[string]string{
		"GET /health":              "health_health_get",
		"POST /login/access-token": "login_access_token_login_access_token_post",
		"POST /users":              "create_user_users_post",
		"GET /users":               "get_users_users_get",
		"GET /users/me":            "get_me_users_me_get",
		"GET /users/{username}":    "get_user_users__username__get",
		"DELETE /users/{username}": "delete_user_users__username__delete",

		"GET /monitors":                               "get_monitors_monitors_get",
		"POST /monitors":                              "create_monitor_monitors_post",
		"GET /monitors/{monitor_id}":                  "get_monitor_monitors__monitor_id__get",
		"PATCH /monitors/{monitor_id}":                "update_monitor_monitors__monitor_id__patch",
		"DELETE /monitors/{monitor_id}":               "delete_monitor_monitors__monitor_id__delete",
		"POST /monitors/{monitor_id}/pause":           "pause_monitor_monitors__monitor_id__pause_post",
		"POST /monitors/{monitor_id}/resume":          "resume_monitor_monitors__monitor_id__resume_post",
		"GET /monitors/{monitor_id}/beats":            "monitor_beats_monitors__monitor_id__beats_get",
		"POST /monitors/{monitor_id}/tag":             "add_monitor_tag_monitors__monitor_id__tag_post",
		"DELETE /monitors/{monitor_id}/tag":           "delete_monitor_tag_monitors__monitor_id__tag_delete",
		"GET /statuspages":                            "get_all_status_pages_statuspages_get",
		"POST /statuspages":                           "add_status_page_statuspages_post",
		"GET /statuspages/{slug}":                     "get_status_page_statuspages__slug__get",
		"POST /statuspages/{slug}":                    "save_status_page_statuspages__slug__post",
		"DELETE /statuspages/{slug}":                  "delete_status_page_statuspages__slug__delete",
		"POST /statuspages/{slug}/incident":           "post_incident_statuspages__slug__incident_post",
		"DELETE /statuspages/{slug}/incident/unpin":   "unpin_incident_statuspages__slug__incident_unpin_delete",
		"GET /maintenance":                            "get_maintenances_maintenance_get",
		"POST /maintenance":                           "create_maintenance_maintenance_post",
		"GET /maintenance/{maintenance_id}":           "get_maintenance_maintenance__maintenance_id__get",
		"PATCH /maintenance/{maintenance_id}":         "update_maintenance_maintenance__maintenance_id__patch",
		"DELETE /maintenance/{maintenance_id}":        "delete_maintenance_maintenance__maintenance_id__delete",
		"POST /maintenance/{maintenance_id}/pause":    "pause_maintenance_maintenance__maintenance_id__pause_post",
		"POST /maintenance/{maintenance_id}/resume":   "resume_maintenance_maintenance__maintenance_id__resume_post",
		"GET /maintenance/{maintenance_id}/monitors":  "get_monitor_maintenance_maintenance__maintenance_id__monitors_get",
		"POST /maintenance/{maintenance_id}/monitors": "add_monitor_maintenance_maintenance__maintenance_id__monitors_post",
		"GET /tags":                               "get_tags_tags_get",
		"POST /tags":                              "add_tag_tags_post",
		"GET /tags/{tag_id}":                      "get_tag_tags__tag_id__get",
		"PATCH /tags/{tag_id}":                    "update_tag_tags__tag_id__patch",
		"DELETE /tags/{tag_id}":                   "delete_tag_tags__tag_id__delete",
		"GET /notifications":                      "get_notifications_notifications_get",
		"POST /notifications":                     "add_notification_notifications_post",
		"POST /notifications/test":                "test_notification_notifications_test_post",
		"GET /notifications/{notification_id}":    "get_notification_notifications__notification_id__get",
		"PATCH /notifications/{notification_id}":  "update_notification_notifications__notification_id__patch",
		"DELETE /notifications/{notification_id}": "delete_notification_notifications__notification_id__delete",
		"GET /proxies":                            "get_proxies_proxies_get",
		"POST /proxies":                           "add_proxy_proxies_post",
		"GET /proxies/{proxy_id}":                 "get_proxy_proxies__proxy_id__get",
		"PATCH /proxies/{proxy_id}":               "update_proxy_proxies__proxy_id__patch",
		"DELETE /proxies/{proxy_id}":              "delete_proxy_proxies__proxy_id__delete",
		"GET /docker_hosts":                       "get_docker_hosts_docker_hosts_get",
		"POST /docker_hosts":                      "add_docker_host_docker_hosts_post",
		"POST /docker_hosts/test":                 "test_docker_host_docker_hosts_test_post",
		"GET /docker_hosts/{docker_host_id}":      "get_docker_host_docker_hosts__docker_host_id__get",
		"PATCH /docker_hosts/{docker_host_id}":    "update_docker_host_docker_hosts__docker_host_id__patch",
		"DELETE /docker_hosts/{docker_host_id}":   "delete_docker_host_docker_hosts__docker_host_id__delete",
		"POST /settings/upload_backup":            "upload_backup_settings_upload_backup_post",
		"GET /database/size":                      "get_db_size_database_size_get",
		"POST /database/shrink":                   "shrink_db_database_shrink_post",
		"GET /cert_info":                          "get_cert_info_cert_info_get",
		"GET /info":                               "get_info_info_get",
		"GET /ping":                               "get_avg_ping_ping_get",
		"GET /uptime":                             "get_uptime_uptime_get",
	}
	public := map[string]bool{"GET /health": true, "POST /login/access-token": true}
	n := 0
	for path, ops := range doc.Paths {
		for method, op := range ops {
			key := strings.ToUpper(method) + " " + path
			n++
			if op["operationId"] != wantIDs[key] {
				t.Errorf("%s: operationId %v, want %s", key, op["operationId"], wantIDs[key])
			}
			if s, _ := op["summary"].(string); s == "" {
				t.Errorf("%s has no summary", key)
			}
			_, secured := op["security"]
			if secured == public[key] {
				t.Errorf("%s: security declared %v", key, secured)
			}
			// Every operation documents its answer.
			if !hasResponseSchema(op) {
				t.Errorf("%s: no response schema", key)
			}
			// Every protected operation answers 401 without a token.
			if !public[key] {
				p := regexp.MustCompile(`\{[a-z_]+\}`).ReplaceAllString(path, "1")
				if res := env.do(t, strings.ToUpper(method), p, "", "application/json", "{}"); res.StatusCode != 401 {
					t.Errorf("%s without token: %d", key, res.StatusCode)
				}
			}
		}
	}
	if n != len(wantIDs) {
		t.Fatalf("%d operations, want %d", n, len(wantIDs))
	}
	for _, name := range []string{"Token", "UserCreate", "UserResponse", "Message", "HTTPValidationError", "HTTPError", "Body_login_access_token_login_access_token_post"} {
		if doc.Components.Schemas[name] == nil {
			t.Errorf("schema %s missing", name)
		}
	}
	for _, name := range []string{"ErrorModel", "ErrorDetail"} {
		if doc.Components.Schemas[name] != nil {
			t.Errorf("schema %s documents an error format the API never sends", name)
		}
	}
	login := doc.Paths["/login/access-token"]["post"]["requestBody"].(map[string]any)["content"].(map[string]any)
	if _, ok := login["application/x-www-form-urlencoded"]; !ok || len(login) != 1 {
		t.Fatalf("login body %v", login)
	}
	if res := env.do(t, "GET", "/docs", "", "", ""); res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("docs %d %v", res.StatusCode, res.Header)
	}
	if res := env.do(t, "GET", "/redoc", "", "", ""); res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("redoc %d %v", res.StatusCode, res.Header)
	} else if b, _ := io.ReadAll(res.Body); !strings.Contains(string(b), `<redoc spec-url="/openapi.json">`) {
		t.Fatalf("redoc page %s", b)
	}
	if res := env.do(t, "GET", "/schemas/Token.json", "", "", ""); res.StatusCode != 401 {
		t.Fatalf("/schemas is not served: %d", res.StatusCode)
	}
	if n := env.kuma.Calls("login"); n != 0 {
		t.Fatalf("Uptime Kuma called: %d logins", n)
	}
}

// hasResponseSchema reports whether an operation documents the body of its
// success answer.
func hasResponseSchema(op map[string]any) bool {
	responses, _ := op["responses"].(map[string]any)
	for code, r := range responses {
		if code == "default" || code == "422" {
			continue
		}
		content, _ := r.(map[string]any)["content"].(map[string]any)
		for _, mt := range content {
			if schema, _ := mt.(map[string]any)["schema"].(map[string]any); len(schema) > 0 {
				return true
			}
		}
	}
	return false
}

func TestResponsesHaveNoSchemaLinks(t *testing.T) {
	env := newEnv(t, Options{})
	res := env.do(t, "GET", "/health", "", "", "")
	b, _ := io.ReadAll(res.Body)
	if string(b) != `{"status":"ok","version":"3.0.0-test"}` || res.Header.Get("Link") != "" {
		t.Fatalf("%s %v", b, res.Header)
	}
}

func TestDeletedUsersTokenIsRejectedAtOnce(t *testing.T) {
	env := newEnv(t, Options{})
	admin := env.login(t, "admin", "admin-password")
	if res := env.do(t, "POST", "/users", admin, "application/json", `{"username":"alice","password":"alice-password"}`); res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
	alice := env.login(t, "alice", "alice-password")
	for range 3 { // warm the cache
		if res := env.do(t, "GET", "/users/me", alice, "", ""); res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
	}
	if res := env.do(t, "DELETE", "/users/alice", admin, "", ""); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if res := env.do(t, "GET", "/users/me", alice, "", ""); res.StatusCode != 401 || detail(t, res) != "Invalid or expired token" {
		t.Fatalf("deleted user's token: %d", res.StatusCode)
	}
	// /users/me reflects the login's last_visit (cache refreshed on login).
	before := env.do(t, "GET", "/users/me", admin, "", "")
	var u1, u2 struct {
		LastVisit string `json:"last_visit"`
	}
	json.NewDecoder(before.Body).Decode(&u1)
	time.Sleep(2 * time.Millisecond)
	admin = env.login(t, "admin", "admin-password")
	json.NewDecoder(env.do(t, "GET", "/users/me", admin, "", "").Body).Decode(&u2)
	if u2.LastVisit <= u1.LastVisit || u1.LastVisit == "" {
		t.Fatalf("last_visit %q then %q", u1.LastVisit, u2.LastVisit)
	}
}

func TestAdminPasswordFollowsTheEnvironment(t *testing.T) {
	env := newEnv(t, Options{})
	ctx := context.Background()
	if r, err := EnsureAdmin(ctx, env.store, env.pw, "admin-password"); err != nil || r != AdminUnchanged {
		t.Fatalf("%v %v", r, err)
	}
	if r, err := EnsureAdmin(ctx, env.store, env.pw, "a-new-password"); err != nil || r != AdminPasswordUpdated {
		t.Fatalf("%v %v", r, err)
	}
	env.login(t, "admin", "a-new-password")
	res := env.do(t, "POST", "/login/access-token", "", "application/x-www-form-urlencoded", "username=admin&password=admin-password")
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("old password: %d", res.StatusCode)
	}
	if _, err := EnsureAdmin(ctx, env.store, env.pw, strings.Repeat("x", 73)); err == nil {
		t.Fatal("73-byte password accepted")
	}
	if _, err := EnsureAdmin(ctx, failingStore{}, env.pw, "x"); err == nil {
		t.Fatal("store error ignored")
	}
	if _, err := EnsureAdmin(ctx, failingStore{notFound: true}, env.pw, "x"); err == nil {
		t.Fatal("create error ignored")
	}
	if _, err := EnsureAdmin(ctx, failingStore{notFound: true}, env.pw, strings.Repeat("x", 73)); err == nil {
		t.Fatal("hash error ignored")
	}
	if _, err := EnsureAdmin(ctx, failingStore{exists: true}, env.pw, "x"); err == nil {
		t.Fatal("update error ignored")
	}
}

var errBroken = errors.New("disk on fire")

// failingStore fails every call; notFound/exists choose what User answers.
type failingStore struct{ notFound, exists bool }

func (f failingStore) User(context.Context, string) (store.User, error) {
	switch {
	case f.notFound:
		return store.User{}, store.ErrNotFound
	case f.exists:
		return store.User{Username: "admin", PasswordHash: "$2b$04$invalid"}, nil
	}
	return store.User{}, errBroken
}
func (failingStore) Users(context.Context) ([]store.User, error) { return nil, errBroken }
func (failingStore) CreateUser(context.Context, string, string) (store.User, error) {
	return store.User{}, errBroken
}
func (failingStore) DeleteUser(context.Context, string) error              { return errBroken }
func (failingStore) TouchLastVisit(context.Context, string) error          { return errBroken }
func (failingStore) SetPasswordHash(context.Context, string, string) error { return errBroken }

// adminOnly authenticates admin, then fails the requested operation.
type adminOnly struct {
	failingStore
	admin store.User
	touch error
	user  error
}

func (a adminOnly) User(_ context.Context, name string) (store.User, error) {
	if name == "admin" {
		return a.admin, nil
	}
	if a.user != nil {
		return store.User{}, a.user
	}
	return store.User{}, store.ErrNotFound
}
func (a adminOnly) TouchLastVisit(context.Context, string) error { return a.touch }

func TestStoreFailuresAre500WithoutDetails(t *testing.T) {
	pw, _ := auth.NewPasswords(bcrypt.MinCost)
	hash, _ := pw.Hash("admin-password")
	admin := store.User{ID: 1, Username: "admin", PasswordHash: hash}
	tk, _ := auth.NewTokens(testKey, time.Hour)
	token := tk.Issue("admin")

	env := newEnv(t, Options{Users: failingStore{}})
	if res := env.do(t, "GET", "/users/me", token, "", ""); res.StatusCode != 500 || detail(t, res) != "Internal Server Error" {
		t.Fatalf("lookup failure: %d", res.StatusCode)
	}
	if res := env.do(t, "POST", "/login/access-token", "", "application/x-www-form-urlencoded", "username=admin&password=x"); res.StatusCode != 500 {
		t.Fatalf("login lookup failure: %d", res.StatusCode)
	}
	if strings.Contains(env.logs.String(), "disk on fire") == false {
		t.Fatal("error not logged")
	}

	env = newEnv(t, Options{Users: adminOnly{admin: admin}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/users", ""},
		{"GET", "/users/bob", ""},
		{"DELETE", "/users/bob", ""},
		{"POST", "/users", `{"username":"bob","password":"bob-password"}`},
	} {
		res := env.do(t, c.method, c.path, token, "application/json", c.body)
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 500 && !(c.method == "GET" && c.path == "/users/bob" && res.StatusCode == 404) {
			t.Errorf("%s %s: %d %s", c.method, c.path, res.StatusCode, b)
		}
		if bytes.Contains(b, []byte("disk on fire")) {
			t.Errorf("%s %s leaks the error: %s", c.method, c.path, b)
		}
	}
	env = newEnv(t, Options{Users: adminOnly{admin: admin, user: errBroken}})
	if res := env.do(t, "GET", "/users/bob", token, "", ""); res.StatusCode != 500 {
		t.Fatalf("get user failure: %d", res.StatusCode)
	}
	env = newEnv(t, Options{Users: adminOnly{admin: admin, touch: errBroken}})
	if res := env.do(t, "POST", "/login/access-token", "", "application/x-www-form-urlencoded", "username=admin&password=admin-password"); res.StatusCode != 500 {
		t.Fatalf("touch failure: %d", res.StatusCode)
	}
	env = newEnv(t, Options{Users: adminOnly{admin: admin, touch: store.ErrNotFound}})
	if res := env.do(t, "POST", "/login/access-token", "", "application/x-www-form-urlencoded", "username=admin&password=admin-password"); res.StatusCode != 401 {
		t.Fatalf("user deleted during login: %d", res.StatusCode)
	}
}

// A username taken between the check and the insert is still a 409.
func TestCreateRaceIsConflict(t *testing.T) {
	pw, _ := auth.NewPasswords(bcrypt.MinCost)
	hash, _ := pw.Hash("admin-password")
	tk, _ := auth.NewTokens(testKey, time.Hour)
	env := newEnv(t, Options{Users: racingStore{adminOnly{admin: store.User{Username: "admin", PasswordHash: hash}}}})
	res := env.do(t, "POST", "/users", tk.Issue("admin"), "application/json", `{"username":"bob","password":"bob-password"}`)
	if res.StatusCode != 409 || detail(t, res) != "Username already exists" {
		t.Fatalf("%d", res.StatusCode)
	}
}

type racingStore struct{ adminOnly }

func (racingStore) CreateUser(context.Context, string, string) (store.User, error) {
	return store.User{}, store.ErrExists
}

func TestBodyLimit(t *testing.T) {
	env := newEnv(t, Options{MaxBodyBytes: 64})
	big := strings.Repeat("x", 100)
	res := env.do(t, "POST", "/login/access-token", "", "application/x-www-form-urlencoded", big)
	if res.StatusCode != 413 || !strings.HasPrefix(detail(t, res), "Request body too large") {
		t.Fatalf("declared length: %d", res.StatusCode)
	}
	// Without Content-Length (chunked), the limit applies while reading.
	req := httptest.NewRequest("POST", "/login/access-token", io.MultiReader(strings.NewReader(big)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	env.srv.ServeHTTP(rec, req)
	if rec.Code != 413 || rec.Header().Get("Content-Type") != "application/json" || !strings.HasPrefix(rec.Body.String(), `{"detail":"request body is too large`) {
		t.Fatalf("streamed: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

func TestRequestIDAndAccessLog(t *testing.T) {
	env := newEnv(t, Options{})
	token := env.login(t, "admin", "admin-password")
	req := httptest.NewRequest("GET", "/users/me?secret=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Request-ID", "abc-123")
	rec := httptest.NewRecorder()
	env.srv.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("request id %q", rec.Header().Get("X-Request-ID"))
	}
	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(env.logs.String()), "\n") {
		var m map[string]any
		json.Unmarshal([]byte(l), &m)
		if m["msg"] == "request" && m["request_id"] == "abc-123" {
			line = m
		}
	}
	if line == nil || line["status"] != float64(200) || line["user"] != "admin" || line["path"] != "/users/me" || line["method"] != "GET" {
		t.Fatalf("access log %v\n%s", line, env.logs)
	}
	for _, secret := range []string{token, "admin-password", "secret=1"} {
		if strings.Contains(env.logs.String(), secret) {
			t.Fatalf("log contains %q", secret)
		}
	}
	for _, bad := range []string{"", "has space", strings.Repeat("a", 129), "a\nb"} {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header["X-Request-Id"] = []string{bad}
		rec := httptest.NewRecorder()
		env.srv.ServeHTTP(rec, req)
		if id := rec.Header().Get("X-Request-ID"); id == bad || len(id) != 32 {
			t.Fatalf("%q replaced by %q", bad, id)
		}
	}
}

func TestPanicRecovery(t *testing.T) {
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	h := withRequestID(withRecovery(log, withAccessLog(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || rec.Body.String() != `{"detail":"Internal Server Error"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), `"panic":"boom"`) || !strings.Contains(logs.String(), `"status":500`) {
		t.Fatalf("logs %s", logs)
	}

	// After the header is written, the status is left alone.
	rec = httptest.NewRecorder()
	withRecovery(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		panic("late")
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 202 {
		t.Fatalf("%d", rec.Code)
	}

	// http.ErrAbortHandler is passed on to net/http.
	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Fatalf("recovered %v", p)
		}
	}()
	withRecovery(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestCORSPreflightFailures(t *testing.T) {
	env := newEnv(t, Options{CORSOrigins: []string{"https://a.example"}})
	req := httptest.NewRequest("OPTIONS", "/users", nil)
	req.Header.Set("Origin", "https://a.example")
	req.Header.Set("Access-Control-Request-Method", "TRACE")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	rec := httptest.NewRecorder()
	env.srv.ServeHTTP(rec, req)
	if rec.Code != 400 || rec.Body.String() != "Disallowed CORS method, private-network" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

// Tokens and users written by 2.0 keep working: the 2.0 database, its
// generated secret_key file and a token 2.0 issued.
func TestUpgradeFrom2_0(t *testing.T) {
	var fixture struct {
		V20 struct {
			Users      []struct{ Username, Password string }
			AliceToken string `json:"alice_token"`
			Listed     []map[string]any
		} `json:"v2.0"`
	}
	b, err := os.ReadFile("../store/testdata/users.json")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(b, &fixture)

	dir := filepath.Join(t.TempDir(), "db")
	os.MkdirAll(dir, 0o750)
	for _, name := range []string{"db.sqlite3", "secret_key"} {
		b, err := os.ReadFile(filepath.Join("..", "store", "testdata", "v2.0", name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, name), b, 0o600)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "db.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key, generated, err := auth.ResolveSecretKey("", filepath.Join(dir, "db.sqlite3"))
	if err != nil || generated {
		t.Fatalf("secret key: %v %v", generated, err)
	}
	pw, _ := auth.NewPasswords(bcrypt.MinCost)
	tk, _ := auth.NewTokens(key, time.Hour)
	srv, err := New(Options{Users: st, Passwords: pw, Tokens: tk, Kuma: newEnv(t, Options{}).client})
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{srv: srv}

	// A token issued by 2.0 before the upgrade.
	res := env.do(t, "GET", "/users/me", fixture.V20.AliceToken, "", "")
	var me map[string]any
	json.NewDecoder(res.Body).Decode(&me)
	if res.StatusCode != 200 || me["username"] != "alice" {
		t.Fatalf("2.0 token: %d %v", res.StatusCode, me)
	}
	// Same answer as 2.0 gave on GET /users for this database.
	if res.StatusCode != 200 || me["created_at"] != fixture.V20.Listed[1]["created_at"] || me["last_visit"] != fixture.V20.Listed[1]["last_visit"] {
		t.Fatalf("2.0 listed %v, 3.0 %v", fixture.V20.Listed[1], me)
	}
	// Log in with the 2.0 password (bcrypt cost 12, written by Python).
	carol := fixture.V20.Users[2]
	token := env.login(t, carol.Username, carol.Password)
	if res := env.do(t, "GET", "/users/me", token, "", ""); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
}

func TestServeAndGracefulShutdown(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
		io.WriteString(w, "done")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewHTTPServer(h, time.Second, slog.New(slog.DiscardHandler))
	if srv.ReadHeaderTimeout == 0 || srv.WriteTimeout <= time.Second || srv.IdleTimeout == 0 {
		t.Fatalf("timeouts %+v", srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, srv, ln, 5*time.Second) }()

	got := make(chan string, 1)
	go func() {
		res, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			got <- err.Error()
			return
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		got <- string(b)
	}()
	<-started
	cancel() // SIGTERM: stop accepting, drain the request in flight
	time.Sleep(50 * time.Millisecond)
	close(release)
	if body := <-got; body != "done" {
		t.Fatalf("in-flight request: %q", body)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}

	// A drain that does not finish in time is reported.
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	block := make(chan struct{})
	defer close(block)
	entered := make(chan struct{})
	srv2 := NewHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-block
	}), time.Second, slog.New(slog.DiscardHandler))
	ctx2, cancel2 := context.WithCancel(context.Background())
	served2 := make(chan error, 1)
	go func() { served2 <- Serve(ctx2, srv2, ln2, 50*time.Millisecond) }()
	go http.Get("http://" + ln2.Addr().String() + "/")
	<-entered
	cancel2()
	if err := <-served2; err == nil {
		t.Fatal("no error after drain timeout")
	}

	// A listener that fails is reported.
	ln3, _ := net.Listen("tcp", "127.0.0.1:0")
	ln3.Close()
	if err := Serve(context.Background(), NewHTTPServer(h, time.Second, slog.New(slog.DiscardHandler)), ln3, time.Second); err == nil {
		t.Fatal("no error for a closed listener")
	}
}

// A login or user creation that gets no bcrypt slot in time is a 503 with
// Retry-After, and they work again once a slot is free.
func TestPasswordsWithoutABcryptSlotAre503(t *testing.T) {
	e := newEnv(t, Options{PasswordWait: 20 * time.Millisecond})
	token := e.login(t, "admin", "admin-password")
	slow, err := auth.NewPasswordsLimit(10, 1)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.passwords = slow
	for _, c := range []struct{ path, token, contentType, body string }{
		{"/login/access-token", "", "application/x-www-form-urlencoded", "username=admin&password=admin-password"},
		{"/users", token, "application/json", `{"username":"bob","password":"bob-password"}`},
	} {
		busy := make(chan struct{})
		go func() {
			defer close(busy)
			slow.Hash("occupies the only slot")
		}()
		deadline := time.Now().Add(5 * time.Second)
		var res *http.Response
		for {
			res = e.do(t, "POST", c.path, c.token, c.contentType, c.body)
			if res.StatusCode == 503 || time.Now().After(deadline) {
				break
			}
		}
		if res.StatusCode != 503 || res.Header.Get("Retry-After") != "1" || detail(t, res) != "Too many logins in progress, retry later" {
			t.Fatalf("%s: %d %v", c.path, res.StatusCode, res.Header)
		}
		<-busy
		if res := e.do(t, "POST", c.path, c.token, c.contentType, c.body); res.StatusCode/100 != 2 {
			t.Fatalf("%s with a free slot: %d", c.path, res.StatusCode)
		}
	}
}
