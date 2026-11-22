package refresh

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
	"github.com/suppressio/aipermiler/gocore/internal/refresh/policy"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

const (
	// heartbeat is how often the loop looks at the wall clock. Go timers do not
	// count the time a machine sleeps, so the loop compares wall-clock times
	// instead of waiting for one long timer.
	heartbeat = time.Minute
	// resumeGap: a heartbeat this late means the machine slept, and the loop
	// reads at once (#246: refresh at start and after a resume).
	resumeGap = 3 * heartbeat
	// riseThreshold is the rise in percentage points that counts as consumption
	// (D-3.3, budget.ACTIVITY_EPSILON); riseTolerance absorbs float rounding.
	riseThreshold = 0.05
	riseTolerance = 1e-9
)

// ErrStopped is returned by Refresh once the loop has stopped.
var ErrStopped = errors.New("refresh loop stopped")

var errAccountGone = errors.New("account removed during the read")

// Fetcher reads one account's current values (CLIFetcher in the service).
type Fetcher interface {
	Fetch(ctx context.Context, account store.Account) (usage.Usage, error)
}

// Clock is the loop's time source; tests pass a fake one.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

// Now drops the monotonic reading: wall-clock differences must include the
// time the machine slept.
func (realClock) Now() time.Time                         { return time.Now().Round(0) }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Config is what the loop needs; Clock, Location and Logger are optional.
type Config struct {
	Store    *store.Store
	Fetcher  Fetcher
	Clock    Clock          // nil means the real clock
	Location *time.Location // the local calendar of the history; nil means time.Local
	Logger   *slog.Logger
}

// Loop reads the enabled accounts at start, then at the interval of the
// settings (FastRefresh while consumption rises, policy.NextDelay), after a
// resume from sleep, and on request. One run at a time: requests that arrive
// during a run are joined into one more run with the strongest mode, as in
// Electron today (main.ts refreshAndBroadcast); thanks to the backoff and the
// manual cache, only a forced request reads again an account just read.
type Loop struct {
	cfg  Config
	wake chan struct{}
	done chan struct{} // closed when Run returns

	mu       sync.Mutex
	pending  policy.Mode // "" when no run is requested
	waiters  []chan struct{}
	busy     bool
	lastErrs map[string]error

	// Owned by Run's goroutine.
	states map[string]policy.AccountState
	rose   bool
}

// New returns a loop that starts with Run.
func New(cfg Config) *Loop {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Loop{
		cfg: cfg, wake: make(chan struct{}, 1), done: make(chan struct{}),
		lastErrs: map[string]error{}, states: map[string]policy.AccountState{},
	}
}

// Run reads at once, then keeps the schedule until ctx ends. A stop cancels
// the read in progress (the fetcher kills the child's tree) and records
// nothing of it.
func (l *Loop) Run(ctx context.Context) error {
	defer close(l.done)
	l.run(ctx, policy.Scheduled)
	due, last := l.nextDue(), l.cfg.Clock.Now()
	beat := l.cfg.Clock.After(heartbeat)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-l.wake:
			l.mu.Lock()
			mode, waiters := l.pending, l.waiters
			l.pending, l.waiters = "", nil
			l.mu.Unlock()
			if mode == "" {
				continue
			}
			l.run(ctx, mode)
			for _, w := range waiters {
				close(w)
			}
		case <-beat:
			now := l.cfg.Clock.Now()
			if now.Sub(last) <= resumeGap && now.Before(due) {
				last, beat = now, l.cfg.Clock.After(heartbeat)
				continue
			}
			if now.Sub(last) > resumeGap {
				l.cfg.Logger.Info("Resumed from sleep, refreshing", "gap", now.Sub(last).Round(time.Second))
			}
			l.run(ctx, policy.Scheduled)
		}
		if ctx.Err() != nil {
			return nil
		}
		// After every run: the schedule restarts from its end, and a long run
		// is not mistaken for a sleep.
		due, last = l.nextDue(), l.cfg.Clock.Now()
		beat = l.cfg.Clock.After(heartbeat)
	}
}

// Refresh asks for a run and returns once a run that started after the
// request has ended. It returns ctx's error, or ErrStopped once Run returned.
func (l *Loop) Refresh(ctx context.Context, mode policy.Mode) error {
	ch := make(chan struct{})
	l.mu.Lock()
	if l.pending == "" {
		l.pending = mode
	} else {
		l.pending = policy.Stronger(l.pending, mode)
	}
	l.waiters = append(l.waiters, ch)
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default: // a wake-up is already queued
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return ErrStopped
	}
}

// LastError is the account's last read error, nil once a read succeeds. While
// it is set, the account's lastGood is last known data: stale.
func (l *Loop) LastError(accountID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastErrs[accountID]
}

func (l *Loop) nextDue() time.Time {
	return l.cfg.Clock.Now().Add(policy.NextDelay(l.rose, interval(l.cfg.Store.Snapshot().Settings)))
}

func interval(s store.Settings) time.Duration {
	if s.RefreshIntervalMinutes <= 0 {
		return time.Duration(store.Empty().Settings.RefreshIntervalMinutes) * time.Minute
	}
	return time.Duration(s.RefreshIntervalMinutes) * time.Minute
}

func (l *Loop) setBusy(busy bool) {
	l.mu.Lock()
	l.busy = busy
	l.mu.Unlock()
}

func (l *Loop) setError(accountID string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		delete(l.lastErrs, accountID)
	} else {
		l.lastErrs[accountID] = err
	}
}

// run reads the accounts the policy picks, one at a time (two Claude reads
// would share the probe folder), and commits each fresh value at once.
func (l *Loop) run(ctx context.Context, mode policy.Mode) {
	l.setBusy(true)
	defer l.setBusy(false)
	snap := l.cfg.Store.Snapshot()
	every := interval(snap.Settings)
	read, rose := false, false
	for _, account := range snap.Accounts {
		if !account.Enabled {
			continue
		}
		state, known := l.states[account.ID]
		var previous *policy.AccountState
		if known {
			previous = &state
		}
		// Without a last good value there is nothing to reuse: always read.
		if account.LastGood != nil && !policy.ShouldFetch(previous, mode, l.cfg.Clock.Now(), every) {
			continue
		}
		values, err := l.cfg.Fetcher.Fetch(ctx, account)
		if ctx.Err() != nil {
			return
		}
		at := l.cfg.Clock.Now()
		l.states[account.ID] = policy.Attempted(previous, at, err == nil)
		read = true
		if err != nil {
			l.cfg.Logger.Warn("Account read failed", "account", account.ID, "error", err)
			l.setError(account.ID, err)
			continue
		}
		up, err := l.record(account.ID, values, at)
		switch {
		case errors.Is(err, errAccountGone):
			continue
		case err != nil:
			l.cfg.Logger.Warn("Cannot save the account's values", "account", account.ID, "error", err)
			l.setError(account.ID, err)
			continue
		}
		l.setError(account.ID, nil)
		rose = rose || up
	}
	// Nothing read: nothing new is known, the pace stays as it was.
	if read {
		l.rose = rose
	}
}

// record saves a fresh read: lastGood, and one sample per window through
// history.Record. It reports whether any window rose since its last sample.
func (l *Loop) record(accountID string, values usage.Usage, at time.Time) (bool, error) {
	rose := false
	err := l.cfg.Store.Update(func(s *store.State) error {
		account := s.Account(accountID)
		if account == nil {
			return errAccountGone
		}
		lastGood := store.NewLastGood(values, at)
		account.LastGood = &lastGood
		for _, w := range values.QuotaWindows {
			used, ok := utilization(w)
			if !ok {
				continue
			}
			past := account.History[w.ID].RecentSamples
			if n := len(past); n > 0 && used-past[n-1].Used > riseThreshold+riseTolerance {
				rose = true
			}
			account.Record(w.ID, history.Sample{At: at, Used: used, Fresh: true}, at, l.historySettings(*s, *account, w, values, at))
		}
		return nil
	})
	if errors.Is(err, store.ErrNotSynced) {
		// Committed: never retried, a second Record would count the sample twice.
		l.cfg.Logger.Warn("Store change saved but not synced to disk", "error", err)
		err = nil
	}
	return rose && err == nil, err
}

// utilization is the window's value in percentage points, as
// budget.normalizedUtilization computes it today; false without a total.
func utilization(w usage.QuotaWindow) (float64, bool) {
	if w.Unit == usage.UnitPercentage {
		return w.Used, true
	}
	if w.Total != nil && *w.Total > 0 {
		return w.Used / *w.Total * 100, true
	}
	return 0, false
}

// historySettings gives history.Record the calendar, the retention and the
// window's period, resolved as core/usage.ts resolvePeriodBounds does.
func (l *Loop) historySettings(s store.State, account store.Account, w usage.QuotaWindow, values usage.Usage, at time.Time) history.Settings {
	retention := s.Settings.RetentionDays
	if retention <= 0 {
		retention = store.Empty().Settings.RetentionDays
	}
	settings := history.Settings{Location: l.cfg.Location, RetentionDays: retention}
	qw := quotaperiod.Window{Kind: quotaperiod.Kind(w.PeriodType)}
	if w.PeriodLength != nil {
		qw.Length = int(*w.PeriodLength)
	}
	if w.ResetsAt != nil {
		qw.ResetsAt = *w.ResetsAt
	}
	var renewal time.Time
	if values.SubscriptionRenewsAt != nil {
		renewal = *values.SubscriptionRenewsAt
	}
	manual := quotaperiod.RenewalRule{Type: "dayOfMonth", Day: account.RenewalDay}
	if period, err := quotaperiod.ResolvePeriod(qw, renewal, manual, at, l.cfg.Location); err == nil {
		settings.Period.Start = period.Start
	}
	// The last point of an earlier day. Its time is not stored: the end of its
	// day stands for it, so "before the period start" compares days, as
	// budget.updateDailyPoint does.
	today := startOfDay(at, l.cfg.Location)
	var previous *history.DailyPoint
	for i, point := range account.History[w.ID].DailyUsage {
		if point.Day.Before(today) && (previous == nil || point.Day.After(previous.Day)) {
			previous = &account.History[w.ID].DailyUsage[i]
		}
	}
	if previous != nil {
		settings.Period.PreviousAt = previous.Day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		settings.Period.PreviousUsed = previous.Used
	}
	return settings
}

func startOfDay(at time.Time, loc *time.Location) time.Time {
	local := at.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}
