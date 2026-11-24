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
	unread   bool // Changed asked to read the accounts the loop knows nothing of
	busy     bool
	lastErrs map[string]error
	read     map[string]bool // accounts this process has read, well or not
	// gens counts the Changed calls per account: a read that started under an
	// older generation read old settings, and nothing of it is kept.
	gens map[string]uint64

	// Owned by Run's goroutine.
	states map[string]trackedState
	rose   bool
}

// trackedState is an account's policy state and the generation it was read
// under: one of an older generation is forgotten.
type trackedState struct {
	policy.AccountState
	gen uint64
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
		lastErrs: map[string]error{}, read: map[string]bool{}, gens: map[string]uint64{}, states: map[string]trackedState{},
	}
}

// Run reads at once, then keeps the schedule until ctx ends. A stop cancels
// the read in progress (the fetcher kills the child's tree) and records
// nothing of it.
func (l *Loop) Run(ctx context.Context) error {
	defer close(l.done)
	l.run(ctx, policy.Scheduled, false)
	due, last := l.nextDue(), l.cfg.Clock.Now()
	beat := l.cfg.Clock.After(heartbeat)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-l.wake:
			l.mu.Lock()
			mode, waiters, unread := l.pending, l.waiters, l.unread
			l.pending, l.waiters, l.unread = "", nil, false
			l.mu.Unlock()
			if mode == "" {
				if !unread {
					continue
				}
				// Only the accounts Changed named: the others keep their
				// schedule, so due stays as it was.
				l.run(ctx, policy.Scheduled, true)
				if ctx.Err() != nil {
					return nil
				}
				last, beat = l.cfg.Clock.Now(), l.cfg.Clock.After(heartbeat)
				continue
			}
			// A full run also reads the accounts Changed named: they have no state.
			l.run(ctx, mode, false)
			if ctx.Err() != nil {
				return nil // the waiters get ErrStopped: their run was cut
			}
			for _, w := range waiters {
				close(w)
			}
		case <-beat:
			now := l.cfg.Clock.Now()
			// A wall clock set back would hold the schedule back as much.
			if wait := l.delay(); due.Sub(now) > wait {
				due = now.Add(wait)
			}
			if now.Sub(last) <= resumeGap && now.Before(due) {
				last, beat = now, l.cfg.Clock.After(heartbeat)
				continue
			}
			if now.Sub(last) > resumeGap {
				l.cfg.Logger.Info("Resumed from sleep, refreshing", "gap", now.Sub(last).Round(time.Second))
			}
			l.run(ctx, policy.Scheduled, false)
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

// Changed tells the loop that the store's settings of these accounts changed
// in a way that changes their reads (store.Account.ReadsLike), or that they
// were added, removed, enabled or disabled (#284). The loop forgets what it
// knew of them (last error, last attempt, backoff), so LastError is
// ErrNotRead at once, as after a restart, and it reads them at once, alone,
// whatever their backoff: the other accounts keep their schedule. A read of
// them already in progress does not count as their read: its error and its
// backoff state are dropped, and its values are committed only if the
// account is still enabled and read the same way (store.Account.ReadsLike),
// otherwise dropped too. A read that starts between the store commit and
// Changed is discarded the same way and repeated: one extra read, harmless.
// Call it after the store change is committed. It does not wait.
func (l *Loop) Changed(accountIDs ...string) {
	l.mu.Lock()
	for _, id := range accountIDs {
		l.gens[id]++
		delete(l.read, id)
		delete(l.lastErrs, id)
	}
	l.unread = true
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default: // a wake-up is already queued
	}
}

// ErrNotRead is LastError before this process has read the account, or since
// Changed named it: its lastGood may be old, from before a restart or a
// settings change. Not not_ready, which clients take for Claude Code not
// reaching its prompt (main/accountNotice.ts): unavailable with its own reason.
var ErrNotRead = &usage.Error{Code: usage.CodeUnavailable, Reason: "not_read_yet", Message: "Not read yet by the service"}

// LastError is the account's last read error, nil once a read succeeds, and
// ErrNotRead before this process has read the account. While it is set, the
// account's lastGood is last known data: stale.
func (l *Loop) LastError(accountID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.read[accountID] {
		return ErrNotRead
	}
	return l.lastErrs[accountID]
}

func (l *Loop) delay() time.Duration {
	return policy.NextDelay(l.rose, interval(l.cfg.Store.Snapshot().Settings))
}

func (l *Loop) nextDue() time.Time { return l.cfg.Clock.Now().Add(l.delay()) }

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

// generations returns the current generation of every account.
func (l *Loop) generations() map[string]uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]uint64, len(l.gens))
	for id, g := range l.gens {
		out[id] = g
	}
	return out
}

// setError records the outcome of a read made under generation gen; false,
// recording nothing, when Changed named the account since.
func (l *Loop) setError(accountID string, gen uint64, err error) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gens[accountID] != gen {
		return false
	}
	l.read[accountID] = true
	if err == nil {
		delete(l.lastErrs, accountID)
	} else {
		l.lastErrs[accountID] = err
	}
	return true
}

// run reads the accounts the policy picks, one at a time (two Claude reads
// would share the probe folder), and commits each fresh value at once. With
// unreadOnly it reads only the accounts it has no state of (new, or named by
// Changed).
func (l *Loop) run(ctx context.Context, mode policy.Mode, unreadOnly bool) {
	l.setBusy(true)
	defer l.setBusy(false)
	// The generations before the snapshot: Changed comes after its store
	// change, so a snapshot older than a change always meets a newer
	// generation at commit time.
	gens := l.generations()
	snap := l.cfg.Store.Snapshot()
	every := interval(snap.Settings)
	for id := range l.states {
		if snap.Account(id) == nil {
			delete(l.states, id)
		}
	}
	read, rose := false, false
	for _, account := range snap.Accounts {
		if !account.Enabled {
			continue
		}
		gen := gens[account.ID]
		state, known := l.states[account.ID]
		known = known && state.gen == gen
		if unreadOnly && known {
			continue
		}
		var previous *policy.AccountState
		// An attempt "in the future" means the wall clock was set back: its
		// backoff and cache no longer measure anything.
		if known && !state.LastAttempt.After(l.cfg.Clock.Now()) {
			previous = &state.AccountState
		}
		// Without a last good value or a state there is nothing to reuse:
		// always read.
		if account.LastGood != nil && !policy.ShouldFetch(previous, mode, l.cfg.Clock.Now(), every) {
			continue
		}
		values, err := l.cfg.Fetcher.Fetch(ctx, account)
		if ctx.Err() != nil {
			return
		}
		at := l.cfg.Clock.Now()
		attempted := trackedState{policy.Attempted(previous, at, err == nil), gen}
		if err != nil {
			if l.setError(account.ID, gen, err) {
				l.cfg.Logger.Warn("Account read failed", "account", account.ID, "error", err)
				l.states[account.ID] = attempted
				read = true
			}
			continue
		}
		up, err := l.record(account, values, at)
		switch {
		case errors.Is(err, errAccountGone):
			continue
		case err != nil:
			if l.setError(account.ID, gen, err) {
				l.cfg.Logger.Warn("Cannot save the account's values", "account", account.ID, "error", err)
				l.states[account.ID] = attempted
				read = true
			}
			continue
		}
		if l.setError(account.ID, gen, nil) {
			l.states[account.ID] = attempted
			read = true
			rose = rose || up
		}
	}
	switch {
	case !read: // nothing new is known, the pace stays as it was
	case unreadOnly:
		l.rose = l.rose || rose // the other accounts were not read
	default:
		l.rose = rose
	}
}

// record saves a fresh read of account: lastGood, and one sample per window
// through history.Record. It reports whether any window rose since its last
// sample. An account removed, disabled or read differently since the read
// started (Changed) gets nothing: errAccountGone.
func (l *Loop) record(read store.Account, values usage.Usage, at time.Time) (bool, error) {
	rose := false
	err := l.cfg.Store.Update(func(s *store.State) error {
		account := s.Account(read.ID)
		if account == nil || !account.Enabled || !account.ReadsLike(read) {
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
