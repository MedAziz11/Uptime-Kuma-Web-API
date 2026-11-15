package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// answer is a response read in full.
type answer struct {
	status int
	header http.Header
	body   string
}

func (e *testEnv) get(t *testing.T, path string, header ...string) answer {
	t.Helper()
	if e.adminToken == "" {
		e.adminToken = e.login(t, "admin", "admin-password")
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+e.adminToken)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Add(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	return answer{res.StatusCode, res.Header, string(b)}
}

// populated is an environment with one object of each kind and statistics.
func populated(t *testing.T) (*testEnv, int) {
	e := newEnv(t, Options{})
	id := int(get[float64](t, e.ok(t, "POST", "/monitors", `{"type":"http","name":"web <&>","url":"http://x","notificationIDList":[1]}`), "monitorID"))
	e.ok(t, "POST", "/monitors", `{"type":"group","name":"g"}`)
	e.ok(t, "POST", "/notifications", `{"name":"n","type":"webhook","webhookURL":"http://h"}`)
	e.ok(t, "POST", "/proxies", `{"protocol":"http","host":"p","port":3128}`)
	e.ok(t, "POST", "/docker_hosts", `{"name":"d","dockerType":"socket"}`)
	e.ok(t, "POST", "/maintenance", `{"title":"w","strategy":"manual"}`)
	e.ok(t, "POST", "/statuspages", `{"title":"p","slug":"p"}`)
	e.kuma.Beat(id, 1, 25)
	e.kuma.CertInfo(id, `{"valid":true}`)
	waitUntil(t, func() bool {
		return len(first(e.client.State().CertInfo())) == 1 && len(first(e.client.State().Uptime())) == 1
	})
	return e, id
}

// The cached answers are the bytes 2.0's handlers encoded (the encoding of
// the views, as before the cache).
func TestCachedBodiesAreTheEncodedViews(t *testing.T) {
	e, id := populated(t)
	st := e.client.State()
	encode := func(v any) string {
		b, err := marshalJSON(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	list := func(l kuma.List, view func(json.RawMessage) any) []any {
		objs, _ := st.Objects(l)
		out := make([]any, len(objs))
		for i, o := range objs {
			out[i] = view(o)
		}
		return out
	}
	object := func(l kuma.List, id int64) json.RawMessage {
		o, _, _ := st.Object(l, id)
		return o
	}
	info, _ := st.Info()
	want := map[string]string{
		"/monitors":                     encode(map[string]any{"monitors": list(kuma.Monitors, monitorView)}),
		fmt.Sprintf("/monitors/%d", id): encode(map[string]any{"monitor": monitorView(object(kuma.Monitors, int64(id)))}),
		"/notifications":                encode(map[string]any{"notifications": list(kuma.Notifications, notificationView)}),
		"/proxies":                      encode(map[string]any{"proxies": list(kuma.Proxies, proxyView)}),
		"/docker_hosts":                 encode(map[string]any{"docker_hosts": list(kuma.DockerHosts, rawView)}),
		"/maintenance":                  encode(map[string]any{"maintenances": list(kuma.Maintenances, rawView)}),
		"/statuspages":                  encode(map[string]any{"statuspages": list(kuma.StatusPages, rawView)}),
		"/info":                         encode(info),
		"/ping":                         encode(byMonitor(first(st.AvgPing()))),
		"/uptime":                       encode(byMonitor(first(st.Uptime()))),
		"/cert_info":                    encode(byMonitor(first(st.CertInfo()))),
		"/notifications/" + firstID(e, kuma.Notifications): encode(map[string]any{"notification": notificationView(object(kuma.Notifications, mustID(t, firstID(e, kuma.Notifications))))}),
		"/proxies/" + firstID(e, kuma.Proxies):             encode(map[string]any{"proxy": proxyView(object(kuma.Proxies, mustID(t, firstID(e, kuma.Proxies))))}),
		"/docker_hosts/" + firstID(e, kuma.DockerHosts):    encode(map[string]any{"docker_host": object(kuma.DockerHosts, mustID(t, firstID(e, kuma.DockerHosts)))}),
	}
	for path, body := range want {
		for range 2 { // built, then cached
			a := e.get(t, path)
			if a.status != 200 || a.body != body {
				t.Fatalf("%s: %d\n%s\nwant\n%s", path, a.status, a.body, body)
			}
			if a.header.Get("Content-Type") != "application/json" || a.header.Get("Cache-Control") != "private, no-cache" ||
				a.header.Get("Content-Length") != fmt.Sprint(len(body)) || !strings.HasPrefix(a.header.Get("ETag"), `"`) {
				t.Fatalf("%s: headers %v", path, a.header)
			}
		}
	}
	if !strings.Contains(want["/monitors"], "web <&>") {
		t.Fatal("HTML characters are escaped")
	}
}

func firstID(e *testEnv, l kuma.List) string {
	ids, _, _ := e.client.State().ObjectsByID(l)
	return fmt.Sprint(ids[0])
}

func mustID(t *testing.T, s string) int64 {
	var id int64
	if _, err := fmt.Sscan(s, &id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Every push that changes a list changes the cached answer at once.
func TestCacheFollowsEveryPush(t *testing.T) {
	e, id := populated(t)
	st := e.client.State()
	key := fmt.Sprint(id)
	monitor := e.kuma.MonitorList()[key]
	for _, c := range []struct {
		name  string
		paths []string
		l     kuma.List
		push  func()
		check string // in the new body
	}{
		{"updateMonitorIntoList", []string{"/monitors", "/monitors/" + key}, kuma.Monitors, func() {
			e.kuma.UpdateMonitor(id, "name", "updated")
		}, `"updated"`},
		{"monitorList", []string{"/monitors", "/monitors/" + key}, kuma.Monitors, func() {
			m := map[string]any{}
			for k, v := range monitor {
				m[k] = v
			}
			m["name"] = "listed"
			e.kuma.Push("monitorList", map[string]any{key: m})
		}, `"listed"`},
		{"notificationList", []string{"/notifications"}, kuma.Notifications, func() {
			e.kuma.Push("notificationList", []any{map[string]any{"id": 7, "name": "pushed", "config": "{}"}})
		}, `"pushed"`},
		{"proxyList", []string{"/proxies"}, kuma.Proxies, func() {
			e.kuma.Push("proxyList", []any{map[string]any{"id": 7, "host": "pushed"}})
		}, `"pushed"`},
		{"dockerHostList", []string{"/docker_hosts"}, kuma.DockerHosts, func() {
			e.kuma.Push("dockerHostList", []any{map[string]any{"id": 7, "name": "pushed"}})
		}, `"pushed"`},
		{"maintenanceList", []string{"/maintenance"}, kuma.Maintenances, func() {
			e.kuma.Push("maintenanceList", map[string]any{"7": map[string]any{"id": 7, "title": "pushed"}})
		}, `"pushed"`},
		{"statusPageList", []string{"/statuspages"}, kuma.StatusPages, func() {
			e.kuma.Push("statusPageList", map[string]any{"7": map[string]any{"id": 7, "slug": "pushed"}})
		}, `"pushed"`},
		{"info", []string{"/info"}, kuma.Info, func() {
			e.kuma.Push("info", map[string]any{"version": "9.9.9"})
		}, `"9.9.9"`},
		{"avgPing", []string{"/ping"}, kuma.AvgPing, func() { e.kuma.Push("avgPing", id, 4242) }, `4242`},
		{"uptime", []string{"/uptime"}, kuma.Uptime, func() { e.kuma.Push("uptime", id, 24, 0.4242) }, `0.4242`},
		{"certInfo", []string{"/cert_info"}, kuma.CertInfo, func() { e.kuma.CertInfo(id, `{"pushed":true}`) }, `"pushed"`},
		{"deleteMonitorFromList", []string{"/monitors", "/ping", "/uptime", "/cert_info"}, kuma.Monitors, func() {
			e.kuma.Push("deleteMonitorFromList", id)
		}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := map[string]answer{}
			for _, p := range c.paths {
				before[p] = e.get(t, p)
			}
			v := st.Version(c.l)
			c.push()
			waitUntil(t, func() bool { return st.Version(c.l) > v })
			for _, p := range c.paths {
				a := e.get(t, p)
				if a.status != 200 || a.body == before[p].body || a.header.Get("ETag") == before[p].header.Get("ETag") {
					t.Fatalf("%s: %d %s (unchanged)", p, a.status, a.body)
				}
				if !strings.Contains(a.body, c.check) || c.check == "" && strings.Contains(a.body, `"`+key+`"`) {
					t.Fatalf("%s: %s", p, a.body)
				}
				if a := e.get(t, p, "If-None-Match", before[p].header.Get("ETag")); a.status != 200 {
					t.Fatalf("%s: old ETag answered %d", p, a.status)
				}
			}
		})
	}
	if a := e.get(t, "/monitors/"+key); a.status != 404 || !strings.Contains(a.body, "Not found") {
		t.Fatalf("deleted monitor: %d %s", a.status, a.body)
	}
}

func TestConditionalRequests(t *testing.T) {
	e, id := populated(t)
	for _, path := range []string{"/monitors", fmt.Sprintf("/monitors/%d", id), "/info"} {
		full := e.get(t, path)
		etag := full.header.Get("ETag")
		if full.status != 200 || len(etag) < 10 {
			t.Fatalf("%s: %d %q", path, full.status, etag)
		}
		opaque := strings.Trim(etag, `"`)
		for _, inm := range []string{
			etag, "W/" + etag, `"other", ` + etag, `"a",W/"b",  ` + etag + ` , "c"`, "*", "junk, " + etag,
		} {
			a := e.get(t, path, "If-None-Match", inm)
			if a.status != 304 || a.body != "" || a.header.Get("ETag") != etag || a.header.Get("Cache-Control") != "private, no-cache" ||
				a.header.Get("Content-Type") != "" {
				t.Fatalf("%s If-None-Match %s: %d %v %q", path, inm, a.status, a.header, a.body)
			}
		}
		for _, inm := range []string{`"other"`, opaque, `W/"x"`, `"` + opaque, "W/", `"unterminated`, ",,"} {
			if a := e.get(t, path, "If-None-Match", inm); a.status != 200 || a.body != full.body {
				t.Fatalf("%s If-None-Match %s: %d", path, inm, a.status)
			}
		}
		// Several header lines.
		if a := e.get(t, path, "If-None-Match", `"x"`, "If-None-Match", etag); a.status != 304 {
			t.Fatalf("%s: second If-None-Match line ignored: %d", path, a.status)
		}
	}
	// HEAD answers the headers of GET (net/http drops the body).
	req := httptest.NewRequest(http.MethodHead, "/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+e.adminToken)
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("ETag") == "" {
		t.Fatalf("HEAD: %d %v", rec.Code, rec.Header())
	}
}

func TestETagMatch(t *testing.T) {
	for _, c := range []struct {
		values []string
		want   bool
	}{
		{nil, false},
		{[]string{""}, false},
		{[]string{`"e"`}, true},
		{[]string{`W/"e"`}, true},
		{[]string{` "a" ,W/"e"`}, true},
		{[]string{`*`}, true},
		{[]string{`"a"`, `"e"`}, true},
		{[]string{`e`}, false},
		{[]string{`"a" x, "e"`}, true}, // garbage between members is skipped
		{[]string{`bad, "e"`}, true},
		{[]string{`"e`}, false},
		{[]string{`W/`}, false},
	} {
		if got := etagMatch(c.values, `"e"`); got != c.want {
			t.Errorf("%q: %v", c.values, got)
		}
	}
}

// After a reset, nothing cached is answered: 503 until the next login has
// pushed everything, then the new state.
func TestNoCachedBodyWhileUnsyncedOrAfterReconnect(t *testing.T) {
	e, id := populated(t)
	st := e.client.State()
	key := fmt.Sprint(id)
	paths := []string{"/monitors", "/monitors/" + key, "/notifications", "/info", "/ping"}
	for _, p := range paths {
		if a := e.get(t, p); a.status != 200 {
			t.Fatalf("%s: %d", p, a.status)
		}
	}
	// Changed in Uptime Kuma without a push: only a new login shows it.
	e.kuma.NoPush(true)
	e.kuma.UpdateMonitor(id, "name", "changed while away")
	e.kuma.NoPush(false)
	if a := e.get(t, "/monitors/"+key); strings.Contains(a.body, "changed while away") {
		t.Fatal("not pushed, yet visible")
	}
	v := st.Version(kuma.Monitors)
	e.kuma.DisconnectAll()
	waitUntil(t, func() bool { return st.Version(kuma.Monitors) > v })
	// From the reset on, an answer is either a 503 or the new state.
	waitUntil(t, func() bool {
		for _, p := range paths {
			a := e.get(t, p)
			switch {
			case a.status == 503:
				return false
			case a.status != 200:
				t.Fatalf("%s: %d", p, a.status)
			case strings.HasPrefix(p, "/monitors") && !strings.Contains(a.body, "changed while away"):
				t.Fatalf("%s: stale body after the reconnection: %s", p, a.body)
			}
		}
		return true
	})

	// A reset with no reconnection (the session's state is lost and Sync
	// cannot help): 503, never the cached body.
	st.Reset()
	for _, p := range paths {
		if a := e.get(t, p); a.status != 503 || !strings.Contains(a.body, "Uptime Kuma is unavailable") {
			t.Fatalf("%s after reset: %d %s", p, a.status, a.body)
		}
	}
}

// A write that returned is visible to the next read, through the cache, and
// concurrent readers of a changed list share one construction.
func TestReadYourWritesThroughTheCache(t *testing.T) {
	e, id := populated(t)
	path := fmt.Sprintf("/monitors/%d", id)
	for i := range 20 {
		name := fmt.Sprintf("name-%d", i)
		e.ok(t, "PATCH", path, fmt.Sprintf(`{"name":%q}`, name))
		var wg sync.WaitGroup
		for _, p := range []string{path, "/monitors", path, "/monitors"} {
			wg.Go(func() {
				if a := e.get(t, p); a.status != 200 || !strings.Contains(a.body, `"`+name+`"`) {
					t.Errorf("%s after PATCH %s: %d", p, name, a.status)
				}
			})
		}
		wg.Wait()
	}
	e.ok(t, "POST", "/notifications", `{"name":"second","type":"webhook","webhookURL":"http://h"}`)
	if a := e.get(t, "/notifications"); !strings.Contains(a.body, `"second"`) {
		t.Fatal("added notification not visible")
	}
	e.ok(t, "POST", "/statuspages", `{"title":"q","slug":"q"}`)
	if a := e.get(t, "/statuspages"); !strings.Contains(a.body, `"q"`) {
		t.Fatal("added status page not visible")
	}
}

// Invalid ids are 422 as huma answered them, before Uptime Kuma is asked.
func TestCachedObjectParameters(t *testing.T) {
	e := newEnv(t, Options{})
	for _, p := range []string{"/monitors/abc", "/monitors/99999999999999999999", "/proxies/x", "/notifications/1.5", "/docker_hosts/x"} {
		a := e.get(t, p)
		if a.status != 422 || !strings.Contains(a.body, `"type":"int_parsing"`) || !strings.Contains(a.body, `"loc":["path",`) {
			t.Fatalf("%s: %d %s", p, a.status, a.body)
		}
	}
	if a := e.get(t, "/monitors/-1"); a.status != 404 {
		t.Fatalf("negative id: %d", a.status)
	}
}

// A build failure is a 500 without details; a canceled request is not
// logged as one.
func TestCacheErrors(t *testing.T) {
	e := newEnv(t, Options{})
	broken := newBodyCache(kuma.Monitors, func(*kuma.State, *bodies) (*bodies, kuma.Snapshot, error) {
		return nil, kuma.Snapshot{Synced: true}, fmt.Errorf("broken")
	})
	ctx := context.Background()
	if err := e.client.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.cached(ctx, broken); err == nil || err.(*apiError).status != 500 {
		t.Fatalf("%v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	broken.building <- struct{}{} // a construction in progress
	if _, err := e.srv.cached(canceled, broken); err == nil || err.(*apiError).status != 503 {
		t.Fatalf("%v", err)
	}
	<-broken.building
	rec := httptest.NewRecorder()
	writeAPIError(rec, fmt.Errorf("not an API error"))
	if rec.Code != 500 {
		t.Fatal(rec.Code)
	}
	// Encoding failures.
	bad := func(json.RawMessage) any { return make(chan int) }
	if _, _, err := objectRoutes(kuma.Monitors, "m", "", bad).get(ctx, populatedState(t, e)); err == nil {
		t.Fatal("object encoding")
	}
	whole := wholeRoute(kuma.Info, func(st *kuma.State) (any, bool, kuma.Snapshot) { return make(chan int), true, st.Snapshot(kuma.Info) })
	if _, _, err := whole.get(ctx, e.client.State()); err == nil {
		t.Fatal("whole encoding")
	}
}

func populatedState(t *testing.T, e *testEnv) *kuma.State {
	e.ok(t, "POST", "/monitors", `{"type":"group","name":"g"}`)
	return e.client.State()
}
