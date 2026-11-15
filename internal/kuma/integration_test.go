package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
)

// TestRealKuma runs against a real Uptime Kuma 2.x when KUMA_URL is set
// (e.g. http://localhost:3001). KUMA_USERNAME and KUMA_PASSWORD default to
// admin / Admin-Pass-12345. A fresh instance is set up with them first.
func TestRealKuma(t *testing.T) {
	base := os.Getenv("KUMA_URL")
	if base == "" {
		t.Skip("KUMA_URL is not set")
	}
	user, pass := os.Getenv("KUMA_USERNAME"), os.Getenv("KUMA_PASSWORD")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "Admin-Pass-12345"
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Set up a fresh instance (no login needed for needSetup / setup). The
	// server answers commands only once it sent loginRequired or autoLogin.
	ready := make(chan struct{}, 1)
	c, err := sio.Dial(ctx, base, sio.Options{OnEvent: func(name string, _ []json.RawMessage) {
		if name == eventLoginRequired || name == eventAutoLogin {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ready, "loginRequired or autoLogin")
	ack, err := c.Call(ctx, "needSetup")
	if err != nil {
		t.Fatal(err)
	}
	if string(ack[0]) == "true" {
		ack, err := c.Call(ctx, "setup", user, pass)
		if err != nil {
			t.Fatal(err)
		}
		if err := commandError("setup", ack[0]); err != nil {
			t.Fatal(err)
		}
	}
	_ = c.Close()

	s, err := New(Config{URL: base, Username: user, Password: pass, Logger: slog.New(slog.NewTextHandler(testWriter{t}, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	monitorList := make(chan struct{}, 1)
	s.Handle("monitorList", func(args []json.RawMessage) {
		select {
		case monitorList <- struct{}{}:
		default:
		}
	})

	res, err := s.Call(ctx, "getSettings")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res, &settings); err != nil || !settings.OK || len(settings.Data) == 0 {
		t.Fatalf("getSettings = %s, %v", res, err)
	}
	waitFor(t, monitorList, "the monitorList push after login")

	// An unknown monitor is answered with {ok: false, msg}.
	_, err = s.Call(ctx, "getMonitor", 999999)
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("getMonitor(999999): err = %v, want a *CommandError", err)
	}
	t.Logf("getMonitor(999999): %v", err)

	// After a server-side logout the socket stays open but answers "You are
	// not logged in.": the session must log in again (with its token) and
	// CallIdempotent must retry.
	if _, err := s.Call(ctx, "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CallIdempotent(ctx, "getSettings"); err != nil {
		t.Fatalf("CallIdempotent(getSettings) after logout: %v", err)
	}
	// Call does not retry: it reports the lost login as ErrUnavailable.
	if _, err := s.Call(ctx, "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(ctx, "getSettings"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Call(getSettings) after logout: err = %v, want ErrUnavailable", err)
	}
}

// testWriter sends log lines to t.Log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}

// TestRealKumaClient checks State and read-your-writes against a real
// Uptime Kuma 2.x (KUMA_URL, KUMA_USERNAME, KUMA_PASSWORD, as for
// TestRealKuma, which sets the instance up). Everything it creates is
// deleted again.
func TestRealKumaClient(t *testing.T) {
	base := os.Getenv("KUMA_URL")
	if base == "" {
		t.Skip("KUMA_URL is not set")
	}
	user, pass := os.Getenv("KUMA_USERNAME"), os.Getenv("KUMA_PASSWORD")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "Admin-Pass-12345"
	}
	s, err := New(Config{URL: base, Username: user, Password: pass, Logger: slog.New(slog.NewTextHandler(testWriter{t}, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := NewClient(s)
	ctx := context.Background()
	must := mustFor(t)
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	info, ok := infoOf(c.State())
	var v struct{ Version string }
	if !ok || json.Unmarshal(info, &v) != nil || !strings.HasPrefix(v.Version, "2.") {
		t.Fatalf("info %s", info)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e9, 10)

	m, err := NewMonitor(map[string]any{"type": "group", "name": "kuma-it " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	id := int64(field[float64](t, must(c.AddMonitor(ctx, m)), "monitorID"))
	defer func() { _, _ = c.DeleteMonitor(context.Background(), id) }()
	if _, ok := objectOf(c.State(), Monitors, id); !ok {
		t.Fatal("added monitor not in State")
	}
	must(c.PauseMonitor(ctx, id))
	cur, _ := objectOf(c.State(), Monitors, id)
	if field[bool](t, cur, "active") {
		t.Fatal("pause not visible")
	}
	full := field[json.RawMessage](t, must(c.GetMonitor(ctx, id)), "monitor")
	edited, err := MergeMonitor(full, map[string]any{"name": "kuma-it renamed " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	must(c.EditMonitor(ctx, edited))
	cur, _ = objectOf(c.State(), Monitors, id)
	if field[string](t, cur, "name") != "kuma-it renamed "+suffix {
		t.Fatalf("edit not visible: %s", cur)
	}

	slug := "kuma-it-" + suffix
	must(c.AddStatusPage(ctx, "Kuma IT", slug))
	var pageID int64
	defer func() { _, _ = c.DeleteStatusPage(context.Background(), slug, pageID) }()
	for _, p := range objectsOf(c.State(), StatusPages) {
		if field[string](t, p, "slug") == slug {
			pageID = int64(field[float64](t, p, "id"))
		}
	}
	if pageID == 0 {
		t.Fatal("added status page not in State")
	}
	cfg := field[map[string]any](t, must(c.GetStatusPage(ctx, slug)), "config")
	cfg["title"] = "Kuma IT renamed"
	must(c.SaveStatusPage(ctx, slug, cfg, "/icon.svg", []any{map[string]any{"name": "g", "monitorList": []any{map[string]any{"id": id}}}}))
	p, _ := objectOf(c.State(), StatusPages, pageID)
	if field[string](t, p, "title") != "Kuma IT renamed" {
		t.Fatalf("save not visible: %s", p)
	}
	must(c.PostIncident(ctx, slug, map[string]any{"title": "t", "content": "c", "style": "info"}))
	pub := must(c.PublicStatusPage(ctx, slug))
	if len(field[[]any](t, pub, "incidents")) != 1 || len(field[[]any](t, pub, "publicGroupList")) != 1 {
		t.Fatalf("public page %s", pub)
	}
	must(c.UnpinIncident(ctx, slug))
	if n := len(field[[]any](t, must(c.PublicStatusPage(ctx, slug)), "incidents")); n != 0 {
		t.Fatalf("%d incidents after unpin (Kuma's response cache?)", n)
	}
	must(c.DeleteStatusPage(ctx, slug, pageID))
	if _, err := c.PublicStatusPage(ctx, slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted page: %v", err)
	}

	mid := int64(field[float64](t, must(c.AddMaintenance(ctx, map[string]any{"title": "kuma-it " + suffix, "strategy": "manual",
		"active": true, "description": "", "intervalDay": 1, "dateRange": []any{}, "weekdays": []any{}, "daysOfMonth": []any{},
		"timeRange": []any{}, "cron": "30 3 * * *", "durationMinutes": 60, "timezoneOption": nil})), "maintenanceID"))
	defer func() { _, _ = c.DeleteMaintenance(context.Background(), mid) }()
	must(c.PauseMaintenance(ctx, mid))
	mt, _ := objectOf(c.State(), Maintenances, mid)
	if field[bool](t, mt, "active") {
		t.Fatal("maintenance pause not visible")
	}
	must(c.DeleteMaintenance(ctx, mid))
	if _, ok := objectOf(c.State(), Maintenances, mid); ok {
		t.Fatal("deleted maintenance in State")
	}
	must(c.DeleteMonitor(ctx, id))
	if _, ok := objectOf(c.State(), Monitors, id); ok {
		t.Fatal("deleted monitor in State")
	}
}

// realKumaClient returns a logged-in Client on the Uptime Kuma of KUMA_URL,
// or skips.
func realKumaClient(t *testing.T) *Client {
	t.Helper()
	base := os.Getenv("KUMA_URL")
	if base == "" {
		t.Skip("KUMA_URL is not set")
	}
	user, pass := os.Getenv("KUMA_USERNAME"), os.Getenv("KUMA_PASSWORD")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "Admin-Pass-12345"
	}
	s, err := New(Config{URL: base, Username: user, Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := NewClient(s)
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func monitorIDs(st *State) string {
	return ids(objectsOf(st, Monitors))
}

// TestRealKumaMonitorListConsistency runs concurrent tag deletions (which
// fetch the monitor list) and monitor creations, pauses, resumes and
// deletions on a real Uptime Kuma, whose monitor pushes can then arrive
// out of order, and checks that State ends equal to what a fresh login
// sees. Everything it creates is deleted again.
func TestRealKumaMonitorListConsistency(t *testing.T) {
	c := realKumaClient(t)
	ctx := context.Background()
	var mu sync.Mutex
	var created []int64
	t.Cleanup(func() {
		for _, id := range created {
			_, _ = c.DeleteMonitor(context.Background(), id)
		}
	})
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
	// A longer list takes Kuma longer to build, which widens the window in
	// which a list can be overtaken by a later change.
	for i := range 30 {
		m, _ := NewMonitor(map[string]any{"type": "group", "name": "kuma-it " + suffix + " idle " + strconv.Itoa(i), "active": false})
		ack, err := c.AddMonitor(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		var r struct{ MonitorID int64 }
		_ = json.Unmarshal(ack, &r)
		created = append(created, r.MonitorID)
		if _, err := c.PauseMonitor(ctx, r.MonitorID); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for w := range 12 {
		wg.Go(func() {
			for range 5 {
				ack, err := c.AddTag(ctx, map[string]any{"name": "kuma-it " + suffix, "color": "#000000"})
				if err != nil {
					errs <- err
					return
				}
				var r struct{ Tag struct{ ID int64 } }
				_ = json.Unmarshal(ack, &r)
				if _, err := c.DeleteTag(ctx, r.Tag.ID); err != nil {
					errs <- err
				}
			}
		})
		wg.Go(func() {
			for i := range 5 {
				m, _ := NewMonitor(map[string]any{"type": "group", "name": "kuma-it " + suffix + " " + strconv.Itoa(w*10+i)})
				ack, err := c.AddMonitor(ctx, m)
				if err != nil {
					errs <- err
					return
				}
				var r struct{ MonitorID int64 }
				_ = json.Unmarshal(ack, &r)
				mu.Lock()
				created = append(created, r.MonitorID)
				mu.Unlock()
				for _, step := range []func(context.Context, int64) (json.RawMessage, error){c.PauseMonitor, c.ResumeMonitor, c.DeleteMonitor} {
					if _, err := step(ctx, r.MonitorID); err != nil {
						errs <- err
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// Let a verification of a suspect list finish.
	time.Sleep(raceWindow + 2*verifyDelay)

	// Read State before the second login: that login makes Uptime Kuma
	// push a fresh list to every socket of the user, which would hide a
	// wrong State.
	got := monitorIDs(c.State())
	fresh := realKumaClient(t)
	if want := monitorIDs(fresh.State()); got != want {
		t.Fatalf("State's monitors %q, a fresh login's %q", got, want)
	}
}
