package pace

import "math"

// Ceiling returns C = rem / H in pp/h (D-3.12): the fastest pace that does not
// exhaust the quota before the reset. H is the working hours left, or the
// calendar hours when no working time is left (and for rolling-hours windows,
// where the caller passes 0 working hours). It is 0 when remaining is 0, and
// false when calendarHours <= 0: the reset is missing or already past.
//
// C is only a ceiling: near the reset H shrinks and C explodes (#72), so the
// gauge compares with Target, never with C directly.
func Ceiling(remaining, workingHours, calendarHours float64) (float64, bool) {
	if calendarHours <= 0 {
		return 0, false
	}
	if remaining <= 0 {
		return 0, true
	}
	hours := calendarHours
	if workingHours > 0 {
		hours = workingHours
	}
	return remaining / hours, true
}

// Target returns T = min(C, s_h) (D-3.13), the pace the gauge compares v with.
// It never exceeds the ceiling and never rises because the reset is near. It is
// false when the ceiling is undefined (reset missing or past) or the even
// hourly share is (period start unknown).
func Target(ceiling float64, hasCeiling bool, idealHourly float64, hasIdeal bool) (float64, bool) {
	if !hasCeiling || !hasIdeal {
		return 0, false
	}
	return math.Min(ceiling, idealHourly), true
}

// gaugeMin is the smallest position of the gauge for a pace above 0 (D-3.14).
const gaugeMin = 0.02

// GaugePosition returns g(v, T) in [0, 1] (D-3.14): 0 if v <= 0, 1 if T <= 0,
// else 0.5 + log2(v/T)/4 clamped to [0.02, 1]. The target is in the middle,
// half of it at a quarter, double at three quarters, 4x or more at the end.
func GaugePosition(v, target float64) float64 {
	if v <= 0 {
		return 0
	}
	if target <= 0 {
		return 1
	}
	return math.Min(1, math.Max(gaugeMin, 0.5+math.Log2(v/target)/4))
}
