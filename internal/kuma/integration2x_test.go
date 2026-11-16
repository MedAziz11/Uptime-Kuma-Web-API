package kuma

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// What the 2.x resources do on a real Uptime Kuma, as read in its source.
// Everything created is deleted again.

// API key and remote browser writes push their list, which State applies
// before the write returns.
func TestRealKumaAPIKeysAndRemoteBrowsers(t *testing.T) {
	c := realKumaClient(t)
	ctx := context.Background()
	must := mustFor(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)

	ack := must(c.AddAPIKey(ctx, map[string]any{"name": "kuma-it " + suffix, "active": true, "expires": nil}))
	id := int64(field[float64](t, ack, "keyID"))
	defer func() { _, _ = c.DeleteAPIKey(context.Background(), id) }()
	if key := field[string](t, ack, "key"); key[:len(fmt.Sprintf("uk%d_", id))] != fmt.Sprintf("uk%d_", id) {
		t.Fatalf("clear key without its id prefix")
	}
	raw, ok := objectOf(c.State(), APIKeys, id)
	if !ok || field[string](t, raw, "status") != "active" || field[float64](t, raw, "active") != 1 {
		t.Fatalf("added key: %s", raw)
	}
	must(c.DisableAPIKey(ctx, id))
	if raw, _ := objectOf(c.State(), APIKeys, id); field[string](t, raw, "status") != "inactive" {
		t.Fatalf("disabled key: %s", raw)
	}
	must(c.EnableAPIKey(ctx, id))
	must(c.DeleteAPIKey(ctx, id))
	if _, ok := objectOf(c.State(), APIKeys, id); ok {
		t.Fatal("deleted key in State")
	}
	// Unknown ids succeed (and push the list): the API checks State first.
	v := c.State().Version(APIKeys)
	must(c.EnableAPIKey(ctx, 999999))
	if c.State().Version(APIKeys) == v {
		t.Fatal("no push after enabling an unknown key")
	}

	bid := int64(field[float64](t, must(c.SaveRemoteBrowser(ctx, map[string]any{"name": "kuma-it " + suffix, "url": "ws://127.0.0.1:1"}, nil)), "id"))
	defer func() { _, _ = c.DeleteRemoteBrowser(context.Background(), bid) }()
	must(c.SaveRemoteBrowser(ctx, map[string]any{"name": "kuma-it " + suffix, "url": "ws://127.0.0.1:2"}, &bid))
	if raw, _ := objectOf(c.State(), RemoteBrowsers, bid); field[string](t, raw, "url") != "ws://127.0.0.1:2" {
		t.Fatalf("edited browser: %s", raw)
	}
	if _, err := c.TestRemoteBrowser(ctx, map[string]any{"url": "ws://127.0.0.1:1"}); err == nil {
		t.Fatal("test of a closed port succeeded")
	}
	must(c.DeleteRemoteBrowser(ctx, bid))
	if _, ok := objectOf(c.State(), RemoteBrowsers, bid); ok {
		t.Fatal("deleted browser in State")
	}
	if _, err := c.DeleteRemoteBrowser(ctx, bid); err == nil {
		t.Fatal("deleting an unknown browser succeeded")
	}
}

// The incident history pages with a creation date to the second: incidents
// created within one second end the history after its first page.
func TestRealKumaIncidentHistory(t *testing.T) {
	c := realKumaClient(t)
	ctx := context.Background()
	must := mustFor(t)
	slug := "kuma-it-inc-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	must(c.AddStatusPage(ctx, "Kuma IT incidents", slug))
	defer func() { _, _ = c.s.Call(context.Background(), "deleteStatusPage", slug) }()
	var ids []int64
	for i := range 12 {
		inc := field[map[string]any](t, must(c.PostIncident(ctx, slug, map[string]any{"title": fmt.Sprint(i), "content": "c", "style": "info"})), "incident")
		ids = append(ids, int64(inc["id"].(float64)))
	}
	h := must(c.IncidentHistory(ctx, slug, ""))
	n := len(field[[]any](t, h, "incidents"))
	if field[float64](t, h, "total") != 12 || n != 10 {
		t.Fatalf("history: %s", h)
	}
	// Usually all 12 are created in the same second; then hasMore is false.
	t.Logf("first page of 10 of 12, hasMore %v", field[bool](t, h, "hasMore"))

	edited := must(c.EditIncident(ctx, slug, ids[0], map[string]any{"title": "e", "content": "c", "style": "nope", "pin": false}))
	if inc := field[map[string]any](t, edited, "incident"); inc["style"] != "warning" || inc["pin"] != false {
		t.Fatalf("edit: %s", edited)
	}
	resolved := must(c.ResolveIncident(ctx, slug, ids[1]))
	if inc := field[map[string]any](t, resolved, "incident"); inc["active"] != false || inc["pin"] != false || field[string](t, resolved, "msg") != "Resolved" {
		t.Fatalf("resolve: %s", resolved)
	}
	must(c.DeleteIncident(ctx, slug, ids[1]))
	if _, err := c.DeleteIncident(ctx, slug, ids[1]); err == nil {
		t.Fatal("deleting a deleted incident succeeded")
	}
	if _, err := c.EditIncident(ctx, slug, ids[0], map[string]any{"title": " ", "content": "c"}); err == nil {
		t.Fatal("blank title accepted")
	}
}

// Chart data and important heartbeats answer an unknown monitor with
// success and empty data: the API checks State first.
func TestRealKumaChartOfUnknownMonitor(t *testing.T) {
	c := realKumaClient(t)
	ctx := context.Background()
	must := mustFor(t)
	if _, ok := objectOf(c.State(), Monitors, 999999); ok {
		t.Skip("monitor 999999 exists")
	}
	if _, err := c.MonitorChartData(ctx, 999999, 24); err != nil {
		t.Fatalf("chart of an unknown monitor: %v", err)
	}
	if n := field[float64](t, must(c.ImportantHeartbeatCount(ctx, 999999)), "count"); n != 0 {
		t.Fatalf("count %v", n)
	}
	if d := field[[]any](t, must(c.ImportantHeartbeats(ctx, 999999, 0, 5)), "data"); len(d) != 0 {
		t.Fatalf("data %v", d)
	}
}
