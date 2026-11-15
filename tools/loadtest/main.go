// Command loadtest sends HTTP requests to the API at a fixed rate and
// reports the latency distribution.
//
// The load is open loop: request i is due at start + i/rate whatever the
// server does, and its latency is measured from that due time, so a server
// that falls behind shows its queueing in the latencies instead of slowing
// the generator down. With -rate 0 the load is closed loop: each
// connection sends its next request when the previous one is answered.
//
// Each of the -conns workers owns one keep-alive connection. The token is
// taken from LOADTEST_TOKEN, or obtained by logging in as -user with the
// password in LOADTEST_PASSWORD; secrets are never command-line arguments.
//
// -flood K runs K more connections that log in continuously with a wrong
// password during the test (each attempt costs the server a bcrypt check)
// and reports their answers separately.
//
// Example, inside the Docker network of the API:
//
//	LOADTEST_PASSWORD=... loadtest -url http://ukapi-p4-go:8000 -path /monitors \
//	    -rate 5000 -conns 64 -duration 30s -warmup 5s
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

// config is the command line.
type config struct {
	base       string
	path       string
	method     string
	body       string
	user       string
	rate       float64
	conns      int
	duration   time.Duration
	warmup     time.Duration
	revalidate float64
	flood      int
	timeout    time.Duration
	jsonOut    bool
}

func parseFlags(args []string, stderr io.Writer) (*config, error) {
	c := &config{}
	fs := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.base, "url", "http://127.0.0.1:8000", "base URL of the API")
	fs.StringVar(&c.path, "path", "/monitors", "path to request")
	fs.StringVar(&c.method, "method", http.MethodGet, "HTTP method")
	fs.StringVar(&c.body, "body", "", "JSON request body (sent with Content-Type application/json)")
	fs.StringVar(&c.user, "user", "admin", "user to log in as when LOADTEST_TOKEN is not set")
	fs.Float64Var(&c.rate, "rate", 1000, "requests per second; 0 sends back to back on each connection (closed loop)")
	fs.IntVar(&c.conns, "conns", 64, "keep-alive connections")
	fs.DurationVar(&c.duration, "duration", 10*time.Second, "measured duration")
	fs.DurationVar(&c.warmup, "warmup", 0, "load before the measurement, not recorded")
	fs.Float64Var(&c.revalidate, "revalidate", 0, "fraction of requests sent with If-None-Match (the last ETag seen)")
	fs.IntVar(&c.flood, "flood", 0, "connections logging in continuously with a wrong password")
	fs.DurationVar(&c.timeout, "timeout", 30*time.Second, "timeout of one request")
	fs.BoolVar(&c.jsonOut, "json", false, "also print the results as one JSON line")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch {
	case c.conns < 1:
		return nil, errors.New("-conns must be at least 1")
	case c.rate < 0 || c.revalidate < 0 || c.revalidate > 1 || c.flood < 0:
		return nil, errors.New("-rate, -revalidate (0 to 1) and -flood must not be negative")
	case c.duration <= 0:
		return nil, errors.New("-duration must be positive")
	}
	if _, err := url.Parse(c.base); err != nil {
		return nil, fmt.Errorf("-url: %w", err)
	}
	c.base = strings.TrimSuffix(c.base, "/")
	return c, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "seed":
			return runSeed(ctx, args[1:], getenv, stdout)
		case "unseed":
			return runUnseed(ctx, args[1:], getenv, stdout)
		}
	}
	c, err := parseFlags(args, stdout)
	if err != nil {
		return err
	}
	token, err := bearer(ctx, getenv, c.base, c.user)
	if err != nil {
		return err
	}
	r := newRunner(c, token)
	res := r.run(ctx)
	res.print(stdout)
	if c.jsonOut {
		b, _ := json.Marshal(res)
		fmt.Fprintf(stdout, "%s\n", b)
	}
	return nil
}

// bearer returns LOADTEST_TOKEN, or logs in with LOADTEST_PASSWORD.
func bearer(ctx context.Context, getenv func(string) string, base, user string) (string, error) {
	if token := getenv("LOADTEST_TOKEN"); token != "" {
		return token, nil
	}
	pw := getenv("LOADTEST_PASSWORD")
	if pw == "" {
		return "", errors.New("set LOADTEST_TOKEN, or LOADTEST_PASSWORD to log in")
	}
	return login(ctx, newClient(30*time.Second), base, user, pw)
}

// newClient returns a client with one keep-alive connection.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:        1,
			MaxIdleConnsPerHost: 1,
			MaxConnsPerHost:     1,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true, // the API does not compress; do not ask
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// login gets a token from /login/access-token.
func login(ctx context.Context, client *http.Client, base, user, password string) (string, error) {
	form := url.Values{"username": {user}, "password": {password}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/login/access-token", strings.NewReader(form))
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	defer res.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login: HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("login: no token in the answer: %v", err)
	}
	return tok.AccessToken, nil
}

// sample is one answered (or failed) request.
type sample struct {
	latency time.Duration
	status  int // 0: transport error
	bytes   int64
}

// stats accumulates the samples of one worker; merged at the end.
type stats struct {
	latencies []time.Duration
	statuses  map[int]int
	bytes     int64
	errors    int // transport errors and unexpected statuses
}

func newStats() *stats { return &stats{statuses: map[int]int{}} }

func (s *stats) add(x sample, ok func(int) bool) {
	s.latencies = append(s.latencies, x.latency)
	s.statuses[x.status]++
	s.bytes += x.bytes
	if !ok(x.status) {
		s.errors++
	}
}

func (s *stats) merge(o *stats) {
	s.latencies = append(s.latencies, o.latencies...)
	for k, v := range o.statuses {
		s.statuses[k] += v
	}
	s.bytes += o.bytes
	s.errors += o.errors
}

// runner runs one load test.
type runner struct {
	c     *config
	token string
	etag  atomic.Pointer[string]
	// late counts requests sent after their due time because every
	// connection was busy (open loop only): their wait is in the latency.
	late atomic.Int64
}

func newRunner(c *config, token string) *runner { return &runner{c: c, token: token} }

// Results is what a run reports. Latencies are in milliseconds.
type Results struct {
	Method      string             `json:"method"`
	URL         string             `json:"url"`
	Rate        float64            `json:"rate"`
	Conns       int                `json:"conns"`
	Duration    float64            `json:"duration_s"`
	Revalidate  float64            `json:"revalidate"`
	Requests    int                `json:"requests"`
	Errors      int                `json:"errors"`
	Late        int64              `json:"late"`
	Achieved    float64            `json:"achieved_rps"`
	Statuses    map[string]int     `json:"statuses"`
	Bytes       int64              `json:"bytes"`
	Percentiles map[string]float64 `json:"percentiles"`
	Flood       *FloodResults      `json:"flood,omitempty"`
}

// FloodResults are the answers to the login flood.
type FloodResults struct {
	Conns       int                `json:"conns"`
	Requests    int                `json:"requests"`
	Statuses    map[string]int     `json:"statuses"`
	Percentiles map[string]float64 `json:"percentiles"`
}

var percentiles = []struct {
	name string
	q    float64
}{{"p50", 0.50}, {"p90", 0.90}, {"p99", 0.99}, {"p99.9", 0.999}, {"max", 1}}

func summarize(lat []time.Duration) map[string]float64 {
	out := map[string]float64{}
	if len(lat) == 0 {
		return out
	}
	slices.Sort(lat)
	for _, p := range percentiles {
		i := int(p.q*float64(len(lat))+0.5) - 1
		i = max(0, min(i, len(lat)-1))
		out[p.name] = float64(lat[i].Microseconds()) / 1000
	}
	return out
}

func statusNames(m map[int]int) map[string]int {
	out := map[string]int{}
	for k, v := range m {
		name := strconv.Itoa(k)
		if k == 0 {
			name = "error"
		}
		out[name] = v
	}
	return out
}

func (r *runner) run(ctx context.Context) *Results {
	c := r.c
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var flood []*stats
	var floodWG sync.WaitGroup
	for range c.flood {
		s := newStats()
		flood = append(flood, s)
		floodWG.Go(func() { r.floodLogins(ctx, s) })
	}

	total := newStats()
	measured := r.load(ctx, total)
	cancel()
	floodWG.Wait()

	res := &Results{
		Method: c.method, URL: c.base + c.path, Rate: c.rate, Conns: c.conns,
		Duration: c.duration.Seconds(), Revalidate: c.revalidate,
		Requests: len(total.latencies), Errors: total.errors, Late: r.late.Load(),
		Achieved: float64(len(total.latencies)) / measured.Seconds(),
		Statuses: statusNames(total.statuses), Bytes: total.bytes,
		Percentiles: summarize(total.latencies),
	}
	if c.flood > 0 {
		f := newStats()
		for _, s := range flood {
			f.merge(s)
		}
		res.Flood = &FloodResults{Conns: c.flood, Requests: len(f.latencies), Statuses: statusNames(f.statuses), Percentiles: summarize(f.latencies)}
	}
	return res
}

// load sends the requests and returns the measured duration.
func (r *runner) load(ctx context.Context, total *stats) time.Duration {
	c := r.c
	start := time.Now()
	measureFrom := start.Add(c.warmup)
	end := measureFrom.Add(c.duration)

	var due chan time.Time // open loop: the due time of each request
	if c.rate > 0 {
		due = make(chan time.Time, 1<<16)
		go r.schedule(ctx, start, end, due)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range c.conns {
		wg.Go(func() {
			s := newStats()
			r.worker(ctx, newClient(c.timeout), due, end, measureFrom, s)
			mu.Lock()
			total.merge(s)
			mu.Unlock()
		})
	}
	wg.Wait()
	return c.duration
}

// schedule emits the due times of an open-loop load until end.
func (r *runner) schedule(ctx context.Context, start, end time.Time, due chan<- time.Time) {
	defer close(due)
	interval := time.Duration(float64(time.Second) / r.c.rate)
	for i := 0; ; i++ {
		t := start.Add(time.Duration(i) * interval)
		if !t.Before(end) {
			return
		}
		if d := time.Until(t); d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return
			}
		}
		select {
		case due <- t:
		case <-ctx.Done():
			return
		}
	}
}

func (r *runner) worker(ctx context.Context, client *http.Client, due <-chan time.Time, end, measureFrom time.Time, s *stats) {
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	expected := func(status int) bool {
		return status >= 200 && status < 300 || status == http.StatusNotModified
	}
	for {
		var t time.Time
		if due != nil {
			var ok bool
			select {
			case t, ok = <-due:
				if !ok {
					return
				}
			case <-ctx.Done():
				return
			}
			if time.Since(t) > time.Millisecond {
				r.late.Add(1)
			}
		} else {
			t = time.Now()
			if !t.Before(end) || ctx.Err() != nil {
				return
			}
		}
		x := r.send(ctx, client, rng.Float64() < r.c.revalidate)
		x.latency = time.Since(t)
		if !t.Before(measureFrom) {
			s.add(x, expected)
		}
	}
}

// send sends one request of the load.
func (r *runner) send(ctx context.Context, client *http.Client, revalidate bool) sample {
	var body io.Reader
	if r.c.body != "" {
		body = strings.NewReader(r.c.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.c.method, r.c.base+r.c.path, body)
	if err != nil {
		return sample{}
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if revalidate {
		if etag := r.etag.Load(); etag != nil {
			req.Header.Set("If-None-Match", *etag)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return sample{}
	}
	n, err := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if err != nil {
		return sample{bytes: n}
	}
	if etag := res.Header.Get("ETag"); etag != "" && res.StatusCode == http.StatusOK {
		r.etag.Store(&etag)
	}
	return sample{status: res.StatusCode, bytes: n}
}

// floodLogins logs in with a wrong password until ctx ends.
func (r *runner) floodLogins(ctx context.Context, s *stats) {
	client := newClient(r.c.timeout)
	form := url.Values{"username": {"loadtest-flood"}, "password": {"not-the-password"}}.Encode()
	expected := func(status int) bool { return status == http.StatusUnauthorized }
	for ctx.Err() == nil {
		t := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.c.base+"/login/access-token", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() == nil {
				s.add(sample{latency: time.Since(t)}, expected)
			}
			continue
		}
		n, _ := io.Copy(io.Discard, res.Body)
		res.Body.Close()
		s.add(sample{latency: time.Since(t), status: res.StatusCode, bytes: n}, expected)
	}
}

func (res *Results) print(w io.Writer) {
	fmt.Fprintf(w, "%s %s  rate %s  conns %d  duration %gs  revalidate %.2f\n",
		res.Method, res.URL, rateText(res.Rate), res.Conns, res.Duration, res.Revalidate)
	fmt.Fprintf(w, "requests %d  errors %d  late %d  achieved %.1f/s\n", res.Requests, res.Errors, res.Late, res.Achieved)
	fmt.Fprintf(w, "statuses %s\n", statusText(res.Statuses))
	fmt.Fprintf(w, "latency ms %s\n", percentileText(res.Percentiles))
	per := int64(0)
	if res.Requests > 0 {
		per = res.Bytes / int64(res.Requests)
	}
	fmt.Fprintf(w, "bytes %d (%d per response, %.1f MB/s)\n", res.Bytes, per, float64(res.Bytes)/res.Duration/1e6)
	if f := res.Flood; f != nil {
		fmt.Fprintf(w, "login flood: conns %d  requests %d  statuses %s\n", f.Conns, f.Requests, statusText(f.Statuses))
		fmt.Fprintf(w, "login flood latency ms %s\n", percentileText(f.Percentiles))
	}
}

func rateText(rate float64) string {
	if rate == 0 {
		return "closed-loop"
	}
	return strconv.FormatFloat(rate, 'f', -1, 64) + "/s"
}

func statusText(m map[string]int) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+":"+strconv.Itoa(m[k]))
	}
	return strings.Join(parts, " ")
}

func percentileText(m map[string]float64) string {
	parts := make([]string, 0, len(percentiles))
	for _, p := range percentiles {
		parts = append(parts, fmt.Sprintf("%s %.3f", p.name, m[p.name]))
	}
	return strings.Join(parts, "  ")
}
