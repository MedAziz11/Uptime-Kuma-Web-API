// Command uptime-kuma-api serves the REST API for Uptime Kuma 2.x.
//
// It is configured with environment variables (see internal/config) and
// logs JSON lines to standard error. Exit status: 0 after a clean shutdown
// (SIGTERM or SIGINT), 2 for a configuration error, 1 for any other
// failure.
//
// `uptime-kuma-api healthcheck` checks a running server instead (the
// container's HEALTHCHECK): exit status 0 when it is healthy, 1 otherwise.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/config"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/httpapi"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/version"
)

var _ httpapi.Kuma = (*kuma.Client)(nil)

// Exit statuses.
const (
	exitOK     = 0
	exitFailed = 1
	exitConfig = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr, hooks{})
	stop()
	os.Exit(code)
}

// hooks are what tests change in run. The zero value is production.
type hooks struct {
	// ready, if not nil, is called with the listening address.
	ready func(net.Addr)
	// bcryptCost replaces auth.DefaultCost when not zero: cost 12 makes
	// every login take seconds under the race detector.
	bcryptCost int
	// ensureAdmin replaces httpapi.EnsureAdmin when not nil.
	ensureAdmin func(context.Context, httpapi.AdminStore, *auth.Passwords, string) (httpapi.AdminResult, error)
}

// run is main without the process: it serves until ctx is done.
func run(ctx context.Context, args []string, lookup config.LookupFunc, stdout, stderr io.Writer, h hooks) int {
	if len(args) == 1 && args[0] == "healthcheck" {
		return healthcheck(lookup, stderr)
	}
	flags := flag.NewFlagSet("uptime-kuma-api", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return exitConfig
	}
	if *showVersion {
		fmt.Fprintln(stdout, version.Version)
		return exitOK
	}

	cfg, err := config.Load(lookup)
	if err != nil {
		slog.New(slog.NewJSONHandler(stderr, nil)).Error("cannot start: "+err.Error(), "version", version.Version)
		return exitConfig
	}
	lw := newLogWriter(stderr)
	defer lw.Close()
	log := slog.New(slog.NewJSONHandler(lw, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := serve(ctx, cfg, log, h); err != nil {
		log.Error("uptime-kuma-api failed: "+err.Error(), "version", version.Version)
		return exitFailed
	}
	return exitOK
}

// healthcheck is the `healthcheck` subcommand, the image's HEALTHCHECK (the
// image has no shell and no HTTP client): exit 0 if GET /health on the
// server at LISTEN_ADDR answers 200 within 3 seconds, 1 otherwise. An
// unspecified listen host (":8000", "0.0.0.0:8000") is reached on 127.0.0.1.
func healthcheck(lookup config.LookupFunc, stderr io.Writer) int {
	addr, ok := lookup("LISTEN_ADDR")
	if !ok {
		addr = config.DefaultListenAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck: LISTEN_ADDR:", err)
		return exitFailed
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return exitFailed
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: /health answered", res.Status)
		return exitFailed
	}
	return exitOK
}

func serve(ctx context.Context, cfg *config.Config, log *slog.Logger, h hooks) error {
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open the user database (DB_PATH): %w", err)
	}
	defer st.Close()

	key, generated, err := auth.ResolveSecretKey(cfg.SecretKey.Value(), cfg.DBPath)
	if err != nil {
		return fmt.Errorf("resolve the token signing key: %w", err)
	}
	if generated {
		log.Warn("SECRET_KEY is not set: generated one and stored it in " +
			filepath.Join(filepath.Dir(cfg.DBPath), auth.SecretKeyFile))
	}
	tokens, err := auth.NewTokens(key, cfg.AccessTokenExpiration)
	if err != nil {
		return err
	}
	cost := auth.DefaultCost
	if h.bcryptCost != 0 {
		cost = h.bcryptCost
	}
	passwords, err := auth.NewPasswords(cost)
	if err != nil {
		return err
	}

	// Created, not connected: the first call connects, so the API starts
	// and answers /health while Uptime Kuma is down.
	var client *http.Client
	if !cfg.KumaSSLVerify {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // KUMA_SSL_VERIFY=false, the operator's choice
		client = &http.Client{Transport: t}
	}
	session, err := kuma.New(kuma.Config{
		URL:        cfg.KumaServer,
		Username:   cfg.KumaUsername,
		Password:   cfg.KumaPassword.Value(),
		Timeout:    cfg.KumaTimeout,
		HTTPClient: client,
		Logger:     log,
	})
	if err != nil {
		return fmt.Errorf("configure Uptime Kuma (KUMA_SERVER): %w", err)
	}
	defer session.Close()

	// The admin user is set up while the server already answers: checking
	// ADMIN_PASSWORD is a bcrypt operation (about 300 ms), which would
	// otherwise delay /health at every start. Until it is done, logins and
	// authenticated requests wait for it (httpapi.Options.AdminReady).
	adminReady := make(chan struct{})
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	ensureAdmin := httpapi.EnsureAdmin
	if h.ensureAdmin != nil {
		ensureAdmin = h.ensureAdmin
	}
	var adminErr error
	adminDone := make(chan struct{})
	go func() {
		defer close(adminDone)
		switch res, err := ensureAdmin(ctx, st, passwords, cfg.AdminPassword.Value()); {
		case err != nil && ctx.Err() != nil:
			return // shutting down
		case err != nil:
			adminErr = fmt.Errorf("set up the admin user: %w", err)
			stop() // shuts the server down: serve fails
			return
		case res == httpapi.AdminCreated:
			log.Info("created the 'admin' user")
		case res == httpapi.AdminPasswordUpdated:
			log.Info("updated the 'admin' password from ADMIN_PASSWORD")
		}
		close(adminReady)
	}()
	// Before the store is closed (deferred above), on every return.
	defer func() {
		stop()
		<-adminDone
	}()

	api, err := httpapi.New(httpapi.Options{
		Users:       st,
		Passwords:   passwords,
		Tokens:      tokens,
		Kuma:        kuma.NewClient(session),
		CORSOrigins: cfg.CORSOrigins,
		Logger:      log,
		Version:     version.Version,
		AdminReady:  adminReady,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s (LISTEN_ADDR): %w", cfg.ListenAddr, err)
	}
	log.Info("uptime-kuma-api started", "version", version.Version, "addr", ln.Addr().String())
	if h.ready != nil {
		h.ready(ln.Addr())
	}
	srv := httpapi.NewHTTPServer(api, cfg.KumaTimeout, log)
	err = httpapi.Serve(ctx, srv, ln, httpapi.ShutdownTimeout)
	stop()
	<-adminDone
	if adminErr != nil {
		return adminErr
	}
	if err != nil {
		return err
	}
	log.Info("uptime-kuma-api stopped")
	return nil
}
