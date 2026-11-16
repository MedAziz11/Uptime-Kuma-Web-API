package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// errorCase is a request and the answer it must get: a status and a text
// detail, or the type and loc of the first field error (as in
// TestKumaRoutesErrors).
type errorCase struct {
	method, path, body string
	status             int
	detail             string
}

func (e *testEnv) checkErrors(t *testing.T, cases []errorCase) {
	t.Helper()
	for _, c := range cases {
		status, v := e.call(t, c.method, c.path, c.body)
		if status != c.status {
			t.Errorf("%s %s %s: %d %v, want %d", c.method, c.path, c.body, status, v, c.status)
			continue
		}
		var got string
		switch d := v.(map[string]any)["detail"].(type) {
		case string:
			got = d
		case []any:
			first := d[0].(map[string]any)
			var loc []string
			for _, l := range first["loc"].([]any) {
				loc = append(loc, fmt.Sprint(l))
			}
			got = fmt.Sprint(first["type"]) + " " + strings.Join(loc, ".")
		}
		if !strings.HasPrefix(got, c.detail) {
			t.Errorf("%s %s %s: detail %q, want %q", c.method, c.path, c.body, got, c.detail)
		}
	}
}

// checkKumaFailures makes Uptime Kuma refuse each event and checks that the
// route answers 400 with its message.
func (e *testEnv) checkKumaFailures(t *testing.T, cases []struct{ event, method, path, body string }) {
	t.Helper()
	for _, c := range cases {
		e.kuma.Fail(c.event, "Something went wrong")
		status, v := e.call(t, c.method, c.path, c.body)
		e.kuma.Fail(c.event, "")
		if status != 400 || get[string](t, v, "detail") != "Something went wrong" {
			t.Errorf("%s (%s %s): %d %v", c.event, c.method, c.path, status, v)
		}
	}
}

func TestAPIKeyRoutes(t *testing.T) {
	e := newEnv(t, Options{})
	if keys := get[[]any](t, e.ok(t, "GET", "/api_keys", ""), "api_keys"); len(keys) != 0 {
		t.Fatalf("keys %v", keys)
	}
	created := e.ok(t, "POST", "/api_keys", `{"name":"ci"}`)
	id := int(get[float64](t, created, "keyID"))
	if created["msg"] != "Added Successfully." || created["key"] != fmt.Sprintf("uk%d_fake-clear-key", id) {
		t.Fatalf("create %v", created)
	}
	path := fmt.Sprintf("/api_keys/%d", id)
	k := e.ok(t, "GET", path, "")["api_key"].(map[string]any)
	if k["name"] != "ci" || k["active"] != true || k["expires"] != nil || k["key"] != nil {
		t.Fatalf("key %v", k)
	}
	if r := e.ok(t, "POST", path+"/disable", ""); r["msg"] != "Disabled Successfully." {
		t.Fatalf("disable %v", r)
	}
	if k := get[[]any](t, e.ok(t, "GET", "/api_keys", ""), "api_keys"); len(k) != 1 || get[bool](t, k, 0, "active") || get[string](t, k, 0, "status") != "inactive" {
		t.Fatalf("after disable %v", k)
	}
	if r := e.ok(t, "POST", path+"/enable", ""); r["msg"] != "Enabled Successfully." {
		t.Fatalf("enable %v", r)
	}
	if e.ok(t, "GET", path, "")["api_key"].(map[string]any)["active"] != true {
		t.Fatal("enable not visible")
	}
	// Other fields pass through; active and expires as sent.
	other := e.ok(t, "POST", "/api_keys", `{"name":"later","active":false,"expires":"2099-01-01 00:00:00","extra":1}`)
	oid := int(get[float64](t, other, "keyID"))
	if k := e.ok(t, "GET", fmt.Sprintf("/api_keys/%d", oid), "")["api_key"].(map[string]any); k["active"] != false || k["expires"] != "2099-01-01 00:00:00" {
		t.Fatalf("second key %v", k)
	}
	if r := e.ok(t, "DELETE", path, ""); r["msg"] != "Deleted Successfully." {
		t.Fatalf("delete %v", r)
	}
	e.ok(t, "DELETE", fmt.Sprintf("/api_keys/%d", oid), "")
	if status, _ := e.call(t, "GET", path, ""); status != 404 {
		t.Fatalf("deleted key: %d", status)
	}
	if n := e.kuma.Counts()["api keys"]; n != 0 {
		t.Fatalf("%d keys left", n)
	}

	e.checkErrors(t, []errorCase{
		{"GET", "/api_keys/99999", "", 404, "API key does not exist"},
		{"POST", "/api_keys/99999/enable", "", 404, "API key does not exist"},
		{"POST", "/api_keys/99999/disable", "", 404, "API key does not exist"},
		{"DELETE", "/api_keys/99999", "", 404, "API key does not exist"},
		{"GET", "/api_keys/x", "", 422, "int_parsing path.api_key_id"},
		{"POST", "/api_keys/x/enable", "", 422, "int_parsing path.api_key_id"},
		{"POST", "/api_keys", `{}`, 422, "missing 1 required argument: 'name'"},
		{"POST", "/api_keys", `{"name":1}`, 422, "string_type body.name"},
		{"POST", "/api_keys", `{"name":"x","active":"yes"}`, 422, "bool_parsing body.active"},
		{"POST", "/api_keys", `[]`, 422, "dict_type body"},
	})

	kid := int(get[float64](t, e.ok(t, "POST", "/api_keys", `{"name":"f"}`), "keyID"))
	e.checkKumaFailures(t, []struct{ event, method, path, body string }{
		{"addAPIKey", "POST", "/api_keys", `{"name":"x"}`},
		{"enableAPIKey", "POST", fmt.Sprintf("/api_keys/%d/enable", kid), ""},
		{"disableAPIKey", "POST", fmt.Sprintf("/api_keys/%d/disable", kid), ""},
		{"deleteAPIKey", "DELETE", fmt.Sprintf("/api_keys/%d", kid), ""},
	})
}

func TestRemoteBrowserRoutes(t *testing.T) {
	e := newEnv(t, Options{})
	created := e.ok(t, "POST", "/remote_browsers", `{"name":"chrome","url":"ws://chrome:3000/playwright"}`)
	if created["msg"] != "Saved." {
		t.Fatalf("create %v", created)
	}
	id := int(get[float64](t, created, "id"))
	path := fmt.Sprintf("/remote_browsers/%d", id)
	b := e.ok(t, "GET", path, "")["remote_browser"].(map[string]any)
	if b["name"] != "chrome" || b["url"] != "ws://chrome:3000/playwright" {
		t.Fatalf("browser %v", b)
	}
	e.ok(t, "PATCH", path, `{"name":"renamed"}`)
	if l := get[[]any](t, e.ok(t, "GET", "/remote_browsers", ""), "remote_browsers"); len(l) != 1 || get[string](t, l, 0, "name") != "renamed" || get[string](t, l, 0, "url") != "ws://chrome:3000/playwright" {
		t.Fatalf("after patch %v", l)
	}
	if status, v := e.call(t, "POST", "/remote_browsers/test", `{"url":"ws://nowhere"}`); status != 400 || !strings.Contains(get[string](t, v, "detail"), "ECONNREFUSED") {
		t.Fatalf("test: %d %v", status, v)
	}
	if r := e.ok(t, "DELETE", path, ""); r["msg"] != "Deleted Successfully." {
		t.Fatalf("delete %v", r)
	}
	if status, _ := e.call(t, "GET", path, ""); status != 404 {
		t.Fatalf("deleted browser: %d", status)
	}

	e.checkErrors(t, []errorCase{
		{"GET", "/remote_browsers/99999", "", 404, "remote browser does not exist"},
		{"PATCH", "/remote_browsers/99999", `{}`, 404, "remote browser does not exist"},
		{"DELETE", "/remote_browsers/99999", "", 404, "remote browser does not exist"},
		{"GET", "/remote_browsers/x", "", 422, "int_parsing path.remote_browser_id"},
		{"POST", "/remote_browsers", `{"name":"x"}`, 422, "missing 1 required argument: 'url'"},
		{"POST", "/remote_browsers", `{}`, 422, "missing 2 required argument: 'name', 'url'"},
		{"POST", "/remote_browsers", `{"name":"x","url":1}`, 422, "string_type body.url"},
		{"POST", "/remote_browsers/test", `{}`, 422, "missing 1 required argument: 'url'"},
		{"POST", "/remote_browsers/test", `[]`, 422, "dict_type body"},
	})
	bid := int(get[float64](t, e.ok(t, "POST", "/remote_browsers", `{"name":"b","url":"ws://b"}`), "id"))
	if status, v := e.call(t, "PATCH", fmt.Sprintf("/remote_browsers/%d", bid), `{"url":null}`); status != 422 || get[string](t, v, "detail") != "missing 1 required argument: 'url'" {
		t.Fatalf("patch url null: %d %v", status, v)
	}
	if status, _ := e.call(t, "PATCH", fmt.Sprintf("/remote_browsers/%d", bid), `[]`); status != 422 {
		t.Fatalf("patch list: %d", status)
	}
	e.checkKumaFailures(t, []struct{ event, method, path, body string }{
		{"addRemoteBrowser", "POST", "/remote_browsers", `{"name":"x","url":"ws://x"}`},
		{"addRemoteBrowser", "PATCH", fmt.Sprintf("/remote_browsers/%d", bid), `{}`},
		{"deleteRemoteBrowser", "DELETE", fmt.Sprintf("/remote_browsers/%d", bid), ""},
	})
}

func TestIncidentRoutes(t *testing.T) {
	e := newEnv(t, Options{})
	e.ok(t, "POST", "/statuspages", `{"slug":"page","title":"Page"}`)
	if h := e.ok(t, "GET", "/statuspages/page/incidents", ""); fmt.Sprint(h) != "map[hasMore:false incidents:[] nextCursor:<nil> total:0]" {
		t.Fatalf("empty history %v", h)
	}
	var ids []int
	for i := range 12 {
		inc := e.ok(t, "POST", "/statuspages/page/incident", fmt.Sprintf(`{"title":"t%d","content":"c","style":"info"}`, i))
		ids = append(ids, int(get[float64](t, inc, "id")))
	}
	h := e.ok(t, "GET", "/statuspages/page/incidents", "")
	if len(get[[]any](t, h, "incidents")) != 10 || h["hasMore"] != true || h["total"] != 12.0 {
		t.Fatalf("history %v", h)
	}
	h2 := e.ok(t, "GET", "/statuspages/page/incidents?cursor="+strings.ReplaceAll(get[string](t, h, "nextCursor"), " ", "%20"), "")
	if l := get[[]any](t, h2, "incidents"); len(l) != 2 || get[float64](t, l, 1, "id") != float64(ids[0]) {
		t.Fatalf("second page %v", h2)
	}

	// The oldest incident is on the second page: PATCH finds it there.
	path := fmt.Sprintf("/statuspages/page/incidents/%d", ids[0])
	r := e.ok(t, "PATCH", path, `{"content":"updated","pin":false}`)
	if r["msg"] != "Saved." || get[string](t, r, "incident", "title") != "t0" || get[string](t, r, "incident", "content") != "updated" ||
		get[string](t, r, "incident", "style") != "info" || get[bool](t, r, "incident", "pin") {
		t.Fatalf("patch %v", r)
	}
	newest := fmt.Sprintf("/statuspages/page/incidents/%d", ids[11])
	if get[float64](t, e.ok(t, "GET", "/statuspages/page", ""), "incident", "id") != float64(ids[11]) {
		t.Fatal("newest pinned incident not shown")
	}
	r = e.ok(t, "POST", newest+"/resolve", "")
	if r["msg"] != "Resolved" || get[bool](t, r, "incident", "active") || get[bool](t, r, "incident", "pin") {
		t.Fatalf("resolve %v", r)
	}
	if get[float64](t, e.ok(t, "GET", "/statuspages/page", ""), "incident", "id") != float64(ids[10]) {
		t.Fatal("resolved incident still shown")
	}
	if r := e.ok(t, "DELETE", newest, ""); r["msg"] != "Deleted Successfully." {
		t.Fatalf("delete %v", r)
	}
	if get[float64](t, e.ok(t, "GET", "/statuspages/page/incidents", ""), "total") != 11 {
		t.Fatal("delete not visible")
	}

	// Created in one second, only the first page of the history is
	// reachable; a pinned incident beyond it is found on the public page.
	e.kuma.IncidentsInOneSecond()
	if h := e.ok(t, "GET", "/statuspages/page/incidents", ""); h["hasMore"] != false || len(get[[]any](t, h, "incidents")) != 10 {
		t.Fatalf("history in one second %v", h)
	}
	oldest := fmt.Sprintf("/statuspages/page/incidents/%d", ids[1])
	if r := e.ok(t, "PATCH", oldest, `{"title":"found"}`); get[string](t, r, "incident", "title") != "found" {
		t.Fatalf("patch beyond the history %v", r)
	}
	// ids[0] is unpinned: not on the public page either.
	if status, _ := e.call(t, "PATCH", path, `{}`); status != 404 {
		t.Fatalf("unpinned incident beyond the history: %d", status)
	}

	e.checkErrors(t, []errorCase{
		{"GET", "/statuspages/nope/incidents", "", 404, "slug is not found"},
		{"PATCH", "/statuspages/nope/incidents/1", `{}`, 404, "slug is not found"},
		{"PATCH", "/statuspages/page/incidents/99999", `{}`, 404, "incident does not exist"},
		{"DELETE", "/statuspages/page/incidents/99999", "", 404, "Incident not found or access denied"},
		{"POST", "/statuspages/page/incidents/99999/resolve", "", 404, "Incident not found or access denied"},
		{"DELETE", "/statuspages/nope/incidents/1", "", 404, "slug is not found"},
		{"PATCH", "/statuspages/page/incidents/x", `{}`, 422, "int_parsing path.incident_id"},
		{"PATCH", oldest, `{"style":"pink"}`, 422, "enum body.style"},
		{"PATCH", oldest, `{"pin":"no"}`, 422, "bool_parsing body.pin"},
		{"PATCH", oldest, `{"title":null}`, 422, "missing 1 required argument: 'title'"},
		{"PATCH", oldest, `{"title":" "}`, 400, "Please input title"},
		{"PATCH", path, `[]`, 422, "model_attributes_type body"},
	})
	e.checkKumaFailures(t, []struct{ event, method, path, body string }{
		{"getIncidentHistory", "GET", "/statuspages/page/incidents", ""},
		{"getIncidentHistory", "PATCH", oldest, `{}`},
		{"editIncident", "PATCH", oldest, `{}`},
		{"deleteIncident", "DELETE", path, ""},
		{"resolveIncident", "POST", path + "/resolve", ""},
	})
}

func TestChartAndImportantBeatRoutes(t *testing.T) {
	e := newEnv(t, Options{})
	id := int(get[float64](t, e.ok(t, "POST", "/monitors", `{"type":"http","name":"m","url":"http://x"}`), "monitorID"))
	for i := range 3 {
		e.kuma.Beat(id, i%2, 10+i)
	}
	chart := get[[]any](t, e.ok(t, "GET", fmt.Sprintf("/monitors/%d/chart?period=1.5", id), ""), "chart_data")
	if len(chart) != 3 || get[float64](t, chart, 1, "up") != 1 || get[float64](t, chart, 1, "avgPing") != 11 {
		t.Fatalf("chart %v", chart)
	}
	if c := get[[]any](t, e.ok(t, "GET", fmt.Sprintf("/monitors/%d/chart", id), ""), "chart_data"); len(c) != 3 {
		t.Fatalf("default period %v", c)
	}
	r := e.ok(t, "GET", fmt.Sprintf("/monitors/%d/important_beats?offset=1&limit=1", id), "")
	if beats := get[[]any](t, r, "important_beats"); r["total"] != 3.0 || len(beats) != 1 || get[float64](t, beats, 0, "ping") != 11 {
		t.Fatalf("important beats %v", r)
	}
	if r := e.ok(t, "GET", fmt.Sprintf("/monitors/%d/important_beats", id), ""); len(get[[]any](t, r, "important_beats")) != 3 {
		t.Fatalf("default page %v", r)
	}

	e.checkErrors(t, []errorCase{
		{"GET", "/monitors/99999/chart", "", 404, "Not found"},
		{"GET", "/monitors/99999/important_beats", "", 404, "Not found"},
		{"GET", fmt.Sprintf("/monitors/%d/chart?period=0", id), "", 422, "greater_than query.period"},
		{"GET", fmt.Sprintf("/monitors/%d/chart?period=x", id), "", 422, "float_parsing query.period"},
		{"GET", fmt.Sprintf("/monitors/%d/important_beats?limit=0", id), "", 422, "greater_than_equal query.limit"},
		{"GET", fmt.Sprintf("/monitors/%d/important_beats?limit=1001", id), "", 422, "less_than_equal query.limit"},
		{"GET", fmt.Sprintf("/monitors/%d/important_beats?offset=-1", id), "", 422, "greater_than_equal query.offset"},
		{"GET", "/monitors/x/chart", "", 422, "int_parsing path.monitor_id"},
	})
	e.checkKumaFailures(t, []struct{ event, method, path, body string }{
		{"getMonitorChartData", "GET", fmt.Sprintf("/monitors/%d/chart", id), ""},
		{"monitorImportantHeartbeatListCount", "GET", fmt.Sprintf("/monitors/%d/important_beats", id), ""},
		{"monitorImportantHeartbeatListPaged", "GET", fmt.Sprintf("/monitors/%d/important_beats", id), ""},
	})
}

// Acks without the expected data are internal errors, not empty answers.
func TestChartAndImportantBeatsMalformed(t *testing.T) {
	e := newEnv(t, Options{})
	id := int(get[float64](t, e.ok(t, "POST", "/monitors", `{"type":"http","name":"m","url":"http://x"}`), "monitorID"))
	e.srv.kuma = malformedAcks{Client: e.client}
	for _, path := range []string{"/monitors/%d/chart", "/monitors/%d/important_beats"} {
		if status, _ := e.call(t, "GET", fmt.Sprintf(path, id), ""); status != 500 {
			t.Errorf("%s: %d", path, status)
		}
	}
	e.srv.kuma = malformedAcks{Client: e.client, pageOnly: true}
	if status, _ := e.call(t, "GET", fmt.Sprintf("/monitors/%d/important_beats", id), ""); status != 500 {
		t.Errorf("malformed page: %d", status)
	}
}

// malformedAcks answers {"ok": true} for the chart and heartbeat reads;
// with pageOnly, only for the page of heartbeats.
type malformedAcks struct {
	*kuma.Client
	pageOnly bool
}

func (m malformedAcks) MonitorChartData(context.Context, int64, float64) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func (m malformedAcks) ImportantHeartbeatCount(ctx context.Context, id int64) (json.RawMessage, error) {
	if m.pageOnly {
		return m.Client.ImportantHeartbeatCount(ctx, id)
	}
	return json.RawMessage(`{"ok":true}`), nil
}

func (m malformedAcks) ImportantHeartbeats(context.Context, int64, int64, int64) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func TestPushExampleRoute(t *testing.T) {
	e := newEnv(t, Options{})
	if r := e.ok(t, "GET", "/push_examples/javascript-fetch", ""); r["code"] != "fetch(pushURL)\n" {
		t.Fatalf("example %v", r)
	}
	e.checkErrors(t, []errorCase{
		{"GET", "/push_examples/cobol", "", 404, "Not found"},
		{"GET", "/push_examples/Bad_Lang", "", 400, "Invalid language"},
	})
	e.checkKumaFailures(t, []struct{ event, method, path, body string }{
		{"getPushExample", "GET", "/push_examples/javascript-fetch", ""},
	})
}

// Commands an older Uptime Kuma does not have answer 501 at once, without
// a call that would only end at the timeout.
func TestCommandsOfANewerKuma(t *testing.T) {
	e := newEnv(t, Options{})
	e.kuma.SetVersion("2.0.0")
	e.kuma.CachePublicPages(true)
	e.ok(t, "POST", "/statuspages", `{"slug":"page","title":"Page"}`)
	if page := e.ok(t, "GET", "/statuspages/page", ""); page["incident"] != nil {
		t.Fatalf("incident before posting one: %v", page["incident"])
	}
	inc := e.ok(t, "POST", "/statuspages/page/incident", `{"title":"t","content":"c"}`)
	path := fmt.Sprintf("/statuspages/page/incidents/%d", int(get[float64](t, inc, "id")))
	const detail = "Uptime Kuma 2.0.0 does not support "
	e.checkErrors(t, []errorCase{
		{"GET", "/statuspages/page/incidents", "", 501, detail + "getIncidentHistory: it needs Uptime Kuma 2.1.0 or later"},
		{"PATCH", path, `{"title":"x"}`, 501, detail + "getIncidentHistory"},
		{"DELETE", path, "", 501, detail + "deleteIncident"},
		{"POST", path + "/resolve", "", 501, detail + "resolveIncident"},
	})
	for _, ev := range []string{"getIncidentHistory", "editIncident", "deleteIncident", "resolveIncident"} {
		if n := e.kuma.Calls(ev); n != 0 {
			t.Errorf("%s sent %d times", ev, n)
		}
	}
	// Before 2.1.0 the public page has the pinned incident only.
	if page := e.ok(t, "GET", "/statuspages/page", ""); get[string](t, page, "incident", "title") != "t" {
		t.Errorf("incident of a 2.0.0 page: %v", page["incident"])
	}

	// Before 2.1.0 Uptime Kuma pushes nothing after a monitor tag command:
	// the monitor list is fetched again.
	id := int(get[float64](t, e.ok(t, "POST", "/monitors", `{"type":"http","name":"m","url":"http://x"}`), "monitorID"))
	tag := int(get[float64](t, e.ok(t, "POST", "/tags", `{"name":"t","color":"#000000"}`), "id"))
	before := e.kuma.Calls("getMonitorList")
	e.ok(t, "POST", fmt.Sprintf("/monitors/%d/tag", id), fmt.Sprintf(`{"tag_id":%d,"value":"v"}`, tag))
	if e.kuma.Calls("getMonitorList") == before {
		t.Error("the monitor list was not fetched after addMonitorTag")
	}
	if tags := get[[]any](t, e.ok(t, "GET", fmt.Sprintf("/monitors/%d", id), ""), "monitor", "tags"); len(tags) != 1 {
		t.Errorf("tags after addMonitorTag: %v", tags)
	}
}

// A field the server's database has no column for is a 400 that names it.
func TestFieldOfANewerKuma(t *testing.T) {
	e := newEnv(t, Options{})
	e.kuma.Fail("add", "SQLITE_ERROR: table monitor has no column named bearer_token")
	e.checkErrors(t, []errorCase{{"POST", "/monitors", `{"type":"http","name":"m","url":"http://x","bearer_token":"t"}`, 400,
		`this Uptime Kuma has no field "bearer_token": it was added by a later Uptime Kuma release`}})
	e.kuma.Fail("add", "ER_BAD_FIELD_ERROR: Unknown column 'gamedig_token' in 'field list'")
	e.checkErrors(t, []errorCase{{"POST", "/monitors", `{"type":"http","name":"m","url":"http://x"}`, 400,
		`this Uptime Kuma has no field "gamedig_token"`}})
}
