// Package kuma talks to Uptime Kuma 2.x over Socket.IO.
package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio"
)

// Defaults and bounds of Config and of the reconnection backoff.
const (
	DefaultTimeout = 10 * time.Second
	minBackoff     = 250 * time.Millisecond
	maxBackoff     = 15 * time.Second
)

// Config configures a Session.
type Config struct {
	// URL is Uptime Kuma's base URL (http or https), e.g.
	// "http://uptime-kuma:3001". A path is kept as a prefix of /socket.io/.
	URL      string
	Username string
	Password string
	// Timeout bounds every call, the connection handshake and each login
	// step (KUMA_TIMEOUT). Default: DefaultTimeout.
	Timeout time.Duration
	// HTTPClient carries the TLS settings (KUMA_SSL_VERIFY). Default:
	// http.DefaultClient.
	HTTPClient *http.Client
	// Logger receives connection events. Default: discard.
	Logger *slog.Logger
}

// Handler receives the arguments of an event pushed by Uptime Kuma. See
// Session.Handle for the rules it must follow.
type Handler func(args []json.RawMessage)

// Session is one shared, authenticated connection to Uptime Kuma.
//
// It connects lazily on the first Call and then stays connected: a
// supervisor goroutine logs in, watches the connection and reconnects with
// exponential backoff and jitter (250 ms to 15 s), logging in again with the
// session token and falling back to the password once if the token is
// rejected. Calls run concurrently on the connection, correlated by ack id.
type Session struct {
	url      string
	username string
	password string
	timeout  time.Duration
	client   *http.Client
	log      *slog.Logger

	// Backoff bounds; fields so that tests can shorten them.
	minBackoff, maxBackoff time.Duration

	ctx    context.Context // canceled by Close
	cancel context.CancelFunc
	wg     sync.WaitGroup

	handlersMu sync.RWMutex
	handlers   map[string]Handler

	mu      sync.Mutex
	started bool
	closed  bool
	conn    *sio.Conn     // set while connected and logged in
	attempt chan struct{} // set while a connection attempt runs; closed when it ends
	lastErr error         // why the session is not connected (wraps ErrUnavailable)
	fatal   error         // a configuration error: no more attempts
	changed chan struct{} // closed and replaced whenever the state above changes

	token string // the session token; used by the supervisor goroutine only
}

// New returns a Session. It does not connect: the first Call does.
func New(cfg Config) (*Session, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("kuma: invalid URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("kuma: invalid URL %q: want http(s)://host[:port]", u.Redacted())
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("kuma: negative timeout %v", cfg.Timeout)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		url:        cfg.URL,
		username:   cfg.Username,
		password:   cfg.Password,
		timeout:    cfg.Timeout,
		client:     cfg.HTTPClient,
		log:        cfg.Logger.With("component", "kuma"),
		minBackoff: minBackoff,
		maxBackoff: maxBackoff,
		ctx:        ctx,
		cancel:     cancel,
		handlers:   make(map[string]Handler),
		changed:    make(chan struct{}),
	}, nil
}

// Handle registers h for the event pushed by Uptime Kuma under name,
// replacing any previous handler; a nil h removes it. Register handlers
// before the first Call so that the pushes following the login are seen.
//
// Handlers run on the connection's reader goroutine, one at a time and in
// the order the events arrive. args are only valid during the call: decode
// or copy them. A handler must return quickly and must not wait for a Call,
// whose answer could only be read after the handler returned.
func (s *Session) Handle(name string, h Handler) {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()
	if h == nil {
		delete(s.handlers, name)
		return
	}
	s.handlers[name] = h
}

func (s *Session) dispatch(name string, args []json.RawMessage) {
	s.handlersMu.RLock()
	h := s.handlers[name]
	s.handlersMu.RUnlock()
	if h != nil {
		h(args)
	}
}

// Call sends a command and returns the first argument of Uptime Kuma's
// acknowledgement (null if there is none). The call is bounded by ctx and by
// the configured timeout.
//
// Errors: ErrTimeout when no answer arrived in time (a late answer is
// dropped); ErrUnavailable when Uptime Kuma cannot be reached or the
// connection is lost while waiting (the command may or may not have been
// executed); *CommandError when Kuma answered {ok: false}; ctx.Err() when
// ctx was canceled; any other error means the arguments could not be sent.
func (s *Session) Call(ctx context.Context, event string, args ...any) (json.RawMessage, error) {
	res, _, err := s.call(ctx, event, args)
	return res, err
}

// CallIdempotent is Call for commands that are safe to repeat (reads): if
// the connection is lost while the call is in flight, it waits for the
// session to reconnect (within the timeout) and sends the command once more.
func (s *Session) CallIdempotent(ctx context.Context, event string, args ...any) (json.RawMessage, error) {
	res, used, err := s.call(ctx, event, args)
	if err == nil || used == nil || !errors.Is(err, sio.ErrClosed) {
		return res, err
	}
	if s.awaitReconnect(ctx, used) != nil {
		return nil, err
	}
	res, _, err = s.call(ctx, event, args)
	return res, err
}

func (s *Session) call(ctx context.Context, event string, args []any) (json.RawMessage, *sio.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	c, err := s.ready(ctx)
	if err != nil {
		return nil, nil, err
	}
	ack, err := c.Call(ctx, event, args...)
	if err != nil {
		return nil, c, callError(ctx, event, err)
	}
	res := json.RawMessage("null")
	if len(ack) > 0 {
		res = ack[0]
	}
	if err := commandError(event, res); err != nil {
		return nil, c, err
	}
	return res, c, nil
}

func callError(ctx context.Context, event string, err error) error {
	switch {
	case errors.Is(err, sio.ErrClosed):
		return fmt.Errorf("%w: %s: connection lost: %w", ErrUnavailable, event, err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %s: %w", ErrTimeout, event, context.DeadlineExceeded)
	case ctx.Err() != nil:
		return fmt.Errorf("kuma: %s: %w", event, ctx.Err())
	default:
		return fmt.Errorf("kuma: %s: %w", event, err)
	}
}

// ready returns the live connection, starting the supervisor on first use
// and waiting for a connection attempt in progress. While the session backs
// off after a failure it fails fast with the last error.
func (s *Session) ready(ctx context.Context) (*sio.Conn, error) {
	waited := false
	for {
		s.mu.Lock()
		switch {
		case s.closed:
			s.mu.Unlock()
			return nil, errSessionClosed()
		case s.fatal != nil:
			err := s.fatal
			s.mu.Unlock()
			return nil, err
		case !s.started:
			s.started = true
			s.attempt = make(chan struct{})
			s.wg.Add(1)
			go s.supervise()
		}
		c, attempt, lastErr := s.conn, s.attempt, s.lastErr
		s.mu.Unlock()

		switch {
		case c != nil:
			return c, nil
		case attempt == nil || waited:
			return nil, lastErr
		}
		select {
		case <-attempt:
			waited = true
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: still connecting: %w", ErrUnavailable, ctx.Err())
		}
	}
}

// awaitReconnect waits until the session holds a connection other than old.
func (s *Session) awaitReconnect(ctx context.Context, old *sio.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	for {
		s.mu.Lock()
		c, changed, closed, fatal := s.conn, s.changed, s.closed, s.fatal
		s.mu.Unlock()
		switch {
		case closed:
			return errSessionClosed()
		case fatal != nil:
			return fatal
		case c != nil && c != old:
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func errSessionClosed() error { return fmt.Errorf("%w: session closed", ErrUnavailable) }

// notifyLocked wakes the goroutines waiting in awaitReconnect.
func (s *Session) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// supervise connects, logs in, waits for the connection to drop and starts
// over, until Close or a configuration error.
func (s *Session) supervise() {
	defer s.wg.Done()
	backoff := s.minBackoff
	for {
		c, err := s.connect()

		s.mu.Lock()
		close(s.attempt)
		s.attempt = nil
		if err != nil {
			if s.ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			s.lastErr = err
			fatal := errors.Is(err, ErrTwoFactorRequired)
			if fatal {
				s.fatal = err
			}
			s.notifyLocked()
			s.mu.Unlock()
			if fatal {
				s.log.Error("cannot log in to Uptime Kuma; not retrying", "err", err)
				return
			}
			s.log.Warn("cannot connect to Uptime Kuma", "err", err)
		} else {
			s.conn = c
			s.lastErr = nil
			s.notifyLocked()
			s.mu.Unlock()
			s.log.Info("connected to Uptime Kuma")

			since := time.Now()
			select {
			case <-c.Done():
			case <-s.ctx.Done():
			}
			s.mu.Lock()
			s.conn = nil
			s.lastErr = fmt.Errorf("%w: connection lost: %w", ErrUnavailable, c.Err())
			s.notifyLocked()
			s.mu.Unlock()
			_ = c.Close() // fails the calls in flight and waits for the goroutines
			if s.ctx.Err() != nil {
				return
			}
			s.log.Warn("lost the connection to Uptime Kuma", "err", c.Err())
			if time.Since(since) >= s.maxBackoff {
				backoff = s.minBackoff // it was stable: start over quickly
			}
		}

		t := time.NewTimer(jitter(backoff, s.maxBackoff))
		select {
		case <-t.C:
		case <-s.ctx.Done():
			t.Stop()
			return
		}
		backoff = min(2*backoff, s.maxBackoff)

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.attempt = make(chan struct{})
		s.mu.Unlock()
	}
}

// jitter returns d plus up to 50 %, capped at limit.
func jitter(d, limit time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return min(d+rand.N(d/2+1), limit)
}

// connect dials Uptime Kuma and logs in.
func (s *Session) connect() (*sio.Conn, error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
	defer cancel()
	c, err := sio.Dial(ctx, s.url, sio.Options{HTTPClient: s.client, OnEvent: s.dispatch})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err := s.login(c); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"` // 2FA code; empty
}

type loginResponse struct {
	OK            bool            `json:"ok"`
	Token         string          `json:"token"`
	TokenRequired bool            `json:"tokenRequired"`
	Msg           json.RawMessage `json:"msg"`
}

// login authenticates c: with the session token when there is one, falling
// back once to the password if Uptime Kuma rejects the token.
func (s *Session) login(c *sio.Conn) error {
	if s.token != "" {
		r, err := s.loginCall(c, "loginByToken", s.token)
		if err != nil {
			return err
		}
		if r.OK {
			return nil
		}
		s.log.Info("Uptime Kuma rejected the session token; logging in with the password", "msg", message(r.Msg))
		s.token = ""
	}
	r, err := s.loginCall(c, "login", loginRequest{Username: s.username, Password: s.password})
	switch {
	case err != nil:
		return err
	case r.TokenRequired:
		return fmt.Errorf("%w: %w", ErrUnavailable, ErrTwoFactorRequired)
	case !r.OK:
		return fmt.Errorf("%w: %w: %s", ErrUnavailable, ErrLoginRejected, message(r.Msg))
	}
	s.token = r.Token
	return nil
}

func (s *Session) loginCall(c *sio.Conn, event string, arg any) (loginResponse, error) {
	var r loginResponse
	ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
	defer cancel()
	ack, err := c.Call(ctx, event, arg)
	if err != nil {
		return r, fmt.Errorf("%w: %s: %w", ErrUnavailable, event, err)
	}
	if len(ack) == 0 {
		return r, fmt.Errorf("%w: %s: empty answer", ErrUnavailable, event)
	}
	if err := json.Unmarshal(ack[0], &r); err != nil {
		return r, fmt.Errorf("%w: %s: unexpected answer: %w", ErrUnavailable, event, err)
	}
	return r, nil
}

// Close closes the connection and stops reconnecting. Calls in flight fail
// with ErrUnavailable, and so do later calls. It waits for the session's
// goroutines to exit.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.wg.Wait()
		return nil
	}
	s.closed = true
	s.notifyLocked()
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	return nil
}
