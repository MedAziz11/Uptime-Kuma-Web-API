package kuma

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Refreshes are coalesced: callers that arrive while one runs share the
// next one, which starts after their write and therefore covers it.
func TestCoalescedRefreshCoversALaterWrite(t *testing.T) {
	checkLeaks(t)
	c, k, logs := newClient(t, testTimeout)
	ctx := context.Background()
	id := addMonitor(t, c, "a")
	inFirst, release := make(chan struct{}), make(chan struct{})
	// The first refresh reads the list, then a write happens in Uptime
	// Kuma (without a push) before that list is pushed: the list is stale.
	k.OnceBeforeMonitorList(func() {
		k.NoPush(true)
		k.UpdateMonitor(int(id), "name", "b")
		k.NoPush(false)
		close(inFirst)
		<-release
	})
	first := make(chan struct{})
	go func() {
		defer close(first)
		c.refreshMonitors(ctx)
	}()
	<-inFirst
	// Callers whose write came after the first refresh read the list.
	const n = 5
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { c.refreshMonitors(ctx) })
	}
	waitRefreshWaiters(t, c)
	close(release)
	<-first
	wg.Wait()
	m, _ := objectOf(c.State(), Monitors, id)
	if field[string](t, m, "name") != "b" {
		t.Fatalf("a refresh that started before the write was taken as covering it: %s", m)
	}
	if got := k.Calls("getMonitorList"); got != 2 {
		t.Fatalf("getMonitorList called %d times for %d refreshes, want 2", got, n+1)
	}
	if strings.Contains(logs.String(), "kept changing") {
		t.Fatalf("logs: %s", logs.String())
	}

	// A waiter whose context ends returns at once; the refresh goes on.
	inSecond, release2 := make(chan struct{}), make(chan struct{})
	k.OnceBeforeMonitorList(func() { close(inSecond); <-release2 })
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.refreshMonitors(ctx)
	}()
	<-inSecond
	canceled, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	c.refreshMonitors(canceled)
	close(release2)
	<-done
}

// waitRefreshWaiters waits until a refresh runs and a later one is due.
func waitRefreshWaiters(t *testing.T, c *Client) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.refreshMu.Lock()
		running := c.refreshRunning
		c.refreshMu.Unlock()
		if running {
			// The waiters have no state of their own to observe; give them
			// time to reach the wait.
			time.Sleep(200 * time.Millisecond)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no refresh running")
		}
		time.Sleep(time.Millisecond)
	}
}
