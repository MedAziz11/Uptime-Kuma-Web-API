package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio/siotest"
	"github.com/coder/websocket"
)

func TestLazyConnectLoginAndPushes(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)

	pushes := make(chan string, 8)
	s.Handle("info", func(args []json.RawMessage) { pushes <- "info " + string(args[0]) })
	s.Handle("monitorList", func(args []json.RawMessage) { pushes <- "monitorList " + string(args[0]) })
	s.Handle("ignored", func([]json.RawMessage) { t.Error("removed handler called") })
	s.Handle("ignored", nil)

	if f.Connections() != 0 {
		t.Fatal("New connected eagerly")
	}
	got, err := s.Call(context.Background(), "echo", map[string]int{"id": 7})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"id":7}` {
		t.Fatalf("Call = %s", got)
	}
	if f.count("login") != 1 || f.count("loginByToken") != 0 {
		t.Fatalf("login = %d, loginByToken = %d", f.count("login"), f.count("loginByToken"))
	}
	if p := waitFor(t, pushes, "info"); p != `info {"primaryBaseURL":null,"serverTimezone":"UTC"}` {
		t.Fatal(p)
	}
	if p := waitFor(t, pushes, "monitorList"); p != `monitorList {"1":{"id":1,"name":"m"}}` {
		t.Fatal(p)
	}

	// The connection is reused.
	for range 3 {
		if _, err := s.Call(context.Background(), "echo", 1); err != nil {
			t.Fatal(err)
		}
	}
	if f.Connections() != 1 || f.count("login") != 1 {
		t.Fatalf("connections = %d, logins = %d", f.Connections(), f.count("login"))
	}
}

func TestCommandError(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	_, err := s.Call(context.Background(), "fail")
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Event != "fail" || ce.Msg != "Monitor not found" {
		t.Fatalf("err = %#v", err)
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrTimeout) {
		t.Fatal("command error classified as unavailable/timeout")
	}
	if err.Error() != "kuma: fail: Monitor not found" {
		t.Fatal(err.Error())
	}
}

func TestConcurrentFirstCallsShareOneAttempt(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)

	const n = 200
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			got, err := s.Call(context.Background(), "echo", i)
			if err == nil && string(got) != fmt.Sprint(i) {
				err = fmt.Errorf("call %d got %s", i, got)
			}
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if f.Connections() != 1 || f.count("login") != 1 {
		t.Fatalf("connections = %d, logins = %d, want 1 and 1", f.Connections(), f.count("login"))
	}
}

func TestTimeoutThenLateAck(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	slow := make(chan func(), 1)
	f.command("slow", func(s *siotest.Socket, ev siotest.Event) {
		slow <- func() { _ = s.Ack(ev.ID, map[string]any{"ok": true, "late": true}) }
	})
	s := newSession(t, f, 200*time.Millisecond)
	if _, err := s.Call(context.Background(), "echo", 0); err != nil { // connect and log in first
		t.Fatal(err)
	}

	_, err := s.Call(context.Background(), "slow")
	if !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	ack := waitFor(t, slow, "the slow command")
	ack() // late: must be dropped

	got, err := s.Call(context.Background(), "echo", "next")
	if err != nil || string(got) != `"next"` {
		t.Fatalf("after late ack: %s, %v", got, err)
	}
	if f.Connections() != 1 {
		t.Fatalf("connections = %d: a timeout must not reconnect", f.Connections())
	}
}

func TestCallerCancellation(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	received := make(chan struct{}, 1)
	f.command("hang", func(*siotest.Socket, siotest.Event) { received <- struct{}{} })
	s := newSession(t, f, testTimeout)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Call(ctx, "hang")
		done <- err
	}()
	waitFor(t, received, "the command")
	cancel()
	err := waitFor(t, done, "the call")
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want context.Canceled only", err)
	}
}

func TestDisconnectMidCallThenReconnectWithToken(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.command("die", func(s *siotest.Socket, _ siotest.Event) { s.Kill() })
	s := newSession(t, f, testTimeout)

	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.Logins, "the first login")

	_, err := s.Call(context.Background(), "die")
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, sio.ErrClosed) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if how := waitFor(t, f.Logins, "the reconnection"); how != "loginByToken" {
		t.Fatalf("reconnected with %s, want loginByToken", how)
	}
	if got, err := s.Call(context.Background(), "echo", 2); err != nil || string(got) != "2" {
		t.Fatalf("after reconnect: %s, %v", got, err)
	}
	if f.count("login") != 1 || f.count("loginByToken") != 1 || f.Connections() != 2 {
		t.Fatalf("login = %d, loginByToken = %d, connections = %d", f.count("login"), f.count("loginByToken"), f.Connections())
	}
}

func TestTokenRejectedFallsBackToPassword(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.Logins, "the first login")

	f.set(func(f *fakeKuma) { f.rejectTokens = true })
	f.DisconnectAll()
	if how := waitFor(t, f.Logins, "the reconnection"); how != "login" {
		t.Fatalf("reconnected with %s, want login", how)
	}
	if _, err := s.Call(context.Background(), "echo", 2); err != nil {
		t.Fatal(err)
	}
	if f.count("loginByToken") != 1 || f.count("login") != 2 {
		t.Fatalf("loginByToken = %d, login = %d, want 1 and 2", f.count("loginByToken"), f.count("login"))
	}
}

func TestTwoFactorRequiredIsFatal(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.set(func(f *fakeKuma) { f.require2FA = true })
	s := newSession(t, f, testTimeout)

	for range 3 {
		_, err := s.Call(context.Background(), "echo", 1)
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrTwoFactorRequired) {
			t.Fatalf("err = %v, want ErrTwoFactorRequired", err)
		}
	}
	if f.count("login") != 1 || f.Connections() != 1 {
		t.Fatalf("login = %d, connections = %d: 2FA must not be retried", f.count("login"), f.Connections())
	}
}

func TestWrongPasswordBacksOff(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.set(func(f *fakeKuma) { f.password = "other" })
	s := newSession(t, f, testTimeout)
	s.minBackoff, s.maxBackoff = time.Hour, time.Hour // the second call lands in the backoff

	for range 2 {
		_, err := s.Call(context.Background(), "echo", 1)
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrLoginRejected) {
			t.Fatalf("err = %v, want ErrLoginRejected", err)
		}
		var ce *CommandError
		if errors.As(err, &ce) {
			t.Fatal("a login failure must not look like a command error")
		}
	}
	if f.count("login") != 1 {
		t.Fatalf("login = %d: calls during the backoff must not log in", f.count("login"))
	}
}

func TestWrongPasswordRetriesAfterBackoff(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.set(func(f *fakeKuma) { f.password = "other" })
	s := newSession(t, f, testTimeout)
	if _, err := s.Call(context.Background(), "echo", 1); !errors.Is(err, ErrLoginRejected) {
		t.Fatalf("err = %v", err)
	}
	f.set(func(f *fakeKuma) { f.password = "secret" })
	if how := waitFor(t, f.Logins, "a retried login"); how != "login" {
		t.Fatal(how)
	}
	if _, err := s.CallIdempotent(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
}

func TestPingTimeoutReconnects(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{NoPing: true, PingInterval: 10 * time.Millisecond, PingTimeout: 10 * time.Millisecond})
	s := newSession(t, f, testTimeout)
	if _, err := s.Call(context.Background(), "echo", 1); err != nil && !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	waitFor(t, f.Logins, "the first login")
	// No ping ever arrives: the session must drop the connection and log in
	// again with its token.
	if how := waitFor(t, f.Logins, "a login after the ping timeout"); how != "loginByToken" {
		t.Fatal(how)
	}
}

func TestCloseWithCallsInFlight(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	const n = 10
	received := make(chan struct{}, n)
	f.command("hang", func(*siotest.Socket, siotest.Event) { received <- struct{}{} })
	s := newSession(t, f, testTimeout)

	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := s.Call(context.Background(), "hang")
			errs <- err
		}()
	}
	for range n {
		waitFor(t, received, "the calls to reach the server")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for range n {
		if err := waitFor(t, errs, "a call to fail"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	}
	if _, err := s.Call(context.Background(), "echo", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("call after Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal("second Close:", err)
	}
}

func TestCloseWhileConnecting(t *testing.T) {
	checkLeaks(t)
	hs, accepted := hangingServer(t)
	s, err := New(Config{URL: hs.URL, Username: "u", Password: "p", Timeout: testTimeout})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), "echo", 1)
		done <- err
	}()
	waitFor(t, accepted, "the WebSocket")
	_ = s.Close()
	if err := waitFor(t, done, "the call"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectTimeout(t *testing.T) {
	checkLeaks(t)
	hs, _ := hangingServer(t)
	s, err := New(Config{URL: hs.URL, Username: "u", Password: "p", Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Call(context.Background(), "echo", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// hangingServer accepts WebSockets and never speaks.
func hangingServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	accepted := make(chan struct{}, 16)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		accepted <- struct{}{}
		_, _, _ = ws.Read(r.Context()) // until the client goes away
	}))
	t.Cleanup(hs.Close)
	return hs, accepted
}

func TestUnreachable(t *testing.T) {
	checkLeaks(t)
	hs := httptest.NewServer(http.NotFoundHandler())
	url := hs.URL
	hs.Close()
	s, err := New(Config{URL: url, Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Call(context.Background(), "echo", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestCallIdempotentRetriesOnce(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.command("read", func(s *siotest.Socket, ev siotest.Event) {
		if f.count("read") == 1 {
			s.Kill()
			return
		}
		_ = s.Ack(ev.ID, map[string]any{"ok": true, "n": f.count("read")})
	})
	f.command("write", func(s *siotest.Socket, ev siotest.Event) { s.Kill() })
	s := newSession(t, f, testTimeout)

	got, err := s.CallIdempotent(context.Background(), "read")
	if err != nil || string(got) != `{"n":2,"ok":true}` {
		t.Fatalf("CallIdempotent = %s, %v", got, err)
	}

	waitFor(t, f.Logins, "the first login")
	waitFor(t, f.Logins, "the second login")
	if _, err := s.Call(context.Background(), "write"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("write: %v", err)
	}
	if f.count("write") != 1 {
		t.Fatalf("write sent %d times: Call must not retry", f.count("write"))
	}
	waitFor(t, f.Logins, "the login after the write")
	// Errors other than a lost connection are not retried either.
	if _, err := s.CallIdempotent(context.Background(), "fail"); !errors.As(err, new(*CommandError)) || f.count("fail") != 1 {
		t.Fatalf("fail: %v (%d sends)", err, f.count("fail"))
	}
}

func TestCallIdempotentGivesUpWhenClosed(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	f.command("read", func(*siotest.Socket, siotest.Event) {
		go func() { _ = s.Close() }() // the session is closed while the call is in flight
	})
	if _, err := s.CallIdempotent(context.Background(), "read"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if f.count("read") != 1 {
		t.Fatalf("read sent %d times", f.count("read"))
	}
}

func TestArgumentErrors(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	_, err := s.Call(context.Background(), "echo", func() {})
	if err == nil || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
}

func TestNoAckArguments(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.command("logout", func(s *siotest.Socket, ev siotest.Event) { _ = s.Ack(ev.ID) })
	s := newSession(t, f, testTimeout)
	got, err := s.Call(context.Background(), "logout")
	if err != nil || string(got) != "null" {
		t.Fatalf("Call = %s, %v", got, err)
	}
}

func TestNewValidation(t *testing.T) {
	for _, cfg := range []Config{
		{URL: "uptime-kuma:3001"},
		{URL: "ftp://host"},
		{URL: "http://"},
		{URL: "http://%zz"},
		{URL: "http://host", Timeout: -1},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v): want error", cfg)
		}
	}
	s, err := New(Config{URL: "https://user:pw@kuma.example/sub/"})
	if err != nil {
		t.Fatal(err)
	}
	if s.timeout != DefaultTimeout {
		t.Fatalf("timeout = %v", s.timeout)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestJitter(t *testing.T) {
	if jitter(0, time.Second) != 0 {
		t.Fatal("jitter(0)")
	}
	for range 1000 {
		d := jitter(minBackoff, maxBackoff)
		if d < minBackoff || d > minBackoff*3/2 {
			t.Fatalf("jitter(250ms) = %v", d)
		}
		if d := jitter(maxBackoff, maxBackoff); d != maxBackoff {
			t.Fatalf("jitter(max) = %v", d)
		}
	}
}

func TestCommandErrorDecoding(t *testing.T) {
	cases := []struct {
		ack  string
		want *CommandError
	}{
		{`{"ok":true}`, nil},
		{`{"ok":true,"msg":"Saved."}`, nil},
		{`true`, nil},
		{`null`, nil},
		{`[{"ok":false}]`, nil},
		{`{"monitorID":1}`, nil},
		{`{"note":"\"ok\":false"}`, nil},
		{`{"ok":"false"}`, nil},
		{`{"ok":false,"msg":"Monitor not found"}`, &CommandError{Event: "e", Msg: "Monitor not found"}},
		{` {"ok" : false, "msg":"authIncorrectCreds","msgi18n":true}`, &CommandError{Event: "e", Msg: "authIncorrectCreds", I18n: true}},
		{`{"ok":false}`, &CommandError{Event: "e"}},
		{`{"ok":false,"msg":null}`, &CommandError{Event: "e"}},
		{`{"ok":false,"msg":{"code":"SQLITE_CONSTRAINT"}}`, &CommandError{Event: "e", Msg: `{"code":"SQLITE_CONSTRAINT"}`}},
	}
	for _, tc := range cases {
		err := commandError("e", json.RawMessage(tc.ack))
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: err = %v, want nil", tc.ack, err)
			}
			continue
		}
		var ce *CommandError
		if !errors.As(err, &ce) || *ce != *tc.want {
			t.Errorf("%s: err = %#v, want %#v", tc.ack, err, tc.want)
		}
	}
	if (&CommandError{Event: "e"}).Error() != "kuma: e failed" {
		t.Error((&CommandError{Event: "e"}).Error())
	}
}
