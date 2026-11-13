package sio_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
	"github.com/coder/websocket"
)

const openFrame = `0{"sid":"s","upgrades":[],"pingInterval":25000,"pingTimeout":20000,"maxPayload":1000000}`

// scriptedServer writes before, reads the client's namespace CONNECT, writes
// after, then reads until the client goes away. A frame starting with "\x00"
// is sent as a binary frame. Every frame the client sends after its CONNECT
// is delivered on the returned channel.
func scriptedServer(t *testing.T, before, after []string) (string, <-chan string) {
	t.Helper()
	got := make(chan string, 16)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx := r.Context()
		send := func(frames []string) bool {
			for _, f := range frames {
				typ, b := websocket.MessageText, []byte(f)
				if len(b) > 0 && b[0] == 0 {
					typ, b = websocket.MessageBinary, b[1:]
				}
				if ws.Write(ctx, typ, b) != nil {
					return false
				}
			}
			return true
		}
		if !send(before) {
			return
		}
		if _, msg, err := ws.Read(ctx); err != nil || string(msg) != "40" {
			return
		}
		if !send(after) {
			return
		}
		for {
			_, msg, err := ws.Read(ctx)
			if err != nil {
				return
			}
			select {
			case got <- string(msg):
			default:
			}
		}
	}))
	t.Cleanup(hs.Close)
	return hs.URL, got
}

func TestHandshake(t *testing.T) {
	cases := []struct {
		name          string
		before, after []string
		ok            bool
		check         func(t *testing.T, err error)
	}{
		{name: "bad open JSON", before: []string{"0{bad"}},
		{name: "open without ping settings", before: []string{`0{"sid":"s"}`}},
		{name: "not an open packet", before: []string{`40{"sid":"x"}`}},
		{name: "binary open packet", before: []string{"\x00\x01"}},
		{name: "engine close", before: []string{openFrame}, after: []string{"1"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, sio.ErrServerDisconnect) {
					t.Fatalf("err = %v", err)
				}
			}},
		{name: "namespace disconnect", before: []string{openFrame}, after: []string{"41"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, sio.ErrServerDisconnect) {
					t.Fatalf("err = %v", err)
				}
			}},
		{name: "malformed answer", before: []string{openFrame}, after: []string{"4x"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, sio.ErrMalformed) {
					t.Fatalf("err = %v", err)
				}
			}},
		{name: "connect error as a string", before: []string{openFrame}, after: []string{`44"nope"`},
			check: func(t *testing.T, err error) {
				var ce *sio.ConnectError
				if !errors.As(err, &ce) || ce.Message != "nope" || string(ce.Data) != `"nope"` {
					t.Fatalf("err = %v", err)
				}
			}},
		{name: "noise before the answer", ok: true, before: []string{openFrame},
			after: []string{"2", "6", "3", "\x00\x01", `40/admin,{"sid":"a"}`, `42["early"]`, `40{"sid":"x"}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkLeaks(t)
			url, got := scriptedServer(t, tc.before, tc.after)
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			c, err := sio.Dial(ctx, url, sio.Options{})
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if f := waitFor(t, got, "the pong"); f != "3" {
					t.Fatalf("first frame after CONNECT = %q, want the pong", f)
				}
				return
			}
			if err == nil {
				_ = c.Close()
				t.Fatal("Dial succeeded")
			}
			if tc.check != nil {
				tc.check(t, err)
			}
		})
	}
}
