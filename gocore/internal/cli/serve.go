package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/config"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

// runServe keeps the process up as the always-on service until SIGINT/SIGTERM
// (or env.Context ends). It takes no flags: the port is random and the
// directory is the core's config directory (#247).
func runServe(env Env, args []string, logger *slog.Logger) int {
	flags := newFlags("serve")
	if err := flags.Parse(args); err != nil {
		return flagError(env, false, err)
	}
	if flags.NArg() != 0 {
		return usageError(env, false, "Unexpected arguments")
	}
	dir, err := config.Ensure()
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, oneLine(err.Error()))
		return ExitProviderError
	}
	ctx := env.Context
	if ctx == nil {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		// After the first signal a second one gets the default action, so a stuck
		// shutdown can still be interrupted.
		go func() { <-ctx.Done(); stop() }()
	}
	err = service.Run(ctx, service.Options{
		Dir: dir, Version: buildinfo.Version, ContractVersion: usage.SchemaVersion, Logger: logger,
	})
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, service.ErrAlreadyRunning):
		_, _ = fmt.Fprintln(env.Stderr, "The service is already running")
		return ExitAlreadyRunning
	default:
		_, _ = fmt.Fprintln(env.Stderr, oneLine(err.Error()))
		return ExitProviderError
	}
}
