package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
)

// TestRealKuma runs against a real Uptime Kuma 2.x when KUMA_URL is set
// (e.g. http://localhost:3001). KUMA_USERNAME and KUMA_PASSWORD default to
// admin / Admin-Pass-12345. A fresh instance is set up with them first.
func TestRealKuma(t *testing.T) {
	base := os.Getenv("KUMA_URL")
	if base == "" {
		t.Skip("KUMA_URL is not set")
	}
	user, pass := os.Getenv("KUMA_USERNAME"), os.Getenv("KUMA_PASSWORD")
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "Admin-Pass-12345"
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Set up a fresh instance (no login needed for needSetup / setup).
	c, err := sio.Dial(ctx, base, sio.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.Call(ctx, "needSetup")
	if err != nil {
		t.Fatal(err)
	}
	if string(ack[0]) == "true" {
		ack, err := c.Call(ctx, "setup", user, pass)
		if err != nil {
			t.Fatal(err)
		}
		if err := commandError("setup", ack[0]); err != nil {
			t.Fatal(err)
		}
	}
	_ = c.Close()

	s, err := New(Config{URL: base, Username: user, Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	monitorList := make(chan struct{}, 1)
	s.Handle("monitorList", func(args []json.RawMessage) {
		select {
		case monitorList <- struct{}{}:
		default:
		}
	})

	res, err := s.Call(ctx, "getSettings")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(res, &settings); err != nil || !settings.OK || len(settings.Data) == 0 {
		t.Fatalf("getSettings = %s, %v", res, err)
	}
	waitFor(t, monitorList, "the monitorList push after login")

	// An unknown monitor is answered with {ok: false, msg}.
	_, err = s.Call(ctx, "getMonitor", 999999)
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("getMonitor(999999): err = %v, want a *CommandError", err)
	}
	t.Logf("getMonitor(999999): %v", err)
}
