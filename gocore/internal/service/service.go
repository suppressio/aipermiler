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
// deletes api.json, stops accepting and closes the remaining connections within
// the shutdown timeout, then releases the lock.
func Run(ctx context.Context, opts Options) error {
	lock, err := AcquireLock(opts.Dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	// The lock is ours, so whatever api.json and temp files are here are stale:
	// drop them now, so a failed start never leaves an old port and token behind.
	removeLeftovers(opts.Dir)
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	// Not the stop context: a stop that arrives while starting is a clean stop
	// (the select below sees it at once), not a listen error.
	listener, err := new(net.ListenConfig).Listen(context.WithoutCancel(ctx), "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a tcp listener always has a TCPAddr

	// In-flight work gets its own context so a stop can cancel it.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	server := &http.Server{
		Handler: NewHandler(HandlerConfig{
			Token: token, Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
			Version: opts.Version, ContractVersion: opts.ContractVersion,
		}),
		BaseContext:    func(net.Listener) context.Context { return workCtx },
		ErrorLog:       slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		MaxHeaderBytes: 64 << 10,
		// No WriteTimeout on purpose: a refresh can last minutes. Endpoints that
		// can be slow set their own deadline (http.ResponseController).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	// abort tears down a start that failed or a server that died: nothing to drain.
	abort := func() {
		cancelWork()
		_ = server.Close()
		removeLeftovers(opts.Dir)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	info := Info{
		SchemaVersion: SchemaVersion, Port: port, Token: token, Version: opts.Version,
		ContractVersion: opts.ContractVersion, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if err := WriteInfo(opts.Dir, info); err != nil {
		abort()
		return err
	}
	opts.Logger.Info("Service listening", "port", port, "version", opts.Version)

	select {
	case err := <-serveErr:
		abort()
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	timeout := opts.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}
	opts.Logger.Info("Service stopping")
	// By design (#246, decision 4) a stop cancels in-flight work at once instead of
	// draining it: fetches are re-run by the next refresh. api.json goes first, so
	// no client is pointed at a port that is going away while connections drain.
	cancelWork()
	if err := RemoveInfo(opts.Dir); err != nil {
		opts.Logger.Warn("Cannot delete api.json", "error", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	// The stop was asked for, so it succeeds: what could not be done cleanly is
	// logged. Connections that outlast the bound are closed by force, and api.json
	// is only a hint (clients check it against /health), so neither makes a stop
	// look like a crash to a supervisor.
	if err := server.Shutdown(shutdownCtx); err != nil {
		opts.Logger.Warn("Shutdown bound reached; closing connections", "error", err)
		_ = server.Close()
	}
	return nil
}
