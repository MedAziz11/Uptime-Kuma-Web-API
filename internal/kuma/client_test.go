package kuma

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/kumatest"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// newClient returns a Client on a fresh fake Kuma.
func newClient(t *testing.T, timeout time.Duration) (*Client, *kumatest.Server, *syncBuffer) {
	t.Helper()
	k := kumatest.New()
	t.Cleanup(k.Close)
	logs := &syncBuffer{}
	s, err := New(Config{URL: k.URL, Username: kumatest.Username, Password: kumatest.Password, Timeout: timeout,
		Logger: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	s.minBackoff, s.maxBackoff, s.stableAfter = time.Millisecond, 4*time.Millisecond, 4*time.Millisecond
	c := NewClient(s)
	t.Cleanup(func() { _ = s.Close() })
	return c, k, logs
}

func field[T any](t *testing.T, raw json.RawMessage, name string) T {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	var v T
	if err := json.Unmarshal(m[name], &v); err != nil {
		t.Fatalf("%s.%s: %v", raw, name, err)
	}
	return v
}

// mustFor returns a function that fails t on an error and returns the ack.
func mustFor(t *testing.T) func(json.RawMessage, error) json.RawMessage {
	return func(raw json.RawMessage, err error) json.RawMessage {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
}

// Every write is visible in State when it returns.
func TestClientReadYourWrites(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	st := c.State()

	m, err := NewMonitor(map[string]any{"name": "web", "url": "http://x"})
	if err != nil {
		t.Fatal(err)
	}
	id := int64(field[float64](t, must(c.AddMonitor(ctx, m)), "monitorID"))
	if _, ok := st.Object(Monitors, id); !ok {
		t.Fatal("added monitor not in State")
	}
	must(c.PauseMonitor(ctx, id))
	cur, _ := st.Object(Monitors, id)
	if field[bool](t, cur, "active") {
		t.Fatal("pause not visible")
	}
	full := must(c.GetMonitor(ctx, id))
	edited, err := MergeMonitor(field[json.RawMessage](t, full, "monitor"), map[string]any{"name": "renamed"})
	if err != nil {
		t.Fatal(err)
	}
	must(c.EditMonitor(ctx, edited))
	cur, _ = st.Object(Monitors, id)
	if field[string](t, cur, "name") != "renamed" {
		t.Fatal("edit not visible")
	}
	must(c.ResumeMonitor(ctx, id))

	tag := field[map[string]any](t, must(c.AddTag(ctx, map[string]any{"name": "t", "color": "#fff"})), "tag")
	tagID := int64(tag["id"].(float64))
	must(c.AddMonitorTag(ctx, tagID, id, "v"))
	cur, _ = st.Object(Monitors, id)
	if tags := field[[]map[string]any](t, cur, "tags"); len(tags) != 1 || tags[0]["name"] != "t" {
		t.Fatalf("tags %v", tags)
	}
	tag["name"] = "t2"
	must(c.EditTag(ctx, tag))
	cur, _ = st.Object(Monitors, id)
	if tags := field[[]map[string]any](t, cur, "tags"); tags[0]["name"] != "t2" {
		t.Fatalf("tag rename not visible in the monitor: %v", tags)
	}
	must(c.DeleteMonitorTag(ctx, tagID, id, "v"))
	must(c.DeleteTag(ctx, tagID))
	if tags := field[[]any](t, must(c.Tags(ctx)), "tags"); len(tags) != 0 {
		t.Fatal("tag kept")
	}

	nid := int64(field[float64](t, must(c.SaveNotification(ctx, map[string]any{"name": "n", "type": "webhook"}, nil)), "id"))
	if _, ok := st.Object(Notifications, nid); !ok {
		t.Fatal("notification not in State")
	}
	must(c.TestNotification(ctx, map[string]any{"name": "n"}))
	must(c.DeleteNotification(ctx, nid))
	pid := int64(field[float64](t, must(c.SaveProxy(ctx, map[string]any{"protocol": "http", "host": "h", "port": 1}, nil)), "id"))
	must(c.SaveProxy(ctx, map[string]any{"protocol": "http", "host": "h", "port": 2}, &pid))
	p, _ := st.Object(Proxies, pid)
	if field[int](t, p, "port") != 2 {
		t.Fatalf("proxy %s", p)
	}
	must(c.DeleteProxy(ctx, pid))
	did := int64(field[float64](t, must(c.SaveDockerHost(ctx, map[string]any{"name": "d", "dockerType": "socket"}, nil)), "id"))
	if _, err := c.TestDockerHost(ctx, map[string]any{"name": "d"}); err == nil {
		t.Fatal("testDockerHost: no error")
	}
	must(c.DeleteDockerHost(ctx, did))
	for _, l := range []List{Notifications, Proxies, DockerHosts} {
		if len(st.Objects(l)) != 0 {
			t.Fatalf("%v kept", l)
		}
	}

	mid := int64(field[float64](t, must(c.AddMaintenance(ctx, map[string]any{"title": "w", "strategy": "manual", "dateRange": []any{}})), "maintenanceID"))
	must(c.PauseMaintenance(ctx, mid))
	mt, _ := st.Object(Maintenances, mid)
	if field[bool](t, mt, "active") {
		t.Fatal("maintenance pause not visible")
	}
	must(c.ResumeMaintenance(ctx, mid))
	got := must(c.GetMaintenance(ctx, mid))
	edit := field[map[string]any](t, got, "maintenance")
	edit["title"] = "w2"
	must(c.EditMaintenance(ctx, edit))
	mt, _ = st.Object(Maintenances, mid)
	if field[string](t, mt, "title") != "w2" {
		t.Fatal("maintenance edit not visible")
	}
	must(c.SetMaintenanceMonitors(ctx, mid, []map[string]any{{"id": id}}))
	if mons := field[[]map[string]any](t, must(c.MaintenanceMonitors(ctx, mid)), "monitors"); len(mons) != 1 {
		t.Fatalf("maintenance monitors %v", mons)
	}
	must(c.DeleteMaintenance(ctx, mid))
	if len(st.Objects(Maintenances)) != 0 {
		t.Fatal("maintenance kept")
	}

	k.Beat(int(id), 1, 12)
	beats := field[[]any](t, must(c.MonitorBeats(ctx, id, 1)), "data")
	if len(beats) != 1 {
		t.Fatalf("beats %v", beats)
	}
	must(c.DeleteMonitor(ctx, id))
	if _, ok := st.Object(Monitors, id); ok {
		t.Fatal("deleted monitor kept")
	}
	if field[int](t, must(c.DatabaseSize(ctx)), "size") == 0 {
		t.Fatal("size")
	}
	must(c.ShrinkDatabase(ctx))
}

func TestClientStatusPages(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, _, _ := newClient(t, testTimeout)
	ctx := context.Background()
	st := c.State()
	must(c.AddStatusPage(ctx, "Page", "page"))
	pages := st.Objects(StatusPages)
	if len(pages) != 1 || field[string](t, pages[0], "slug") != "page" {
		t.Fatalf("pages %s", pages)
	}
	id := int64(field[float64](t, pages[0], "id"))
	cfg := field[map[string]any](t, must(c.GetStatusPage(ctx, "page")), "config")
	cfg["slug"], cfg["title"] = "renamed", "Renamed"
	ack := must(c.SaveStatusPage(ctx, "page", cfg, "/icon.svg", []any{map[string]any{"name": "g", "monitorList": []any{}}}))
	if groups := field[[]any](t, ack, "publicGroupList"); len(groups) != 1 {
		t.Fatalf("ack %s", ack)
	}
	p, _ := st.Object(StatusPages, id)
	if field[string](t, p, "slug") != "renamed" {
		t.Fatalf("save not visible: %s", p)
	}
	inc := must(c.PostIncident(ctx, "renamed", map[string]any{"title": "t", "content": "c", "style": "info"}))
	if field[map[string]any](t, inc, "incident")["title"] != "t" {
		t.Fatalf("incident %s", inc)
	}
	pub := must(c.PublicStatusPage(ctx, "renamed"))
	if len(field[[]any](t, pub, "incidents")) != 1 || len(field[[]any](t, pub, "publicGroupList")) != 1 {
		t.Fatalf("public %s", pub)
	}
	must(c.UnpinIncident(ctx, "renamed"))
	must(c.DeleteStatusPage(ctx, "renamed", id))
	if len(st.Objects(StatusPages)) != 0 {
		t.Fatal("deleted page kept")
	}
	if _, err := c.PublicStatusPage(ctx, "renamed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	var ce *CommandError
	if _, err := c.GetStatusPage(ctx, "renamed"); !errors.As(err, &ce) || ce.Msg != "No slug?" {
		t.Fatalf("err = %v", err)
	}
	for name, f := range map[string]func() (json.RawMessage, error){
		"add":    func() (json.RawMessage, error) { return c.AddStatusPage(ctx, "x", "Bad Slug") },
		"save":   func() (json.RawMessage, error) { return c.SaveStatusPage(ctx, "nope", cfg, "", nil) },
		"delete": func() (json.RawMessage, error) { return c.DeleteStatusPage(ctx, "nope", 1) },
	} {
		if _, err := f(); !errors.As(err, &ce) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// A push that never comes delays the write by the timeout, not forever,
// and the write still succeeds.
func TestClientMissingPush(t *testing.T) {
	checkLeaks(t)
	c, k, logs := newClient(t, 200*time.Millisecond)
	ctx := context.Background()
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	k.NoPush(true)
	start := time.Now()
	if _, err := c.SaveProxy(ctx, map[string]any{"protocol": "http", "host": "h", "port": 1}, nil); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 200*time.Millisecond || d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	if !strings.Contains(logs.String(), "did not push the change in time") {
		t.Fatalf("logs %s", logs)
	}
	k.Fail("getMonitorList", "boom")
	if _, err := c.DeleteTag(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "cannot refresh the monitor list") {
		t.Fatalf("logs %s", logs)
	}
	k.Fail("getStatusPage", "boom")
	if _, err := c.AddStatusPage(ctx, "x", "x"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "cannot read a status page back") {
		t.Fatalf("logs %s", logs)
	}
	// Failed writes return Kuma's error and do not wait.
	for _, ev := range []string{"addProxy", "addTag", "editTag", "deleteTag", "addStatusPage", "saveStatusPage", "deleteStatusPage"} {
		k.Fail(ev, "refused")
	}
	var ce *CommandError
	for name, f := range map[string]func() (json.RawMessage, error){
		"proxy":  func() (json.RawMessage, error) { return c.SaveProxy(ctx, nil, nil) },
		"edit":   func() (json.RawMessage, error) { return c.EditTag(ctx, nil) },
		"delete": func() (json.RawMessage, error) { return c.DeleteTag(ctx, 1) },
		"add":    func() (json.RawMessage, error) { return c.AddStatusPage(ctx, "a", "a") },
		"save":   func() (json.RawMessage, error) { return c.SaveStatusPage(ctx, "a", nil, "", nil) },
		"del":    func() (json.RawMessage, error) { return c.DeleteStatusPage(ctx, "a", 1) },
	} {
		if _, err := f(); !errors.As(err, &ce) || ce.Msg != "refused" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// State is reset for each connection and filled again by the next login.
func TestClientStateFollowsReconnects(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	m, _ := NewMonitor(map[string]any{"name": "a"})
	id := int64(field[float64](t, must(c.AddMonitor(ctx, m)), "monitorID"))
	k.Beat(int(id), 1, 10)
	waitUntil(t, func() bool { return len(c.State().Heartbeats(id)) == 1 })
	c.State().PutObject(StatusPages, json.RawMessage(`{"id":99,"slug":"stale"}`))

	k.DisconnectAll()
	waitUntil(t, func() bool { _, ok := c.State().Object(StatusPages, 99); return !ok })
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.State().Object(Monitors, id); !ok {
		t.Fatal("monitor list not pushed again")
	}
	if _, ok := c.State().Info(); !ok {
		t.Fatal("info not pushed again")
	}
	if len(c.State().Heartbeats(id)) != 1 || len(c.State().AvgPing()) != 1 {
		t.Fatalf("heartbeats %d, avgPing %v", len(c.State().Heartbeats(id)), c.State().AvgPing())
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSyncFailsWhileKumaIsDown(t *testing.T) {
	checkLeaks(t)
	s, err := New(Config{URL: "http://127.0.0.1:9", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := NewClient(s)
	if err := c.Sync(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.PublicStatusPage(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
