package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Timeouts of the HTTP server. WriteTimeout must cover the slowest handler:
// a call to Uptime Kuma (KUMA_TIMEOUT) or a bcrypt check; see NewHTTPServer.
const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	IdleTimeout       = 120 * time.Second
	MaxHeaderBytes    = 64 << 10
	// ShutdownTimeout bounds how long in-flight requests may take to
	// finish after SIGTERM.
	ShutdownTimeout = 20 * time.Second
)

// NewHTTPServer returns an http.Server for h with the timeouts above and a
// write timeout of handlerTimeout plus a margin.
func NewHTTPServer(h http.Handler, handlerTimeout time.Duration, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      handlerTimeout + 30*time.Second,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Serve serves srv on ln until ctx is done, then stops accepting
// connections and waits up to drain for in-flight requests. It returns nil
// after a clean shutdown.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, drain time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	err := srv.Shutdown(sctx)
	if serr := <-errc; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", serr)
	}
	if err != nil {
		srv.Close()
		return fmt.Errorf("http server shutdown: %w", err)
	}
	return nil
}
