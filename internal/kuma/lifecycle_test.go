package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio/siotest"
)

// Readiness: Uptime Kuma drops commands sent before loginRequired/autoLogin.

func TestCommandsWaitForReadiness(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{PingInterval: 10 * time.Millisecond})
	f.set(func(f *fakeKuma) { f.holdReady = true })
	s := newSession(t, f, testTimeout)

	done := make(chan error, 1)
	go func() {
		got, err := s.Call(context.Background(), "echo", 1)
		if err == nil && string(got) != "1" {
			err = errors.New("unexpected answer " + string(got))
		}
		done <- err
	}()
	waitFor(t, f.Accepted(), "the connection")
	// A client that does not wait sends its login right after the CONNECT
	// answer; a pong to a ping sent later arrives after it.
	waitFor(t, f.Pongs, "a pong")
	f.release <- struct{}{} // the server registers its handlers now
	if err := waitFor(t, done, "the call"); err != nil {
		t.Fatal(err)
	}
	if n := f.droppedCommands(); n != 0 {
		t.Fatalf("%d commands were sent before the server was ready", n)
	}
	if f.count("login") != 1 {
		t.Fatalf("login = %d", f.count("login"))
	}
}

func TestAutoLoginSkipsLogin(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.set(func(f *fakeKuma) { f.autoLogin = true })
	s := newSession(t, f, testTimeout)
	got, err := s.Call(context.Background(), "echo", 1)
	if err != nil || string(got) != "1" {
		t.Fatalf("Call = %s, %v", got, err)
	}
	if f.count("login") != 0 || f.count("loginByToken") != 0 || f.droppedCommands() != 0 {
		t.Fatalf("login = %d, loginByToken = %d, dropped = %d, want 0", f.count("login"), f.count("loginByToken"), f.droppedCommands())
	}
}

func TestReadinessNeverArrives(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	f.set(func(f *fakeKuma) { f.holdReady = true }) // and never released
	s := newSession(t, f, 100*time.Millisecond)
	s.minBackoff, s.maxBackoff = time.Hour, time.Hour

	if _, err := s.Call(context.Background(), "echo", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	// The attempt ended before this call's own deadline: it gets the
	// attempt's error, then the session backs off.
	_, err := s.Call(context.Background(), "echo", 1)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "loginRequired") {
		t.Fatalf("err = %v, want the readiness failure", err)
	}
	if _, err := s.Call(context.Background(), "echo", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if f.Connections() != 1 || f.droppedCommands() != 0 {
		t.Fatalf("connections = %d, dropped = %d: want 1 attempt and no command sent", f.Connections(), f.droppedCommands())
	}
}

// "You are not logged in." means the session lost its login.

func TestNotLoggedInLogsInAgain(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.Logins, "the first login")

	f.logoutAll()
	_, err := s.Call(context.Background(), "echo", 2)
	var ce *CommandError
	if !errors.Is(err, ErrUnavailable) || errors.As(err, &ce) {
		t.Fatalf("err = %v, want ErrUnavailable and no CommandError", err)
	}
	if how := waitFor(t, f.Logins, "the new login"); how != "loginByToken" {
		t.Fatal(how)
	}
	if got, err := s.Call(context.Background(), "echo", 3); err != nil || string(got) != "3" {
		t.Fatalf("after re-login: %s, %v", got, err)
	}
	if f.Connections() != 2 {
		t.Fatalf("connections = %d, want 2", f.Connections())
	}
}

func TestNotLoggedInCallIdempotentRetries(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	f.logoutAll()
	got, err := s.CallIdempotent(context.Background(), "echo", 2)
	if err != nil || string(got) != "2" {
		t.Fatalf("CallIdempotent = %s, %v", got, err)
	}
	if f.count("echo") != 3 {
		t.Fatalf("echo handled %d times, want 3 (one refused, one retried)", f.count("echo"))
	}
}

// Reconnection timing.

func TestStableConnectionReconnectsAtOnce(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	s.stableAfter = 0                                 // every connection counts as stable
	s.minBackoff, s.maxBackoff = time.Hour, time.Hour // any backoff would hang the test
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, f.Accepted(), "the first connection")

	f.set(func(f *fakeKuma) { f.holdReady = true })
	f.DisconnectAll()
	waitFor(t, f.Accepted(), "the immediate reconnection")

	// A call made while that attempt runs waits for it instead of failing.
	done := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), "echo", 2)
		done <- err
	}()
	f.release <- struct{}{}
	if err := waitFor(t, done, "the call"); err != nil {
		t.Fatalf("call during the reconnection: %v", err)
	}
}

func TestBackoffAfterFailedImmediateReconnect(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	s.stableAfter = 0
	s.minBackoff, s.maxBackoff = time.Hour, time.Hour
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeKuma) { f.rejectTokens, f.password = true, "changed" })
	f.DisconnectAll()
	waitFor(t, f.Rejected, "the token login")
	waitFor(t, f.Rejected, "the password login")

	for range 2 {
		if _, err := s.Call(context.Background(), "echo", 2); !errors.Is(err, ErrLoginRejected) {
			t.Fatalf("err = %v, want ErrLoginRejected", err)
		}
	}
	if f.Connections() != 2 || f.count("login") != 2 {
		t.Fatalf("connections = %d, login = %d: the second failure must back off", f.Connections(), f.count("login"))
	}
}

func TestUnstableConnectionBacksOff(t *testing.T) {
	checkLeaks(t)
	f := newFakeKuma(t, siotest.Options{})
	s := newSession(t, f, testTimeout)
	s.stableAfter = time.Hour
	s.minBackoff, s.maxBackoff = time.Hour, time.Hour
	if _, err := s.Call(context.Background(), "echo", 1); err != nil {
		t.Fatal(err)
	}
	f.DisconnectAll()
	for range 3 {
		if _, err := s.Call(context.Background(), "echo", 2); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	}
	if f.Connections() != 1 {
		t.Fatalf("connections = %d: an unstable connection must not reconnect at once", f.Connections())
	}
}

// Recorded acks from Uptime Kuma 2.5.11 (phase 0).

func TestCommandErrorOnRecordedAcks(t *testing.T) {
	type recording struct {
		Ack json.RawMessage `json:"ack"`
	}
	seen := map[string]*CommandError{}
	for _, name := range []string{"lifecycle", "bootstrap"} {
		b, err := os.ReadFile(filepath.Join("testdata", "2.5.11", name, "acks.json"))
		if err != nil {
			t.Fatal(err)
		}
		var recs map[string]recording
		if err := json.Unmarshal(b, &recs); err != nil {
			t.Fatal(err)
		}
		for event, rec := range recs {
			var want struct {
				OK      *bool  `json:"ok"`
				Msg     string `json:"msg"`
				MsgI18n bool   `json:"msgi18n"`
			}
			_ = json.Unmarshal(rec.Ack, &want) // not every ack is an object
			err := commandError(event, rec.Ack)
			var ce *CommandError
			if want.OK != nil && !*want.OK {
				if !errors.As(err, &ce) || ce.Msg != want.Msg || ce.I18n != want.MsgI18n {
					t.Errorf("%s/%s %s: err = %#v", name, event, rec.Ack, err)
				}
				seen[event] = ce
			} else if err != nil {
				t.Errorf("%s/%s %.80s: unexpected error %v", name, event, rec.Ack, err)
			}
		}
	}
	for event, want := range map[string]CommandError{
		"loginByToken.invalid":  {Event: "loginByToken.invalid", Msg: "authInvalidToken", I18n: true},
		"getMonitor.missing":    {Event: "getMonitor.missing", Msg: "Cannot read properties of null (reading 'id')"},
		"getStatusPage.missing": {Event: "getStatusPage.missing", Msg: "No slug?"},
	} {
		if got := seen[event]; got == nil || *got != want {
			t.Errorf("%s: got %#v, want %#v", event, got, want)
		}
	}
}
