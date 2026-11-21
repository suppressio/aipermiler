package outlook

import "time"

// Hourly is the outlook of a rolling-hours window, read in hours (§3.9).
type Hourly struct {
	// HoursLeft is max(0, resetsAt - now).
	HoursLeft float64
	// ProjectedAtReset is x + v * HoursLeft in pp, not capped.
	ProjectedAtReset float64
	// AutonomyHours is the hours the quota lasts at the instant pace; only
	// meaningful when HasAutonomy.
	AutonomyHours float64
	HasAutonomy   bool
	// ResetPassed is true when resetsAt <= now (stale data, E5): the window
	// has no forward-looking verdict.
	ResetPassed bool
}

// HourlyOutlook returns the outlook of a rolling-hours window (D-3.20). No
// measured instant pace counts as 0 for the projection. Autonomy is 0 when
// x >= 100, (100 - x) / v when v > 0, otherwise absent. With the reset passed
// the hours left are 0, the projection is x and there is no autonomy
// whatever x and v. It is false without a reset moment (hasReset false).
func HourlyOutlook(used, instantPace float64, hasPace bool, resetsAt time.Time, hasReset bool, now time.Time) (Hourly, bool) {
	if !hasReset {
		return Hourly{}, false
	}
	if !resetsAt.After(now) {
		return Hourly{ProjectedAtReset: used, ResetPassed: true}, true
	}
	v := 0.0
	if hasPace {
		v = instantPace
	}
	hoursLeft := resetsAt.Sub(now).Hours()
	out := Hourly{HoursLeft: hoursLeft, ProjectedAtReset: used + v*hoursLeft}
	switch {
	case used >= 100:
		out.HasAutonomy = true
	case v > 0:
		out.AutonomyHours, out.HasAutonomy = (100-used)/v, true
	}
	return out, true
}
