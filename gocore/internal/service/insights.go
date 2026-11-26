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
			entry.running = true
			go c.run(c.ctx, windowDays, now)
		}
		result := entry.result
		c.mu.Unlock()
		return result
	}
	c.mu.Unlock()
	// No count yet: the request waits for one, as Electron's refresh does.
	// Concurrent first requests may each compute: rare, and only once.
	return c.run(ctx, windowDays, now)
}

func (c *localInsightsCache) run(ctx context.Context, windowDays int, now time.Time) *insights.Result {
	result, err := c.compute(ctx, windowDays, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[windowDays]
	entry.running = false
	if err != nil || result == nil {
		c.logger.Warn("Cannot count the local Claude Code sessions", "error", err)
		return entry.result
	}
	entry.result, entry.at = result, now
	return result
}
