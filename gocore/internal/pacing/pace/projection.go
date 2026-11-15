package pace

import "math"

// Blend returns the pace p used by projection, autonomy and the verdict
// (D-3.8): the period average x / E blended 50/50 with the recent pace. Just
// x / E when there is no recent pace, just the recent pace when E <= 0, and
// false when neither exists.
func Blend(used, elapsedUnits, recent float64, hasRecent bool) (float64, bool) {
	if elapsedUnits <= 0 {
		return recent, hasRecent
	}
	average := used / elapsedUnits
	if !hasRecent {
		return average, true
	}
	return (average + recent) / 2, true
}

// Projection returns proj = x + p * R in pp (D-3.9), not capped at 100. Without
// a pace (hasPace false) it is x.
func Projection(used, pace float64, hasPace bool, remainingUnits float64) float64 {
	if !hasPace {
		return used
	}
	return used + pace*remainingUnits
}

// Autonomy returns a = (100 - x) / p in working units (D-3.10): 0 when x >= 100,
// +Inf when the pace is not positive or unknown. The result is always defined;
// the caller compares it with the time left R.
func Autonomy(used, pace float64, hasPace bool) float64 {
	switch {
	case used >= 100:
		return 0
	case !hasPace || pace <= 0:
		return math.Inf(1)
	default:
		return (100 - used) / pace
	}
}
