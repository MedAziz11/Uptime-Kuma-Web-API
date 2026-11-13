package kuma

import (
	"encoding/json"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio/siotest"
)

const testTimeout = 5 * time.Second

// fakeKuma mimics the parts of Uptime Kuma 2.5.11's socket API the session
// relies on (server/server.js): "info" and "loginRequired" after connect,
// login / loginByToken with their acks, and the pushes following a login.
type fakeKuma struct {
	*siotest.Server

	// Logins reports each successful login: "login" or "loginByToken".
	Logins chan string

	mu           sync.Mutex
	password     string
	require2FA   bool
	rejectTokens bool
	tokens       map[string]bool
	counts       map[string]int
	commands     map[string]func(s *siotest.Socket, ev siotest.Event)
}

func newFakeKuma(t *testing.T, opts siotest.Options) *fakeKuma {
	t.Helper()
	f := &fakeKuma{
		Logins:   make(chan string, 64),
		password: "secret",
		tokens:   map[string]bool{},
		counts:   map[string]int{},
		commands: map[string]func(*siotest.Socket, siotest.Event){},
	}
	opts.OnConnect = func(s *siotest.Socket) {
		_ = s.Emit("info", map[string]any{"version": "2.5.11", "primaryBaseURL": nil})
		_ = s.Emit("loginRequired")
	}
	opts.OnEvent = f.onEvent
	f.Server = siotest.NewServer(opts)
	t.Cleanup(f.Close)
	return f
}

// set runs fn under the fake's lock, to change its behaviour.
func (f *fakeKuma) set(fn func(f *fakeKuma)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// command installs a handler for a command.
func (f *fakeKuma) command(name string, h func(s *siotest.Socket, ev siotest.Event)) {
	f.set(func(f *fakeKuma) { f.commands[name] = h })
}

// count returns how many times the server received event.
func (f *fakeKuma) count(event string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[event]
}

func (f *fakeKuma) onEvent(s *siotest.Socket, ev siotest.Event) {
	f.mu.Lock()
	f.counts[ev.Name]++
	n := f.counts[ev.Name]
	h := f.commands[ev.Name]
	password, require2FA, rejectTokens := f.password, f.require2FA, f.rejectTokens
	f.mu.Unlock()

	switch {
	case h != nil:
		h(s, ev)
	case ev.Name == "login":
		var req struct{ Username, Password, Token string }
		_ = json.Unmarshal(ev.Args[0], &req)
		switch {
		case req.Password != password:
			_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "authIncorrectCreds", "msgi18n": true})
		case require2FA && req.Token == "":
			_ = s.Ack(ev.ID, map[string]any{"tokenRequired": true})
		default:
			token := "jwt-" + strconv.Itoa(n)
			f.set(func(f *fakeKuma) { f.tokens[token] = true })
			_ = s.Ack(ev.ID, map[string]any{"ok": true, "token": token})
			f.loggedIn(s, "login")
		}
	case ev.Name == "loginByToken":
		var token string
		_ = json.Unmarshal(ev.Args[0], &token)
		f.mu.Lock()
		valid := f.tokens[token] && !rejectTokens
		f.mu.Unlock()
		if !valid {
			_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "authInvalidToken", "msgi18n": true})
			return
		}
		_ = s.Ack(ev.ID, map[string]any{"ok": true})
		f.loggedIn(s, "loginByToken")
	case ev.Name == "echo":
		_ = s.Ack(ev.ID, ev.Args[0])
	case ev.Name == "fail":
		_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "Monitor not found"})
	}
}

func (f *fakeKuma) loggedIn(s *siotest.Socket, how string) {
	_ = s.Emit("monitorList", map[string]any{"1": map[string]any{"id": 1, "name": "m"}})
	select {
	case f.Logins <- how:
	default:
	}
}

// newSession returns a session on f with short backoffs, closed at cleanup.
func newSession(t *testing.T, f *fakeKuma, timeout time.Duration) *Session {
	t.Helper()
	s, err := New(Config{URL: f.URL, Username: "admin", Password: "secret", Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	s.minBackoff, s.maxBackoff = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// checkLeaks fails the test if a goroutine of the session, the client or the
// fake server outlives the test's other cleanups. Call it first.
func checkLeaks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		buf := make([]byte, 1<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		for _, fn := range []string{"kuma.(*Session).supervise", "sio.(*Conn).readLoop", "sio.(*Conn).writeLoop", "siotest.(*Socket).pingLoop", "siotest.(*Server).serve"} {
			if strings.Contains(stacks, fn) {
				t.Errorf("goroutine leak: %s is still running\n%s", fn, stacks)
			}
		}
	})
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}
