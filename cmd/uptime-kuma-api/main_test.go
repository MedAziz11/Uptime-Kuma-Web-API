package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/version"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func lookup(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
}

func baseEnv(dir string) map[string]string {
	return map[string]string{
		"KUMA_SERVER":     "http://127.0.0.1:9", // nothing listens: the API must start anyway
		"KUMA_USERNAME":   "kuma-user",
		"KUMA_PASSWORD":   "kuma-password-secret",
		"ADMIN_PASSWORD":  "admin-password-secret",
		"DB_PATH":         filepath.Join(dir, "db", "db.sqlite3"),
		"LISTEN_ADDR":     "127.0.0.1:0",
		"KUMA_SSL_VERIFY": "false",
	}
}

// start runs the command until the returned stop is called; stop returns
// the exit status.
func start(t *testing.T, vars map[string]string, logs io.Writer) (string, func() int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	code := make(chan int, 1)
	go func() { code <- run(ctx, nil, lookup(vars), io.Discard, logs, func(a net.Addr) { addr <- a }) }()
	select {
	case a := <-addr:
		return "http://" + a.String(), func() int { cancel(); return <-code }
	case c := <-code:
		cancel()
		t.Fatalf("exited with %d: %s", c, logs)
	case <-time.After(30 * time.Second):
		t.Fatal("did not start")
	}
	return "", nil
}

func login(t *testing.T, base, password string) (string, int) {
	t.Helper()
	res, err := http.PostForm(base+"/login/access-token", url.Values{"username": {"admin"}, "password": {password}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	json.NewDecoder(res.Body).Decode(&tok)
	return tok.AccessToken, res.StatusCode
}

func TestStartServeAndRestart(t *testing.T) {
	dir := t.TempDir()
	vars := baseEnv(dir)
	logs := &lockedBuffer{}
	base, stop := start(t, vars, logs)

	res, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(b) != `{"status":"ok","version":"`+version.Version+`"}` {
		t.Fatalf("health %d %s", res.StatusCode, b)
	}
	token, status := login(t, base, "admin-password-secret")
	if status != 200 {
		t.Fatalf("login %d", status)
	}
	if code := stop(); code != 0 {
		t.Fatalf("exit %d: %s", code, logs)
	}

	out := logs.String()
	for _, want := range []string{`"msg":"created the 'admin' user"`, `"msg":"SECRET_KEY is not set: generated one and stored it in `, `"msg":"uptime-kuma-api started"`, `"msg":"uptime-kuma-api stopped"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
	for _, secret := range []string{"admin-password-secret", "kuma-password-secret", token} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains a secret: %s", out)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("not a JSON log line: %s", line)
		}
	}

	// Restart with another ADMIN_PASSWORD: same key file (the token still
	// works), and the admin password follows the environment.
	vars["ADMIN_PASSWORD"] = "changed-password"
	logs2 := &lockedBuffer{}
	base, stop = start(t, vars, logs2)
	req, _ := http.NewRequest("GET", base+"/users/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("token after restart: %d", res.StatusCode)
	}
	if _, status := login(t, base, "admin-password-secret"); status != 401 {
		t.Fatalf("old admin password: %d", status)
	}
	if _, status := login(t, base, "changed-password"); status != 200 {
		t.Fatalf("new admin password: %d", status)
	}
	stop()
	if !strings.Contains(logs2.String(), "updated the 'admin' password from ADMIN_PASSWORD") {
		t.Fatalf("logs %s", logs2)
	}
}

func TestConfigurationErrors(t *testing.T) {
	var stderr bytes.Buffer
	code := run(context.Background(), nil, lookup(map[string]string{"KUMA_PASSWORD": "kuma-password-secret", "KUMA_TIMEOUT": "0"}), io.Discard, &stderr, nil)
	if code != exitConfig {
		t.Fatalf("exit %d", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "missing required environment variables: KUMA_SERVER, KUMA_USERNAME, ADMIN_PASSWORD") ||
		!strings.Contains(out, "KUMA_TIMEOUT must be greater than 0") || strings.Contains(out, "kuma-password-secret") || !json.Valid(bytes.TrimSpace(stderr.Bytes())) {
		t.Fatalf("stderr %s", out)
	}
	if code := run(context.Background(), []string{"-bogus"}, lookup(nil), io.Discard, io.Discard, nil); code != exitConfig {
		t.Fatalf("bad flag: %d", code)
	}
	var stdout bytes.Buffer
	if code := run(context.Background(), []string{"-version"}, lookup(nil), &stdout, io.Discard, nil); code != exitOK || stdout.String() != version.Version+"\n" {
		t.Fatalf("-version: %d %q", code, stdout.String())
	}
}

func TestRuntimeErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, nil, 0o600)

	for name, mutate := range map[string]func(map[string]string){
		"database": func(v map[string]string) { v["DB_PATH"] = filepath.Join(blocker, "db.sqlite3") },
		"secret key": func(v map[string]string) {
			os.MkdirAll(filepath.Join(dir, "k", "secret_key"), 0o700)
			v["DB_PATH"] = filepath.Join(dir, "k", "db.sqlite3")
		},
		"listen": func(v map[string]string) { v["LISTEN_ADDR"] = "256.0.0.1:1" },
	} {
		t.Run(name, func(t *testing.T) {
			vars := baseEnv(t.TempDir())
			mutate(vars)
			var stderr lockedBuffer
			if code := run(context.Background(), nil, lookup(vars), io.Discard, &stderr, nil); code != exitFailed {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), `"level":"ERROR"`) {
				t.Fatalf("stderr %s", stderr.String())
			}
		})
	}
}
