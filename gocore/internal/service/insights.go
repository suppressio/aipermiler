package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/insights"
)

// LocalInsightsFunc counts this machine's Claude Code sessions over the last
// windowDays local days as of now (insights.Compute, #298).
type LocalInsightsFunc func(ctx context.Context, windowDays int, now time.Time) (*insights.Result, error)

// localInsightsTTL is how long a count is reused, as Electron's
// LOCAL_INSIGHTS_RECOMPUTE_INTERVAL_MS: reading the session files costs more
// than a refresh.
const localInsightsTTL = 2 * time.Hour

// localInsightsCache keeps one count per analysis window. A missing count is
// computed during the request; an expired one is served while a new one is
// computed in the background, as Electron does with its cache. A failure keeps
// the last count, if any: the local insights never fail a snapshot.
type localInsightsCache struct {
	compute LocalInsightsFunc
	ctx     context.Context
	logger  *slog.Logger

	mu      sync.Mutex
	entries map[int]*insightsEntry
}

type insightsEntry struct {
	result  *insights.Result
	at      time.Time
	running bool
	// done is closed when the running count ends: a request without a
	// result waits for it instead of scanning the files again, as Electron
	// shares its running count.
	done chan struct{}
}

func newLocalInsightsCache(ctx context.Context, compute LocalInsightsFunc, logger *slog.Logger) *localInsightsCache {
	if compute == nil {
		return nil
	}
	return &localInsightsCache{compute: compute, ctx: ctx, logger: logger, entries: map[int]*insightsEntry{}}
}

// get returns the count over windowDays to use now, nil when there is none.
func (c *localInsightsCache) get(ctx context.Context, windowDays int, now time.Time) *insights.Result {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	entry, found := c.entries[windowDays]
	if !found {
		entry = &insightsEntry{}
		c.entries[windowDays] = entry
	}
	if entry.result != nil {
		fresh := now.Sub(entry.at) < localInsightsTTL && !now.Before(entry.at)
		if !fresh && !entry.running {
			c.start(entry, windowDays, now)
		}
		result := entry.result
		c.mu.Unlock()
		return result
	}
	// No count yet: the request waits for one, as Electron's refresh does,
	// sharing the one already running.
	if !entry.running {
		c.start(entry, windowDays, now)
	}
	done := entry.done
	c.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return entry.result
}

// start runs a count in the background, bound to the service's context (a
// stop cancels it), never to a request's. Called with c.mu held.
func (c *localInsightsCache) start(entry *insightsEntry, windowDays int, now time.Time) {
	entry.running, entry.done = true, make(chan struct{})
	go c.run(entry, windowDays, now)
}

func (c *localInsightsCache) run(entry *insightsEntry, windowDays int, now time.Time) {
	result, err := c.compute(c.ctx, windowDays, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	defer close(entry.done)
	entry.running = false
	switch {
	case err != nil:
		c.logger.Warn("Cannot count the local Claude Code sessions", "error", err)
	case result == nil:
		// No output tokens in the window: nothing to keep, counted again next
		// time (Electron keeps no empty count either).
	default:
		entry.result, entry.at = result, now
	}
}
