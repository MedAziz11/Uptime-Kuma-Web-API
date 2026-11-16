package kuma

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/kumatest"
)

// objectField returns a field of the object with id in list l of State.
func objectField(t *testing.T, st *State, l List, id int64, name string) any {
	t.Helper()
	raw, ok, _ := st.Object(l, id)
	if !ok {
		t.Fatalf("%s %d not in State", l, id)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m[name]
}

// API key writes are visible in State when they return.
func TestClientAPIKeys(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	st := c.State()
	ack := must(c.AddAPIKey(ctx, map[string]any{"name": "ci", "active": true, "expires": nil}))
	id := int64(field[float64](t, ack, "keyID"))
	if field[string](t, ack, "key") == "" {
		t.Fatalf("no clear key: %s", ack)
	}
	if objectField(t, st, APIKeys, id, "name") != "ci" || objectField(t, st, APIKeys, id, "active") != 1.0 {
		t.Fatal("added key not in State")
	}
	must(c.DisableAPIKey(ctx, id))
	if objectField(t, st, APIKeys, id, "active") != 0.0 {
		t.Fatal("disable not in State")
	}
	must(c.EnableAPIKey(ctx, id))
	if objectField(t, st, APIKeys, id, "active") != 1.0 {
		t.Fatal("enable not in State")
	}
	must(c.DeleteAPIKey(ctx, id))
	if _, ok, _ := st.Object(APIKeys, id); ok {
		t.Fatal("deleted key still in State")
	}
	k.Fail("addAPIKey", "nope")
	if _, err := c.AddAPIKey(ctx, map[string]any{"name": "x"}); err == nil {
		t.Fatal("refusal not reported")
	}
}

// Remote browser writes are visible in State when they return.
func TestClientRemoteBrowsers(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	st := c.State()
	id := int64(field[float64](t, must(c.SaveRemoteBrowser(ctx, map[string]any{"name": "b", "url": "ws://x"}, nil)), "id"))
	if objectField(t, st, RemoteBrowsers, id, "url") != "ws://x" {
		t.Fatal("added browser not in State")
	}
	must(c.SaveRemoteBrowser(ctx, map[string]any{"name": "b", "url": "ws://y"}, &id))
	if objectField(t, st, RemoteBrowsers, id, "url") != "ws://y" {
		t.Fatal("edit not in State")
	}
	if _, err := c.TestRemoteBrowser(ctx, map[string]any{"url": "ws://y"}); err == nil {
		t.Fatal("test of an unreachable browser succeeded")
	}
	must(c.DeleteRemoteBrowser(ctx, id))
	if _, ok, _ := st.Object(RemoteBrowsers, id); ok {
		t.Fatal("deleted browser still in State")
	}
	if n := k.Counts()["remote browsers"]; n != 0 {
		t.Fatalf("%d browsers left", n)
	}
}

// Incident commands, and the history paged with Kuma's cursor.
func TestClientIncidents(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, _, _ := newClient(t, testTimeout)
	ctx := context.Background()
	must(c.AddStatusPage(ctx, "P", "p"))
	var ids []int64
	for i := range kumatest.IncidentPageSize + 2 {
		inc := field[map[string]any](t, must(c.PostIncident(ctx, "p", map[string]any{"title": fmt.Sprint(i), "content": "c", "style": "info"})), "incident")
		ids = append(ids, int64(inc["id"].(float64)))
	}
	first := must(c.IncidentHistory(ctx, "p", ""))
	if n := len(field[[]any](t, first, "incidents")); n != kumatest.IncidentPageSize || !field[bool](t, first, "hasMore") || field[float64](t, first, "total") != float64(len(ids)) {
		t.Fatalf("first page %s", first)
	}
	second := must(c.IncidentHistory(ctx, "p", field[string](t, first, "nextCursor")))
	if inc := field[[]map[string]any](t, second, "incidents"); len(inc) != 2 || inc[1]["id"] != float64(ids[0]) || field[bool](t, second, "hasMore") {
		t.Fatalf("second page %s", second)
	}
	edited := must(c.EditIncident(ctx, "p", ids[0], map[string]any{"title": "new", "content": "c2", "style": "danger"}))
	if inc := field[map[string]any](t, edited, "incident"); inc["title"] != "new" || inc["pin"] != true {
		t.Fatalf("edit %s", edited)
	}
	resolved := must(c.ResolveIncident(ctx, "p", ids[0]))
	if inc := field[map[string]any](t, resolved, "incident"); inc["active"] != false || inc["pin"] != false {
		t.Fatalf("resolve %s", resolved)
	}
	must(c.DeleteIncident(ctx, "p", ids[0]))
	if _, err := c.DeleteIncident(ctx, "p", ids[0]); err == nil {
		t.Fatal("second delete succeeded")
	}
	if _, err := c.IncidentHistory(ctx, "nope", ""); err == nil {
		t.Fatal("history of an unknown page")
	}
}

func TestClientChartAndImportantBeats(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	m, err := NewMonitor(map[string]any{"name": "web", "url": "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	id := int64(field[float64](t, must(c.AddMonitor(ctx, m)), "monitorID"))
	k.Beat(int(id), 1, 20)
	k.Beat(int(id), 0, nil)
	if d := field[[]map[string]any](t, must(c.MonitorChartData(ctx, id, 24)), "data"); len(d) != 2 || d[0]["up"] != 1.0 {
		t.Fatalf("chart %v", d)
	}
	if n := field[float64](t, must(c.ImportantHeartbeatCount(ctx, id)), "count"); n != 2 {
		t.Fatalf("count %v", n)
	}
	if d := field[[]map[string]any](t, must(c.ImportantHeartbeats(ctx, id, 1, 5)), "data"); len(d) != 1 || d[0]["status"] != 1.0 {
		t.Fatalf("paged %v", d)
	}
}
