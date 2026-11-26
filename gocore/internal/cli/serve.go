package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/buildinfo"
	"github.com/suppressio/aipermiler/gocore/internal/config"
	"github.com/suppressio/aipermiler/gocore/internal/insights"
	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/service"
	"github.com/suppressio/aipermiler/gocore/internal/store"
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
		// After the first signal a further one gets the default action, so a stuck
		// shutdown can still be interrupted. One that arrives before stop() runs
		// is absorbed by NotifyContext's buffer: an instant, documented.
		go func() { <-ctx.Done(); stop() }()
	}
	// The service reads the providers by running this same binary (#246).
	self, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, oneLine(err.Error()))
		return ExitProviderError
	}
	fetcher := refresh.CLIFetcher{Binary: self}
	err = service.Run(ctx, service.Options{
		Dir: dir, Version: buildinfo.Version, ContractVersion: usage.SchemaVersion, Logger: logger,
		Loop: func(st *store.Store) service.Loop {
			return refresh.New(refresh.Config{Store: st, Fetcher: fetcher, Logger: logger})
		},
		Raw:           fetcher.Raw,
		Accounts:      fetcher,
		LocalInsights: localInsights(env, logger),
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

// localInsights counts this machine's Claude Code sessions for the snapshot
// (#340) as `insights claude` does: the same folder, the probe sessions
// skipped, the same 2 minute deadline, in the service's calendar (time.Local).
func localInsights(env Env, logger *slog.Logger) service.LocalInsightsFunc {
	return func(ctx context.Context, windowDays int, now time.Time) (*insights.Result, error) {
		root, err := claudeRoot(env)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		return insights.Compute(ctx, insights.Options{
			Root: root, WindowDays: windowDays, ProbeDir: probeDir(env, logger), Now: now, Location: time.Local, Logger: logger,
		})
	}
}
