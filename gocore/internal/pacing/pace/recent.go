package pace

import (
	"slices"
	"time"

	"github.com/suppressio/aipermiler/gocore/internal/pacing/history"
	"github.com/suppressio/aipermiler/gocore/internal/pacing/quotaperiod"
)

const (
	recentPaceDays    = 3
	recentPaceMinDays = 2
)

// RecentPace returns p_r in pp per working unit (D-3.7): over the last 3
// completed working days of the history, the sum of their deltas over the sum
// of their units. Today and later days are excluded (still running), as are
// days off (unit 0) and reset days (a negative delta is not attributable). It
// is false with fewer than 2 usable days. A day's unit is its part inside the
// period (Period.DayUnitInside): on a rolling-days window the day it started
// weighs only the hours after the start, and earlier days are skipped (HYP#78).
//
// The delta of a day is Used - DayStartUsed (D-3.21). Points may come in any
// order.
func RecentPace(points []history.DailyPoint, schedule quotaperiod.Schedule, period quotaperiod.Period, now time.Time, loc *time.Location) (float64, bool) {
	nowLocal := now.In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)

	sorted := slices.SortedStableFunc(slices.Values(points), func(a, b history.DailyPoint) int { return a.Day.Compare(b.Day) })
	var deltas, units []float64
	for _, point := range sorted {
		delta := point.Used - point.DayStartUsed
		unit := period.DayUnitInside(point.Day, loc, schedule)
		if !point.Day.Before(today) || delta < 0 || unit <= 0 {
			continue
		}
		deltas, units = append(deltas, delta), append(units, unit)
	}
	deltas, units = lastN(deltas, recentPaceDays), lastN(units, recentPaceDays)
	if len(deltas) < recentPaceMinDays {
		return 0, false
	}
	var sumDelta, sumUnit float64
	for i := range deltas {
		sumDelta += deltas[i]
		sumUnit += units[i]
	}
	return sumDelta / sumUnit, true
}

func lastN(values []float64, n int) []float64 {
	return values[max(0, len(values)-n):]
}
