package policy_test

import (
	"testing"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/refresh/policy"
)

// Same cases as main/refreshPolicy.test.ts.

const interval = 30 * time.Minute

var t0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func TestNextDelay(t *testing.T) {
	t.Parallel()
	if got := policy.NextDelay(true, interval); got != policy.FastRefresh {
		t.Errorf("rising: %v", got)
	}
	if got := policy.NextDelay(false, interval); got != interval {
		t.Errorf("not rising: %v", got)
	}
	// A configured interval already at the fast pace is never slowed down.
	if got := policy.NextDelay(true, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("short interval: %v", got)
	}
}

func TestMinFetchGapDoublesPerFailure(t *testing.T) {
	t.Parallel()
	for failures, want := range map[int]time.Duration{0: 5 * time.Minute, 1: 10 * time.Minute, 2: 20 * time.Minute, 3: interval, 10: interval} {
		if got := policy.MinFetchGap(failures, interval); got != want {
			t.Errorf("failures %d: %v; want %v", failures, got, want)
		}
	}
}

func TestFirstAndForcedAlwaysFetch(t *testing.T) {
	t.Parallel()
	if !policy.ShouldFetch(nil, policy.Scheduled, t0, interval) {
		t.Error("first fetch skipped")
	}
	justFailed := &policy.AccountState{LastAttempt: t0, Failures: 5}
	if !policy.ShouldFetch(justFailed, policy.Forced, t0.Add(time.Millisecond), interval) {
		t.Error("forced skipped")
	}
}

func TestScheduledRespectsBackoff(t *testing.T) {
	t.Parallel()
	failedOnce := policy.Attempted(nil, t0, false)
	if failedOnce.Failures != 1 {
		t.Fatalf("failures = %d", failedOnce.Failures)
	}
	if policy.ShouldFetch(&failedOnce, policy.Scheduled, t0.Add(5*time.Minute), interval) {
		t.Error("fetched inside the backoff")
	}
	if !policy.ShouldFetch(&failedOnce, policy.Scheduled, t0.Add(10*time.Minute), interval) {
		t.Error("skipped after the backoff")
	}
	// A tick a few seconds early still counts.
	if !policy.ShouldFetch(&failedOnce, policy.Scheduled, t0.Add(10*time.Minute-5*time.Second), interval) {
		t.Error("early tick skipped")
	}
	healthy := policy.Attempted(&failedOnce, t0, true)
	if healthy.Failures != 0 || !policy.ShouldFetch(&healthy, policy.Scheduled, t0.Add(5*time.Minute), interval) {
		t.Errorf("healthy account: %+v", healthy)
	}
}

func TestManualReusesARecentFetch(t *testing.T) {
	t.Parallel()
	state := policy.Attempted(nil, t0, true)
	if policy.ShouldFetch(&state, policy.Manual, t0.Add(policy.ManualCache-time.Millisecond), interval) {
		t.Error("manual inside the cache fetched")
	}
	if !policy.ShouldFetch(&state, policy.Manual, t0.Add(policy.ManualCache), interval) {
		t.Error("manual after the cache skipped")
	}
}

func TestStronger(t *testing.T) {
	t.Parallel()
	cases := []struct{ a, b, want policy.Mode }{
		{policy.Scheduled, policy.Manual, policy.Manual},
		{policy.Forced, policy.Manual, policy.Forced},
		{policy.Scheduled, policy.Scheduled, policy.Scheduled},
	}
	for _, c := range cases {
		if got := policy.Stronger(c.a, c.b); got != c.want {
			t.Errorf("Stronger(%s, %s) = %s", c.a, c.b, got)
		}
	}
}
