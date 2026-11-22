package refresh_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/refresh"
	"github.com/suppressio/aipermiler/gocore/internal/refresh/policy"
	"github.com/suppressio/aipermiler/gocore/internal/store"
	"github.com/suppressio/aipermiler/gocore/internal/usage"
)

var start = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

// fakeClock: time moves only on Advance; After channels fire when it passes them.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{c.now.Add(d), ch})
	return ch
}

// Rewind sets the wall clock back (NTP, a manual change); timers keep running.
func (c *fakeClock) Rewind(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(-d)
	for i := range c.waiters {
		c.waiters[i].at = c.waiters[i].at.Add(-d)
	}
	c.mu.Unlock()
}

// Advance moves the wall clock by d and fires what is due, as a resume from
// sleep does: one jump, the pending timer fires once.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
	c.mu.Unlock()
}

func (c *fakeClock) waiting() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// fakeFetcher answers from a per-account function and counts the reads.
type fakeFetcher struct {
	mu     sync.Mutex
	reads  map[string]int
	answer func(ctx context.Context, account store.Account) (usage.Usage, error)
}

func (f *fakeFetcher) Fetch(ctx context.Context, account store.Account) (usage.Usage, error) {
	f.mu.Lock()
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[account.ID]++
	f.mu.Unlock()
	return f.answer(ctx, account)
}

func (f *fakeFetcher) count(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[id]
}

func window(used float64) usage.Usage {
	reset := start.Add(3 * time.Hour)
	return usage.Usage{QuotaWindows: []usage.QuotaWindow{{
		ID: "claude-5h", Label: "5h", PeriodType: usage.PeriodRollingHours, PeriodLength: ptr(5.0),
		Unit: usage.UnitPercentage, Used: used, Total: ptr(100.0), ResetsAt: &reset,
	}}}
}

type harness struct {
	t      *testing.T
	clock  *fakeClock
	fetch  *fakeFetcher
	store  *store.Store
	loop   *refresh.Loop
	cancel context.CancelFunc
	done   chan error
}

func newHarness(t *testing.T, intervalMinutes int, accounts ...store.Account) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Settings.RefreshIntervalMinutes = intervalMinutes
		s.Accounts = accounts
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, clock: &fakeClock{now: start}, store: st, done: make(chan error, 1),
		fetch: &fakeFetcher{answer: func(context.Context, store.Account) (usage.Usage, error) { return window(10), nil }}}
	h.loop = refresh.New(refresh.Config{Store: st, Fetcher: h.fetch, Clock: h.clock, Location: time.UTC})
	return h
}

func (h *harness) run() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- h.loop.Run(ctx) }()
	h.idle()
	h.t.Cleanup(func() {
		cancel()
		if err := <-h.done; err != nil {
			h.t.Errorf("Run: %v", err)
		}
	})
}

// idle waits until the loop sleeps on its heartbeat.
func (h *harness) idle() {
	h.t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if h.clock.waiting() > 0 && !h.loop.Busy() {
			return
		}
	}
	h.t.Fatal("loop never went idle")
}

// advance moves time minute by minute, letting the loop handle each heartbeat.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	for step := time.Duration(0); step < d; step += time.Minute {
		h.clock.Advance(time.Minute)
		h.idle()
	}
}

func claude(id string) store.Account {
	return store.Account{ID: id, Provider: "claude", Label: id, Enabled: true}
}

func TestStartReadsEveryEnabledAccountAndRecords(t *testing.T) {
	t.Parallel()
	off := claude("off")
	off.Enabled = false
	h := newHarness(t, 30, claude("a"), off)
	h.run()
	if h.fetch.count("a") != 1 || h.fetch.count("off") != 0 {
		t.Fatalf("reads = %v", h.fetch.reads)
	}
	acc := h.store.Snapshot().Account("a")
	if acc.LastGood == nil || len(acc.LastGood.QuotaWindows) != 1 {
		t.Fatalf("lastGood = %+v", acc.LastGood)
	}
	hist := acc.History["claude-5h"]
	if len(hist.RecentSamples) != 1 || hist.RecentSamples[0].Used != 10 || len(hist.DailyUsage) != 1 {
		t.Errorf("history = %+v", hist)
	}
}

func TestScheduleAtTheIntervalOrFastWhileRising(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 30, claude("a"))
	used := 10.0
	h.fetch.answer = func(context.Context, store.Account) (usage.Usage, error) { return window(used), nil }
	h.run()
	h.advance(29 * time.Minute)
	if n := h.fetch.count("a"); n != 1 {
		t.Fatalf("read before the interval: %d", n)
	}
	used = 12 // the next read sees a rise
	h.advance(time.Minute)
	if n := h.fetch.count("a"); n != 2 {
		t.Fatalf("reads at the interval = %d", n)
	}
	h.advance(5 * time.Minute)
	if n := h.fetch.count("a"); n != 3 {
		t.Fatalf("reads 5 min after a rise = %d", n)
	}
	// No rise since: back to the interval.
	h.advance(25 * time.Minute)
	if n := h.fetch.count("a"); n != 3 {
		t.Fatalf("read before the interval after the rise stopped: %d", n)
	}
	h.advance(5 * time.Minute)
	if n := h.fetch.count("a"); n != 4 {
		t.Fatalf("reads = %d", n)
	}
}

// A heartbeat that arrives much later than scheduled is a resume from sleep:
// the loop reads at once, even before the interval is over.
func TestResumeFromSleepReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 180, claude("a"))
	h.run()
	h.clock.Advance(20 * time.Minute) // one jump: the machine slept
	h.idle()
	if n := h.fetch.count("a"); n != 2 {
		t.Fatalf("reads after a resume = %d", n)
	}
	// Twenty ordinary minutes are not a resume.
	h.advance(20 * time.Minute)
	if n := h.fetch.count("a"); n != 2 {
		t.Fatalf("reads without a resume = %d", n)
	}
}

func TestFailedReadKeepsLastGoodAndBacksOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 30, claude("a"))
	h.run()
	failure := &usage.Error{Code: usage.CodeUnavailable, Message: "down"}
	h.fetch.answer = func(context.Context, store.Account) (usage.Usage, error) { return usage.Usage{}, failure }
	h.advance(30 * time.Minute)
	if h.fetch.count("a") != 2 {
		t.Fatalf("reads = %d", h.fetch.count("a"))
	}
	if acc := h.store.Snapshot().Account("a"); acc.LastGood == nil || acc.LastGood.QuotaWindows[0].Used != 10 {
		t.Errorf("lastGood lost: %+v", acc.LastGood)
	}
	if err := h.loop.LastError("a"); !errors.Is(err, failure) {
		t.Errorf("LastError = %v", err)
	}
	// Failed once: the next scheduled read waits 10 min, even with a 5 min tick.
	if err := h.loop.Refresh(context.Background(), policy.Scheduled); err != nil {
		t.Fatal(err)
	}
	if h.fetch.count("a") != 2 {
		t.Errorf("read inside the backoff")
	}
	h.fetch.answer = func(context.Context, store.Account) (usage.Usage, error) { return window(11), nil }
	if err := h.loop.Refresh(context.Background(), policy.Forced); err != nil {
		t.Fatal(err)
	}
	if h.fetch.count("a") != 3 || h.loop.LastError("a") != nil {
		t.Errorf("forced read: reads %d, error %v", h.fetch.count("a"), h.loop.LastError("a"))
	}
}

// Requests during a run join one more run with the strongest mode; a manual
// request inside the cache never reads the account again.
func TestRequestsDuringARunAreCoalesced(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 30, claude("a"))
	h.run()
	release := make(chan struct{})
	reading := make(chan struct{}, 4)
	h.fetch.answer = func(context.Context, store.Account) (usage.Usage, error) {
		reading <- struct{}{}
		<-release
		return window(10), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = h.loop.Refresh(context.Background(), policy.Forced) }()
	<-reading // the forced run is reading
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h.loop.Refresh(context.Background(), policy.Manual) }()
	}
	for h.loop.Pending() != policy.Manual {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	// Start + forced; the queued manual run is inside the 1 min cache.
	if n := h.fetch.count("a"); n != 2 {
		t.Errorf("reads = %d; want 2", n)
	}
}

func TestStopCancelsAReadInProgress(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 30, claude("a"))
	reading := make(chan struct{})
	h.fetch.answer = func(ctx context.Context, _ store.Account) (usage.Usage, error) {
		close(reading)
		<-ctx.Done()
		return usage.Usage{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.loop.Run(ctx) }()
	<-reading
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the stop")
	}
	if h.store.Snapshot().Account("a").LastGood != nil {
		t.Error("a cancelled read wrote a result")
	}
}

// The first read of a day takes yesterday's last value as the day's baseline,
// unless yesterday is before the period start (a reset): then 0.
func TestDayBaselineFromThePreviousDay(t *testing.T) {
	t.Parallel()
	weekly := func(used float64, reset time.Time) usage.Usage {
		return usage.Usage{QuotaWindows: []usage.QuotaWindow{{
			ID: "claude-7d", Label: "7d", PeriodType: usage.PeriodRollingDays, PeriodLength: ptr(7.0),
			Unit: usage.UnitPercentage, Used: used, Total: ptr(100.0), ResetsAt: &reset,
		}}}
	}
	yesterday := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		reset time.Time
		want  float64
	}{
		"same period":        {start.Add(72 * time.Hour), 40},
		"period began today": {start.Add(7*24*time.Hour - time.Hour), 0},
	} {
		acc := claude("a")
		acc.History = map[string]history.State{"claude-7d": {DailyUsage: []history.DailyPoint{{Day: yesterday, Used: 40, DayStartUsed: 30}}}}
		h := newHarness(t, 30, acc)
		h.fetch.answer = func(context.Context, store.Account) (usage.Usage, error) { return weekly(45, c.reset), nil }
		h.run()
		points := h.store.Snapshot().Account("a").History["claude-7d"].DailyUsage
		if len(points) != 2 || points[1].DayStartUsed != c.want {
			t.Errorf("%s: points = %+v; want today's baseline %v", name, points, c.want)
		}
	}
}

// A wall clock set back does not hold the schedule back by the same amount.
func TestClockSetBackKeepsTheSchedule(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 30, claude("a"))
	h.run()
	h.clock.Rewind(2 * time.Hour)
	// The first heartbeat after the change restarts the wait: one interval
	// from then, not two hours more.
	h.advance(31 * time.Minute)
	if n := h.fetch.count("a"); n != 2 {
		t.Errorf("reads 31 min after the clock went back 2 h = %d; want 2", n)
	}
}

// A request whose run is cut by the stop fails: nothing was read for it.
func TestRefreshCutByTheStopFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 30, claude("a"))
	h.run()
	reading := make(chan struct{})
	h.fetch.answer = func(ctx context.Context, _ store.Account) (usage.Usage, error) {
		close(reading)
		<-ctx.Done()
		return usage.Usage{}, ctx.Err()
	}
	got := make(chan error, 1)
	go func() { got <- h.loop.Refresh(context.Background(), policy.Forced) }()
	<-reading
	h.cancel()
	select {
	case err := <-got:
		if !errors.Is(err, refresh.ErrStopped) {
			t.Errorf("Refresh = %v; want ErrStopped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh did not return")
	}
}

// Before this process has read an account, its lastGood (from the store, maybe
// written before a restart) is not known to be current: not_ready (#248 review).
func TestLastErrorIsNotReadyBeforeTheFirstRead(t *testing.T) {
	t.Parallel()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var e *usage.Error
	if err := refresh.New(refresh.Config{Store: st}).LastError("a"); !errors.As(err, &e) || e.Code != usage.CodeNotReady {
		t.Errorf("LastError = %v; want not_ready", err)
	}
}
