// Package siotest provides an in-process Socket.IO server for tests. It
// speaks Engine.IO 4 and Socket.IO 5 over a real WebSocket (httptest +
// github.com/coder/websocket), with knobs to misbehave: no pings, connect
// errors, delayed or dropped acks, abrupt disconnects.
package siotest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
	"github.com/coder/websocket"
)

// Event is an event received from a client.
type Event struct {
	Name string
	// Args excludes the event name. The slices are owned by the handler.
	Args []json.RawMessage
	// ID is the ack id; HasID is false when the client expects no ack.
	ID    uint64
	HasID bool
}

// Options configure a Server. Zero values select usable defaults.
type Options struct {
	// PingInterval and PingTimeout are announced in the open packet.
	// Defaults: 25s and 20s, as Socket.IO's.
	PingInterval, PingTimeout time.Duration
	// NoPing stops the server from sending pings.
	NoPing bool
	// MaxPayload is announced in the open packet. Default: 1e6.
	MaxPayload int
	// ConnectError, when set, is sent as the CONNECT_ERROR message instead
	// of accepting the namespace CONNECT.
	ConnectError string
	// OnConnect runs after the namespace CONNECT was accepted, before any
	// event of the client is read.
	OnConnect func(s *Socket)
	// OnEvent runs for each event, in order, on the socket's reader
	// goroutine. It may answer with s.Ack, now or later.
	OnEvent func(s *Socket, ev Event)
	// OnPong runs for each pong received, on the socket's reader goroutine.
	OnPong func(s *Socket)
	// HTTP, when set, serves the requests for paths other than /socket.io/.
	HTTP http.Handler
}

// Server is a test Socket.IO server. Close it with Close.
type Server struct {
	// URL is the base http:// URL; the endpoint is URL + "/socket.io/".
	URL string

	opts Options
	http *httptest.Server

	mu      sync.Mutex
	sockets map[*Socket]struct{}
	conns   int
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	connCh  chan *Socket
}

// NewServer starts a server.
func NewServer(opts Options) *Server {
	if opts.PingInterval <= 0 {
		opts.PingInterval = 25 * time.Second
	}
	if opts.PingTimeout <= 0 {
		opts.PingTimeout = 20 * time.Second
	}
	if opts.MaxPayload <= 0 {
		opts.MaxPayload = 1e6
	}
	s := &Server{opts: opts, sockets: map[*Socket]struct{}{}, connCh: make(chan *Socket, 64)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.http.URL
	return s
}

// Close disconnects every socket, stops the server and waits for its
// goroutines.
func (s *Server) Close() {
	s.mu.Lock()
	s.cancel()
	for sock := range s.sockets {
		sock.ws.CloseNow()
	}
	s.mu.Unlock()
	s.http.Close()
	s.wg.Wait()
}

// Connections returns how many sockets completed the namespace CONNECT.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// Accepted delivers each socket after its namespace CONNECT, until the
// buffer (64) is full.
func (s *Server) Accepted() <-chan *Socket { return s.connCh }

// DisconnectAll closes every socket abruptly (no close frame).
func (s *Server) DisconnectAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sock := range s.sockets {
		sock.ws.CloseNow()
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/socket.io/" && s.opts.HTTP != nil {
		s.opts.HTTP.ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/socket.io/" || r.URL.Query().Get("EIO") != "4" || r.URL.Query().Get("transport") != "websocket" {
		http.Error(w, "bad endpoint", http.StatusBadRequest)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(int64(s.opts.MaxPayload) + 16)
	// Under mu, so that Close either sees this Add before it waits or has
	// cancelled s.ctx before the check.
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		ws.CloseNow()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	sock := &Socket{ws: ws, srv: s, pong: make(chan struct{}, 1), closed: make(chan struct{})}
	s.mu.Lock()
	s.sockets[sock] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sockets, sock)
		s.mu.Unlock()
		close(sock.closed)
		ws.CloseNow()
	}()
	sock.run(s.ctx)
}

// Socket is the server side of one client connection.
type Socket struct {
	ws     *websocket.Conn
	srv    *Server
	pong   chan struct{}
	closed chan struct{}
}

// Closed is closed when the socket's connection ended.
func (sock *Socket) Closed() <-chan struct{} { return sock.closed }

func (sock *Socket) run(ctx context.Context) {
	o := sock.srv.opts
	open := fmt.Sprintf(`0{"sid":"eio-sid","upgrades":[],"pingInterval":%d,"pingTimeout":%d,"maxPayload":%d}`,
		o.PingInterval.Milliseconds(), o.PingTimeout.Milliseconds(), o.MaxPayload)
	if sock.WriteRaw(open) != nil {
		return
	}
	typ, msg, err := sock.ws.Read(ctx)
	if err != nil || typ != websocket.MessageText || string(msg) != "40" {
		return
	}
	if o.ConnectError != "" {
		b, _ := json.Marshal(map[string]string{"message": o.ConnectError})
		_ = sock.WriteRaw("44" + string(b))
		return
	}
	if sock.WriteRaw(`40{"sid":"sio-sid"}`) != nil {
		return
	}
	sock.srv.mu.Lock()
	sock.srv.conns++
	sock.srv.mu.Unlock()
	select {
	case sock.srv.connCh <- sock:
	default:
	}

	if !o.NoPing {
		pingCtx, stop := context.WithCancel(ctx)
		defer stop()
		sock.srv.wg.Add(1)
		go sock.pingLoop(pingCtx)
	}
	if o.OnConnect != nil {
		o.OnConnect(sock)
	}

	for {
		typ, msg, err := sock.ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText || len(msg) == 0 {
			return
		}
		switch msg[0] {
		case '3':
			select {
			case sock.pong <- struct{}{}:
			default:
			}
			if o.OnPong != nil {
				o.OnPong(sock)
			}
			continue
		case '4':
		default:
			return
		}
		p, err := sio.ParsePacket(msg[1:])
		if err != nil {
			return
		}
		switch p.Type {
		case sio.PacketEvent:
			args, err := sio.SplitArray(nil, p.Data)
			if err != nil {
				return
			}
			name, err := sio.EventName(args[0])
			if err != nil {
				return
			}
			if o.OnEvent != nil {
				o.OnEvent(sock, Event{Name: name, Args: args[1:], ID: p.ID, HasID: p.HasID})
			}
		case sio.PacketDisconnect:
			_ = sock.ws.Close(websocket.StatusNormalClosure, "")
			return
		default:
			return
		}
	}
}

func (sock *Socket) pingLoop(ctx context.Context) {
	defer sock.srv.wg.Done()
	o := sock.srv.opts
	t := time.NewTicker(o.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if sock.WriteRaw("2") != nil {
			return
		}
		timeout := time.NewTimer(o.PingTimeout)
		select {
		case <-ctx.Done():
			timeout.Stop()
			return
		case <-sock.pong:
			timeout.Stop()
		case <-timeout.C:
			sock.ws.CloseNow()
			return
		}
	}
}

// WriteRaw sends one text frame as is (an Engine.IO packet).
func (sock *Socket) WriteRaw(frame string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return sock.ws.Write(ctx, websocket.MessageText, []byte(frame))
}

// WriteBinary sends one binary frame (a binary attachment).
func (sock *Socket) WriteBinary(b []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return sock.ws.Write(ctx, websocket.MessageBinary, b)
}

// Emit pushes an event to the client.
func (sock *Socket) Emit(event string, args ...any) error {
	payload, err := json.Marshal(append([]any{event}, args...))
	if err != nil {
		return err
	}
	return sock.WriteRaw("42" + string(payload))
}

// Ack answers the event with ack id id.
func (sock *Socket) Ack(id uint64, args ...any) error {
	if args == nil {
		args = []any{}
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return sock.WriteRaw("43" + strconv.FormatUint(id, 10) + string(payload))
}

// Kill closes the connection abruptly, without a close frame.
func (sock *Socket) Kill() { sock.ws.CloseNow() }
