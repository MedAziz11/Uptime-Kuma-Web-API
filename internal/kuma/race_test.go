package kuma

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// Uptime Kuma emits monitor lists built before a change committed after the
// push of that change (see onMonitorList). These tests replay those orders
// with the fake.

// flush waits until every push the fake sent so far was applied: Kuma's
// answer to a later call comes after them on the same socket.
func flush(t *testing.T, c *Client) {
	t.Helper()
	if _, err := c.Tags(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func addMonitor(t *testing.T, c *Client, name string) int64 {
	t.Helper()
	m, err := NewMonitor(map[string]any{"type": "group", "name": name})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := c.AddMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return int64(field[float64](t, ack, "monitorID"))
}

// A deleted monitor does not come back with a stale list, a stale update or
// late statistics.
func TestDeletedMonitorStaysDeleted(t *testing.T) {
	checkLeaks(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	id := addMonitor(t, c, "doomed")
	stale := k.MonitorList()
	key := strconv.FormatInt(id, 10)
	if _, err := c.DeleteMonitor(ctx, id); err != nil {
		t.Fatal(err)
	}
	k.Push("monitorList", stale)
	k.Push("updateMonitorIntoList", map[string]any{key: stale[key]})
	k.Push("heartbeatList", key, []any{map[string]any{"monitorID": id}}, true)
	k.Push("heartbeat", map[string]any{"monitorID": id})
	k.Push("avgPing", id, 3)
	k.Push("uptime", id, 24, 1)
	k.Push("certInfo", id, `{"valid":true}`)
	flush(t, c)
	st := c.State()
	if _, ok := objectOf(st, Monitors, id); ok {
		t.Fatal("the deleted monitor came back")
	}
	if len(heartbeatsOf(st, id)) != 0 || len(avgPingOf(st)) != 0 || len(uptimeOf(st)) != 0 || len(certInfoOf(st)) != 0 {
		t.Fatalf("statistics of the deleted monitor: %v %v %v", avgPingOf(st), uptimeOf(st), certInfoOf(st))
	}
}

// A full list that misses a monitor created just before it is fetched again.
func TestStaleListIsVerified(t *testing.T) {
	checkLeaks(t)
	c, k, _ := newClient(t, testTimeout)
	c.verifyDelay = 5 * time.Millisecond
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	stale := k.MonitorList()
	id := addMonitor(t, c, "new")
	k.Push("monitorList", stale)
	flush(t, c)
	waitUntil(t, func() bool { _, ok := objectOf(c.State(), Monitors, id); return ok })
	waitUntil(t, func() bool { return k.Calls("getMonitorList") == 1 })
}

// Our own refresh is fetched again when an update came in between.
func TestRefreshRetriesAfterAnInterleavedUpdate(t *testing.T) {
	checkLeaks(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	id := addMonitor(t, c, "a")
	tag := field[map[string]any](t, mustFor(t)(c.AddTag(ctx, map[string]any{"name": "t", "color": "#fff"})), "tag")
	k.OnceBeforeMonitorList(func() { k.UpdateMonitor(int(id), "name", "b") })
	if _, err := c.DeleteTag(ctx, int64(tag["id"].(float64))); err != nil {
		t.Fatal(err)
	}
	m, _ := objectOf(c.State(), Monitors, id)
	if field[string](t, m, "name") != "b" {
		t.Fatalf("the stale list reverted the edit: %s", m)
	}
	if n := k.Calls("getMonitorList"); n != 2 {
		t.Fatalf("getMonitorList called %d times, want 2", n)
	}
}

// A list nobody changed anything around is not verified: two API instances
// on one Kuma user would otherwise fetch each other's lists forever.
func TestQuietListIsNotVerified(t *testing.T) {
	checkLeaks(t)
	c, k, _ := newClient(t, testTimeout)
	c.verifyDelay = time.Millisecond
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	k.Push("monitorList", k.MonitorList())
	flush(t, c)
	time.Sleep(50 * time.Millisecond)
	if n := k.Calls("getMonitorList"); n != 0 {
		t.Fatalf("getMonitorList called %d times", n)
	}
}

func TestSuspectListWindowAndTombstoneBound(t *testing.T) {
	st := NewState()
	h := handlers(t, st)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return clock }
	suspect := 0
	st.onRacedList = func() { suspect++ }
	list := []json.RawMessage{json.RawMessage(`{}`)}

	h["monitorList"](list) // no event yet
	h["updateMonitorIntoList"]([]json.RawMessage{json.RawMessage(`{"1":{"id":1}}`)})
	clock = clock.Add(raceWindow / 2)
	h["monitorList"](list)
	clock = clock.Add(raceWindow)
	h["monitorList"](list)
	if suspect != 1 {
		t.Fatalf("%d suspect lists, want 1", suspect)
	}

	for i := range maxTombstones {
		h["deleteMonitorFromList"]([]json.RawMessage{json.RawMessage(strconv.Itoa(1000 + i))})
	}
	clock = clock.Add(tombstoneTTL + time.Second)
	h["deleteMonitorFromList"]([]json.RawMessage{json.RawMessage("5")})
	h["deleteMonitorFromList"]([]json.RawMessage{json.RawMessage("6")})
	if len(st.deleted) != 2 {
		t.Fatalf("%d tombstones kept, want 2", len(st.deleted))
	}
	st.Reset()
	if len(st.deleted) != 0 {
		t.Fatal("tombstones kept after Reset")
	}
}

// Reads report whether State holds a complete login state.
func TestSnapshotsReportSynced(t *testing.T) {
	checkLeaks(t)
	c, _, _ := newClient(t, testTimeout)
	st := c.State()
	if _, snap := st.Objects(Monitors); snap.Synced {
		t.Fatal("synced before the login")
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, snap := st.Objects(Monitors); !snap.Synced {
		t.Fatal("not synced after the login")
	}
	st.Reset()
	for name, snap := range map[string]Snapshot{
		"objects": func() Snapshot { _, s := st.Objects(Monitors); return s }(),
		"object":  func() Snapshot { _, _, s := st.Object(Monitors, 1); return s }(),
		"info":    func() Snapshot { _, s := st.Info(); return s }(),
		"beats":   func() Snapshot { _, s := st.Heartbeats(1); return s }(),
		"ping":    func() Snapshot { _, s := st.AvgPing(); return s }(),
		"uptime":  func() Snapshot { _, s := st.Uptime(); return s }(),
		"cert":    func() Snapshot { _, s := st.CertInfo(); return s }(),
	} {
		if snap.Synced {
			t.Errorf("%s: synced after Reset", name)
		}
	}
}
