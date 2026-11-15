package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI answers like the API: a login, a list with an ETag, creations
// and deletions.
type fakeAPI struct {
	mu          sync.Mutex
	nextID      int64
	objects     map[string]bool // "/monitors/1"
	revalidated atomic.Int64
	logins      atomic.Int64
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	f := &fakeAPI{objects: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/login/access-token" {
		f.logins.Add(1)
		_ = r.ParseForm()
		if r.PostForm.Get("password") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"tok","token_type":"bearer"}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/monitors":
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("If-None-Match") == `"v1"` {
			f.revalidated.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.WriteString(w, `{"monitors":[]}`)
	case r.Method == http.MethodPost && (r.URL.Path == "/monitors" || r.URL.Path == "/tags"):
		f.nextID++
		f.objects[r.URL.Path+"/"+strconv.FormatInt(f.nextID, 10)] = true
		if r.URL.Path == "/tags" {
			_, _ = io.WriteString(w, `{"id":`+strconv.FormatInt(f.nextID, 10)+`}`)
			return
		}
		_, _ = io.WriteString(w, `{"msg":"Added Successfully.","monitorID":`+strconv.FormatInt(f.nextID, 10)+`}`)
	case r.Method == http.MethodDelete:
		if !f.objects[r.URL.Path] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.objects, r.URL.Path)
		_, _ = io.WriteString(w, `{}`)
	case r.Method == http.MethodPatch:
		_, _ = io.WriteString(w, `{}`)
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestOpenLoopWithRevalidationAndFlood(t *testing.T) {
	f, srv := newFakeAPI(t)
	var out bytes.Buffer
	err := run(context.Background(), []string{"-url", srv.URL + "/", "-rate", "400", "-conns", "4",
		"-duration", "500ms", "-warmup", "100ms", "-revalidate", "0.5", "-flood", "2", "-json"},
		env(map[string]string{"LOADTEST_PASSWORD": "secret"}), &out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var res Results
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	// 400/s for 0.5 s: about 200 requests.
	if res.Requests < 150 || res.Requests > 210 || res.Errors != 0 {
		t.Fatalf("%+v", res)
	}
	if res.Statuses["200"] == 0 || res.Statuses["304"] == 0 || f.revalidated.Load() == 0 {
		t.Fatalf("statuses %v", res.Statuses)
	}
	if res.Flood == nil || res.Flood.Requests == 0 || res.Flood.Statuses["401"] != res.Flood.Requests {
		t.Fatalf("flood %+v", res.Flood)
	}
	for _, p := range []string{"p50", "p99", "max"} {
		if res.Percentiles[p] <= 0 {
			t.Fatalf("%s: %v", p, res.Percentiles)
		}
	}
	if !strings.Contains(out.String(), "login flood latency ms") {
		t.Fatal(out.String())
	}
}

func TestClosedLoopWithBodyAndToken(t *testing.T) {
	_, srv := newFakeAPI(t)
	var out bytes.Buffer
	err := run(context.Background(), []string{"-url", srv.URL, "-path", "/tags/1", "-method", "PATCH", "-body", `{"name":"x"}`,
		"-rate", "0", "-conns", "2", "-duration", "200ms"}, env(map[string]string{"LOADTEST_TOKEN": "tok"}), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "closed-loop") || !strings.Contains(out.String(), "statuses 200:") {
		t.Fatal(out.String())
	}
}

func TestUnexpectedStatusesAreErrors(t *testing.T) {
	_, srv := newFakeAPI(t)
	var out bytes.Buffer
	err := run(context.Background(), []string{"-url", srv.URL, "-path", "/other", "-rate", "100", "-conns", "1", "-duration", "100ms", "-json"},
		env(map[string]string{"LOADTEST_TOKEN": "tok"}), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"errors":10`) && !strings.Contains(out.String(), "418:") {
		t.Fatal(out.String())
	}
}

func TestArgumentErrors(t *testing.T) {
	_, srv := newFakeAPI(t)
	for _, c := range []struct {
		args []string
		env  map[string]string
	}{
		{[]string{"-conns", "0"}, nil},
		{[]string{"-rate", "-1"}, nil},
		{[]string{"-revalidate", "2"}, nil},
		{[]string{"-duration", "0s"}, nil},
		{[]string{"-url", "::"}, nil},
		{[]string{"-nope"}, nil},
		{[]string{"-url", srv.URL}, nil}, // no credentials
		{[]string{"-url", srv.URL}, map[string]string{"LOADTEST_PASSWORD": "wrong"}},
		{[]string{"-url", "http://127.0.0.1:1"}, map[string]string{"LOADTEST_PASSWORD": "x"}},
		{[]string{"seed"}, nil}, // no state
		{[]string{"seed", "-state", "x", "-monitors", "-1"}, nil},
		{[]string{"unseed", "-state", filepath.Join(t.TempDir(), "missing")}, nil},
	} {
		if err := run(context.Background(), c.args, env(c.env), io.Discard); err == nil {
			t.Errorf("%v: no error", c.args)
		}
	}
}

func TestSeedAndUnseed(t *testing.T) {
	f, srv := newFakeAPI(t)
	state := filepath.Join(t.TempDir(), "seed.json")
	e := env(map[string]string{"LOADTEST_PASSWORD": "secret"})
	var out bytes.Buffer
	if err := run(context.Background(), []string{"seed", "-url", srv.URL, "-state", state, "-monitors", "20", "-tags", "2"}, e, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.objects) != 22 {
		t.Fatalf("%d objects", len(f.objects))
	}
	var st seedState
	b, _ := os.ReadFile(state)
	if err := json.Unmarshal(b, &st); err != nil || len(st.Monitors) != 20 || len(st.Tags) != 2 {
		t.Fatalf("%v %s", err, b)
	}
	// A second seed would lose track of the first.
	if err := run(context.Background(), []string{"seed", "-url", srv.URL, "-state", state}, e, io.Discard); err == nil {
		t.Fatal("seeded over an existing state file")
	}
	// Something removed meanwhile is not an error.
	f.mu.Lock()
	delete(f.objects, "/monitors/"+strconv.FormatInt(st.Monitors[0], 10))
	f.mu.Unlock()
	out.Reset()
	if err := run(context.Background(), []string{"unseed", "-url", srv.URL, "-state", state}, e, &out); err != nil {
		t.Fatal(err)
	}
	if len(f.objects) != 0 || !strings.Contains(out.String(), "removed 21 objects (1 were already gone)") {
		t.Fatalf("%v %s", f.objects, out.String())
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state file kept")
	}
}

func TestSeedFailures(t *testing.T) {
	_, srv := newFakeAPI(t)
	dir := t.TempDir()
	e := env(map[string]string{"LOADTEST_TOKEN": "bad"})
	if err := run(context.Background(), []string{"seed", "-url", srv.URL, "-state", filepath.Join(dir, "a"), "-monitors", "1"}, e, io.Discard); err == nil {
		t.Fatal("seed with a bad token")
	}
	if err := run(context.Background(), []string{"seed", "-url", srv.URL, "-state", filepath.Join(dir, "b"), "-monitors", "0", "-tags", "1"}, e, io.Discard); err == nil {
		t.Fatal("tag with a bad token")
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{"), 0o600)
	if err := run(context.Background(), []string{"unseed", "-url", srv.URL, "-state", bad}, e, io.Discard); err == nil {
		t.Fatal("unseed of a broken state file")
	}
	st := filepath.Join(dir, "c.json")
	_ = writeState(st, &seedState{Monitors: []int64{1}})
	if err := run(context.Background(), []string{"unseed", "-url", srv.URL, "-state", st}, e, io.Discard); err == nil {
		t.Fatal("unseed with a bad token")
	}
	if err := run(context.Background(), []string{"seed", "-url", srv.URL, "-state", filepath.Join(dir, "missing", "x")}, env(map[string]string{"LOADTEST_TOKEN": "tok"}), io.Discard); err == nil {
		t.Fatal("seed into a missing directory")
	}
}

func TestCanceledRunStops(t *testing.T) {
	_, srv := newFakeAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := run(ctx, []string{"-url", srv.URL, "-rate", "50", "-conns", "1", "-duration", "1m", "-flood", "1"},
		env(map[string]string{"LOADTEST_TOKEN": "tok"}), io.Discard); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("did not stop")
	}
}

func TestSummarize(t *testing.T) {
	if len(summarize(nil)) != 0 {
		t.Fatal("percentiles of nothing")
	}
	var lat []time.Duration
	for i := 1; i <= 1000; i++ {
		lat = append(lat, time.Duration(i)*time.Millisecond)
	}
	p := summarize(lat)
	if p["p50"] != 500 || p["p99"] != 990 || p["p99.9"] != 999 || p["max"] != 1000 {
		t.Fatal(p)
	}
	if statusNames(map[int]int{0: 1})["error"] != 1 {
		t.Fatal("transport errors")
	}
}
