package sio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Errors reported by a Conn. Errors returned by Dial and Call wrap them.
var (
	// ErrClosed reports that the connection is closed: by Close, by the
	// server, or after a transport or protocol error.
	ErrClosed = errors.New("sio: connection closed")
	// ErrPingTimeout reports that the server sent no ping within
	// pingInterval + pingTimeout.
	ErrPingTimeout = errors.New("sio: ping timeout")
	// ErrServerDisconnect reports that the server closed the namespace or
	// the Engine.IO session.
	ErrServerDisconnect = errors.New("sio: disconnected by the server")
	// ErrBinaryUnsupported reports a binary ack, which this client does not
	// reassemble (see the package documentation).
	ErrBinaryUnsupported = errors.New("sio: binary packets are not supported")
	// ErrPayloadTooLarge reports a call whose frame exceeds the maxPayload
	// announced by the server; it was not sent.
	ErrPayloadTooLarge = errors.New("sio: payload exceeds the server's maxPayload")
)

// ConnectError is the CONNECT_ERROR the server answered to the namespace
// CONNECT, e.g. when a middleware rejects the client.
type ConnectError struct {
	Message string
	// Data is the raw payload of the packet.
	Data json.RawMessage
}

func (e *ConnectError) Error() string { return "sio: connect error: " + e.Message }

// Options configure Dial. The zero value is usable.
type Options struct {
	// HTTPClient is used for the WebSocket handshake (TLS settings, proxy).
	// Default: http.DefaultClient.
	HTTPClient *http.Client
	// Header is sent with the WebSocket handshake.
	Header http.Header
	// Path is the Socket.IO endpoint, appended to the URL path.
	// Default: "/socket.io/".
	Path string
	// ReadLimit bounds the size of a received frame. Default: 32 MiB.
	ReadLimit int64
	// QueueSize is the capacity of the outgoing frame queue. Default: 64.
	QueueSize int
	// OnEvent receives the events pushed by the server, in order, on the
	// reader goroutine. args excludes the event name. name and args are only
	// valid during the call: copy what must outlive it. OnEvent must return
	// quickly and must not wait for a Call on the same Conn (the ack could
	// only be read once OnEvent has returned).
	OnEvent func(name string, args []json.RawMessage)
}

const (
	defaultReadLimit = 32 << 20
	defaultQueueSize = 64
	// A read buffer that grew beyond this size is not kept for the next frame.
	maxRetainedBuffer = 1 << 20
	// How long Close waits for the namespace DISCONNECT to be written.
	closeGrace = time.Second
)

var pongFrame = []byte{enginePong}

// Conn is a Socket.IO client connection to the default namespace, over one
// WebSocket. A reader goroutine and a writer goroutine own the socket; Call
// is safe for concurrent use.
type Conn struct {
	ws         *websocket.Conn
	onEvent    func(string, []json.RawMessage)
	maxPayload int
	pingWindow time.Duration
	pingTimer  *time.Timer

	out    chan *frame
	pong   chan struct{}
	stop   chan struct{} // closed to stop the writer
	done   chan struct{} // closed when the connection is unusable
	wg     sync.WaitGroup
	frames sync.Pool

	mu      sync.Mutex
	closed  bool
	err     error
	nextID  uint64
	pending map[uint64]chan ackResult

	closeOnce sync.Once
}

type ackResult struct {
	args []json.RawMessage
	err  error
}

// frame is an outgoing text frame. Frames are pooled per connection.
type frame struct {
	buf  bytes.Buffer
	enc  *json.Encoder
	sent chan struct{} // if set, closed once the frame is written
}

type handshake struct {
	SID          string `json:"sid"`
	PingInterval int64  `json:"pingInterval"`
	PingTimeout  int64  `json:"pingTimeout"`
	MaxPayload   int    `json:"maxPayload"`
}

// Dial opens a WebSocket to the Socket.IO server at rawURL (http, https, ws
// or wss; its path is kept as a prefix of Options.Path), performs the
// Engine.IO handshake and connects to the default namespace. ctx bounds the
// whole handshake. A CONNECT_ERROR answer is returned as a *ConnectError.
func Dial(ctx context.Context, rawURL string, opts Options) (*Conn, error) {
	u, err := endpoint(rawURL, opts.Path)
	if err != nil {
		return nil, err
	}
	readLimit := opts.ReadLimit
	if readLimit <= 0 {
		readLimit = defaultReadLimit
	}
	queue := opts.QueueSize
	if queue <= 0 {
		queue = defaultQueueSize
	}

	ws, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPClient: opts.HTTPClient,
		HTTPHeader: opts.Header,
	})
	if err != nil {
		return nil, fmt.Errorf("sio: dial: %w", err)
	}
	ws.SetReadLimit(readLimit)

	hs, err := clientHandshake(ctx, ws)
	if err != nil {
		ws.CloseNow()
		return nil, err
	}

	c := &Conn{
		ws:         ws,
		onEvent:    opts.OnEvent,
		maxPayload: hs.MaxPayload,
		pingWindow: time.Duration(hs.PingInterval+hs.PingTimeout) * time.Millisecond,
		out:        make(chan *frame, queue),
		pong:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		pending:    make(map[uint64]chan ackResult),
	}
	c.frames.New = newFrame
	c.pingTimer = time.AfterFunc(c.pingWindow, func() { c.fail(ErrPingTimeout) })
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
	return c, nil
}

func endpoint(rawURL, path string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("sio: bad URL: %w", err)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("sio: bad URL %q: scheme must be http, https, ws or wss", rawURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("sio: bad URL %q: no host", rawURL)
	}
	if path == "" {
		path = "/socket.io/"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawPath = ""
	u.RawQuery = "EIO=4&transport=websocket"
	u.Fragment = ""
	return u.String(), nil
}

// clientHandshake reads the Engine.IO open packet, sends the namespace
// CONNECT and waits for its answer. It runs before the goroutines start, so
// it reads and writes the socket directly.
func clientHandshake(ctx context.Context, ws *websocket.Conn) (handshake, error) {
	var hs handshake
	typ, msg, err := ws.Read(ctx)
	if err != nil {
		return hs, fmt.Errorf("sio: handshake: %w", err)
	}
	if typ != websocket.MessageText || len(msg) == 0 || msg[0] != engineOpen {
		return hs, fmt.Errorf("sio: handshake: expected an open packet, got %.32q", msg)
	}
	if err := json.Unmarshal(msg[1:], &hs); err != nil {
		return hs, fmt.Errorf("sio: handshake: %w", err)
	}
	if hs.PingInterval <= 0 || hs.PingTimeout <= 0 || hs.MaxPayload < 0 {
		return hs, fmt.Errorf("sio: handshake: invalid open packet %.128q", msg)
	}

	if err := ws.Write(ctx, websocket.MessageText, []byte{engineMessage, '0' + byte(PacketConnect)}); err != nil {
		return hs, fmt.Errorf("sio: connect: %w", err)
	}
	for {
		typ, msg, err := ws.Read(ctx)
		if err != nil {
			return hs, fmt.Errorf("sio: connect: %w", err)
		}
		if typ != websocket.MessageText || len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case enginePing:
			if err := ws.Write(ctx, websocket.MessageText, pongFrame); err != nil {
				return hs, fmt.Errorf("sio: connect: %w", err)
			}
			continue
		case engineClose:
			return hs, fmt.Errorf("sio: connect: %w", ErrServerDisconnect)
		case engineMessage:
		default:
			continue
		}
		p, err := ParsePacket(msg[1:])
		if err != nil {
			return hs, fmt.Errorf("sio: connect: %w", err)
		}
		if p.Namespace != "" {
			continue
		}
		switch p.Type {
		case PacketConnect:
			return hs, nil
		case PacketConnectError:
			return hs, connectError(p.Data)
		case PacketDisconnect:
			return hs, fmt.Errorf("sio: connect: %w", ErrServerDisconnect)
		}
		// Anything else before the CONNECT answer is not expected from a
		// conforming server and is ignored.
	}
}

func connectError(data []byte) error {
	e := &ConnectError{Data: append(json.RawMessage(nil), data...)}
	var body struct {
		Message string `json:"message"`
	}
	if firstByte(data) == '"' {
		_ = json.Unmarshal(data, &e.Message)
	} else if json.Unmarshal(data, &body) == nil {
		e.Message = body.Message
	}
	return e
}

// Call emits event with args (each encoded with encoding/json; a
// json.RawMessage is sent as is) and an ack id, and waits for the server's
// acknowledgement. It returns the ack arguments, which the caller owns.
//
// It returns ctx.Err() (wrapped) when ctx ends first; an ack arriving later
// is dropped. It returns an error wrapping ErrClosed when the connection is
// or becomes closed before the ack arrives.
func (c *Conn) Call(ctx context.Context, event string, args ...any) ([]json.RawMessage, error) {
	f := c.frames.Get().(*frame)
	ch := make(chan ackResult, 1)

	c.mu.Lock()
	if c.closed {
		err := c.closedErrLocked()
		c.mu.Unlock()
		c.putFrame(f)
		return nil, err
	}
	id := c.nextID
	c.nextID++
	c.pending[id] = ch
	c.mu.Unlock()

	if err := f.encodeEvent(id, event, args); err != nil {
		c.forget(id)
		c.putFrame(f)
		return nil, err
	}
	if c.maxPayload > 0 && f.buf.Len() > c.maxPayload {
		n := f.buf.Len()
		c.forget(id)
		c.putFrame(f)
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrPayloadTooLarge, n, c.maxPayload)
	}

	select {
	case c.out <- f:
	case <-ctx.Done():
		c.putFrame(f)
		return c.abandon(id, ch, ctx)
	case <-c.done:
		c.putFrame(f)
		r := <-ch // fail() delivered the error before closing done
		return r.args, r.err
	}

	select {
	case r := <-ch:
		return r.args, r.err
	case <-ctx.Done():
		return c.abandon(id, ch, ctx)
	}
}

// abandon removes a pending call whose context ended. If the result was
// delivered in the meantime, it is returned instead.
func (c *Conn) abandon(id uint64, ch chan ackResult, ctx context.Context) ([]json.RawMessage, error) {
	if !c.forget(id) {
		r := <-ch
		return r.args, r.err
	}
	return nil, fmt.Errorf("sio: waiting for ack: %w", context.Cause(ctx))
}

// forget removes a pending call; it reports false if the call was already
// removed, in which case its result is (or is about to be) in its channel.
func (c *Conn) forget(id uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[id]; !ok {
		return false
	}
	delete(c.pending, id)
	return true
}

func newFrame() any {
	f := &frame{}
	f.enc = json.NewEncoder(&f.buf)
	f.enc.SetEscapeHTML(false)
	return f
}

func (f *frame) encodeEvent(id uint64, event string, args []any) error {
	var num [20]byte
	f.buf.WriteByte(engineMessage)
	f.buf.WriteByte('0' + byte(PacketEvent))
	f.buf.Write(strconv.AppendUint(num[:0], id, 10))
	f.buf.WriteByte('[')
	if plainASCII(event) { // event names are constants: skip the encoder
		f.buf.WriteByte('"')
		f.buf.WriteString(event)
		f.buf.WriteByte('"')
	} else if err := f.encode(event); err != nil {
		return err
	}
	for i, a := range args {
		f.buf.WriteByte(',')
		if err := f.encode(a); err != nil {
			return fmt.Errorf("sio: encode argument %d of %q: %w", i, event, err)
		}
	}
	f.buf.WriteByte(']')
	return nil
}

// plainASCII reports whether s is printable ASCII that needs no escaping in
// a JSON string.
func plainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// encode writes v without the newline json.Encoder appends.
func (f *frame) encode(v any) error {
	if err := f.enc.Encode(v); err != nil {
		return err
	}
	f.buf.Truncate(f.buf.Len() - 1)
	return nil
}

func (c *Conn) putFrame(f *frame) {
	if f.buf.Cap() > maxRetainedBuffer {
		return
	}
	f.buf.Reset()
	f.sent = nil
	c.frames.Put(f)
}

// Done is closed when the connection becomes unusable.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection closed, or nil while it is open.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close disconnects from the namespace, closes the WebSocket and waits for
// the connection's goroutines to exit. Pending calls fail with ErrClosed.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		// Best effort: tell the server we leave the namespace.
		f := c.frames.Get().(*frame)
		f.buf.WriteByte(engineMessage)
		f.buf.WriteByte('0' + byte(PacketDisconnect))
		sent := make(chan struct{})
		f.sent = sent
		t := time.NewTimer(closeGrace)
		select {
		case c.out <- f:
			select {
			case <-sent:
			case <-c.done:
			case <-t.C:
			}
		case <-c.done:
			c.putFrame(f)
		case <-t.C:
			c.putFrame(f)
		}
		t.Stop()

		if c.shutdown(ErrClosed) {
			// The reader keeps running during the close handshake.
			_ = c.ws.Close(websocket.StatusNormalClosure, "")
		}
		c.ws.CloseNow()
	})
	c.wg.Wait()
	return nil
}

// fail closes the connection after an error.
func (c *Conn) fail(err error) {
	c.shutdown(err)
	c.ws.CloseNow()
}

// shutdown marks the connection closed with cause err, fails the pending
// calls and stops the writer. It reports whether this call closed it.
func (c *Conn) shutdown(err error) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	c.closed = true
	c.err = err
	callErr := c.closedErrLocked()
	for id, ch := range c.pending {
		ch <- ackResult{err: callErr}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.pingTimer.Stop()
	close(c.stop)
	close(c.done)
	return true
}

func (c *Conn) closedErrLocked() error {
	if c.err == nil || errors.Is(c.err, ErrClosed) {
		return ErrClosed
	}
	return fmt.Errorf("%w: %w", ErrClosed, c.err)
}

func (c *Conn) writeLoop() {
	defer c.wg.Done()
	for {
		// Pongs go first, so that a long queue does not delay them.
		select {
		case <-c.pong:
			if !c.write(pongFrame) {
				return
			}
		default:
		}
		select {
		case <-c.stop:
			return
		case <-c.pong:
			if !c.write(pongFrame) {
				return
			}
		case f := <-c.out:
			ok := c.write(f.buf.Bytes())
			if f.sent != nil {
				close(f.sent)
			}
			c.putFrame(f)
			if !ok {
				return
			}
		}
	}
}

// write sends one text frame. A blocked write is released by CloseNow.
func (c *Conn) write(b []byte) bool {
	if err := c.ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		c.fail(fmt.Errorf("sio: write: %w", err))
		return false
	}
	return true
}

func (c *Conn) readLoop() {
	defer c.wg.Done()
	var (
		buf  bytes.Buffer
		args []json.RawMessage
		skip int // binary attachments still to discard
	)
	for {
		typ, r, err := c.ws.Reader(context.Background())
		if err == nil {
			buf.Reset()
			_, err = buf.ReadFrom(r)
		}
		if err != nil {
			c.fail(fmt.Errorf("sio: read: %w", err))
			return
		}
		if typ == websocket.MessageBinary {
			if skip > 0 {
				skip--
			}
			continue
		}
		if err := c.handle(buf.Bytes(), &args, &skip); err != nil {
			c.fail(err)
			return
		}
		if buf.Cap() > maxRetainedBuffer {
			buf = bytes.Buffer{}
		}
		clear(args)
		args = args[:0]
	}
}

// handle processes one Engine.IO text packet.
func (c *Conn) handle(msg []byte, args *[]json.RawMessage, skip *int) error {
	if len(msg) == 0 {
		return fmt.Errorf("%w: empty Engine.IO packet", ErrMalformed)
	}
	switch msg[0] {
	case enginePing:
		c.pingTimer.Reset(c.pingWindow)
		select {
		case c.pong <- struct{}{}:
		default: // a pong is already queued
		}
		return nil
	case enginePong, engineNoop:
		return nil
	case engineClose:
		return ErrServerDisconnect
	case engineMessage:
	default:
		return fmt.Errorf("%w: unexpected Engine.IO packet type %q", ErrMalformed, msg[0])
	}

	p, err := ParsePacket(msg[1:])
	if err != nil {
		return err
	}
	if p.Namespace != "" {
		*skip = p.Attachments
		return nil
	}
	switch p.Type {
	case PacketEvent:
		a, err := SplitArray(*args, p.Data)
		*args = a
		if err != nil {
			return err
		}
		name, err := EventName(a[0])
		if err != nil {
			return err
		}
		if c.onEvent != nil {
			c.onEvent(name, a[1:])
		}
	case PacketAck:
		ch := c.take(p.ID)
		if ch == nil {
			return nil // late ack: the caller gave up
		}
		data := append([]byte(nil), p.Data...)
		a, err := SplitArray(nil, data)
		if err != nil {
			ch <- ackResult{err: err}
			return err
		}
		ch <- ackResult{args: a}
	case PacketBinaryEvent:
		*skip = p.Attachments
	case PacketBinaryAck:
		*skip = p.Attachments
		if ch := c.take(p.ID); ch != nil {
			ch <- ackResult{err: ErrBinaryUnsupported}
		}
	case PacketDisconnect:
		return ErrServerDisconnect
	case PacketConnectError:
		return connectError(p.Data)
	case PacketConnect:
		// Already connected: nothing to do.
	}
	return nil
}

// take removes and returns the channel of a pending call, or nil.
func (c *Conn) take(id uint64) chan ackResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	return ch
}
