package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingRead is a read that waits for release, counting its calls.
type blockingRead struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	ctxErr  chan error // the call's context error when it returns
}

func newBlockingRead() *blockingRead {
	return &blockingRead{started: make(chan struct{}, 10), release: make(chan struct{}), ctxErr: make(chan error, 10)}
}

func (b *blockingRead) fn(ctx context.Context) (json.RawMessage, error) {
	b.calls.Add(1)
	b.started <- struct{}{}
	select {
	case <-b.release:
		b.ctxErr <- ctx.Err()
		return json.RawMessage(`"result"`), nil
	case <-ctx.Done():
		b.ctxErr <- ctx.Err()
		return nil, ctx.Err()
	}
}

// waiters returns how many callers wait for the call of key.
func (g *flightGroup) waiters(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f := g.flights[key]; f != nil {
		return f.waiters
	}
	return 0
}

func flightWait(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFlightCollapsesIdenticalReads(t *testing.T) {
	var g flightGroup
	b := newBlockingRead()
	const n = 10
	results := make(chan string, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			res, err := g.do(context.Background(), "k", 0, b.fn)
			if err != nil {
				t.Error(err)
			}
			results <- string(res)
		})
	}
	<-b.started
	flightWait(t, func() bool { return g.waiters("k") == n })
	// Another key is another call.
	other := newBlockingRead()
	close(other.release)
	if res, err := g.do(context.Background(), "other", 0, other.fn); err != nil || string(res) != `"result"` {
		t.Fatal(res, err)
	}
	close(b.release)
	wg.Wait()
	close(results)
	for r := range results {
		if r != `"result"` {
			t.Fatal(r)
		}
	}
	if b.calls.Load() != 1 || other.calls.Load() != 1 {
		t.Fatalf("%d and %d calls", b.calls.Load(), other.calls.Load())
	}
	if g.waiters("k") != 0 || len(g.flights) != 0 {
		t.Fatal("flight kept")
	}
}

// A read never joins a call sent before a write that returned.
func TestFlightAfterAWriteIsANewCall(t *testing.T) {
	var g flightGroup
	b := newBlockingRead()
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.do(context.Background(), "k", 0, b.fn)
	}()
	<-b.started
	b2 := newBlockingRead()
	close(b2.release)
	if _, err := g.do(context.Background(), "k", 1, b2.fn); err != nil || b2.calls.Load() != 1 {
		t.Fatalf("read after a write: %v, %d calls", err, b2.calls.Load())
	}
	close(b.release)
	<-done
	if len(g.flights) != 0 {
		t.Fatal("flight kept")
	}
}

// The first caller leaving does not cancel the call the others wait for;
// the last one leaving cancels it.
func TestFlightCallerContexts(t *testing.T) {
	var g flightGroup
	b := newBlockingRead()
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := g.do(leaderCtx, "k", 0, b.fn)
		leaderDone <- err
	}()
	<-b.started
	joined := make(chan error, 1)
	go func() {
		res, err := g.do(context.Background(), "k", 0, b.fn)
		if err == nil && string(res) != `"result"` {
			err = errors.New("wrong result")
		}
		joined <- err
	}()
	flightWait(t, func() bool { return g.waiters("k") == 2 })
	cancelLeader()
	flightWait(t, func() bool { return g.waiters("k") == 1 })
	close(b.release)
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	if err := <-b.ctxErr; err != nil {
		t.Fatalf("the call was canceled: %v", err)
	}
	<-leaderDone

	// Everyone leaves: the call is canceled, and nobody joins it any more.
	b = newBlockingRead()
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	go g.do(ctx1, "k", 0, b.fn)
	<-b.started
	errc := make(chan error, 1)
	go func() {
		_, err := g.do(ctx2, "k", 0, b.fn)
		errc <- err
	}()
	flightWait(t, func() bool { return g.waiters("k") == 2 })
	cancel2()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter: %v", err)
	}
	cancel1()
	if err := <-b.ctxErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("the call was not canceled: %v", err)
	}
	flightWait(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.flights) == 0
	})
}

// Through the client: a read after a write sees it, concurrent reads give
// the same answer.
func TestClientReadsAreCollapsedSafely(t *testing.T) {
	c, _, _ := newClient(t, 5*time.Second)
	ctx := context.Background()
	if _, err := c.AddTag(ctx, map[string]any{"new": true, "name": "t1", "color": "#fff"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			ack, err := c.Tags(ctx)
			var got struct{ Tags []json.RawMessage }
			if err != nil || json.Unmarshal(ack, &got) != nil || len(got.Tags) != 1 {
				t.Errorf("%s %v", ack, err)
			}
		})
	}
	wg.Wait()
	if c.s.writes.Load() == 0 {
		t.Fatal("writes not counted")
	}
}
