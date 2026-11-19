package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
// stop (ctx cancelled), ErrAlreadyRunning when another service holds the lock.
// On the way out it stops accepting, cancels in-flight requests, deletes
// api.json and releases the lock, all bounded by the shutdown timeout.
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
		BaseContext:       func(net.Listener) context.Context { return workCtx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	info := Info{
		SchemaVersion: SchemaVersion, Port: port, Token: token, Version: opts.Version,
		ContractVersion: opts.ContractVersion, PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	if err := WriteInfo(opts.Dir, info); err != nil {
		cancelWork()
		_ = server.Close()
		removeLeftovers(opts.Dir)
		return err
	}
	opts.Logger.Info("Service listening", "port", port, "version", opts.Version)

	select {
	case err := <-serveErr:
		_ = RemoveInfo(opts.Dir)
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	timeout := opts.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}
	opts.Logger.Info("Service stopping")
	cancelWork()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	var result error
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		result = fmt.Errorf("shutdown: %w", err)
	}
	if err := RemoveInfo(opts.Dir); err != nil {
		result = errors.Join(result, err)
	}
	return result
}
