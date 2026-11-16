package sio_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio/siotest"
)

// testTimeout bounds every wait in these tests; it is never reached when
// the code is correct.
const testTimeout = 5 * time.Second

// checkLeaks fails the test if a goroutine of the client or of the test
// server is still running once every other cleanup has run. Call it first.
func checkLeaks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		// A goroutine that a Close waited for may still be returning (its
		// deferred wg.Done has run): give it a moment to end.
		deadline := time.Now().Add(2 * time.Second)
		for {
			buf := make([]byte, 1<<20)
			stacks := string(buf[:runtime.Stack(buf, true)])
			leaked := ""
			for _, fn := range []string{"sio.(*Conn).readLoop", "sio.(*Conn).writeLoop", "siotest.(*Socket).pingLoop", "siotest.(*Server).serve"} {
				if strings.Contains(stacks, fn) {
					leaked = fn
				}
			}
			if leaked == "" {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("goroutine leak: %s is still running\n%s", leaked, stacks)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
}

func newServer(t *testing.T, opts siotest.Options) *siotest.Server {
	t.Helper()
	srv := siotest.NewServer(opts)
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *siotest.Server, opts sio.Options) *sio.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	c, err := sio.Dial(ctx, srv.URL, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func echo(s *siotest.Socket, ev siotest.Event) {
	if !ev.HasID {
		return
	}
	args := make([]any, len(ev.Args))
	for i, a := range ev.Args {
		args[i] = a
	}
	_ = s.Ack(ev.ID, args...)
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func call(t *testing.T, c *sio.Conn, event string, args ...any) ([]json.RawMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return c.Call(ctx, event, args...)
}

func TestCallEcho(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{OnEvent: echo})
	c := dial(t, srv, sio.Options{})

	got, err := call(t, c, "echo", map[string]any{"a": "b"}, json.RawMessage(`[1, 2]`), "s", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"a":"b"}`, `[1,2]`, `"s"`, `null`}
	if len(got) != len(want) {
		t.Fatalf("ack = %q, want %q", got, want)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("ack[%d] = %s, want %s", i, got[i], want[i])
		}
	}

	got, err = call(t, c, "echo")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty ack = %q, %v", got, err)
	}
}

func TestConcurrentCallsOutOfOrderAcks(t *testing.T) {
	checkLeaks(t)
	const n = 100
	var mu sync.Mutex
	var batch []siotest.Event
	srv := newServer(t, siotest.Options{OnEvent: func(s *siotest.Socket, ev siotest.Event) {
		mu.Lock()
		batch = append(batch, ev)
		ready := len(batch) == n
		mu.Unlock()
		if ready { // answer every call, newest first
			for i := n - 1; i >= 0; i-- {
				_ = s.Ack(batch[i].ID, batch[i].Args[0])
			}
		}
	}})
	c := dial(t, srv, sio.Options{})

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			got, err := call(t, c, "echo", i)
			if err == nil && (len(got) != 1 || string(got[0]) != fmt.Sprint(i)) {
				err = fmt.Errorf("call %d got %q", i, got)
			}
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestTimeoutThenLateAck(t *testing.T) {
	checkLeaks(t)
	received := make(chan siotest.Event, 1)
	var sock *siotest.Socket
	srv := newServer(t, siotest.Options{OnEvent: func(s *siotest.Socket, ev siotest.Event) {
		if ev.Name == "slow" {
			sock = s
			received <- ev
			return
		}
		echo(s, ev)
	}})
	c := dial(t, srv, sio.Options{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, "slow")
		done <- err
	}()
	ev := waitFor(t, received, "the slow event")
	cancel()
	if err := waitFor(t, done, "the call to give up"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// The late ack must be dropped and the next call answered with its own.
	if err := sock.Ack(ev.ID, "late"); err != nil {
		t.Fatal(err)
	}
	got, err := call(t, c, "echo", "fresh")
	if err != nil || len(got) != 1 || string(got[0]) != `"fresh"` {
		t.Fatalf("after late ack: %q, %v", got, err)
	}
}

func TestDeadlineExceeded(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{})
	c := dial(t, srv, sio.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, "never"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestDisconnectMidCall(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{OnEvent: func(s *siotest.Socket, ev siotest.Event) { s.Kill() }})
	c := dial(t, srv, sio.Options{})
	_, err := call(t, c, "boom")
	if !errors.Is(err, sio.ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
	waitFor(t, c.Done(), "Done")
	if c.Err() == nil {
		t.Fatal("Err() = nil after disconnect")
	}
	if _, err := call(t, c, "again"); !errors.Is(err, sio.ErrClosed) {
		t.Fatalf("call on closed conn: %v", err)
	}
}

func TestServerDisconnects(t *testing.T) {
	for name, frame := range map[string]string{"namespace": "41", "engine": "1"} {
		t.Run(name, func(t *testing.T) {
			checkLeaks(t)
			srv := newServer(t, siotest.Options{OnConnect: func(s *siotest.Socket) { _ = s.WriteRaw(frame) }})
			c := dial(t, srv, sio.Options{})
			waitFor(t, c.Done(), "Done")
			if !errors.Is(c.Err(), sio.ErrServerDisconnect) {
				t.Fatalf("Err() = %v, want ErrServerDisconnect", c.Err())
			}
		})
	}
}

func TestMalformedPacketClosesConnection(t *testing.T) {
	for name, frame := range map[string]string{
		"bad socket.io packet": `42{"not":"an array"}`,
		"bad engine.io type":   "9",
		"connect error":        `44{"message":"kicked"}`,
	} {
		t.Run(name, func(t *testing.T) {
			checkLeaks(t)
			srv := newServer(t, siotest.Options{OnConnect: func(s *siotest.Socket) { _ = s.WriteRaw(frame) }})
			c := dial(t, srv, sio.Options{})
			waitFor(t, c.Done(), "Done")
			var ce *sio.ConnectError
			if !errors.Is(c.Err(), sio.ErrMalformed) && !errors.As(c.Err(), &ce) {
				t.Fatalf("Err() = %v", c.Err())
			}
		})
	}
}

func TestPushedEvents(t *testing.T) {
	checkLeaks(t)
	type ev struct {
		name string
		args []string
	}
	events := make(chan ev, 8)
	srv := newServer(t, siotest.Options{
		OnConnect: func(s *siotest.Socket) {
			_ = s.Emit("loginRequired")
			_ = s.Emit("uptime", 1, 24, 0.9987)
			// Binary event with its attachment: dropped as a whole.
			_ = s.WriteRaw(`451-["bin",{"_placeholder":true,"num":0}]`)
			_ = s.WriteBinary([]byte{1, 2, 3})
			// Other namespaces are ignored.
			_ = s.WriteRaw(`42/admin,["other"]`)
			_ = s.WriteRaw("6")
			_ = s.Emit("heartbeat", map[string]any{"monitorID": 3, "status": 1})
		},
	})
	dial(t, srv, sio.Options{OnEvent: func(name string, args []json.RawMessage) {
		e := ev{name: name}
		for _, a := range args {
			e.args = append(e.args, string(a)) // copy: args are only valid during the call
		}
		events <- e
	}})

	want := []ev{
		{"loginRequired", nil},
		{"uptime", []string{"1", "24", "0.9987"}},
		{"heartbeat", []string{`{"monitorID":3,"status":1}`}},
	}
	for _, w := range want {
		got := waitFor(t, events, w.name)
		if fmt.Sprint(got) != fmt.Sprint(w) {
			t.Fatalf("event = %v, want %v", got, w)
		}
	}
}

func TestBinaryAckFailsTheCallOnly(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{OnEvent: func(s *siotest.Socket, ev siotest.Event) {
		if ev.Name == "bin" {
			_ = s.WriteRaw(fmt.Sprintf(`461-%d[{"_placeholder":true,"num":0}]`, ev.ID))
			_ = s.WriteBinary([]byte{1})
			return
		}
		echo(s, ev)
	}})
	c := dial(t, srv, sio.Options{})
	if _, err := call(t, c, "bin"); !errors.Is(err, sio.ErrBinaryUnsupported) {
		t.Fatalf("err = %v, want ErrBinaryUnsupported", err)
	}
	if got, err := call(t, c, "echo", 1); err != nil || string(got[0]) != "1" {
		t.Fatalf("after binary ack: %q, %v", got, err)
	}
}

func TestPayloadTooLarge(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{MaxPayload: 64, OnEvent: echo})
	c := dial(t, srv, sio.Options{})
	if _, err := call(t, c, "big", strings.Repeat("x", 64)); !errors.Is(err, sio.ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
	if _, err := call(t, c, "small"); err != nil {
		t.Fatalf("after rejected payload: %v", err)
	}
}

func TestEncodeErrorDoesNotBreakConnection(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{OnEvent: echo})
	c := dial(t, srv, sio.Options{})
	if _, err := call(t, c, "x", make(chan int)); err == nil {
		t.Fatal("unsupported argument accepted")
	}
	if _, err := call(t, c, "y"); err != nil {
		t.Fatal(err)
	}
}

func TestPingsAreAnswered(t *testing.T) {
	checkLeaks(t)
	pongs := make(chan struct{}, 16)
	srv := newServer(t, siotest.Options{
		PingInterval: 5 * time.Millisecond,
		PingTimeout:  testTimeout, // the server closes the socket if a pong is missing
		OnPong: func(*siotest.Socket) {
			select {
			case pongs <- struct{}{}:
			default:
			}
		},
	})
	c := dial(t, srv, sio.Options{})
	for range 3 {
		waitFor(t, pongs, "a pong")
	}
	select {
	case <-c.Done():
		t.Fatalf("connection closed: %v", c.Err())
	default:
	}
}

func TestPingTimeout(t *testing.T) {
	checkLeaks(t)
	srv := newServer(t, siotest.Options{NoPing: true, PingInterval: 10 * time.Millisecond, PingTimeout: 10 * time.Millisecond})
	c := dial(t, srv, sio.Options{})

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := c.Call(context.Background(), "unanswered")
		done <- err
	}()
	<-started
	waitFor(t, c.Done(), "the ping timeout")
	if !errors.Is(c.Err(), sio.ErrPingTimeout) {
		t.Fatalf("Err() = %v, want ErrPingTimeout", c.Err())
	}
	if err := waitFor(t, done, "the pending call"); !errors.Is(err, sio.ErrClosed) || !errors.Is(err, sio.ErrPingTimeout) {
		t.Fatalf("pending call err = %v", err)
	}
}

func TestCloseWithCallsInFlight(t *testing.T) {
	checkLeaks(t)
	const n = 10
	received := make(chan struct{}, n)
	srv := newServer(t, siotest.Options{OnEvent: func(*siotest.Socket, siotest.Event) { received <- struct{}{} }})
	c := dial(t, srv, sio.Options{})
	sock := waitFor(t, srv.Accepted(), "the server socket")

	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := c.Call(context.Background(), "pending")
			errs <- err
		}()
	}
	for range n {
		waitFor(t, received, "the calls to reach the server")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for range n {
		if err := waitFor(t, errs, "a call to fail"); !errors.Is(err, sio.ErrClosed) {
			t.Fatalf("err = %v, want ErrClosed", err)
		}
	}
	if !errors.Is(c.Err(), sio.ErrClosed) {
		t.Fatalf("Err() = %v", c.Err())
	}
	waitFor(t, sock.Closed(), "the server to see the close")
	if err := c.Close(); err != nil {
		t.Fatal("second Close:", err)
	}
}

func TestDialErrors(t *testing.T) {
	checkLeaks(t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	for _, u := range []string{"ftp://x", "http://", "://", "http://%zz"} {
		if _, err := sio.Dial(ctx, u, sio.Options{}); err == nil {
			t.Errorf("Dial(%q): want error", u)
		}
	}

	srv := newServer(t, siotest.Options{ConnectError: "Not authorized"})
	_, err := sio.Dial(ctx, srv.URL, sio.Options{})
	var ce *sio.ConnectError
	if !errors.As(err, &ce) || ce.Message != "Not authorized" || ce.Error() == "" {
		t.Fatalf("err = %v, want ConnectError", err)
	}

	ok := newServer(t, siotest.Options{})
	if _, err := sio.Dial(ctx, ok.URL, sio.Options{Path: "/other/"}); err == nil {
		t.Fatal("wrong path: want error")
	}
	// The URL path is kept as a prefix; a trailing slash does not matter.
	c, err := sio.Dial(ctx, ok.URL+"/", sio.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	ws := strings.Replace(ok.URL, "http://", "ws://", 1)
	c, err = sio.Dial(ctx, ws, sio.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := sio.Dial(canceled, ok.URL, sio.Options{}); err == nil {
		t.Fatal("canceled context: want error")
	}
}

// BenchmarkCall measures a Call round trip against the in-process server.
func BenchmarkCall(b *testing.B) {
	srv := siotest.NewServer(siotest.Options{OnEvent: echo})
	b.Cleanup(srv.Close)
	c, err := sio.Dial(context.Background(), srv.URL, sio.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Call(ctx, "echo", 1); err != nil {
			b.Fatal(err)
		}
	}
}
