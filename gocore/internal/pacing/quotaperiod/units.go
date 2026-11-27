package quotaperiod

import "time"

// Schedule uses Sunday-first weights (0, 0.5 or 1) and positive HoursPerDay.
// Its inputs are normalized by the caller, outside this pure package.
type Schedule struct {
	Enabled     bool
	Days        [7]float64
	HoursPerDay float64
}

// DayUnit is the scheduled weight of a local calendar day (D-2.1).
func (s Schedule) DayUnit(day time.Time, loc *time.Location) float64 {
	if !s.Enabled {
		return 1
	}
	return s.Days[day.In(loc).Weekday()]
}

// WorkingUnitsBetween sums whole local days in [start, end), truncating both
// bounds to local midnight. This is W for billing cycles (D-2.2).
func WorkingUnitsBetween(start, end time.Time, loc *time.Location, schedule Schedule) float64 {
	end = localDay(end, loc)
	var units float64
	for day := localDay(start, loc); day.Before(end); day = day.AddDate(0, 0, 1) {
		units += schedule.DayUnit(day, loc)
	}
	return units
}

// WorkingUnits contains N, E, R, U, e and H from PACING.md D-2.2..D-2.7.
// Values retain full precision, including the last partial local day.
type WorkingUnits struct {
	Total          float64
	Elapsed        float64
	Remaining      float64
	FromToday      float64
	TodayElapsed   float64
	RemainingHours float64
}

// Units returns day-based units, or false for rolling hours or missing bounds.
// workedHours is today's observed activity span, nil when no activity was seen;
// rolling days ignore it and use weighted real overlap instead (D-2.3..D-2.6).
func (p Period) Units(now time.Time, loc *time.Location, schedule Schedule, workedHours *float64) (WorkingUnits, bool) {
	if (p.Kind != BillingCycle && p.Kind != RollingDays) || p.Start.IsZero() || p.End.IsZero() {
		return WorkingUnits{}, false
	}
	between := WorkingUnitsBetween
	if p.Kind == RollingDays {
		between = rollingUnitsBetween
	}
	result := WorkingUnits{Total: between(p.Start, p.End, loc, schedule)}
	today := localDay(now, loc)
	current := now
	if p.Kind == BillingCycle {
		current = today
	}
	switch {
	case current.Before(p.Start):
		result.Remaining = result.Total
		result.FromToday = result.Total
	case !current.Before(p.End):
		result.Elapsed = result.Total
	default:
		if p.Kind == RollingDays {
			result.Elapsed = between(p.Start, now, loc, schedule)
			result.Remaining = between(now, p.End, loc, schedule)
			dayStart := today
			if p.Start.After(dayStart) {
				dayStart = p.Start
			}
			result.FromToday = between(dayStart, p.End, loc, schedule)
			result.TodayElapsed = between(dayStart, now, loc, schedule)
		} else {
			result.TodayElapsed = todayElapsed(now, loc, schedule, workedHours)
			result.FromToday = between(today, p.End, loc, schedule)
			result.Elapsed = between(p.Start, today, loc, schedule) + result.TodayElapsed
			result.Remaining = result.FromToday - result.TodayElapsed
		}
	}
	result.RemainingHours = result.Remaining * schedule.HoursPerDay
	return result, true
}

// UnitsToReset returns R, the working units left until the reset, which needs
// only the reset and the schedule, not the period start (D-2.5). It is false
// for rolling hours or a missing reset; a reset already passed leaves zero.
func (p Period) UnitsToReset(now time.Time, loc *time.Location, schedule Schedule, workedHours *float64) (float64, bool) {
	if (p.Kind != BillingCycle && p.Kind != RollingDays) || p.End.IsZero() {
		return 0, false
	}
	if p.Kind == RollingDays {
		if !now.Before(p.End) {
			return 0, true
		}
		return rollingUnitsBetween(now, p.End, loc, schedule), true
	}
	today := localDay(now, loc)
	if !today.Before(p.End) {
		return 0, true
	}
	return WorkingUnitsBetween(today, p.End, loc, schedule) - todayElapsed(now, loc, schedule, workedHours), true
}

func todayElapsed(now time.Time, loc *time.Location, schedule Schedule, workedHours *float64) float64 {
	if !schedule.Enabled {
		// The fraction of the local day, whose length is 23 or 25 hours when
		// daylight saving time changes (I-8: e never exceeds the day unit).
		day := localDay(now, loc)
		return now.Sub(day).Hours() / day.AddDate(0, 0, 1).Sub(day).Hours()
	}
	unit := schedule.DayUnit(now, loc)
	if workedHours == nil || unit == 0 {
		return 0
	}
	return min(unit, max(2, *workedHours)/schedule.HoursPerDay)
}

func rollingUnitsBetween(start, end time.Time, loc *time.Location, schedule Schedule) float64 {
	var units float64
	for cursor := start; cursor.Before(end); {
		day := localDay(cursor, loc)
		dayEnd := day.AddDate(0, 0, 1)
		next := dayEnd
		if end.Before(next) {
			next = end
		}
		// Weighted by the day's real length, 23 or 25 hours when daylight
		// saving time changes, so a day never counts more than its unit (I-8).
		units += schedule.DayUnit(cursor, loc) * next.Sub(cursor).Hours() / dayEnd.Sub(day).Hours()
		cursor = next
	}
	return units
}
