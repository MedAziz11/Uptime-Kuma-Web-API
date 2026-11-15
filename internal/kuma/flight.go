package kuma

import (
	"context"
	"encoding/json"
	"sync"
)

// flightGroup collapses identical concurrent reads into one call to Uptime
// Kuma (architecture §5): a read joins a call in flight for the same key
// instead of sending its own.
//
// Read-your-writes: a read may only join a call that was sent after every
// write that had returned when the read started, or it could miss one of
// them. writes counts the writes that returned (Session.Call); a call
// records the count when it starts and a read joins it only if the count is
// still the same.
//
// Contexts: the call runs on the first caller's goroutine with a context
// that only ends when every caller waiting for it has gone, so a caller
// whose context ends does not cancel the others.
type flightGroup struct {
	mu      sync.Mutex
	flights map[string]*flight
}

type flight struct {
	key     string
	writes  uint64
	done    chan struct{} // closed when res and err are set
	res     json.RawMessage
	err     error
	waiters int // callers still interested; guarded by flightGroup.mu
	cancel  context.CancelFunc
}

// do returns fn's result for key, calling fn unless a call for key that
// started with the same writes count is in flight. The result is shared:
// callers must not modify it.
func (g *flightGroup) do(ctx context.Context, key string, writes uint64, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	g.mu.Lock()
	if g.flights == nil {
		g.flights = map[string]*flight{}
	}
	if f, ok := g.flights[key]; ok && f.writes == writes {
		f.waiters++
		g.mu.Unlock()
		select {
		case <-f.done:
			return f.res, f.err
		case <-ctx.Done():
			g.leave(f)
			return nil, ctx.Err()
		}
	}
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f := &flight{key: key, writes: writes, done: make(chan struct{}), waiters: 1, cancel: cancel}
	g.flights[key] = f // replaces a call that started before a write
	g.mu.Unlock()

	stop := context.AfterFunc(ctx, func() { g.leave(f) })
	f.res, f.err = fn(fctx)
	stop()
	g.mu.Lock()
	if g.flights[key] == f {
		delete(g.flights, key)
	}
	g.mu.Unlock()
	cancel()
	close(f.done)
	return f.res, f.err
}

// leave records that a caller of f stopped waiting; the last one cancels
// the call, and no new caller may join it.
func (g *flightGroup) leave(f *flight) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f.waiters--
	if f.waiters > 0 {
		return
	}
	if g.flights[f.key] == f {
		delete(g.flights, f.key)
	}
	f.cancel()
}
