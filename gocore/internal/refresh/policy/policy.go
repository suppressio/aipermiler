// Package policy decides when to read an account and when to run the next
// refresh: a port of main/refreshPolicy.ts with the same cases. Pure functions,
// no clock, process, store or HTTP: the service's refresh loop owns the timers
// and the per-account state.
//
//   - While consumption rises, the next refresh comes after FastRefresh instead
//     of the configured interval; the first refresh without a rise goes back to it.
//   - Per-account backoff: every consecutive failed read doubles the minimum gap
//     between two reads of that account (5 → 10 → 20 min…), never beyond the
//     configured interval, so a faster pace never insists on a provider that
//     pushes back.
//   - A manual refresh within ManualCache of the last read reuses that result.
package policy

import "time"

const (
	// FastRefresh is the pace while consumption rises.
	FastRefresh = 5 * time.Minute
	// ManualCache is how long a manual refresh reuses the last read.
	ManualCache = time.Minute
	// scheduleTolerance: timers fire a little early or late, so a scheduled tick
	// right at the gap still reads.
	scheduleTolerance = 15 * time.Second
)

// Mode is why a refresh runs.
type Mode string

const (
	// Scheduled is the timer: the per-account backoff applies.
	Scheduled Mode = "scheduled"
	// Manual is a user's request: only the short cache applies.
	Manual Mode = "manual"
	// Forced follows a login or a settings change: it always reads.
	Forced Mode = "forced"
)

func strength(m Mode) int {
	switch m {
	case Forced:
		return 2
	case Manual:
		return 1
	default:
		return 0
	}
}

// Stronger returns the stronger of two requested modes (a queued refresh keeps it).
func Stronger(a, b Mode) Mode {
	if strength(a) >= strength(b) {
		return a
	}
	return b
}

// AccountState is one account's read attempts.
type AccountState struct {
	LastAttempt time.Time
	Failures    int
}

// MinFetchGap is the minimum gap between two reads of an account, from its
// consecutive failures.
func MinFetchGap(failures int, interval time.Duration) time.Duration {
	gap := FastRefresh
	for i := 0; i < failures && gap < interval; i++ {
		gap *= 2
	}
	return min(interval, gap)
}

// ShouldFetch says whether an account is read in this refresh or its last
// result is reused; state is nil before the first attempt.
func ShouldFetch(state *AccountState, mode Mode, now time.Time, interval time.Duration) bool {
	if state == nil || mode == Forced {
		return true
	}
	elapsed := now.Sub(state.LastAttempt)
	if mode == Manual {
		return elapsed >= ManualCache
	}
	return elapsed >= MinFetchGap(state.Failures, interval)-scheduleTolerance
}

// Attempted is the state after a read attempt: failures reset on success and
// are counted on failure.
func Attempted(state *AccountState, now time.Time, ok bool) AccountState {
	next := AccountState{LastAttempt: now}
	if !ok {
		next.Failures = 1
		if state != nil {
			next.Failures = state.Failures + 1
		}
	}
	return next
}

// NextDelay is the delay before the next scheduled refresh: fast while
// consumption rises.
func NextDelay(consumptionRose bool, interval time.Duration) time.Duration {
	if consumptionRose {
		return min(FastRefresh, interval)
	}
	return interval
}
