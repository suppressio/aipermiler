// Package outlook turns the used quota and the pacing quantities into the
// forward-looking readings of a window (docs/PACING.md §3.7 to §3.10 and
// §3.13): today's budget, the redistributed quota, the hourly outlook of
// rolling-hours windows, the verdict and the projection summary. It is pure:
// no I/O and no hidden clock. Undefined values are reported with an explicit
// ok.
package outlook

// PaceAlertRatio is how many times today's budget a day's consumption may be
// before the pace alert (D-3.17), and how many times the redistributed quota
// the blended pace may be before the verdict turns at risk (§3.10 step 3a).
const PaceAlertRatio = 1.5

// resetDrop is the fall below the day's baseline, in pp, beyond which the
// value is a reset and not a provider falling back to an integer percent
// (D-3.16).
const resetDrop = 1.0

// TodayBudget returns B = (100 - x0) / U * u(today) in pp (D-3.15): what was
// left at the start of the day spread over the working units from today on
// (today whole), times today's unit. It is fixed for the whole day and false
// without a baseline, on a day off (dayUnit <= 0), outside the period or when
// U <= 0 (the caller passes unitsFromToday 0 then).
func TodayBudget(baseline float64, hasBaseline bool, unitsFromToday, dayUnit float64) (float64, bool) {
	if !hasBaseline || dayUnit <= 0 || unitsFromToday <= 0 {
		return 0, false
	}
	return (100 - baseline) / unitsFromToday * dayUnit, true
}

// UsedToday returns the consumption of the day in pp (D-3.16): x - x0, never
// negative. A fall of more than 1 pp below the baseline is a reset in the
// day, and the new count x is the consumption; a fall up to 1 pp is noise.
// Both sides keep full precision.
func UsedToday(used, baseline float64) float64 {
	if used < baseline-resetDrop {
		return used
	}
	return max(0, used-baseline)
}

// PaceAlert reports whether the consumption of the day passed 1.5 times
// today's budget (D-3.17). It needs a positive budget.
func PaceAlert(usedToday, budget float64) bool {
	return budget > 0 && usedToday > PaceAlertRatio*budget
}
