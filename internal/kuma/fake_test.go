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
// relies on (server/server.js, docs/kuma-2x-notes.md):
//
//   - after connect it pushes "info", then, at a point the test controls,
//     registers its handlers and emits "loginRequired" (or "autoLogin" when
//     authentication is disabled). Commands received before are dropped
//     without an ack, as the real server does;
//   - login / loginByToken answer like the real handlers, followed by the
//     "monitorList" push;
//   - other commands on a socket that is not logged in are answered
//     {ok:false, msg:"You are not logged in."}.
type fakeKuma struct {
	*siotest.Server

	// Logins reports each successful login: "login" or "loginByToken".
	Logins chan string
	// Rejected reports each rejected login, with the event name.
	Rejected chan string
	// Pongs reports each Engine.IO pong received.
	Pongs chan struct{}
	// release lets one socket waiting for readiness (holdReady) go on.
	release chan struct{}

	wg sync.WaitGroup

	mu           sync.Mutex
	password     string
	require2FA   bool
	rejectTokens bool
	autoLogin    bool
	holdReady    bool
	dropped      int
	tokens       map[string]bool
	counts       map[string]int
	commands     map[string]func(s *siotest.Socket, ev siotest.Event)
	sockets      map[*siotest.Socket]*fakeSocket
}

type fakeSocket struct {
	ready    bool // handlers registered, readiness event sent
	loggedIn bool
}

func newFakeKuma(t *testing.T, opts siotest.Options) *fakeKuma {
	t.Helper()
	f := &fakeKuma{
		Logins:   make(chan string, 64),
		Rejected: make(chan string, 64),
		Pongs:    make(chan struct{}, 1),
		release:  make(chan struct{}),
		password: "secret",
		tokens:   map[string]bool{},
		counts:   map[string]int{},
		commands: map[string]func(*siotest.Socket, siotest.Event){},
		sockets:  map[*siotest.Socket]*fakeSocket{},
	}
	opts.OnConnect = f.onConnect
	opts.OnEvent = f.onEvent
	opts.OnPong = func(*siotest.Socket) {
		select {
		case f.Pongs <- struct{}{}:
		default:
		}
	}
	f.Server = siotest.NewServer(opts)
	t.Cleanup(func() {
		f.Close()
		f.wg.Wait()
	})
	return f
}

// set runs fn under the fake's lock, to change its behaviour.
func (f *fakeKuma) set(fn func(f *fakeKuma)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// command installs a handler for a command (run once the socket is ready
// and logged in).
func (f *fakeKuma) command(name string, h func(s *siotest.Socket, ev siotest.Event)) {
	f.set(func(f *fakeKuma) { f.commands[name] = h })
}

// count returns how many times the server handled event.
func (f *fakeKuma) count(event string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[event]
}

// droppedCommands returns how many commands arrived before readiness.
func (f *fakeKuma) droppedCommands() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dropped
}

// logoutAll makes every socket lose its login, as after a server-side
// logout, while the connections stay open.
func (f *fakeKuma) logoutAll() {
	f.set(func(f *fakeKuma) {
		for _, st := range f.sockets {
			st.loggedIn = false
		}
	})
}

func (f *fakeKuma) onConnect(s *siotest.Socket) {
	f.mu.Lock()
	f.sockets[s] = &fakeSocket{}
	hold := f.holdReady
	f.mu.Unlock()

	_ = s.Emit("info", map[string]any{"primaryBaseURL": nil, "serverTimezone": "UTC"})
	if !hold {
		f.becomeReady(s)
		return
	}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		select {
		case <-f.release:
			f.becomeReady(s)
		case <-s.Closed():
		}
	}()
}

func (f *fakeKuma) becomeReady(s *siotest.Socket) {
	f.mu.Lock()
	st := f.sockets[s]
	st.ready = true
	auto := f.autoLogin
	st.loggedIn = auto
	f.mu.Unlock()
	if auto {
		_ = s.Emit("autoLogin")
		return
	}
	_ = s.Emit("loginRequired")
}

func (f *fakeKuma) onEvent(s *siotest.Socket, ev siotest.Event) {
	f.mu.Lock()
	st := f.sockets[s]
	if !st.ready {
		f.dropped++ // no handler registered yet: never acked
		f.mu.Unlock()
		return
	}
	f.counts[ev.Name]++
	n := f.counts[ev.Name]
	h := f.commands[ev.Name]
	loggedIn := st.loggedIn
	password, require2FA, rejectTokens := f.password, f.require2FA, f.rejectTokens
	f.mu.Unlock()

	switch {
	case ev.Name == "login":
		var req struct{ Username, Password, Token string }
		_ = json.Unmarshal(ev.Args[0], &req)
		switch {
		case req.Password != password:
			_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "authIncorrectCreds", "msgi18n": true})
			f.signal(f.Rejected, "login")
		case require2FA && req.Token == "":
			_ = s.Ack(ev.ID, map[string]any{"tokenRequired": true})
		default:
			token := "jwt-" + strconv.Itoa(n)
			f.set(func(f *fakeKuma) { f.tokens[token] = true; st.loggedIn = true })
			_ = s.Ack(ev.ID, map[string]any{"ok": true, "token": token})
			f.loggedIn(s, "login")
		}
	case ev.Name == "loginByToken":
		var token string
		_ = json.Unmarshal(ev.Args[0], &token)
		f.mu.Lock()
		valid := f.tokens[token] && !rejectTokens
		if valid {
			st.loggedIn = true
		}
		f.mu.Unlock()
		if !valid {
			_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "authInvalidToken", "msgi18n": true})
			f.signal(f.Rejected, "loginByToken")
			return
		}
		_ = s.Ack(ev.ID, map[string]any{"ok": true})
		f.loggedIn(s, "loginByToken")
	case !loggedIn:
		_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "You are not logged in."})
	case h != nil:
		h(s, ev)
	case ev.Name == "echo":
		_ = s.Ack(ev.ID, ev.Args[0])
	case ev.Name == "fail":
		_ = s.Ack(ev.ID, map[string]any{"ok": false, "msg": "Monitor not found"})
	}
}

func (f *fakeKuma) loggedIn(s *siotest.Socket, how string) {
	_ = s.Emit("monitorList", map[string]any{"1": map[string]any{"id": 1, "name": "m"}})
	f.signal(f.Logins, how)
}

func (f *fakeKuma) signal(ch chan string, v string) {
	select {
	case ch <- v:
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
	s.minBackoff, s.maxBackoff, s.stableAfter = time.Millisecond, 4*time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// checkLeaks fails the test if a goroutine of the session, the client or the
// fake server outlives the test's other cleanups. Call it first.
func checkLeaks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		// A goroutine that a Close waited for may still be returning (its
		// deferred wg.Done has run): give it a moment to end.
		deadline := time.Now().Add(2 * time.Second)
		for {
			buf := make([]byte, 1<<20)
			stacks := string(buf[:runtime.Stack(buf, true)])
			leaked := ""
			for _, fn := range []string{"kuma.(*Session).supervise", "sio.(*Conn).readLoop", "sio.(*Conn).writeLoop", "siotest.(*Socket).pingLoop", "siotest.(*Server).serve", "kuma.(*fakeKuma).onConnect"} {
				if strings.Contains(stacks, fn) {
					leaked = fn
				}
			}
			if leaked == "" {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("goroutine leak: %s is still running\n%s", leaked, stacks)
				return
			}
			time.Sleep(5 * time.Millisecond)
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
