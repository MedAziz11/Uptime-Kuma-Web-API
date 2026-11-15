package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// benchMonitors is how many monitors the read benchmarks serve: the load
// target of architecture section 8.
const benchMonitors = 200

// discardWriter is a ResponseWriter that keeps only the status and the
// size, and reuses its header map, so that a benchmark counts the
// allocations of the handler chain rather than those of a recorder.
type discardWriter struct {
	h      http.Header
	status int
	n      int
}

func (w *discardWriter) Header() http.Header { return w.h }

func (w *discardWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (w *discardWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.n += len(b)
	return len(b), nil
}

func (w *discardWriter) reset() {
	clear(w.h)
	w.status, w.n = 0, 0
}

// monitorTemplate is a group monitor as Uptime Kuma 2.5.11 pushes it
// (recorded in internal/kuma/testdata).
func monitorTemplate(tb testing.TB) map[string]any {
	tb.Helper()
	b, err := os.ReadFile("../kuma/testdata/2.5.11/lifecycle/pushed.json")
	if err != nil {
		tb.Fatal(err)
	}
	var pushes []struct {
		After string            `json:"after"`
		Event string            `json:"event"`
		Args  []json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(b, &pushes); err != nil {
		tb.Fatal(err)
	}
	for _, p := range pushes {
		if p.After != "add.group" || p.Event != "updateMonitorIntoList" {
			continue
		}
		var byID map[string]map[string]any
		if err := json.Unmarshal(p.Args[0], &byID); err != nil {
			tb.Fatal(err)
		}
		for _, m := range byID {
			return m
		}
	}
	tb.Fatal("no recorded group monitor")
	return nil
}

// benchEnv is a server whose State holds benchMonitors recorded monitors,
// logging requests as in production (JSON, info level) to nowhere.
func benchEnv(tb testing.TB) (*testEnv, string) {
	tb.Helper()
	e := newEnv(tb, Options{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	token := e.login(tb, "admin", "admin-password")
	ctx := context.Background()
	if err := e.client.Sync(ctx); err != nil {
		tb.Fatal(err)
	}
	tmpl := monitorTemplate(tb)
	list := map[string]any{}
	for i := 1; i <= benchMonitors; i++ {
		m := maps.Clone(tmpl)
		m["id"], m["name"] = i, "bench-"+strconv.Itoa(i)
		list[strconv.Itoa(i)] = m
	}
	st := e.client.State()
	v := st.Version(kuma.Monitors)
	e.kuma.Push("monitorList", list)
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := st.WaitVersion(wctx, kuma.Monitors, v); err != nil {
		tb.Fatal(err)
	}
	if got, _ := st.Objects(kuma.Monitors); len(got) != benchMonitors {
		tb.Fatalf("%d monitors", len(got))
	}
	return e, token
}

// benchRequest serves req repeatedly through the full middleware chain and
// checks the status.
func benchRequest(b *testing.B, h http.Handler, req *http.Request, status int) {
	b.Helper()
	w := &discardWriter{h: http.Header{}}
	h.ServeHTTP(w, req) // warm: the first read builds what later reads reuse
	if w.status != status {
		b.Fatalf("status %d, want %d", w.status, status)
	}
	b.SetBytes(int64(w.n))
	b.ReportAllocs()
	for b.Loop() {
		w.reset()
		h.ServeHTTP(w, req)
		if w.status != status {
			b.Fatalf("status %d, want %d", w.status, status)
		}
	}
}

func authorized(path, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// BenchmarkGetMonitors is GET /monitors with 200 monitors, through every
// middleware.
func BenchmarkGetMonitors(b *testing.B) {
	e, token := benchEnv(b)
	benchRequest(b, e.srv, authorized("/monitors", token), http.StatusOK)
}

// BenchmarkGetMonitorsParallel is BenchmarkGetMonitors from GOMAXPROCS
// goroutines.
func BenchmarkGetMonitorsParallel(b *testing.B) {
	e, token := benchEnv(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := authorized("/monitors", token)
		w := &discardWriter{h: http.Header{}}
		for pb.Next() {
			w.reset()
			e.srv.ServeHTTP(w, req)
			if w.status != http.StatusOK {
				b.Errorf("status %d", w.status)
				return
			}
		}
	})
}

// BenchmarkGetMonitor is GET /monitors/{id}.
func BenchmarkGetMonitor(b *testing.B) {
	e, token := benchEnv(b)
	benchRequest(b, e.srv, authorized("/monitors/100", token), http.StatusOK)
}

// BenchmarkGetMonitorsNotModified is GET /monitors revalidated with the
// ETag of the previous answer.
func BenchmarkGetMonitorsNotModified(b *testing.B) {
	e, token := benchEnv(b)
	req := authorized("/monitors", token)
	w := &discardWriter{h: http.Header{}}
	e.srv.ServeHTTP(w, req)
	status := http.StatusOK // no ETag: nothing to revalidate
	if etag := w.h.Get("ETag"); etag != "" {
		req.Header.Set("If-None-Match", etag)
		status = http.StatusNotModified
	}
	benchRequest(b, e.srv, req, status)
}

// BenchmarkUnauthorized is a request with a token signed with another key.
func BenchmarkUnauthorized(b *testing.B) {
	e, _ := benchEnv(b)
	benchRequest(b, e.srv, authorized("/monitors", e.otherKeyToken(b)), http.StatusUnauthorized)
}

// BenchmarkHandler is a cached read without the middleware: the route
// alone, for a request that is already authenticated.
func BenchmarkHandler(b *testing.B) {
	e, _ := benchEnv(b)
	u, err := e.store.User(context.Background(), AdminUsername)
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct{ name, path, etag string }{
		{"monitors", "/monitors", ""}, {"monitor", "/monitors/100", ""}, {"not_modified", "/monitors", "*"},
	} {
		b.Run(c.name, func(b *testing.B) {
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			status := http.StatusOK
			if c.etag != "" {
				req.Header.Set("If-None-Match", c.etag)
				status = http.StatusNotModified
			}
			ctx := context.WithValue(req.Context(), infoKey, &requestInfo{id: "bench"})
			req = req.WithContext(context.WithValue(ctx, userKey, u))
			benchRequest(b, e.srv.mux, req, status)
		})
	}
}

func (e *testEnv) otherKeyToken(tb testing.TB) string {
	tb.Helper()
	tk, err := auth.NewTokens("another-key-another-key-another-key", time.Hour)
	if err != nil {
		tb.Fatal(err)
	}
	return tk.Issue(AdminUsername)
}
