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

	"github.com/suppressio/aipermiler/gocore/internal/store"
)

// DefaultShutdownTimeout bounds the clean stop.
const DefaultShutdownTimeout = 10 * time.Second

// Options configures Run.
type Options struct {
	Dir             string // the config dir: service.lock and api.json live here
	Version         string
	ContractVersion int
	Logger          *slog.Logger
	ShutdownTimeout time.Duration // zero means DefaultShutdownTimeout
	// Loop makes the refresh loop (#283) on the store, opened once the lock is
	// held. Its Run lasts the whole life of the service: its context ends at the
	// stop, and the lock is released only after it returns. The endpoints read
	// it (#248). Nil runs no loop: nothing is ever read.
	Loop func(st *store.Store) Loop
	// Raw reads an account's diagnostic raw document (GET .../raw).
	Raw RawFunc
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
	// POST /api/v1/shutdown ends the run like a cancelled ctx does (#286).
	ctx, requestStop := context.WithCancel(ctx)
	defer requestStop()
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	// The lock is ours, so whatever api.json and temp files are here are stale:
	// drop them now, so a failed start never leaves an old port and token behind.
	if err := removeLeftovers(opts.Dir); err != nil {
		opts.Logger.Warn("Cannot clean up the files of an earlier service", "error", err)
	}
	if err := store.RemoveLeftovers(opts.Dir); err != nil {
		opts.Logger.Warn("Cannot clean up the store's temp files", "error", err)
	}
	// An unreadable or newer store stops the start: an empty store in its place
	// would overwrite the user's history at the first write.
	st, err := store.Open(opts.Dir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
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

	// In-flight work gets its own context so a stop can cancel it. Endpoints must
	// honour it: Run returns once every connection is closed, and a handler that
	// ignores the cancellation may still be running when the lock is released.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	var loop Loop = noLoop{}
	if opts.Loop != nil {
		loop = opts.Loop(st)
	}
	server := newHTTPServer(workCtx, NewHandler(HandlerConfig{
		Token: token, Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Version: opts.Version, ContractVersion: opts.ContractVersion,
		Shutdown: requestStop, Store: st, Loop: loop, Raw: opts.Raw,
	}), opts.Logger)
	serveErr, serveDone := make(chan error, 1), make(chan struct{})
	go func() {
		serveErr <- server.Serve(listener)
		close(serveDone)
	}()
	workDone := make(chan struct{})
	go func() {
		defer close(workDone)
		_ = loop.Run(workCtx)
	}()
	// teardown is the one way out once Serve runs: cancel the work, close every
	// connection, wait for Serve to let the listener go, delete api.json. Every
	// step is idempotent, so a stop may do the first ones early (see below).
	teardown := func() {
		cancelWork()
		_ = server.Close()
		<-serveDone
		<-workDone
		if err := RemoveInfo(opts.Dir); err != nil {
			opts.Logger.Warn("Cannot delete api.json", "error", err)
		}
	}

	if ctx.Err() != nil { // already stopped: skip api.json rather than advertise a dying port
		teardown()
		return nil
	}
	info := Info{
		SchemaVersion: SchemaVersion, Port: port, Token: token, Version: opts.Version,
		ContractVersion: opts.ContractVersion, PID: os.Getpid(), StartedAt: time.Now().UTC(),
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
	// The first two teardown steps come before the drain: by design (#246,
	// decision 4) a stop cancels in-flight work at once instead of waiting for it
	// (fetches are re-run by the next refresh), and api.json goes first, so no
	// client is pointed at a port that is going away while connections drain.
	// teardown deletes it again afterwards, in case a reader held it open.
	cancelWork()
	_ = RemoveInfo(opts.Dir) // a failure here is retried, and logged once, by teardown
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
