package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type recordedPush struct {
	After string            `json:"after"`
	Event string            `json:"event"`
	Args  []json.RawMessage `json:"args"`
}

func loadPushes(t *testing.T, name string) []recordedPush {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "2.5.11", name, "pushed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p []recordedPush
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// handlers returns the handlers State registers, by event.
func handlers(t *testing.T, st *State) map[string]Handler {
	t.Helper()
	s, err := New(Config{URL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	st.Register(s)
	return s.handlers
}

func ids(objects []json.RawMessage) string {
	var out []string
	for _, o := range objects {
		id, _ := objectID(o)
		out = append(out, strconv.FormatInt(id, 10))
	}
	return strings.Join(out, ",")
}

// The recorded lifecycle (create and delete one of each resource) replayed
// through the handlers leaves the state Uptime Kuma had at the end.
func TestStateReplaysRecordedPushes(t *testing.T) {
	st := NewState()
	h := handlers(t, st)
	for _, p := range loadPushes(t, "lifecycle") {
		if f := h[p.Event]; f != nil {
			f(p.Args)
		}
	}
	for _, c := range []struct {
		list List
		want string
	}{
		{Monitors, "2,3"}, {Maintenances, ""}, {Notifications, "2"}, {Proxies, "2"},
		{DockerHosts, "2"}, {APIKeys, "1"}, {StatusPages, ""},
	} {
		if got := ids(st.Objects(c.list)); got != c.want {
			t.Errorf("%v: ids %q, want %q", c.list, got, c.want)
		}
	}
	info, ok := st.Info()
	var v struct{ Version string }
	if !ok || json.Unmarshal(info, &v) != nil || v.Version != "2.5.11" {
		t.Fatalf("info %s", info)
	}
	if ping := st.AvgPing(); len(ping) != 1 || string(ping[2]) != "null" {
		t.Fatalf("avgPing %v (monitor 1 was deleted)", ping)
	}
	up := st.Uptime()[2]
	if len(up) != 3 || string(up["24"]) != "0" || string(up["720"]) != "0" || string(up["1y"]) != "0" {
		t.Fatalf("uptime %v", up)
	}
	if beats := st.Heartbeats(2); len(beats) != 1 || !strings.Contains(string(beats[0]), `"Group empty"`) {
		t.Fatalf("heartbeats %s", beats)
	}
	if len(st.Heartbeats(1)) != 0 {
		t.Fatal("heartbeats of the deleted monitor kept")
	}
	m, ok := st.Object(Monitors, 3)
	if !ok || !strings.Contains(string(m), `"capture push"`) {
		t.Fatalf("monitor 3: %s", m)
	}
	if _, ok := st.Object(Monitors, 1); ok {
		t.Fatal("deleted monitor kept")
	}
}

func TestStateHandlers(t *testing.T) {
	raw := func(s ...string) []json.RawMessage {
		out := make([]json.RawMessage, len(s))
		for i, v := range s {
			out[i] = json.RawMessage(v)
		}
		return out
	}
	for _, c := range []struct {
		name   string
		events [][2]any // event, args
		check  func(t *testing.T, st *State)
	}{
		{"heartbeatList with a string id, then beats", [][2]any{
			{"heartbeatList", raw(`"7"`, `[{"monitorID":7,"n":1},{"monitorID":7,"n":2}]`, `true`)},
			{"heartbeat", raw(`{"monitorID":7,"n":3}`)},
			{"heartbeatList", raw(`7`, `[{"monitorID":7,"n":4}]`, `false`)},
		}, func(t *testing.T, st *State) {
			if b := st.Heartbeats(7); len(b) != 4 || !strings.Contains(string(b[3]), `"n":4`) {
				t.Fatalf("%s", b)
			}
		}},
		{"heartbeatList overwrites", [][2]any{
			{"heartbeat", raw(`{"monitorID":7,"n":1}`)},
			{"heartbeatList", raw(`7`, `[{"monitorID":7,"n":2}]`, `true`)},
		}, func(t *testing.T, st *State) {
			if b := st.Heartbeats(7); len(b) != 1 || !strings.Contains(string(b[0]), `"n":2`) {
				t.Fatalf("%s", b)
			}
		}},
		{"certInfo is a JSON document in a string", [][2]any{
			{"certInfo", raw(`5`, `"{\"valid\":true}"`)},
			{"certInfo", raw(`6`, `"not json"`)},
		}, func(t *testing.T, st *State) {
			ci := st.CertInfo()
			if len(ci) != 1 || string(ci[5]) != `{"valid":true}` {
				t.Fatalf("%v", ci)
			}
		}},
		{"info without a version is ignored", [][2]any{
			{"info", raw(`{"serverTimezone":"UTC"}`)},
		}, func(t *testing.T, st *State) {
			if _, ok := st.Info(); ok {
				t.Fatal("info set")
			}
		}},
		{"updateMonitorIntoList patches", [][2]any{
			{"monitorList", raw(`{"1":{"id":1,"name":"a"},"2":{"id":2,"name":"b"}}`)},
			{"updateMonitorIntoList", raw(`{"2":{"id":2,"name":"B"},"3":{"id":3,"name":"c"}}`)},
		}, func(t *testing.T, st *State) {
			if got := ids(st.Objects(Monitors)); got != "1,2,3" {
				t.Fatal(got)
			}
			if m, _ := st.Object(Monitors, 2); !strings.Contains(string(m), `"B"`) {
				t.Fatalf("%s", m)
			}
		}},
		{"malformed pushes are ignored", [][2]any{
			{"monitorList", raw(`{"1":{"id":1}}`)},
			{"monitorList", raw(`[1]`)},
			{"monitorList", raw()},
			{"notificationList", raw(`{}`)},
			{"notificationList", raw(`[{"id":"x"},{"name":"no id"}]`)},
			{"updateMonitorIntoList", raw(`3`)},
			{"deleteMonitorFromList", raw(`"x"`)},
			{"deleteMonitorFromList", raw()},
			{"heartbeatList", raw(`1`)},
			{"heartbeatList", raw(`"x"`, `[]`)},
			{"heartbeat", raw(`{"monitorID":"x"}`)},
			{"heartbeat", raw(`[]`)},
			{"avgPing", raw(`1`)},
			{"avgPing", raw(`{}`, `1`)},
			{"uptime", raw(`1`, `24`)},
			{"uptime", raw(`true`, `24`, `1`)},
			{"certInfo", raw(`1`)},
			{"info", raw()},
		}, func(t *testing.T, st *State) {
			if got := ids(st.Objects(Monitors)); got != "1" {
				t.Fatal(got)
			}
			if len(st.Objects(Notifications)) != 0 || len(st.AvgPing()) != 0 || len(st.Uptime()) != 0 {
				t.Fatal("malformed push stored")
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := NewState()
			h := handlers(t, st)
			for _, e := range c.events {
				h[e[0].(string)](e[1].([]json.RawMessage))
			}
			c.check(t, st)
		})
	}
}

func TestHeartbeatsAreBounded(t *testing.T) {
	st := NewState()
	h := handlers(t, st)
	for i := range 3 * MaxHeartbeats {
		h["heartbeat"]([]json.RawMessage{json.RawMessage(`{"monitorID":1,"n":` + strconv.Itoa(i) + `}`)})
	}
	b := st.Heartbeats(1)
	if len(b) != MaxHeartbeats || !strings.Contains(string(b[len(b)-1]), strconv.Itoa(3*MaxHeartbeats-1)) {
		t.Fatalf("%d beats, last %s", len(b), b[len(b)-1])
	}
	long := "[" + strings.Repeat(`{"monitorID":1},`, 2*MaxHeartbeats) + `{"monitorID":1}]`
	h["heartbeatList"]([]json.RawMessage{json.RawMessage("1"), json.RawMessage(long), json.RawMessage("true")})
	if len(st.Heartbeats(1)) != MaxHeartbeats {
		t.Fatalf("%d beats", len(st.Heartbeats(1)))
	}
}

func TestResetAndVersions(t *testing.T) {
	st := NewState()
	h := handlers(t, st)
	v := st.Version(Monitors)
	h["monitorList"]([]json.RawMessage{json.RawMessage(`{"1":{"id":1}}`)})
	h["avgPing"]([]json.RawMessage{json.RawMessage(`1`), json.RawMessage(`5`)})
	if st.Version(Monitors) != v+1 {
		t.Fatal("version did not grow")
	}
	st.Reset()
	if len(st.Objects(Monitors)) != 0 || len(st.AvgPing()) != 0 || st.Version(Monitors) != v+2 {
		t.Fatal("reset kept state")
	}
	if !st.PutObject(StatusPages, json.RawMessage(`{"id":4,"slug":"x"}`)) || st.PutObject(StatusPages, json.RawMessage(`{"slug":"y"}`)) {
		t.Fatal("PutObject")
	}
	if o, ok := st.Object(StatusPages, 4); !ok || string(o) != `{"id":4,"slug":"x"}` {
		t.Fatalf("%s", o)
	}
	st.DeleteObject(StatusPages, 4)
	if len(st.Objects(StatusPages)) != 0 {
		t.Fatal("DeleteObject")
	}
	if Monitors.String() != "monitors" || List(99).String() != "list(99)" {
		t.Fatal("String")
	}
}

func TestWaitVersion(t *testing.T) {
	st := NewState()
	v := st.Version(Proxies)
	done := make(chan error, 1)
	go func() { done <- st.WaitVersion(context.Background(), Proxies, v) }()
	st.PutObject(Monitors, json.RawMessage(`{"id":1}`)) // another list: keeps waiting
	select {
	case err := <-done:
		t.Fatalf("returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	st.PutObject(Proxies, json.RawMessage(`{"id":1}`))
	if err := waitFor(t, done, "WaitVersion"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := st.WaitVersion(ctx, Proxies, st.Version(Proxies)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
