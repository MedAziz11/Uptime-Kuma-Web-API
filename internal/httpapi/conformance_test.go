package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

// v2Responses is testdata/v2_responses.json, recorded from the 2.0 (Python)
// application by testdata/gen_v2_responses.py.
type v2Responses struct {
	SecretKey     string            `json:"secret_key"`
	AdminPassword string            `json:"admin_password"`
	Tokens        map[string]string `json:"tokens"`
	Scenarios     map[string]struct {
		CORSOrigins []string `json:"cors_origins"`
		Database    string   `json:"database"`
		Steps       []v2Step `json:"steps"`
	} `json:"scenarios"`
}

type v2Step struct {
	Name            string            `json:"name"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	SaveToken       string            `json:"save_token"`
	HTML            bool              `json:"html"`
	Status          int               `json:"status"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    any               `json:"response_body"`
}

// recordedHeaders are the headers gen_v2_responses.py records.
var recordedHeaders = []string{
	"content-type", "www-authenticate", "location", "allow", "vary",
	"access-control-allow-origin", "access-control-allow-credentials", "access-control-allow-methods",
	"access-control-allow-headers", "access-control-max-age", "access-control-expose-headers",
}

// difference is how 3.0 deliberately answers differently from 2.0. Every
// entry is listed in docs/kuma-2x-notes.md ("Differences from 2.0") with its
// reason. Zero fields mean "as 2.0".
type difference struct {
	status  int
	headers map[string]string // replaced; "" removes
	body    any               // replaces the body when non-nil
	reason  string
}

var notAuthenticated = map[string]any{"detail": "Not authenticated"}

var differences = map[string]difference{
	"main/health head": {status: 200, headers: map[string]string{"allow": ""},
		reason: "net/http serves HEAD for every GET route; 2.0 answered 405"},
	"main/docs": {headers: map[string]string{"content-type": "text/html"},
		reason: "huma's docs page sets text/html without a charset"},
	"main/openapi": {headers: map[string]string{"content-type": "application/openapi+json"},
		reason: "huma serves the document as application/openapi+json"},
	"main/unknown path without token": {status: 401, headers: map[string]string{"www-authenticate": "Bearer"}, body: notAuthenticated,
		reason: "authentication runs before routing: without a valid token, any non-public path answers 401"},
	"main/unknown path with token": {status: 401, headers: map[string]string{"www-authenticate": "Bearer"},
		body:   map[string]any{"detail": "Invalid or expired token"},
		reason: "same (the recorded request carries an unresolved placeholder, i.e. an invalid token)"},
	"main/create malformed json without token": {status: 401, headers: map[string]string{"www-authenticate": "Bearer"}, body: notAuthenticated,
		reason: "the token is checked before the body is read; 2.0 parsed the JSON first"},
	"main/put user": {headers: map[string]string{"allow": "GET, DELETE"},
		reason: "Allow lists every method of the path; 2.0 listed only the first route's"},
	"main/options without cors": {status: 401, headers: map[string]string{"allow": "", "www-authenticate": "Bearer"}, body: notAuthenticated,
		reason: "authentication before routing (no CORS configured, so OPTIONS is an ordinary request)"},
	"cors/preflight without request method": {status: 401, headers: map[string]string{"allow": "", "www-authenticate": "Bearer"}, body: notAuthenticated,
		reason: "not a preflight (no Access-Control-Request-Method): authentication before routing"},
}

var (
	jwtRE      = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)
	datetimeRE = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)?$`)
)

// normalize is gen_v2_responses.py's normalize().
func normalize(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			s, isString := x.(string)
			switch {
			case k == "access_token" && isString && jwtRE.MatchString(s):
				out[k] = "<jwt>"
			case (k == "created_at" || k == "last_visit") && isString && datetimeRE.MatchString(s):
				if strings.HasSuffix(s, "Z") {
					out[k] = "<datetime:Z>"
				} else {
					out[k] = "<datetime:naive>"
				}
			default:
				out[k] = normalize(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = normalize(x)
		}
		return out
	}
	return v
}

func loadV2Responses(t *testing.T) v2Responses {
	t.Helper()
	b, err := os.ReadFile("testdata/v2_responses.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc v2Responses
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// conformanceServer builds the API as main does, on a database in a
// temporary directory (a copy of database when it is set).
func conformanceServer(t *testing.T, doc v2Responses, origins []string, database string) http.Handler {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db", "db.sqlite3")
	if database != "" {
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(database)))
		if err != nil {
			t.Fatal(err)
		}
		os.MkdirAll(filepath.Dir(path), 0o750)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	pw, err := auth.NewPasswords(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAdmin(ctx, st, pw, doc.AdminPassword); err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokens(doc.SecretKey, 8*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Users: st, Passwords: pw, Tokens: tokens, CORSOrigins: origins, Version: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestConformanceWith2_0 replays the requests recorded from 2.0 and
// compares the answers, except for the documented differences.
func TestConformanceWith2_0(t *testing.T) {
	doc := loadV2Responses(t)
	if len(doc.Scenarios) == 0 {
		t.Fatal("no scenarios")
	}
	used := map[string]bool{}
	for name, sc := range doc.Scenarios {
		t.Run(name, func(t *testing.T) {
			h := conformanceServer(t, doc, sc.CORSOrigins, sc.Database)
			saved := map[string]string{}
			for _, step := range sc.Steps {
				key := name + "/" + step.Name
				want := step
				if d, ok := differences[key]; ok {
					used[key] = true
					want = applyDifference(step, d)
				}
				got, gotHeaders, gotBody := replay(t, h, doc.Tokens, saved, step)
				if got != want.Status {
					t.Errorf("%s: status %d, 2.0 %d (body %v)", step.Name, got, want.Status, gotBody)
					continue
				}
				for _, hn := range recordedHeaders {
					if gotHeaders[hn] != want.ResponseHeaders[hn] {
						t.Errorf("%s: header %s %q, 2.0 %q", step.Name, hn, gotHeaders[hn], want.ResponseHeaders[hn])
					}
				}
				if want.Status == http.StatusUnprocessableEntity {
					gotBody, want.ResponseBody = fieldErrors(gotBody), fieldErrors(want.ResponseBody)
				}
				if !step.HTML && step.Method != http.MethodHead && !reflect.DeepEqual(gotBody, want.ResponseBody) {
					g, _ := json.Marshal(gotBody)
					w, _ := json.Marshal(want.ResponseBody)
					t.Errorf("%s: body\n got %s\n2.0 %s", step.Name, g, w)
				}
			}
		})
	}
	for key := range differences {
		if !used[key] {
			t.Errorf("difference %q matches no recorded step", key)
		}
	}
}

// fieldErrors keeps what 3.0 promises of a 422 answer: the type and loc of
// each error, without the position of a JSON syntax error. Messages and
// echoed input need not match 2.0's.
func fieldErrors(body any) any {
	m, ok := body.(map[string]any)
	if !ok {
		return body
	}
	list, ok := m["detail"].([]any)
	if !ok {
		return body
	}
	out := make([]any, len(list))
	for i, e := range list {
		em, _ := e.(map[string]any)
		loc := em["loc"]
		if em["type"] == "json_invalid" {
			loc = []any{"body"}
		}
		out[i] = map[string]any{"type": em["type"], "loc": loc}
	}
	return map[string]any{"detail": out}
}

func applyDifference(step v2Step, d difference) v2Step {
	if d.status != 0 {
		step.Status = d.status
	}
	h := map[string]string{}
	for k, v := range step.ResponseHeaders {
		h[k] = v
	}
	for k, v := range d.headers {
		if v == "" {
			delete(h, k)
		} else {
			h[k] = v
		}
	}
	step.ResponseHeaders = h
	if d.body != nil {
		b, _ := json.Marshal(d.body)
		var v any
		json.Unmarshal(b, &v)
		step.ResponseBody = v
	}
	return step
}

func replay(t *testing.T, h http.Handler, tokens, saved map[string]string, step v2Step) (int, map[string]string, any) {
	t.Helper()
	req := httptest.NewRequest(step.Method, step.Path, strings.NewReader(step.Body))
	req.Host = "testserver"
	for k, v := range step.Headers {
		for name, tok := range tokens {
			v = strings.ReplaceAll(v, "{"+name+"}", tok)
		}
		for name, tok := range saved {
			v = strings.ReplaceAll(v, "{"+name+"}", tok)
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	raw, _ := io.ReadAll(res.Body)

	headers := map[string]string{}
	for _, hn := range recordedHeaders {
		if vs := res.Header.Values(hn); len(vs) > 0 {
			headers[hn] = strings.Join(vs, ", ")
		}
	}
	var body any
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: invalid JSON %q", step.Name, raw)
		}
		body = normalize(v)
		if strings.HasSuffix(string(raw), "\n") {
			t.Errorf("%s: trailing newline", step.Name)
		}
	} else {
		body = string(raw)
	}
	if step.SaveToken != "" && res.StatusCode == http.StatusOK {
		var tok Token
		if err := json.Unmarshal(raw, &tok); err != nil {
			t.Fatal(err)
		}
		saved[step.SaveToken] = tok.AccessToken
	}
	return res.StatusCode, headers, body
}
