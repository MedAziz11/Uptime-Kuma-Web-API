package main

import (
	"io"
	"sync"
)

// logWriter takes log lines off the request path: Write appends to a buffer
// and one goroutine writes the buffer to out. Writing each access log line
// to standard error took about 0.5 ms per request in a container (the
// write blocks until the container runtime reads the pipe), during which
// slog's handler holds its lock. Lines keep their order; a line is written
// as soon as the goroutine is free, so nothing waits for a timer. When out
// falls behind by maxBuffered bytes, Write waits (back pressure) rather
// than dropping lines or growing without bound. Close writes what is left.
type logWriter struct {
	out io.Writer

	mu       sync.Mutex
	cond     *sync.Cond // signals: buffer not empty, or drained, or closed
	buf      []byte
	closed   bool // Close was called: run drains and stops
	finished bool // run stopped: Write writes directly
	done     chan struct{}
}

const maxBuffered = 1 << 20

func newLogWriter(out io.Writer) *logWriter {
	w := &logWriter{out: out, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	go w.run()
	return w
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for len(w.buf) >= maxBuffered && !w.finished {
		w.cond.Wait()
	}
	if w.finished {
		return w.out.Write(p) // after Close: directly, in order
	}
	w.buf = append(w.buf, p...)
	w.cond.Broadcast()
	return len(p), nil
}

func (w *logWriter) run() {
	defer close(w.done)
	var spare []byte
	w.mu.Lock()
	for {
		for len(w.buf) == 0 && !w.closed {
			w.cond.Wait()
		}
		if len(w.buf) == 0 { // closed and drained
			w.finished = true
			w.cond.Broadcast()
			w.mu.Unlock()
			return
		}
		batch := w.buf
		w.buf = spare[:0]
		w.mu.Unlock()
		_, _ = w.out.Write(batch)
		w.mu.Lock()
		spare = batch
		if cap(spare) > maxBuffered { // after a burst: release the memory
			spare = nil
		}
		w.cond.Broadcast()
	}
}

// Close writes the lines still buffered and stops the goroutine.
func (w *logWriter) Close() error {
	w.mu.Lock()
	w.closed = true
	w.cond.Broadcast()
	w.mu.Unlock()
	<-w.done
	return nil
}
