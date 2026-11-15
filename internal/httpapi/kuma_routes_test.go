package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// call sends a request as admin and decodes the JSON answer.
func (e *testEnv) call(t *testing.T, method, path, body string) (int, any) {
	t.Helper()
	if e.adminToken == "" {
		e.adminToken = e.login(t, "admin", "admin-password")
	}
	res := e.do(t, method, path, e.adminToken, "application/json", body)
	b, _ := io.ReadAll(res.Body)
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s %s: %d %q", method, path, res.StatusCode, b)
	}
	return res.StatusCode, v
}

// ok sends a request that must answer 200 and returns the answer.
func (e *testEnv) ok(t *testing.T, method, path, body string) map[string]any {
	t.Helper()
	status, v := e.call(t, method, path, body)
	if status != 200 {
		t.Fatalf("%s %s: %d %v", method, path, status, v)
	}
	m, _ := v.(map[string]any)
	return m
}

func get[T any](t *testing.T, v any, path ...any) T {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			v = v.(map[string]any)[k]
		case int:
			v = v.([]any)[k]
		}
	}
	out, ok := v.(T)
	if !ok {
		t.Fatalf("%v at %v: %T", v, path, v)
	}
	return out
}

// The whole API against the fake Uptime Kuma: each write is visible to the
// next read, and the answers have 2.0's shapes.
func TestKumaRoutesLifecycle(t *testing.T) {
	e := newEnv(t, Options{})

	// Monitors.
	created := e.ok(t, "POST", "/monitors", `{"type":"http","name":"web","url":"http://x","interval":30,"notificationIDList":[]}`)
	if created["msg"] != "Added Successfully." || created["msgi18n"] != nil || created["ok"] != nil {
		t.Fatalf("create %v", created)
	}
	id := int(get[float64](t, created, "monitorID"))
	m := e.ok(t, "GET", fmt.Sprintf("/monitors/%d", id), "")["monitor"].(map[string]any)
	if m["name"] != "web" || m["interval"] != 30.0 || m["active"] != true || m["authMethod"] != "" {
		t.Fatalf("monitor %v", m)
	}
	if ids := get[[]any](t, m, "notificationIDList"); len(ids) != 0 {
		t.Fatalf("notificationIDList %v", ids)
	}
	if list := get[[]any](t, e.ok(t, "GET", "/monitors", ""), "monitors"); len(list) != 1 {
		t.Fatalf("monitors %v", list)
	}
	patched := e.ok(t, "PATCH", fmt.Sprintf("/monitors/%d", id), `{"name":"renamed","maxretries":2,"custom":"kept"}`)
	if fmt.Sprint(patched["monitor_data"]) != "map[custom:kept maxretries:2 name:renamed]" || patched["msg"] != "Saved." {
		t.Fatalf("patch %v", patched)
	}
	m = e.ok(t, "GET", fmt.Sprintf("/monitors/%d", id), "")["monitor"].(map[string]any)
	if m["name"] != "renamed" || m["maxretries"] != 2.0 || m["interval"] != 30.0 || m["custom"] != "kept" {
		t.Fatalf("after patch %v", m)
	}
	if r := e.ok(t, "POST", fmt.Sprintf("/monitors/%d/pause", id), ""); r["msg"] != "Paused Successfully." {
		t.Fatalf("pause %v", r)
	}
	if e.ok(t, "GET", fmt.Sprintf("/monitors/%d", id), "")["monitor"].(map[string]any)["active"] != false {
		t.Fatal("pause not visible")
	}
	e.ok(t, "POST", fmt.Sprintf("/monitors/%d/resume", id), "")
	e.kuma.Beat(id, 1, 25)
	e.kuma.CertInfo(id, `{"valid":true}`)
	waitUntil(t, func() bool { return len(e.client.State().CertInfo()) == 1 })
	beats := get[[]any](t, e.ok(t, "GET", fmt.Sprintf("/monitors/%d/beats?hours=0.5", id), ""), "monitor_beats")
	if len(beats) != 1 || get[bool](t, beats, 0, "important") != true {
		t.Fatalf("beats %v", beats)
	}
	key := fmt.Sprint(id)
	if p := e.ok(t, "GET", "/ping", ""); p[key] != 25.0 {
		t.Fatalf("ping %v", p)
	}
	if u := e.ok(t, "GET", "/uptime", ""); fmt.Sprint(u[key]) != "map[1y:1 24:1 720:1]" {
		t.Fatalf("uptime %v", u)
	}
	if c := e.ok(t, "GET", "/cert_info", ""); fmt.Sprint(c[key]) != "map[valid:true]" {
		t.Fatalf("cert_info %v", c)
	}
	if v := e.ok(t, "GET", "/info", ""); v["version"] != "2.5.11" {
		t.Fatalf("info %v", v)
	}

	// Tags, and tags of a monitor.
	tag := e.ok(t, "POST", "/tags", `{"name":"t","color":"#fff"}`)
	tagID := int(get[float64](t, tag, "id"))
	if r := e.ok(t, "PATCH", fmt.Sprintf("/tags/%d", tagID), `{"color":"#000"}`); r["msg"] != "Saved." {
		t.Fatalf("patch tag %v", r)
	}
	if got := e.ok(t, "GET", fmt.Sprintf("/tags/%d", tagID), "")["tag"]; fmt.Sprint(got) != fmt.Sprintf("map[color:#000 id:%d name:t]", tagID) {
		t.Fatalf("tag %v", got)
	}
	e.ok(t, "POST", fmt.Sprintf("/monitors/%d/tag", id), fmt.Sprintf(`{"tag_id":%d,"value":"v"}`, tagID))
	tags := get[[]any](t, e.ok(t, "GET", fmt.Sprintf("/monitors/%d", id), ""), "monitor", "tags")
	if len(tags) != 1 || get[float64](t, tags, 0, "tag_id") != float64(tagID) {
		t.Fatalf("monitor tags %v", tags)
	}
	if status, _ := e.call(t, "DELETE", fmt.Sprintf("/monitors/%d/tag", id), fmt.Sprintf(`{"tag_id":%d,"value":"other"}`, tagID)); status != 404 {
		t.Fatalf("unknown monitor tag: %d", status)
	}
	e.ok(t, "DELETE", fmt.Sprintf("/monitors/%d/tag", id), fmt.Sprintf(`{"tag_id":%d,"value":"v"}`, tagID))
	if len(get[[]any](t, e.ok(t, "GET", "/tags", ""), "tags")) != 1 {
		t.Fatal("tags")
	}
	e.ok(t, "DELETE", fmt.Sprintf("/tags/%d", tagID), "")

	// Notifications, proxies, docker hosts.
	n := e.ok(t, "POST", "/notifications", `{"name":"hook","type":"webhook","webhookURL":"http://h"}`)
	nid := int(get[float64](t, n, "id"))
	got := e.ok(t, "GET", fmt.Sprintf("/notifications/%d", nid), "")["notification"].(map[string]any)
	if got["webhookURL"] != "http://h" || got["isDefault"] != false || got["config"] != nil {
		t.Fatalf("notification %v", got)
	}
	e.ok(t, "PATCH", fmt.Sprintf("/notifications/%d", nid), `{"name":"renamed"}`)
	if get[string](t, e.ok(t, "GET", "/notifications", ""), "notifications", 0, "name") != "renamed" {
		t.Fatal("notification rename not visible")
	}
	e.ok(t, "POST", "/notifications/test", `{"name":"hook","type":"webhook"}`)
	e.ok(t, "DELETE", fmt.Sprintf("/notifications/%d", nid), "")

	p := e.ok(t, "POST", "/proxies", `{"protocol":"http","host":"h","port":8080}`)
	pid := int(get[float64](t, p, "id"))
	e.ok(t, "PATCH", fmt.Sprintf("/proxies/%d", pid), `{"port":8081}`)
	proxy := e.ok(t, "GET", fmt.Sprintf("/proxies/%d", pid), "")["proxy"].(map[string]any)
	if proxy["port"] != 8081.0 || proxy["active"] != true || proxy["auth"] != false {
		t.Fatalf("proxy %v", proxy)
	}
	if len(get[[]any](t, e.ok(t, "GET", "/proxies", ""), "proxies")) != 1 {
		t.Fatal("proxies")
	}
	e.ok(t, "DELETE", fmt.Sprintf("/proxies/%d", pid), "")

	d := e.ok(t, "POST", "/docker_hosts", `{"name":"d","dockerType":"tcp"}`)
	did := int(get[float64](t, d, "id"))
	e.ok(t, "PATCH", fmt.Sprintf("/docker_hosts/%d", did), `{"name":"renamed","dockerDaemon":""}`)
	host := e.ok(t, "GET", fmt.Sprintf("/docker_hosts/%d", did), "")["docker_host"].(map[string]any)
	if host["name"] != "renamed" || host["dockerDaemon"] != "tcp://localhost:2375" {
		t.Fatalf("docker host %v", host)
	}
	if len(get[[]any](t, e.ok(t, "GET", "/docker_hosts", ""), "docker_hosts")) != 1 {
		t.Fatal("docker hosts")
	}
	if status, v := e.call(t, "POST", "/docker_hosts/test", `{"name":"d","dockerType":"socket"}`); status != 400 || get[string](t, v, "detail") != "connect ENOENT /var/run/docker.sock" {
		t.Fatalf("test docker host: %d %v", status, v)
	}
	e.ok(t, "DELETE", fmt.Sprintf("/docker_hosts/%d", did), "")

	// Maintenances.
	mt := e.ok(t, "POST", "/maintenance", `{"title":"w","strategy":"manual","weekdays":[]}`)
	mid := int(get[float64](t, mt, "maintenanceID"))
	if ws := get[[]any](t, e.ok(t, "GET", "/maintenance", ""), "maintenances"); len(ws) != 1 || get[string](t, ws, 0, "cron") != "30 3 * * *" {
		t.Fatalf("maintenances %v", ws)
	}
	up := e.ok(t, "PATCH", fmt.Sprintf("/maintenance/%d", mid), `{"title":"w2"}`)
	if fmt.Sprint(up["maintenance_data"]) != "map[title:w2]" {
		t.Fatalf("patch %v", up)
	}
	if get[string](t, e.ok(t, "GET", fmt.Sprintf("/maintenance/%d", mid), ""), "maintenance", "title") != "w2" {
		t.Fatal("maintenance rename")
	}
	e.ok(t, "POST", fmt.Sprintf("/maintenance/%d/monitors", mid), fmt.Sprintf(`[{"id":%d,"name":"x"}]`, id))
	_, linked := e.call(t, "GET", fmt.Sprintf("/maintenance/%d/monitors", mid), "")
	if fmt.Sprint(linked) != fmt.Sprintf("[map[id:%d]]", id) {
		t.Fatalf("linked %v", linked)
	}
	e.ok(t, "POST", fmt.Sprintf("/maintenance/%d/pause", mid), "")
	if get[bool](t, e.ok(t, "GET", "/maintenance", ""), "maintenances", 0, "active") {
		t.Fatal("pause not visible")
	}
	e.ok(t, "POST", fmt.Sprintf("/maintenance/%d/resume", mid), "")
	e.ok(t, "DELETE", fmt.Sprintf("/maintenance/%d", mid), "")

	// Status pages.
	if r := e.ok(t, "POST", "/statuspages", `{"slug":"page","title":"Page"}`); fmt.Sprint(r) != "map[msg:Added Successfully.]" {
		t.Fatalf("add page %v", r)
	}
	if len(get[[]any](t, e.ok(t, "GET", "/statuspages", ""), "statuspages")) != 1 {
		t.Fatal("status page list")
	}
	saved := e.ok(t, "POST", "/statuspages/page", fmt.Sprintf(`{"title":"Renamed","theme":"","googleAnalyticsId":"G-1","icon":"","publicGroupList":[{"name":"g","monitorList":[{"id":%d,"sendUrl":1}]}]}`, id))
	if len(get[[]any](t, saved, "detail", "publicGroupList")) != 1 {
		t.Fatalf("save %v", saved)
	}
	page := e.ok(t, "GET", "/statuspages/page", "")
	if page["title"] != "Renamed" || page["theme"] != "auto" || page["analyticsId"] != "G-1" || page["analyticsType"] != "google" || page["incident"] != nil {
		t.Fatalf("page %v", page)
	}
	if get[bool](t, page, "publicGroupList", 0, "monitorList", 0, "sendUrl") != true {
		t.Fatalf("groups %v", page["publicGroupList"])
	}
	// Saving without groups keeps them.
	e.ok(t, "POST", "/statuspages/page", `{"description":"d"}`)
	if len(get[[]any](t, e.ok(t, "GET", "/statuspages/page", ""), "publicGroupList")) != 1 {
		t.Fatal("groups lost")
	}
	inc := e.ok(t, "POST", "/statuspages/page/incident", `{"title":"Down","content":"c"}`)
	if inc["style"] != "primary" || inc["title"] != "Down" {
		t.Fatalf("incident %v", inc)
	}
	if get[string](t, e.ok(t, "GET", "/statuspages/page", ""), "incident", "title") != "Down" {
		t.Fatal("incident not visible")
	}
	if r := e.ok(t, "DELETE", "/statuspages/page/incident/unpin", ""); r["detail"] != "success" {
		t.Fatalf("unpin %v", r)
	}
	if e.ok(t, "GET", "/statuspages/page", "")["incident"] != nil {
		t.Fatal("incident still pinned")
	}
	if r := e.ok(t, "DELETE", "/statuspages/page", ""); r["detail"] != "success" {
		t.Fatalf("delete page %v", r)
	}

	// Database, and the end of the monitor.
	if s := e.ok(t, "GET", "/database/size", ""); s["unit"] != "octet" || s["size"] != 61440.0 {
		t.Fatalf("size %v", s)
	}
	if s := e.ok(t, "POST", "/database/shrink", ""); fmt.Sprint(s) != "map[details:map[] message:DB Shrinked]" {
		t.Fatalf("shrink %v", s)
	}
	if r := e.ok(t, "DELETE", fmt.Sprintf("/monitors/%d", id), ""); r["msg"] != "Deleted Successfully." {
		t.Fatalf("delete %v", r)
	}
	for kind, n := range e.kuma.Counts() {
		if n != 0 {
			t.Errorf("%d %s left in the fake", n, kind)
		}
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

// 2.0's answers to invalid requests and unknown ids.
func TestKumaRoutesErrors(t *testing.T) {
	e := newEnv(t, Options{})
	mon := int(get[float64](t, e.ok(t, "POST", "/monitors", `{"type":"http","name":"m","url":"http://x"}`), "monitorID"))
	maint := int(get[float64](t, e.ok(t, "POST", "/maintenance", `{"title":"w","strategy":"manual"}`), "maintenanceID"))
	e.ok(t, "POST", "/statuspages", `{"slug":"page","title":"Page"}`)
	tag := int(get[float64](t, e.ok(t, "POST", "/tags", `{"name":"t","color":"#fff"}`), "id"))

	for _, c := range []struct {
		method, path, body string
		status             int
		detail             string // a text detail, or the type and loc of the first field error
	}{
		// Unknown ids.
		{"GET", "/monitors/99999", "", 404, "Not found"},
		{"PATCH", "/monitors/99999", `{"name":"x"}`, 404, "Not found"},
		{"DELETE", "/monitors/99999", "", 404, "monitor does not exist"},
		{"POST", "/monitors/99999/pause", "", 404, "Not found"},
		{"POST", "/monitors/99999/resume", "", 404, "Not found"},
		{"POST", "/monitors/99999/tag", `{"tag_id":99999}`, 404, "Not found"},
		{"POST", fmt.Sprintf("/monitors/%d/tag", mon), `{"tag_id":99999}`, 404, "tag does not exist"},
		{"DELETE", "/monitors/99999/tag", `{"tag_id":1}`, 404, "monitor tag does not exist"},
		{"GET", "/tags/99999", "", 404, "tag does not exist"},
		{"PATCH", "/tags/99999", `{"name":"x"}`, 404, "tag does not exist"},
		{"DELETE", "/tags/99999", "", 404, "tag does not exist"},
		{"GET", "/notifications/99999", "", 404, "notification does not exist"},
		{"PATCH", "/notifications/99999", `{"name":"x"}`, 404, "notification does not exist"},
		{"DELETE", "/notifications/99999", "", 404, "notification does not exist"},
		{"GET", "/proxies/99999", "", 404, "proxy does not exist"},
		{"PATCH", "/proxies/99999", `{}`, 404, "proxy does not exist"},
		{"DELETE", "/proxies/99999", "", 404, "proxy does not exist"},
		{"GET", "/docker_hosts/99999", "", 404, "docker host does not exist"},
		{"PATCH", "/docker_hosts/99999", `{}`, 404, "docker host does not exist"},
		{"DELETE", "/docker_hosts/99999", "", 404, "docker host does not exist"},
		{"GET", "/maintenance/99999", "", 404, "Not found"},
		{"PATCH", "/maintenance/99999", `{"title":"x"}`, 404, "Not found"},
		{"DELETE", "/maintenance/99999", "", 404, "maintenance does not exist"},
		{"POST", "/maintenance/99999/pause", "", 404, "Maintenance not found"},
		{"POST", "/maintenance/99999/resume", "", 404, "Maintenance not found"},
		{"POST", "/maintenance/99999/monitors", `[]`, 404, "Not found"},
		{"GET", "/statuspages/nope", "", 404, "Not found"},
		{"POST", "/statuspages/nope", `{"title":"x"}`, 404, "Not found"},
		{"DELETE", "/statuspages/nope", "", 404, "status page does not exist"},
		{"POST", "/statuspages/nope/incident", `{"title":"a","content":"b"}`, 404, "slug is not found"},
		{"DELETE", "/statuspages/nope/incident/unpin", "", 404, "Not found"},
		// Kuma's own validation.
		{"POST", "/statuspages", `{"slug":"Bad Slug!","title":"x"}`, 400, "Invalid Slug"},
		{"POST", "/statuspages/page", `{"analyticsType":"nope"}`, 400, "Invalid analytics type"},
		// 2.0's argument checks.
		{"POST", "/monitors", `{"type":"http","name":"bad","interval":1}`, 422, "the value of interval must not be less than 20"},
		{"POST", "/monitors", `{"type":"port","name":"p","hostname":"h","port":70000}`, 422, "the value of port must not be larger than 65535"},
		{"POST", "/monitors", `{"type":"http","name":"x","url":null}`, 422, "missing 1 required argument: 'url'"},
		{"POST", "/monitors", `{"type":"radius","name":"x"}`, 422, "missing 5 required argument: 'radiusUsername', 'radiusPassword', 'radiusSecret', 'radiusCalledStationId', 'radiusCallingStationId'"},
		{"POST", "/monitors", `{"type":"http","name":"x","url":"u","accepted_statuscodes":["abc"]}`, 422, `Unknown accepted_statuscodes value: "abc"`},
		{"POST", "/monitors", `{"type":"dns","name":"x","hostname":"h","dns_resolve_type":"XX"}`, 422, `Unknown dns_resolve_type value: "XX"`},
		{"POST", "/monitors", `{"type":"kafka-producer","name":"x","kafkaProducerTopic":"t","kafkaProducerMessage":"m","kafkaProducerSaslOptions":{"mechanism":"x"}}`, 422, `Unknown kafkaProducerSaslOptions["mechanism"] value: "x"`},
		{"PATCH", fmt.Sprintf("/monitors/%d", mon), `{"retryInterval":5}`, 422, "the value of retryInterval must not be less than 20"},
		{"PATCH", fmt.Sprintf("/tags/%d", tag), `{"name":null}`, 422, "missing 1 required argument: 'name'"},
		{"POST", "/notifications", `{"name":"x"}`, 422, "missing 1 required argument: 'type'"},
		{"POST", "/notifications", `{"name":"x","type":"webhook","notAnOption":1}`, 422, "unknown argument 'notAnOption'"},
		{"POST", "/notifications/test", `{"type":"webhook"}`, 422, "missing 1 required argument: 'name'"},
		{"POST", "/proxies", `{"host":"x"}`, 422, "missing 2 required argument: 'protocol', 'port'"},
		{"POST", "/proxies", `{"protocol":"http","host":"x","port":1,"auth":true}`, 422, "missing 2 required argument: 'username', 'password'"},
		{"POST", "/docker_hosts", `{"name":"x"}`, 422, "missing 1 required argument: 'dockerType'"},
		{"POST", "/docker_hosts/test", `{"name":"x"}`, 422, "missing 1 required argument: 'dockerType'"},
		{"POST", "/maintenance", `{"title":"x","strategy":"manual","intervalDay":0}`, 422, "the value of intervalDay must not be less than 1"},
		{"PATCH", fmt.Sprintf("/maintenance/%d", maint), `{"strategy":"recurring-weekday","dateRange":null}`, 422, "missing 1 required argument: 'dateRange'"},
		// Field errors: type and loc.
		{"POST", "/monitors", `{"name":"no type"}`, 422, "missing body.type"},
		{"POST", "/monitors", `{"type":"nope","name":"x"}`, 422, "enum body.type"},
		{"POST", "/monitors", `{"type":"http","name":"x","interval":"x"}`, 422, "int_parsing body.interval"},
		{"POST", "/monitors", `{"type":"http","name":"x","interval":1.5}`, 422, "int_from_float body.interval"},
		{"POST", "/monitors", `{"type":"http","name":"x","interval":true}`, 422, "int_type body.interval"},
		{"POST", "/monitors", `{"type":"http","name":"x","upsideDown":"yes"}`, 422, "bool_parsing body.upsideDown"},
		{"POST", "/monitors", `{"type":"http","name":"x","upsideDown":1}`, 422, "bool_type body.upsideDown"},
		{"POST", "/monitors", `{"type":"http","name":"x","timeout":"x"}`, 422, "float_parsing body.timeout"},
		{"POST", "/monitors", `{"type":"http","name":"x","timeout":[]}`, 422, "float_type body.timeout"},
		{"POST", "/monitors", `{"type":"http","name":"x","notificationIDList":{}}`, 422, "list_type body.notificationIDList"},
		{"POST", "/monitors", `{"type":"http","name":"x","notificationIDList":["a"]}`, 422, "int_parsing body.notificationIDList.0"},
		{"POST", "/monitors", `[]`, 422, "model_attributes_type body"},
		{"POST", "/monitors", `{"type":"http",`, 422, "json_invalid body.15"},
		{"PATCH", fmt.Sprintf("/monitors/%d", mon), `{"name":null}`, 422, "missing 1 required argument: 'name'"},
		{"GET", "/monitors/abc", "", 422, "int_parsing path.monitor_id"},
		{"GET", "/monitors/1/beats?hours=0", "", 422, "greater_than query.hours"},
		{"GET", "/monitors/1/beats?hours=x", "", 422, "float_parsing query.hours"},
		{"POST", "/tags", `{}`, 422, "missing body.name"},
		{"POST", fmt.Sprintf("/monitors/%d/tag", mon), `{"tag_id":1,"value":null}`, 422, "string_type body.value"},
		{"POST", "/notifications", `[]`, 422, "dict_type body"},
		{"POST", "/proxies", `{"protocol":"ftp","host":"h","port":1}`, 422, "enum body.protocol"},
		{"POST", "/maintenance", `{"title":"x","strategy":"manual","timeRange":[1]}`, 422, "dict_type body.timeRange.0"},
		{"POST", "/maintenance/1/monitors", `{}`, 422, "list_type body"},
		{"POST", "/maintenance/1/monitors", `[{"x":1},3]`, 422, "missing body.0.id"},
		{"POST", "/statuspages", `{"slug":"","title":"x"}`, 422, "string_too_short body.slug"},
		{"POST", "/statuspages/page", `{"theme":"neon"}`, 422, "theme must be auto, light or dark"},
		{"POST", "/statuspages/page", `{"theme":1}`, 422, "string_type body.theme"},
		{"POST", "/statuspages/page/incident", `{"title":"a"}`, 422, "missing body.content"},
		{"POST", "/statuspages/page/incident", `{"title":"a","content":"b","style":null}`, 422, "enum body.style"},
		{"POST", "/settings/upload_backup?import_handle=skip", `{}`, 410, "Uptime Kuma 2.x removed backup import"},
	} {
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

// Uptime Kuma's refusals and failures, and what they become.
func TestKumaRoutesKumaFailures(t *testing.T) {
	e := newEnv(t, Options{})
	mon := int(get[float64](t, e.ok(t, "POST", "/monitors", `{"type":"http","name":"m","url":"http://x"}`), "monitorID"))
	maint := int(get[float64](t, e.ok(t, "POST", "/maintenance", `{"title":"w","strategy":"manual"}`), "maintenanceID"))
	e.ok(t, "POST", "/statuspages", `{"slug":"page","title":"Page"}`)
	tag := int(get[float64](t, e.ok(t, "POST", "/tags", `{"name":"t","color":"#fff"}`), "id"))
	nid := int(get[float64](t, e.ok(t, "POST", "/notifications", `{"name":"n","type":"webhook"}`), "id"))
	pid := int(get[float64](t, e.ok(t, "POST", "/proxies", `{"protocol":"http","host":"h","port":1}`), "id"))
	did := int(get[float64](t, e.ok(t, "POST", "/docker_hosts", `{"name":"d","dockerType":"socket"}`), "id"))
	e.ok(t, "POST", fmt.Sprintf("/monitors/%d/tag", mon), fmt.Sprintf(`{"tag_id":%d}`, tag))

	for _, c := range []struct {
		event, method, path, body string
	}{
		{"add", "POST", "/monitors", `{"type":"http","name":"m","url":"u"}`},
		{"getMonitor", "PATCH", fmt.Sprintf("/monitors/%d", mon), `{}`},
		{"editMonitor", "PATCH", fmt.Sprintf("/monitors/%d", mon), `{}`},
		{"deleteMonitor", "DELETE", fmt.Sprintf("/monitors/%d", mon), ""},
		{"pauseMonitor", "POST", fmt.Sprintf("/monitors/%d/pause", mon), ""},
		{"getMonitorBeats", "GET", fmt.Sprintf("/monitors/%d/beats", mon), ""},
		{"addMonitorTag", "POST", fmt.Sprintf("/monitors/%d/tag", mon), fmt.Sprintf(`{"tag_id":%d}`, tag)},
		{"deleteMonitorTag", "DELETE", fmt.Sprintf("/monitors/%d/tag", mon), fmt.Sprintf(`{"tag_id":%d}`, tag)},
		{"getTags", "GET", "/tags", ""},
		{"addTag", "POST", "/tags", `{"name":"x","color":"y"}`},
		{"editTag", "PATCH", fmt.Sprintf("/tags/%d", tag), `{}`},
		{"deleteTag", "DELETE", fmt.Sprintf("/tags/%d", tag), ""},
		{"addNotification", "PATCH", fmt.Sprintf("/notifications/%d", nid), `{}`},
		{"testNotification", "POST", "/notifications/test", `{"name":"n","type":"webhook"}`},
		{"deleteNotification", "DELETE", fmt.Sprintf("/notifications/%d", nid), ""},
		{"addProxy", "PATCH", fmt.Sprintf("/proxies/%d", pid), `{}`},
		{"addDockerHost", "PATCH", fmt.Sprintf("/docker_hosts/%d", did), `{}`},
		{"addMaintenance", "POST", "/maintenance", `{"title":"w","strategy":"manual"}`},
		{"getMaintenance", "GET", fmt.Sprintf("/maintenance/%d", maint), ""},
		{"editMaintenance", "PATCH", fmt.Sprintf("/maintenance/%d", maint), `{}`},
		{"getMonitorMaintenance", "GET", fmt.Sprintf("/maintenance/%d/monitors", maint), ""},
		{"addMonitorMaintenance", "POST", fmt.Sprintf("/maintenance/%d/monitors", maint), `[]`},
		{"addStatusPage", "POST", "/statuspages", `{"slug":"x","title":"x"}`},
		{"getStatusPage", "GET", "/statuspages/page", ""},
		{"saveStatusPage", "POST", "/statuspages/page", `{}`},
		{"deleteStatusPage", "DELETE", "/statuspages/page", ""},
		{"postIncident", "POST", "/statuspages/page/incident", `{"title":"a","content":"b"}`},
		{"unpinIncident", "DELETE", "/statuspages/page/incident/unpin", ""},
		{"getDatabaseSize", "GET", "/database/size", ""},
		{"shrinkDatabase", "POST", "/database/shrink", ""},
	} {
		e.kuma.Fail(c.event, "Something went wrong")
		status, v := e.call(t, c.method, c.path, c.body)
		e.kuma.Fail(c.event, "")
		if status != 400 || get[string](t, v, "detail") != "Something went wrong" {
			t.Errorf("%s (%s %s): %d %v", c.event, c.method, c.path, status, v)
		}
	}

	// A raw error is not shown; a raw "not found" error is a plain 404.
	e.kuma.Fail("getMonitorBeats", "Cannot read properties of undefined (reading 'x')")
	if status, v := e.call(t, "GET", "/monitors/1/beats", ""); status != 400 || get[string](t, v, "detail") != "Uptime Kuma could not process the request" {
		t.Fatalf("raw error: %d %v", status, v)
	}
	e.kuma.Fail("getMonitorBeats", "FOREIGN KEY constraint failed")
	if status, v := e.call(t, "GET", "/monitors/1/beats", ""); status != 404 || get[string](t, v, "detail") != "Not found" {
		t.Fatalf("raw not found: %d %v", status, v)
	}
	if !strings.Contains(e.logs.String(), "Cannot read properties of undefined") {
		t.Fatal("raw error not logged")
	}
}

// When Uptime Kuma cannot be reached, routes answer 503 at once.
func TestKumaRoutesUnavailable(t *testing.T) {
	e := newEnv(t, Options{})
	session, err := kuma.New(kuma.Config{URL: "http://127.0.0.1:9", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	e.srv.kuma = kuma.NewClient(session)
	for _, path := range []string{"/monitors", "/monitors/1", "/notifications", "/notifications/1", "/proxies", "/docker_hosts",
		"/maintenance", "/statuspages", "/info", "/ping", "/uptime", "/cert_info", "/tags", "/database/size", "/monitors/1/beats"} {
		if status, v := e.call(t, "GET", path, ""); status != 503 || get[string](t, v, "detail") != "Uptime Kuma is unavailable" {
			t.Errorf("GET %s: %d %v", path, status, v)
		}
	}
}

func TestKumaErrorMapping(t *testing.T) {
	e := newEnv(t, Options{})
	ctx := context.Background()
	for _, c := range []struct {
		err    error
		status int
		detail string
	}{
		{&kuma.CommandError{Event: "x", Msg: "tagNotFound", I18n: true}, 404, "Tag not found."},
		{&kuma.CommandError{Event: "x", Msg: "No slug?"}, 404, "Not found"},
		{&kuma.CommandError{Event: "x", Msg: "You do not own this monitor."}, 404, "Not found"},
		{&kuma.CommandError{Event: "x", Msg: "monitor not found"}, 404, "monitor not found"},
		{&kuma.CommandError{Event: "x", Msg: "NOT NULL constraint failed: monitor.conditions"}, 400, "Uptime Kuma could not process the request"},
		{&kuma.CommandError{Event: "x", Msg: ""}, 400, "Uptime Kuma could not process the request"},
		{&kuma.CommandError{Event: "x", Msg: "Invalid period."}, 400, "Invalid period."},
		{fmt.Errorf("x: %w", kuma.ErrNotFound), 404, "Not found"},
		{fmt.Errorf("%w: x", kuma.ErrTimeout), 504, "Uptime Kuma did not answer in time"},
		{fmt.Errorf("%w: %w", kuma.ErrUnavailable, kuma.ErrTwoFactorRequired), 503, "the Uptime Kuma account requires two-factor authentication, which the API cannot use"},
		{fmt.Errorf("%w: %w: bad", kuma.ErrUnavailable, kuma.ErrLoginRejected), 503, "Uptime Kuma rejected the login"},
		{fmt.Errorf("%w: down", kuma.ErrUnavailable), 503, "Uptime Kuma is unavailable"},
		{errors.New("other"), 500, "Internal Server Error"},
	} {
		var aerr *apiError
		if !errors.As(e.srv.kumaError(ctx, c.err), &aerr) || aerr.status != c.status || aerr.Detail != c.detail {
			t.Errorf("%v: %+v, want %d %q", c.err, aerr, c.status, c.detail)
		}
	}
}

func TestParamIssues(t *testing.T) {
	got := paramIssues([]*huma.ErrorDetail{
		{Location: "path.monitor_id", Message: "invalid integer", Value: "abc"},
		{Location: "query.hours", Message: "invalid float", Value: "x"},
		{Location: "query.hours", Message: "expected number > 0", Value: 0.0},
		{Location: "header.x", Message: "something else", Value: 1},
	})
	b, _ := marshalJSON(got)
	want := `[{"type":"int_parsing","loc":["path","monitor_id"],"msg":"Input should be a valid integer, unable to parse string as an integer","input":"abc"},` +
		`{"type":"float_parsing","loc":["query","hours"],"msg":"Input should be a valid number, unable to parse string as a number","input":"x"},` +
		`{"type":"greater_than","loc":["query","hours"],"msg":"Input should be greater than 0","input":"0","ctx":{"gt":0}},` +
		`{"type":"value_error","loc":["header","x"],"msg":"something else","input":1}]`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestViewsAndChecks(t *testing.T) {
	if v := monitorView(json.RawMessage(`[1]`)); string(v.(json.RawMessage)) != `[1]` {
		t.Fatalf("%v", v)
	}
	m := monitorView(json.RawMessage(`{"notificationIDList":{"3":true,"1":true,"x":true},"active":1,"authMethod":"basic"}`)).(map[string]any)
	if fmt.Sprint(m) != "map[active:true authMethod:basic notificationIDList:[1 3]]" {
		t.Fatalf("%v", m)
	}
	if v := notificationView(json.RawMessage(`{"id":1,"config":"not json"}`)).(map[string]any); v["config"] != "not json" {
		t.Fatalf("%v", v)
	}
	if v := notificationView(json.RawMessage(`[]`)); string(v.(json.RawMessage)) != `[]` {
		t.Fatal(v)
	}
	if v := proxyView(json.RawMessage(`[]`)); string(v.(json.RawMessage)) != `[]` {
		t.Fatal(v)
	}
	for code, ok := range map[any]bool{"200-299": true, "404": true, "1000": true, "4999": true, "99": false, "5000": false, "0404": false, 200: false} {
		if validStatusCode(code) != ok {
			t.Errorf("validStatusCode(%v) = %v", code, !ok)
		}
	}
	if checkMonitor(map[string]any{"type": "push", "name": "x", "interval": 60, "maxretries": 0, "retryInterval": 60, "dns_resolve_type": nil}) != nil {
		t.Fatal("push monitor refused")
	}
	for typ, n := range map[string]int{"port": 2, "steam": 2, "mqtt": 2, "ping": 1, "tailscale-ping": 1, "keyword": 2,
		"grpc-keyword": 4, "docker": 2, "gamedig": 3, "json-query": 3, "kafka-producer": 2, "real-browser": 1, "group": 0} {
		if got := len(requiredByType(typ)); got != n {
			t.Errorf("%s: %d required", typ, got)
		}
	}
	if jsonText(make(chan int)) != "?" {
		t.Fatal("jsonText")
	}
	if ackBody(json.RawMessage(`[]`)) == nil || ackBody(json.RawMessage(`{"ok":true,"msg":"successAdded"}`))["msg"] != "successAdded" {
		t.Fatal("ackBody keeps a key sent without msgi18n")
	}
	if _, ok := number("1"); ok {
		t.Fatal("number")
	}
	if n, _ := number(int64(3)); n != 3 {
		t.Fatal("number int64")
	}
	if n, _ := number(2); n != 2 {
		t.Fatal("number int")
	}
}

// A body that cannot be encoded is a logged 500.
func TestReplyEncodingFailure(t *testing.T) {
	e := newEnv(t, Options{})
	e.srv.log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	if _, err := replyAs[any](e.srv, context.Background(), make(chan int)); err == nil {
		t.Fatal("no error")
	}
}
