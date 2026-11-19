package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

// DefaultShutdownTimeout bounds the clean stop.
const DefaultShutdownTimeout = 10 * time.Second

// handlerGrace is how long a stop waits, after closing every connection, for
// handlers that ignored the cancellation, before it releases the lock anyway.
const handlerGrace = time.Second

// Options configures Run.
type Options struct {
	Dir             string // the config dir: service.lock and api.json live here
	Version         string
	ContractVersion int
	Logger          *slog.Logger
	ShutdownTimeout time.Duration // zero means DefaultShutdownTimeout
}

// Run holds the single-instance lock, serves the API on a random loopback port
// and writes api.json once the listener is up. It returns nil after a clean
// stop (ctx cancelled), even one that hit the shutdown bound, ErrAlreadyRunning
// when another service holds the lock. On a stop it cancels in-flight requests,
// deletes api.json, stops accepting, waits for open requests up to the shutdown
// timeout and closes whatever is still open, then releases the lock.
func Run(ctx context.Context, opts Options) error {
	return run(ctx, opts, func(ctx context.Context) (net.Listener, error) {
		return new(net.ListenConfig).Listen(ctx, "tcp", "127.0.0.1:0")
	})
}

// run is Run with the listener made by listen, so tests can hand it one that dies.
func run(ctx context.Context, opts Options, listen func(context.Context) (net.Listener, error)) error {
	lock, err := AcquireLock(opts.Dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	// The lock is ours, so whatever api.json and temp files are here are stale:
	// drop them now, so a failed start never leaves an old port and token behind.
	if err := removeLeftovers(opts.Dir); err != nil {
		opts.Logger.Warn("Cannot clean up the files of an earlier service", "error", err)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// Not the stop context: a stop that arrives while starting is a clean stop
	// (the select below sees it at once), not a listen error.
	listener, err := listen(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a tcp listener always has a TCPAddr

	// In-flight work gets its own context so a stop can cancel it.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	var handlers inflight
	server := newHTTPServer(workCtx, handlers.wrap(NewHandler(HandlerConfig{
		Token: token, Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Version: opts.Version, ContractVersion: opts.ContractVersion,
	})), opts.Logger)
	// teardown is the one way out after the server exists: cancel the work, close
	// whatever is still open, close the gate and give handlers that ignored the
	// cancellation a short grace, then remove our api.json (not another
	// service's, see removeOwnInfo). Every step is idempotent and what fails is
	// logged here; the deferred release of the lock comes after.
	teardown := func() {
		cancelWork()
		handlers.close()
		_ = server.Close()
		if !handlers.wait(handlerGrace) {
			opts.Logger.Warn("Handlers still running after the stop; releasing the lock anyway")
		}
		if err := removeOwnInfo(opts.Dir, token); err != nil {
			opts.Logger.Warn("Cannot delete api.json", "error", err)
		}
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	info := Info{
		SchemaVersion: SchemaVersion, Port: port, Token: token, Version: opts.Version,
		ContractVersion: opts.ContractVersion, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if ctx.Err() != nil { // stopped while starting: never advertise this instance
		teardown()
		return nil
	}
	if err := WriteInfo(opts.Dir, info); err != nil {
		teardown()
		return err
	}
	opts.Logger.Info("Service listening", "port", port, "version", opts.Version)

	select {
	case err := <-serveErr:
		teardown()
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	timeout := opts.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}
	opts.Logger.Info("Service stopping")
	// Before the drain: by design (#246, decision 4) a stop cancels in-flight work
	// at once instead of waiting for it (fetches are re-run by the next refresh),
	// and api.json goes first, so no client is pointed at a port that is going
	// away while connections drain. teardown tries the delete again afterwards
	// (a reader may have held the file open).
	cancelWork()
	_ = removeOwnInfo(opts.Dir, token)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	// The stop was asked for, so it succeeds: what could not be done cleanly is
	// logged. Connections that outlast the bound are closed by force, and api.json
	// is only a hint (clients check it against /health), so neither makes a stop
	// look like a crash to a supervisor.
	if err := server.Shutdown(shutdownCtx); err != nil {
		opts.Logger.Warn("Shutdown bound reached; closing connections", "error", err)
	}
	teardown()
	return nil
}

// newHTTPServer configures the server. No WriteTimeout on purpose: a refresh can
// last minutes; endpoints that can be slow set their own deadline
// (http.ResponseController). net/http's own errors (handler panics, accept
// errors) go to the logger at warn, so they show at the default level.
func newHTTPServer(workCtx context.Context, handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           handler,
		BaseContext:       func(net.Listener) context.Context { return workCtx },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		MaxHeaderBytes:    64 << 10,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
