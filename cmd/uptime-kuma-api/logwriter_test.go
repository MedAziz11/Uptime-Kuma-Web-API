package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowWriter records what it is given, slowly.
type slowWriter struct {
	mu    sync.Mutex
	b     bytes.Buffer
	delay time.Duration
	gate  chan struct{} // if not nil, each write waits for it
}

func (s *slowWriter) Write(p []byte) (int, error) {
	if s.gate != nil {
		<-s.gate
	}
	time.Sleep(s.delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *slowWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestLogWriterKeepsOrderAndFlushesOnClose(t *testing.T) {
	out := &slowWriter{delay: time.Millisecond}
	w := newLogWriter(out)
	var want strings.Builder
	for i := range 500 {
		line := fmt.Sprintf("line %d\n", i)
		want.WriteString(line)
		if n, err := w.Write([]byte(line)); n != len(line) || err != nil {
			t.Fatal(n, err)
		}
	}
	w.Close()
	if out.String() != want.String() {
		t.Fatal("lines lost or reordered")
	}
	// After Close, lines are written directly.
	w.Write([]byte("late\n"))
	if !strings.HasSuffix(out.String(), "late\n") {
		t.Fatal("line after Close lost")
	}
}

// When the output falls behind by maxBuffered bytes, Write waits.
func TestLogWriterBackPressure(t *testing.T) {
	out := &slowWriter{gate: make(chan struct{})}
	w := newLogWriter(out)
	big := bytes.Repeat([]byte("x"), maxBuffered)
	w.Write([]byte("first\n")) // taken by the writer goroutine, which blocks
	time.Sleep(10 * time.Millisecond)
	w.Write(big) // fills the buffer
	blocked := make(chan struct{})
	go func() {
		w.Write([]byte("waits\n"))
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("Write did not wait for a full buffer to drain")
	case <-time.After(50 * time.Millisecond):
	}
	close(out.gate)
	<-blocked
	w.Close()
	if s := out.String(); !strings.HasPrefix(s, "first\n") || !strings.HasSuffix(s, "waits\n") || len(s) != len(big)+12 {
		t.Fatalf("%d bytes", len(s))
	}
}
