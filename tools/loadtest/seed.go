package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Seeding: "loadtest seed" creates monitors (and optionally tags) through
// the API for a load test and records their ids in a state file; "loadtest
// unseed" removes exactly the objects recorded there. The monitors are
// groups: Uptime Kuma runs no check for them, so they cost it nothing.

// seedState is the state file: what seed created.
type seedState struct {
	URL      string  `json:"url"`
	Monitors []int64 `json:"monitors"`
	Tags     []int64 `json:"tags"`
}

type seedConfig struct {
	base, user, prefix, state string
	monitors, tags, parallel  int
}

func parseSeedFlags(name string, args []string, out io.Writer) (*seedConfig, error) {
	c := &seedConfig{}
	fs := flag.NewFlagSet("loadtest "+name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&c.base, "url", "http://127.0.0.1:8000", "base URL of the API")
	fs.StringVar(&c.user, "user", "admin", "user to log in as when LOADTEST_TOKEN is not set")
	fs.StringVar(&c.state, "state", "", "state file listing what was created (required)")
	if name == "seed" {
		fs.IntVar(&c.monitors, "monitors", 200, "group monitors to create")
		fs.IntVar(&c.tags, "tags", 0, "tags to create")
		fs.StringVar(&c.prefix, "prefix", "loadtest", "name prefix of the created objects")
		fs.IntVar(&c.parallel, "parallel", 8, "concurrent requests")
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if c.state == "" {
		return nil, errors.New("-state is required")
	}
	if c.monitors < 0 || c.tags < 0 {
		return nil, errors.New("-monitors and -tags must not be negative")
	}
	c.parallel = max(c.parallel, 1)
	c.base = strings.TrimSuffix(c.base, "/")
	return c, nil
}

// api sends authenticated JSON requests.
type api struct {
	client *http.Client
	base   string
	token  string
}

// do sends a request and decodes a 2xx answer into out (if not nil). It
// returns the status.
func (a *api) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return res.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, res.StatusCode, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return res.StatusCode, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return res.StatusCode, nil
}

func newAPI(ctx context.Context, getenv func(string) string, base, user string) (*api, error) {
	token, err := bearer(ctx, getenv, base, user)
	if err != nil {
		return nil, err
	}
	return &api{client: &http.Client{Timeout: 60 * time.Second}, base: base, token: token}, nil
}

func runSeed(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	c, err := parseSeedFlags("seed", args, stdout)
	if err != nil {
		return err
	}
	if _, err := os.Stat(c.state); err == nil {
		return fmt.Errorf("%s exists: unseed it first", c.state)
	}
	a, err := newAPI(ctx, getenv, c.base, c.user)
	if err != nil {
		return err
	}
	st := &seedState{URL: c.base, Monitors: []int64{}, Tags: []int64{}}
	var mu sync.Mutex
	// The state file is rewritten after every creation, so that what was
	// created can be removed even when seeding fails half way.
	record := func(add func()) error {
		mu.Lock()
		defer mu.Unlock()
		add()
		return writeState(c.state, st)
	}
	if err := record(func() {}); err != nil {
		return err
	}
	for i := range c.tags {
		var tag struct {
			ID int64 `json:"id"`
		}
		body := map[string]any{"name": c.prefix + "-tag-" + strconv.Itoa(i+1), "color": "#2563eb"}
		if _, err := a.do(ctx, http.MethodPost, "/tags", body, &tag); err != nil {
			return err
		}
		if err := record(func() { st.Tags = append(st.Tags, tag.ID) }); err != nil {
			return err
		}
	}
	sem := make(chan struct{}, c.parallel)
	errc := make(chan error, c.monitors)
	var wg sync.WaitGroup
	for i := range c.monitors {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			var ack struct {
				MonitorID int64 `json:"monitorID"`
			}
			body := map[string]any{"type": "group", "name": fmt.Sprintf("%s-%03d", c.prefix, i+1)}
			if _, err := a.do(ctx, http.MethodPost, "/monitors", body, &ack); err != nil {
				errc <- err
				return
			}
			if err := record(func() { st.Monitors = append(st.Monitors, ack.MonitorID) }); err != nil {
				errc <- err
			}
		})
	}
	wg.Wait()
	close(errc)
	if err := <-errc; err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created %d monitors and %d tags, recorded in %s\n", len(st.Monitors), len(st.Tags), c.state)
	return nil
}

func writeState(path string, st *seedState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func runUnseed(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	c, err := parseSeedFlags("unseed", args, stdout)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(c.state)
	if err != nil {
		return err
	}
	var st seedState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("%s: %w", c.state, err)
	}
	a, err := newAPI(ctx, getenv, c.base, c.user)
	if err != nil {
		return err
	}
	removed, gone := 0, 0
	remove := func(path string) error {
		status, err := a.do(ctx, http.MethodDelete, path, nil, nil)
		switch {
		case status == http.StatusNotFound:
			gone++
			return nil
		case err != nil:
			return err
		}
		removed++
		return nil
	}
	for _, id := range st.Monitors {
		if err := remove("/monitors/" + strconv.FormatInt(id, 10)); err != nil {
			return err
		}
	}
	for _, id := range st.Tags {
		if err := remove("/tags/" + strconv.FormatInt(id, 10)); err != nil {
			return err
		}
	}
	if err := os.Remove(c.state); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed %d objects (%d were already gone)\n", removed, gone)
	return nil
}
