package kuma

import (
	"context"
	"testing"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio/siotest"
)

// BenchmarkSessionCall measures a full Call round trip (encode, write, fake
// server, ack, decode) on a warm session.
func BenchmarkSessionCall(b *testing.B) {
	f := newBenchKuma(b)
	s := newBenchSession(b, f)
	arg := map[string]any{"id": 1, "name": "monitor"}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Call(ctx, "echo", arg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSessionCallParallel measures concurrent calls on one session.
func BenchmarkSessionCallParallel(b *testing.B) {
	f := newBenchKuma(b)
	s := newBenchSession(b, f)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		arg := map[string]any{"id": 1, "name": "monitor"}
		for pb.Next() {
			if _, err := s.Call(ctx, "echo", arg); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func newBenchKuma(b *testing.B) *siotest.Server {
	srv := siotest.NewServer(siotest.Options{OnConnect: func(s *siotest.Socket) { _ = s.Emit("loginRequired") }, OnEvent: func(s *siotest.Socket, ev siotest.Event) {
		switch ev.Name {
		case "login":
			_ = s.Ack(ev.ID, map[string]any{"ok": true, "token": "jwt"})
		default:
			_ = s.Ack(ev.ID, ev.Args[0])
		}
	}})
	b.Cleanup(srv.Close)
	return srv
}

func newBenchSession(b *testing.B, srv *siotest.Server) *Session {
	s, err := New(Config{URL: srv.URL, Username: "u", Password: "p"})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if _, err := s.Call(context.Background(), "echo", 0); err != nil {
		b.Fatal(err)
	}
	return s
}
